package pvcache

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	TracerName = "weka-csi"
)

const (
	// DefaultFetchInterval is the default interval for fetching PVs from Kubernetes
	DefaultFetchInterval = 60 * time.Second

	// DefaultChannelBufferSize is the buffer size for the PV channel
	DefaultChannelBufferSize = 10000
)

// PvCache provides a thread-safe cache of Kubernetes PersistentVolumes for
// Weka CSI driver. The cache automatically refreshes by polling the Kubernetes
// API and provides efficient lookups by filesystem name for capacity validation.
//
// Thread-safety: All public methods are thread-safe. The cache uses sync.RWMutex
// for concurrent access, allowing multiple readers or a single writer.
type PvCache struct {
	// manager is the controller-runtime manager for accessing Kubernetes API
	manager ctrl.Manager

	// driverName is the CSI driver name to filter PVs (e.g., "csi.weka.io")
	driverName string

	// fetchInterval is how often to fetch PVs from Kubernetes
	fetchInterval time.Duration

	// pvsByUID is the primary index of cached PVs by their UID
	pvsByUID map[types.UID]*pvCacheEntry

	// pvsByFilesystem is a secondary index grouping PVs by filesystem name
	pvsByFilesystem map[string]map[types.UID]*pvCacheEntry

	// pvChan is the channel for streaming PVs from producer to consumer
	pvChan chan *v1.PersistentVolume

	// stopCh signals goroutines to stop
	stopCh chan struct{}

	// wg tracks running goroutines for graceful shutdown
	wg sync.WaitGroup

	sync.RWMutex

	logger zerolog.Logger
}

// New creates a new PvCache instance.
//
// Parameters:
//   - manager: controller-runtime manager for Kubernetes API access
//   - driverName: CSI driver name to filter (e.g., "csi.weka.io")
//   - config: configuration options (can be nil for defaults)
//
// The cache must be started with Start() before use and stopped with Stop() for cleanup.
func New(manager ctrl.Manager, driverName string, config *Config) *PvCache {
	if config == nil {
		config = &Config{
			FetchInterval:     DefaultFetchInterval,
			ChannelBufferSize: DefaultChannelBufferSize,
		}
	}

	if config.FetchInterval <= 0 {
		config.FetchInterval = DefaultFetchInterval
	}
	if config.ChannelBufferSize <= 0 {
		config.ChannelBufferSize = DefaultChannelBufferSize
	}

	return &PvCache{
		manager:         manager,
		driverName:      driverName,
		fetchInterval:   config.FetchInterval,
		pvsByUID:        make(map[types.UID]*pvCacheEntry),
		pvsByFilesystem: make(map[string]map[types.UID]*pvCacheEntry),
		pvChan:          make(chan *v1.PersistentVolume, config.ChannelBufferSize),
		stopCh:          make(chan struct{}),
		logger:          log.With().Str("component", "pvcache").Logger(),
	}
}

// Start begins the cache refresh cycle. It launches two goroutines:
//   - pvStreamer: fetches PVs from Kubernetes API periodically
//   - pvProcessor: processes PVs and updates the cache
//
// This method returns immediately after starting the goroutines.
// Use Stop() to gracefully shut down the cache.
func (pc *PvCache) Start(ctx context.Context) error {
	pc.logger.Info().
		Str("driver", pc.driverName).
		Dur("interval", pc.fetchInterval).
		Msg("Starting PV cache")

	pc.wg.Add(2)

	// Start producer: fetches PVs from Kubernetes
	go func() {
		defer pc.wg.Done()
		pc.pvStreamer(ctx)
	}()

	// Start consumer: processes PVs and updates cache
	go func() {
		defer pc.wg.Done()
		pc.pvProcessor(ctx)
	}()

	return nil
}

// Stop gracefully shuts down the cache by:
//   - Closing the stop channel to signal goroutines
//   - Waiting for all goroutines to finish
//   - Closing the PV channel
func (pc *PvCache) Stop() error {
	pc.logger.Info().Msg("Stopping PV cache")
	close(pc.stopCh)
	pc.wg.Wait()
	close(pc.pvChan)
	pc.logger.Info().Msg("PV cache stopped")
	return nil
}

// GetTotalDirectoryCapacity calculates the total capacity (in bytes) of all
// directory-backed PVs for a specific filesystem.
//
// This method is thread-safe and can be called concurrently.
func (pc *PvCache) GetTotalDirectoryCapacity(ctx context.Context, filesystemName string) int64 {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "GetTotalDirectoryCapacity")
	defer span.End()

	pc.RLock()
	defer pc.RUnlock()

	fsMap := pc.pvsByFilesystem[filesystemName]
	if fsMap == nil {
		return 0
	}

	var total int64
	for _, entry := range fsMap {
		if entry.backingType == VolumeBackingDirectory {
			total += entry.capacity
		}
	}

	return total
}

