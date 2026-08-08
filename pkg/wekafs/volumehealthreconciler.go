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
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"
)

const (
	// volumeHealthReconcileInterval is how long the reconciler waits between finishing one sweep of
	// every volume and starting the next. A sweep that overruns simply delays the next one.
	volumeHealthReconcileInterval = 5 * time.Minute
	// volumeHealthProbeConcurrency bounds how many volumes the reconciler probes at once, and so
	// caps the sustained Weka API call rate it can produce.
	volumeHealthProbeConcurrency = 10
	// volumeHealthMaxAge is how long a probe result may be served before it is reported as unknown
	// instead. Generous relative to the interval, so a slow or partially failing sweep degrades to
	// stale-but-useful rather than blanking every condition at once.
	volumeHealthMaxAge = 30 * time.Minute
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

	var abnormal, unknown, failed int64
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
			volume, condition, err := r.cs.describeVolume(ctx, pv, filesystems)

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
		Dur("duration", time.Since(started)).
		Msg("Volume health reconciliation completed")
}
