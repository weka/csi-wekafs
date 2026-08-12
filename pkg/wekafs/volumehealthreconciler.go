/*
Copyright 2019-2025 Weka.io LTD and The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	v1 "k8s.io/api/core/v1"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// volumeConditionEntry is one volume's last probe result, in domain terms rather than CSI protos so
// nothing shared is mutable.
type volumeConditionEntry struct {
	// known is false when the volume was probed but its condition could not be established, which
	// callers must report as unknown rather than as healthy.
	known    bool
	abnormal bool
	message  string
	capacity int64
	probedAt time.Time
}

// volumeConditionCache holds the reconciler's most recent result per volume handle. ListVolumes
// answers from it, which is what keeps that RPC free of Weka API calls.
type volumeConditionCache struct {
	sync.RWMutex
	entries map[string]volumeConditionEntry
}

func newVolumeConditionCache() *volumeConditionCache {
	return &volumeConditionCache{entries: make(map[string]volumeConditionEntry)}
}

// lookup returns the entry for a volume handle if one exists and is still fresh enough to serve.
func (c *volumeConditionCache) lookup(handle string) (volumeConditionEntry, bool) {
	c.RLock()
	defer c.RUnlock()
	entry, ok := c.entries[handle]
	if !ok || time.Since(entry.probedAt) > volumeHealthMaxAge {
		return volumeConditionEntry{}, false
	}
	return entry, true
}

func (c *volumeConditionCache) store(handle string, entry volumeConditionEntry) {
	c.Lock()
	defer c.Unlock()
	c.entries[handle] = entry
}

// retainOnly drops entries for volumes that no longer exist, so the cache tracks the fleet rather
// than growing forever with deleted volumes.
func (c *volumeConditionCache) retainOnly(handles map[string]struct{}) int {
	c.Lock()
	defer c.Unlock()
	for handle := range c.entries {
		if _, live := handles[handle]; !live {
			delete(c.entries, handle)
		}
	}
	return len(c.entries)
}

// volumeHealthReconciler keeps volume conditions up to date in the background.
//
// Probing a volume costs a couple of Weka API calls and cannot be avoided - resolving the path to an
// inode is the only proof the volume still exists, so it can never be served from a cache. What this
// moves is *when* that work happens. Doing it inside ListVolumes made the whole fleet's probe cost
// fall inside one RPC, and the health monitor sidecar applies its --timeout to an entire paginated
// sweep rather than to a single page: a fleet too large to walk inside that budget was cut off
// mid-sweep and restarted from the beginning next time, so its tail was never checked at all.
// Reconciling separately makes ListVolumes a pure cache read, so the sidecar's deadline stops being
// a limit on how many volumes can be monitored.
type volumeHealthReconciler struct {
	cs    *ControllerServer
	cache *volumeConditionCache
}

func newVolumeHealthReconciler(cs *ControllerServer, cache *volumeConditionCache) *volumeHealthReconciler {
	return &volumeHealthReconciler{cs: cs, cache: cache}
}

// Start satisfies controller-runtime's manager.Runnable. It is registered as a leader-election
// runnable, so exactly one controller replica probes the fleet and the loop stops when leadership is
// lost or the process shuts down.
func (r *volumeHealthReconciler) Start(ctx context.Context) error {
	// The manager hands runnables a bare context. zerolog's log.Ctx returns a *disabled* logger when
	// the context carries none, so deriving from it here would silently swallow every line this loop
	// writes. Build from the global logger instead, and attach it so log.Ctx works downstream.
	logger := log.With().Str("component", "volume-health-reconciler").Logger()
	ctx = logger.WithContext(ctx)

	logger.Info().
		Dur("interval", volumeHealthReconcileInterval).
		Int("concurrency", volumeHealthProbeConcurrency).
		Msg("Starting volume health reconciler")

	for {
		r.reconcileOnce(ctx)

		select {
		case <-ctx.Done():
			logger.Info().Msg("Volume health reconciler stopped")
			return nil
		case <-time.After(volumeHealthReconcileInterval):
		}
	}
}

// reconcileOnce probes every volume of this driver once and refreshes the cache. It never returns an
// error: a sweep is best-effort background work, and one unreachable volume must not stop the rest.
func (r *volumeHealthReconciler) reconcileOnce(ctx context.Context) {
	op := "ReconcileVolumeHealth"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	logger := log.Ctx(ctx)

	started := time.Now()
	pvs, err := r.cs.listDriverPersistentVolumes(ctx, "")
	if err != nil {
		logger.Error().Err(err).Msg("Failed to list volumes for health reconciliation")
		return
	}

	// One filesystem lookup per filesystem for the whole sweep, rather than per volume. Bounds the
	// staleness to a single sweep: a filesystem removed mid-sweep is still reported as present until
	// the next one.
	filesystems := newFilesystemCache()
	live := make(map[string]struct{}, len(pvs))

	var abnormal, unknown, failed, quotaMissing, backfilled, backfillSkipped int64
	var counters sync.Mutex

	var probes errgroup.Group
	probes.SetLimit(volumeHealthProbeConcurrency)
	for _, pv := range pvs {
		handle := pv.Spec.CSI.VolumeHandle
		live[handle] = struct{}{}
		probes.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			volume, condition, vol, health, err := r.cs.describeVolume(ctx, pv, filesystems)

			// Deliberately before the counters lock: this can issue Weka API calls, and holding the
			// lock across them would serialise the whole sweep behind one volume.
			created, backfillErr := r.backfillMissingQuota(ctx, vol, pv, health)

			counters.Lock()
			defer counters.Unlock()
			if err != nil {
				// Keep the previous entry rather than overwriting it with "unknown", so a transient
				// API failure does not erase a condition that was good a moment ago. It ages out
				// through volumeHealthMaxAge if the failure persists.
				logger.Warn().Err(err).Str("volume_id", handle).Msg("Failed to probe volume health")
				failed++
				return nil
			}
			if health != nil && health.QuotaMissing {
				quotaMissing++
			}
			switch {
			case created:
				backfilled++
			case backfillErr != nil:
				backfillSkipped++
			}

			entry := volumeConditionEntry{capacity: volume.CapacityBytes, probedAt: time.Now()}
			if condition != nil {
				entry.known = true
				entry.abnormal = condition.Abnormal
				entry.message = condition.Message
				if condition.Abnormal {
					abnormal++
				}
			} else {
				unknown++
			}
			r.cache.store(handle, entry)
			return nil
		})
	}
	_ = probes.Wait()

	cached := r.cache.retainOnly(live)
	logger.Info().
		Int("volumes", len(pvs)).
		Int("cached", cached).
		Int64("abnormal", abnormal).
		Int64("unknown", unknown).
		Int64("failed", failed).
		Int64("quotas_missing", quotaMissing).
		Int64("quotas_created", backfilled).
		Int64("quotas_not_created", backfillSkipped).
		Dur("duration", time.Since(started)).
		Msg("Volume health reconciliation completed")
}

// provisionedByAnnotation marks a PersistentVolume that a CSI provisioner created. Kubernetes sets
// it on dynamic provisioning and never on a PersistentVolume an administrator wrote by hand, which
// is what separates the two cases here.
const provisionedByAnnotation = "pv.kubernetes.io/provisioned-by"

// quotaEnforcementFromPv reports whether a quota created for this volume should be hard or soft.
//
// The StorageClass parameters a volume was provisioned with are persisted verbatim into the
// PersistentVolume's volumeAttributes, so capacityEnforcement is readable there long after the
// StorageClass itself may have changed - and a StorageClass cannot be edited anyway. For a
// statically provisioned volume the administrator writes those attributes directly, so the same
// lookup honours their choice too.
//
// Absent means hard, matching what provisioning does with an unset parameter.
func quotaEnforcementFromPv(pv *v1.PersistentVolume) (bool, error) {
	if pv == nil || pv.Spec.CSI == nil {
		return true, nil
	}
	return getCapacityEnforcementParam(pv.Spec.CSI.VolumeAttributes)
}

// isStaticallyProvisioned reports whether this PersistentVolume was written by an administrator
// rather than created by the provisioner.
func isStaticallyProvisioned(pv *v1.PersistentVolume) bool {
	if pv == nil {
		return false
	}
	_, ok := pv.Annotations[provisionedByAnnotation]
	return !ok
}

// backfillMissingQuota gives a volume a quota sized from its PersistentVolume, when it has none.
//
// Some volumes have no quota on the Weka cluster, and so no capacity enforcement at all: their
// declared size is recorded only in an extended attribute, which nothing checks, and the volume can
// grow past it unnoticed. Giving them a quota is what eventually allows the extended-attribute path
// to be removed altogether.
//
// Statically provisioned volumes are a separate case behind their own setting. They never had a
// quota by design - the driver did not create them, and their documented behaviour is that an
// administrator sets any quota themselves - so giving them one changes state somebody else owns,
// and silently starts enforcing a limit that was not being enforced before.
//
// The PersistentVolume is the source of truth for the capacity, not the extended attribute. The
// attribute is at best a copy of the same number and at worst stale, and reading it would mean
// mounting the volume - which this reconciler otherwise never does.
//
// Returns whether a quota was created. An error means the volume needs a quota but did not get one;
// it is reported by the caller and never aborts the sweep, since one volume that cannot be given a
// quota must not stop the rest from getting theirs.
func (r *volumeHealthReconciler) backfillMissingQuota(ctx context.Context, vol *Volume, pv *v1.PersistentVolume, health *VolumeHealth) (bool, error) {
	config := r.cs.getConfig()
	if vol == nil || health == nil || !health.QuotaMissing {
		return false, nil
	}
	logger := log.Ctx(ctx).With().Str("volume_id", vol.GetId()).Logger()

	// Whether the volume has a quota comes from the probe that has just run, which had to resolve
	// the inode and fetch the quota anyway. Asking again here would repeat both calls for every
	// volume in the sweep - two extra Weka API requests per volume, per interval, on a fleet that
	// can run to five figures.
	if !config.backfillMissingQuotas {
		logger.Debug().Msg("Volume has no quota and its capacity is not enforced; backfillMissingQuotas is off")
		return false, nil
	}

	if isStaticallyProvisioned(pv) && !config.setQuotaOnStaticVolumes {
		logger.Debug().Msg("Volume has no quota and is statically provisioned; setQuotaOnStaticVolumes is off")
		return false, nil
	}

	// The capacity declared on the PersistentVolume, which is what the quota has to match. Note this
	// is deliberately not the capacity the probe reported: that can come from the backend, and on a
	// volume with no quota the backend has no limit to report. A PersistentVolume carrying no
	// capacity at all gives nothing to size a quota from, and guessing would silently cap the volume
	// at the wrong number.
	capacity := pvCapacityBytes(pv)
	if capacity <= 0 {
		err := errors.New("volume has no declared capacity to size a quota from")
		logger.Warn().Err(err).Msg("Not backfilling quota")
		return false, err
	}

	// Whether the cluster can do this cheaply. Creating a quota over a directory that already holds
	// data makes the cluster walk the whole tree stamping the quota ID onto every file; a data
	// services container runs that walk in the background, and without one it runs inline. Refusing
	// here keeps a fleet-wide sweep from issuing walks that block on the cluster's management path.
	support, err := vol.apiClient.SupportsQuotaOnNonEmptyDirectory(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("Could not determine whether the cluster can quota a non-empty directory")
		return false, err
	}
	if support != apiclient.QuotaOnNonEmptyDirectorySupported {
		err := errors.New(quotaBackfillRemedy(support, vol.FilesystemName, capacity))
		logger.Warn().Err(err).Msg("Cannot backfill quota for volume")
		return false, err
	}

	// Hard or soft comes from the volume itself, never from a default here: a volume provisioned
	// with capacityEnforcement=SOFT must not be quietly given a hard quota, which would start
	// failing writes that the volume was deliberately allowed to make.
	enforceCapacity, err := quotaEnforcementFromPv(pv)
	if err != nil {
		logger.Warn().Err(err).Msg("Volume has an unusable capacityEnforcement, not setting a quota")
		return false, err
	}

	logger.Info().Int64("capacity", capacity).Bool("enforce_capacity", enforceCapacity).
		Msg("Volume has no quota, creating one from its PersistentVolume")
	if _, err := vol.setQuota(ctx, &enforceCapacity, uint64(capacity)); err != nil {
		logger.Error().Err(err).Msg("Failed to create quota for volume")
		return false, err
	}
	logger.Info().Int64("capacity", capacity).Bool("enforce_capacity", enforceCapacity).
		Msg("Created quota for volume")
	return true, nil
}

// quotaBackfillRemedy turns the reason a backfill cannot happen into something an operator can act
// on. Each case has a different fix, and saying only "unsupported" would send them looking in the
// wrong place.
func quotaBackfillRemedy(support apiclient.QuotaOnNonEmptyDirectorySupport, filesystemName string, capacity int64) string {
	switch support {
	case apiclient.QuotaOnNonEmptyDirectoryNoContainer:
		return "the Weka cluster has no data services container, which is required to set a quota on a " +
			"directory that already holds data - deploy one to let quotas be backfilled automatically"
	case apiclient.QuotaOnNonEmptyDirectoryVersionTooOld:
		return fmt.Sprintf("the Weka cluster is older than %s and cannot set a quota on a directory that "+
			"already holds data - either upgrade it, or set the quota externally from a host with the Weka "+
			"client, with the filesystem mounted: weka fs quota set <path> --filesystem %s --type directory "+
			"--hard %d", apiclient.MinimumSupportedWekaVersions.DataServicesContainer,
			filesystemName, capacity)
	default:
		return "could not determine whether the Weka cluster can set a quota on a directory that already holds data"
	}
}