// GetCacheStats returns statistics about cached PVs for a specific filesystem.
// This method is thread-safe and can be called concurrently.
func (pc *PvCache) GetCacheStats(filesystemName string) map[string]interface{} {
	pc.RLock()
	defer pc.RUnlock()

	fsMap := pc.pvsByFilesystem[filesystemName]
	if fsMap == nil {
		return map[string]interface{}{
			"filesystem":        filesystemName,
			"total_pvs":         0,
			"directory_pvs":     0,
			"filesystem_pvs":    0,
			"snapshot_pvs":      0,
			"total_capacity":    int64(0),
			"total_capacity_gi": float64(0),
		}
	}

	var totalCapacity int64
	directoryCount := 0
	filesystemCount := 0
	snapshotCount := 0

	for _, entry := range fsMap {
		switch entry.backingType {
		case VolumeBackingDirectory:
			directoryCount++
			totalCapacity += entry.capacity
		case VolumeBackingFilesystem:
			filesystemCount++
		case VolumeBackingSnapshot:
			snapshotCount++
		}
	}

	return map[string]interface{}{
		"filesystem":        filesystemName,
		"total_pvs":         len(fsMap),
		"directory_pvs":     directoryCount,
		"filesystem_pvs":    filesystemCount,
		"snapshot_pvs":      snapshotCount,
		"total_capacity":    totalCapacity,
		"total_capacity_gi": float64(totalCapacity) / (1024 * 1024 * 1024),
	}
}

// pvStreamer is the producer goroutine that fetches PVs from Kubernetes API
// periodically and streams them to the pvProcessor via the pvChan channel.
func (pc *PvCache) pvStreamer(ctx context.Context) {
	logger := pc.logger.With().Str("routine", "pvStreamer").Logger()
	logger.Info().Msg("PV streamer started")
	defer logger.Info().Msg("PV streamer stopped")

	ticker := time.NewTicker(pc.fetchInterval)
	defer ticker.Stop()

	// Do initial fetch immediately
	pc.fetchAndStreamPVs(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pc.stopCh:
			return
		case <-ticker.C:
			pc.fetchAndStreamPVs(ctx)
		}
	}
}

// fetchAndStreamPVs fetches all PVs from Kubernetes, validates them, and streams
// valid ones to the processing channel. It also triggers pruning of stale entries.
func (pc *PvCache) fetchAndStreamPVs(ctx context.Context) {
	logger := pc.logger.With().Str("routine", "fetchAndStreamPVs").Logger()
	logger.Debug().Msg("Fetching PVs from Kubernetes")

	startTime := time.Now()

	// Fetch PVs from Kubernetes
	pvList := &v1.PersistentVolumeList{}
	err := pc.manager.GetClient().List(ctx, pvList)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch PersistentVolumes from Kubernetes")
		time.Sleep(10 * time.Second) // Brief sleep before retry
		return
	}

	// Sort PVs by UID for deterministic behavior
	slices.SortFunc(pvList.Items, func(a, b v1.PersistentVolume) int {
		if a.GetUID() < b.GetUID() {
			return -1
		}
		return 1
	})

	// Filter and stream valid PVs
	var validPVs []*v1.PersistentVolume
	for i := range pvList.Items {
		pv := &pvList.Items[i]
		if pc.isValidPV(pv) {
			validPVs = append(validPVs, pv)

			select {
			case <-ctx.Done():
				return
			case <-pc.stopCh:
				return
			case pc.pvChan <- pv:
				// Successfully streamed
			}
		}
	}

	duration := time.Since(startTime)
	logger.Info().
		Int("total_pvs", len(pvList.Items)).
		Int("valid_pvs", len(validPVs)).
		Dur("duration", duration).
		Msg("PV fetch completed")

	// Prune stale entries
	pc.pruneOldPVs(validPVs)

	// Log cache statistics after fetch cycle (not in hot path)
	pc.logCacheStatsSummary()
}

