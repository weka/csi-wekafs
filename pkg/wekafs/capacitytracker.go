package wekafs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime"
)

// CapacityReservation tracks a volume create request that passed validation
// but hasn't been confirmed in Kubernetes yet
type CapacityReservation struct {
	Filesystem     string
	SourceCapacity int64 // Current PV size
	TargetCapacity int64 // Desired PV size
	Timestamp      time.Time
}

// CapacityTracker maintains capacity totals and pending reservations for validation
type CapacityTracker struct {
	mu                  sync.Mutex
	confirmedCapacity   map[string]int64               // filesystem → total capacity from last K8s List()
	pendingReservations map[string]CapacityReservation // volumeID → pending create
	lastRefreshTime     time.Time
	manager             controllerruntime.Manager
}

// validateAndReserveCapacity validates capacity and reserves it for this volume.
// currentCapacity: existing PV size (0 for new volumes)
// targetCapacity: desired PV size after operation
func (cs *ControllerServer) validateAndReserveCapacity(ctx context.Context, v *Volume, currentCapacity int64, targetCapacity int64) error {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "ValidateAndReserveCapacity")
	defer span.End()

	logger := log.Ctx(ctx).With().
		Str("volume_id", v.GetId()).
		Str("filesystem", v.FilesystemName).
		Int64("current_capacity", currentCapacity).
		Int64("target_capacity", targetCapacity).
		Logger()

	// Only validate directory-backed volumes
	if !v.hasInnerPath() || v.isOnSnapshot() {
		logger.Debug().Msg("Skipping capacity validation for non-directory-backed volume")
		return nil
	}

	// Check if capacity tracker is available
	if cs.capacityTracker == nil {
		return status.Errorf(codes.Internal,
			"capacity enforcement enabled but tracker not initialized - kubernetes manager may have failed to start")
	}

	// Get filesystem total capacity
	fsObj, err := v.getFilesystemObj(ctx, true)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch filesystem object")
		return status.Errorf(codes.Internal, "failed to fetch filesystem %s: %v", v.FilesystemName, err)
	}
	if fsObj == nil {
		return status.Errorf(codes.Internal, "filesystem %s not found", v.FilesystemName)
	}

	// Validate and reserve capacity (atomic operation)
	cs.capacityTracker.mu.Lock()
	defer cs.capacityTracker.mu.Unlock()

	// Refresh capacity from K8s if needed (every 5s)
	if time.Since(cs.capacityTracker.lastRefreshTime) > capacityRefreshInterval {
		if err := cs.capacityTracker.refreshFromKubernetesLocked(ctx, cs.config.GetDriver().name); err != nil {
			logger.Warn().Err(err).Msg("Failed to refresh capacity from Kubernetes, using stale data")
		}
	}

	// Get confirmed capacity from last refresh
	confirmedCapacity := cs.capacityTracker.confirmedCapacity[v.FilesystemName]

	// Calculate additional capacity
	additionalCapacity := targetCapacity - currentCapacity

	// Check if pending reservation already exists
	existing, existingReservation := cs.capacityTracker.pendingReservations[v.GetId()]

	var sourceCapacity int64
	if existingReservation {
		// Validate: target capacity cannot decrease (prevents conflicting expansions)
		if targetCapacity < existing.TargetCapacity {
			logger.Warn().
				Int64("existing_target", existing.TargetCapacity).
				Int64("new_target", targetCapacity).
				Msg("Rejecting conflicting expansion - target capacity cannot decrease")
			return status.Errorf(codes.FailedPrecondition,
				"conflicting expansion for volume %s: target cannot decrease from %d to %d",
				v.GetId(), existing.TargetCapacity, targetCapacity)
		}
		// Preserve existing SourceCapacity for the reservation record
		sourceCapacity = existing.SourceCapacity
	} else {
		// New reservation - set SourceCapacity from current PV size
		sourceCapacity = currentCapacity
	}

	// Sum pending reservations for this filesystem
	pendingCapacity := cs.capacityTracker.getPendingCapacityLocked(v.FilesystemName)

	// Calculate total including this request
	totalRequired := confirmedCapacity + pendingCapacity + additionalCapacity

	// Validate
	if totalRequired > fsObj.AvailableSsd {
		logger.Warn().
			Int64("confirmed_capacity", confirmedCapacity).
			Int64("pending_capacity", pendingCapacity).
			Int64("additional_capacity", additionalCapacity).
			Int64("total_required", totalRequired).
			Int64("filesystem_capacity", fsObj.TotalCapacity).
			Int64("exceeded_by", totalRequired-fsObj.TotalCapacity).
			Msg("Insufficient filesystem capacity")
		return status.Errorf(codes.ResourceExhausted,
			"insufficient capacity on filesystem %s: required %d bytes (confirmed %d + pending %d + additional %d) exceeds total capacity %d bytes",
			v.FilesystemName, totalRequired, confirmedCapacity, pendingCapacity, additionalCapacity, fsObj.TotalCapacity)
	}

	// Reserve capacity for this volume
	cs.capacityTracker.pendingReservations[v.GetId()] = CapacityReservation{
		Filesystem:     v.FilesystemName,
		SourceCapacity: sourceCapacity,
		TargetCapacity: targetCapacity,
		Timestamp:      time.Now(),
	}

	logger.Debug().
		Int64("confirmed_capacity", confirmedCapacity).
		Int64("pending_capacity", pendingCapacity).
		Int64("additional_capacity", additionalCapacity).
		Int64("total_required", totalRequired).
		Int64("filesystem_capacity", fsObj.TotalCapacity).
		Msg("Capacity validation details")

	logger.Info().Msg("Capacity validation passed")

	return nil
}

