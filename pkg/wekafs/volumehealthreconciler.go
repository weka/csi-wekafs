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
	// labels are this volume's weka_csi_volume_health_status label values (see
	// csiVolumeLabelValues), or nil if a probe has never resolved an API client for it. Carrying
	// these means retainOnly can delete the metric series for a volume that disappears without
	// needing the PersistentVolume to still exist to rebuild them.
	labels []string
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

// stale reports whether this entry's probe result is too old to serve, or to report the volume's
// live status as anything but unknown.
func (e volumeConditionEntry) stale() bool {
	return time.Since(e.probedAt) > volumeHealthMaxAge
}

// lookup returns the entry for a volume handle if one exists and is still fresh enough to serve.
func (c *volumeConditionCache) lookup(handle string) (volumeConditionEntry, bool) {
	c.RLock()
	defer c.RUnlock()
	entry, ok := c.entries[handle]
	if !ok || entry.stale() {
		return volumeConditionEntry{}, false
	}
	return entry, true
}

// store records a probe result. If entry carries no labels - a probe that never resolved an API
// client, e.g. a Secret that was temporarily unreadable - the previous entry's labels are kept
// rather than cleared, since it is the label identity of the volume's metric series, and it is only
// ever cleared for real by retainOnly, once the volume itself is gone. Without this, a single
// transient failure would strand the series unlabeled until the volume's next successful probe.
func (c *volumeConditionCache) store(handle string, entry volumeConditionEntry) {
	c.Lock()
	defer c.Unlock()
	if entry.labels == nil {
		if previous, ok := c.entries[handle]; ok {
			entry.labels = previous.labels
		}
	}
	c.entries[handle] = entry
}

// classifyVolumeHealth turns a probe outcome into the weka_csi_volume_health_status value. It is the
// one place that decides what healthy/abnormal/unknown mean, so the per-sweep tally (computed from a
// fresh probe's condition) and the per-volume gauge (re-derived from a cache entry, including its
// staleness) can't drift apart into two different definitions.
func classifyVolumeHealth(known, abnormal bool) float64 {
	if !known {
		return volumeHealthStatusUnknown
	}
	if abnormal {
		return volumeHealthStatusAbnormal
	}
	return volumeHealthStatusHealthy
}

// volumeHealthStatusSample is one series' worth of weka_csi_volume_health_status to report: the
// label values identify it, value is what to set it to.
type volumeHealthStatusSample struct {
	labels []string
	value  float64
}

// retainOnly drops entries for volumes that no longer exist, so the cache tracks the fleet rather
// than growing forever with deleted volumes, and in the same locked pass reports the current
// weka_csi_volume_health_status sample for every volume that remains. Re-deriving every live
// sample here - not only for the volumes a sweep actually probed - is what turns a volume stuck
// failing probes into "unknown" once its last good result ages past volumeHealthMaxAge, instead of
// leaving its gauge parked at a stale value forever. Doing both in one pass, rather than a second
// full lock-and-scan after this one, matters at a fleet size in the tens of thousands.
func (c *volumeConditionCache) retainOnly(handles map[string]struct{}) (remaining int, removed [][]string, live []volumeHealthStatusSample) {
	c.Lock()
	defer c.Unlock()
	for handle, entry := range c.entries {
		if _, ok := handles[handle]; !ok {
			if entry.labels != nil {
				removed = append(removed, entry.labels)
			}
			delete(c.entries, handle)
			continue
		}
		if entry.labels != nil {
			live = append(live, volumeHealthStatusSample{
				labels: entry.labels,
				value:  classifyVolumeHealth(entry.known && !entry.stale(), entry.abnormal),
			})
		}
	}
	return len(c.entries), removed, live
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

	driverName := r.cs.getConfig().GetDriver().name

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

	var healthy, abnormal, unknown, failed int64
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
			volume, condition, labels, err := r.cs.describeVolume(ctx, pv, filesystems)
			if err != nil {
				// Keep the previous entry rather than overwriting it with "unknown", so a transient
				// API failure does not erase a condition that was good a moment ago. It ages out
				// through volumeHealthMaxAge if the failure persists.
				logger.Warn().Err(err).Str("volume_id", handle).Msg("Failed to probe volume health")
				counters.Lock()
				failed++
				counters.Unlock()
				return nil
			}

			// Built without the counters lock held: none of it touches shared state, and computing it
			// there would serialize this across every one of the volumeHealthProbeConcurrency
			// goroutines for no reason.
			entry := volumeConditionEntry{capacity: volume.CapacityBytes, probedAt: time.Now(), labels: labels}
			known := condition != nil
			isAbnormal := known && condition.Abnormal
			if known {
				entry.known = true
				entry.abnormal = condition.Abnormal
				entry.message = condition.Message
			}

			counters.Lock()
			switch classifyVolumeHealth(known, isAbnormal) {
			case volumeHealthStatusHealthy:
				healthy++
			case volumeHealthStatusAbnormal:
				abnormal++
			default:
				unknown++
			}
			counters.Unlock()

			r.cache.store(handle, entry)
			return nil
		})
	}
	_ = probes.Wait()

	cached, removedLabels, liveStatuses := r.cache.retainOnly(live)
	for _, removed := range removedLabels {
		controllerMetrics.VolumeHealth.Status.DeleteLabelValues(removed...)
	}
	for _, sample := range liveStatuses {
		controllerMetrics.VolumeHealth.Status.WithLabelValues(sample.labels...).Set(sample.value)
	}

	duration := time.Since(started)
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "healthy").Set(float64(healthy))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "abnormal").Set(float64(abnormal))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "unknown").Set(float64(unknown))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "failed").Set(float64(failed))
	controllerMetrics.VolumeHealth.SweepDuration.WithLabelValues(driverName).Observe(duration.Seconds())
	controllerMetrics.VolumeHealth.LastSweepTimestamp.WithLabelValues(driverName).Set(float64(time.Now().Unix()))

	logger.Info().
		Int("volumes", len(pvs)).
		Int("cached", cached).
		Int64("healthy", healthy).
		Int64("abnormal", abnormal).
		Int64("unknown", unknown).
		Int64("failed", failed).
		Dur("duration", duration).
		Msg("Volume health reconciliation completed")
}