// logCacheStatsSummary logs a summary of cache state for all filesystems
// Called after each fetch cycle for observability
func (pc *PvCache) logCacheStatsSummary() {
	// Acquire lock to read totals and copy filesystem names
	pc.RLock()
	totalPVs := len(pc.pvsByUID)
	totalFilesystems := len(pc.pvsByFilesystem)

	// Copy filesystem names while holding lock
	fsNames := make([]string, 0, len(pc.pvsByFilesystem))
	for fsName := range pc.pvsByFilesystem {
		fsNames = append(fsNames, fsName)
	}
	pc.RUnlock() // ← Release lock BEFORE calling GetCacheStats

	logger := pc.logger.With().Str("routine", "logCacheStats").Logger()

	// Log summary at Info level
	logger.Info().
		Int("total_pvs", totalPVs).
		Int("total_filesystems", totalFilesystems).
		Msg("Cache statistics summary")

	// Log detailed per-filesystem stats only if Trace enabled
	if logger.GetLevel() <= zerolog.TraceLevel {
		for _, fsName := range fsNames {
			stats := pc.GetCacheStats(fsName)
			logger.Trace().
				Interface("stats", stats).
				Str("filesystem", fsName).
				Msg("Filesystem cache stats")
		}
	}

}

// isValidPV checks if a PV is valid for caching based on:
//   - Must be a CSI volume
//   - Must match our driver name
//   - Must be in Bound phase
//   - Must not be marked for deletion
//   - Must have capacity
func (pc *PvCache) isValidPV(pv *v1.PersistentVolume) bool {
	// Must be a CSI volume
	if pv.Spec.CSI == nil {
		return false
	}

	// Must match our driver
	if pv.Spec.CSI.Driver != pc.driverName {
		return false
	}

	// Must be bound or released (released PVs still have quotas on Weka)
	if pv.Status.Phase != v1.VolumeBound && pv.Status.Phase != v1.VolumeReleased {
		return false
	}

	// Must not be marked for deletion
	if pv.DeletionTimestamp != nil {
		return false
	}

	// Must have capacity
	if pv.Spec.Capacity == nil || len(pv.Spec.Capacity) == 0 {
		return false
	}

	// Must have volume attributes
	if pv.Spec.CSI.VolumeAttributes == nil {
		return false
	}

	return true
}

// pvProcessor is the consumer goroutine that reads PVs from the channel
// and processes them (parsing and updating the cache).
func (pc *PvCache) pvProcessor(ctx context.Context) {
	logger := pc.logger.With().Str("routine", "pvProcessor").Logger()
	logger.Info().Msg("PV processor started")
	defer logger.Info().Msg("PV processor stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case <-pc.stopCh:
			return
		case pv, ok := <-pc.pvChan:
			if !ok {
				// Channel closed
				return
			}
			if pv != nil {
				pc.processPV(pv)
			}
		}
	}
}

// processPV parses a single PV and updates the cache.
func (pc *PvCache) processPV(pv *v1.PersistentVolume) {
	logger := pc.logger.With().
		Str("pv_name", pv.Name).
		Str("pv_uid", string(pv.UID)).
		Logger()

	// Extract volumeHandle
	volumeHandle := pv.Spec.CSI.VolumeHandle
	if volumeHandle == "" {
		logger.Warn().Msg("PV has empty volumeHandle, skipping")
		return
	}

	// Parse filesystem name
	filesystemName, err := parseFilesystemName(volumeHandle)
	if err != nil {
		logger.Warn().Err(err).Str("volume_handle", volumeHandle).Msg("Failed to parse filesystem name")
		return
	}

	// Parse capacity
	capacityQuantity, ok := pv.Spec.Capacity[v1.ResourceStorage]
	if !ok {
		logger.Warn().Msg("PV missing storage capacity")
		return
	}
	capacity := capacityQuantity.Value()

	// Get volume type
	volumeType := pv.Spec.CSI.VolumeAttributes["volumeType"]

	// Determine backing type
	backingType := determineBackingType(volumeHandle, volumeType)

	// Create cache entry
	entry := &pvCacheEntry{
		pv:             pv,
		filesystemName: filesystemName,
		capacity:       capacity,
		backingType:    backingType,
	}

	// Update cache (thread-safe)
	pc.Lock()
	defer pc.Unlock()

	// Add to primary index (map overwrites duplicates automatically)
	pc.pvsByUID[pv.UID] = entry

	// Add to secondary index (nested map prevents duplicates)
	if pc.pvsByFilesystem[filesystemName] == nil {
		pc.pvsByFilesystem[filesystemName] = make(map[types.UID]*pvCacheEntry)
	}
	pc.pvsByFilesystem[filesystemName][pv.UID] = entry

	logger.Trace().
		Str("filesystem", filesystemName).
		Str("backing_type", string(backingType)).
		Int64("capacity", capacity).
		Msg("Processed PV")
}