// releaseCapacityReservation removes a pending reservation
func (cs *ControllerServer) releaseCapacityReservation(volumeID string) {
	if cs.config.enforceDirVolTotalCapacity && cs.capacityTracker != nil {
		cs.capacityTracker.mu.Lock()
		defer cs.capacityTracker.mu.Unlock()
		cs.capacityTracker.deleteReservationLocked(volumeID)
	}
}

// refreshFromKubernetesLocked fetches current PV list from K8s and updates confirmed capacity.
// REQUIRES: ct.mu must be held by caller.
func (ct *CapacityTracker) refreshFromKubernetesLocked(ctx context.Context, driverName string) error {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "RefreshCapacityFromKubernetes")
	defer span.End()

	logger := log.Ctx(ctx)

	// Check if manager is initialized
	if ct.manager == nil {
		err := fmt.Errorf("kubernetes manager not initialized - cannot refresh capacity")
		logger.Error().Err(err).Msg("")
		return err
	}

	// List all PVs from Kubernetes cache
	pvList := &v1.PersistentVolumeList{}
	if err := ct.manager.GetClient().List(ctx, pvList); err != nil {
		logger.Error().Err(err).Msg("Failed to list PVs from Kubernetes")
		return fmt.Errorf("failed to list PVs: %w", err)
	}

	logger.Info().Int("total_pvs", len(pvList.Items)).Msg("Successfully listed PVs from Kubernetes")

	// calculate capacity AND remove confirmed reservations
	capacityByFS := make(map[string]int64)
	for _, pv := range pvList.Items {
		// Filter for our driver
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != driverName {
			continue
		}

		volumeID := pv.Spec.CSI.VolumeHandle

		// Only count directory-backed volumes for capacity
		fsName := sliceFilesystemNameFromVolumeId(volumeID)
		if fsName == "" {
			continue
		}

		// Skip snapshots
		innerPath := sliceInnerPathFromVolumeId(volumeID)
		if innerPath == "" || strings.Contains(volumeID, ":") {
			continue
		}

		if capacity, ok := pv.Spec.Capacity[v1.ResourceStorage]; ok {
			capacityByFS[fsName] += capacity.Value()

			// Update pending reservation based on PV state
			if reservation, exists := ct.pendingReservations[volumeID]; exists {
				pvCapacity := capacity.Value()
				if pvCapacity >= reservation.TargetCapacity {
					// Operation completed - PV reached target size
					delete(ct.pendingReservations, volumeID)
				} else {
					// Operation still in progress - update source to current PV size
					reservation.SourceCapacity = pvCapacity
					ct.pendingReservations[volumeID] = reservation
				}
			}
		}
	}

	// Update confirmed capacity
	ct.confirmedCapacity = capacityByFS
	ct.lastRefreshTime = time.Now()

	// Warn about stale reservations
	ct.logStaleReservationsLocked()

	return nil
}

// logStaleReservationsLocked logs warnings for pending reservations older than threshold.
// REQUIRES: ct.mu must be held by caller.
func (ct *CapacityTracker) logStaleReservationsLocked() {
	now := time.Now()
	for volumeID, reservation := range ct.pendingReservations {
		age := now.Sub(reservation.Timestamp)
		if age > pendingReservationTTL {
			log.Warn().
				Str("volume_id", volumeID).
				Str("filesystem", reservation.Filesystem).
				Int64("source_capacity", reservation.SourceCapacity).
				Int64("target_capacity", reservation.TargetCapacity).
				Dur("age", age).
				Msg("Stale pending reservation detected")
		}
	}
}

// getPendingCapacityLocked sums all pending capacity deltas for a filesystem.
// REQUIRES: ct.mu must be held by caller.
func (ct *CapacityTracker) getPendingCapacityLocked(filesystem string) int64 {
	var total int64
	for _, reservation := range ct.pendingReservations {
		if reservation.Filesystem == filesystem {
			total += reservation.TargetCapacity - reservation.SourceCapacity
		}
	}
	return total
}

// deleteReservationLocked removes a pending reservation.
// REQUIRES: ct.mu must be held by caller.
func (ct *CapacityTracker) deleteReservationLocked(volumeID string) {
	delete(ct.pendingReservations, volumeID)
}