// pruneOldPVs removes PVs from the cache that are no longer present in Kubernetes.
// This is called after each fetch cycle to keep the cache in sync.
func (pc *PvCache) pruneOldPVs(currentPVs []*v1.PersistentVolume) {
	logger := pc.logger.With().Str("routine", "pruneOldPVs").Logger()

	// Build set of current UIDs
	currentUIDs := make(map[types.UID]struct{}, len(currentPVs))
	for _, pv := range currentPVs {
		currentUIDs[pv.UID] = struct{}{}
	}

	pc.Lock()
	defer pc.Unlock()

	// Find UIDs to remove
	var toRemove []types.UID
	for uid := range pc.pvsByUID {
		if _, exists := currentUIDs[uid]; !exists {
			toRemove = append(toRemove, uid)
		}
	}

	if len(toRemove) == 0 {
		return
	}

	// Remove from both indexes
	for _, uid := range toRemove {
		entry := pc.pvsByUID[uid]
		delete(pc.pvsByUID, uid)

		// Remove from filesystem index (nested map)
		if entry != nil {
			fsName := entry.filesystemName
			if fsMap := pc.pvsByFilesystem[fsName]; fsMap != nil {
				delete(fsMap, uid)
				// Clean up empty filesystem map
				if len(fsMap) == 0 {
					delete(pc.pvsByFilesystem, fsName)
				}
			}
		}
	}

	logger.Info().Int("pruned_count", len(toRemove)).Msg("Pruned stale PVs from cache")
}

// parseFilesystemName extracts the filesystem name from a Weka CSI volumeHandle.
// Supported formats:
//   - "weka/v2/<filesystem>[:<snapshot>][/<innerPath>]"
//   - "dir/v1/<filesystem>[:<snapshot>][/<innerPath>]"
func parseFilesystemName(volumeHandle string) (string, error) {
	if volumeHandle == "" {
		return "", fmt.Errorf("volumeHandle is empty")
	}

	// Split by "/" to get segments
	// Format: <volumeType>/<version>/<filesystem>[:<snapshot>][/<innerPath>...]
	segments := strings.Split(volumeHandle, "/")
	if len(segments) < 3 {
		return "", fmt.Errorf("invalid volumeHandle format: %s (expected at least 3 segments)", volumeHandle)
	}

	// Filesystem name is in segment 2 (0-indexed)
	// Format: <filesystem> or <filesystem>:<snapshot>
	fsWithSnapshot := segments[2]
	if fsWithSnapshot == "" {
		return "", fmt.Errorf("filesystem name is empty in volumeHandle: %s", volumeHandle)
	}

	// Strip snapshot name if present (format: "filesystem:snapshot")
	fsParts := strings.Split(fsWithSnapshot, ":")
	filesystemName := fsParts[0]

	if filesystemName == "" {
		return "", fmt.Errorf("filesystem name is empty after parsing volumeHandle: %s", volumeHandle)
	}

	return filesystemName, nil
}

// determineBackingType determines the backing type of volume based on its
// volumeHandle and volumeType attribute.
//
// Returns one of:
//   - VolumeBackingSnapshot: Snapshot-backed volume (has ":" in filesystem segment)
//   - VolumeBackingDirectory: Directory-backed volume (has inner path or dir/ volumeType)
//   - VolumeBackingFilesystem: Filesystem-backed volume (dedicated filesystem)
func determineBackingType(volumeHandle string, volumeType string) VolumeBackingType {
	if volumeHandle == "" {
		return VolumeBackingFilesystem // Default to filesystem for invalid/empty handles
	}

	segments := strings.Split(volumeHandle, "/")
	if len(segments) < 3 {
		return VolumeBackingFilesystem // Invalid format, assume filesystem
	}

	// Check if volumeHandle contains snapshot
	// Format: "weka/v2/filesystem:snapshot/path" - the ":" indicates snapshot
	if strings.Contains(segments[2], ":") {
		return VolumeBackingSnapshot
	}

	// Check if volumeType indicates directory-backed
	if strings.HasPrefix(volumeType, "dir/") {
		return VolumeBackingDirectory
	}

	// Check if volumeHandle has inner path (more than 3 segments means directory-backed)
	// Format: volumeType/version/filesystem/innerPath...
	// - 3 segments: "weka/v2/filesystem" (filesystem-backed)
	// - 4+ segments: "weka/v2/filesystem/path/to/dir" (directory-backed)
	if len(segments) > 3 {
		return VolumeBackingDirectory
	}

	return VolumeBackingFilesystem
}
