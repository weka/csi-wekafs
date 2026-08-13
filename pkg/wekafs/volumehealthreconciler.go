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
	"slices"
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
	// labels are this volume's weka_csi_volume_health_status label values (see
	// csiVolumeLabelValues), or nil if a probe has never resolved an API client for it. Carrying
	// these means retainOnly can delete the metric series for a volume that disappears without
	// needing the PersistentVolume to still exist to rebuild them.
	labels []string
	// conditions are the condition label values last reported for this volume (see
	// VolumeHealth.Conditions). Kept so a condition that clears can have its series deleted, rather
	// than sitting at 1 forever once the volume recovers.
	conditions []string
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
// It returns the conditions this volume carried before and no longer does, so the caller can delete
// those series. A condition gauge that is only ever set would stay at 1 after the volume recovered.
func (c *volumeConditionCache) store(handle string, entry volumeConditionEntry) (cleared []string) {
	c.Lock()
	defer c.Unlock()
	previous, existed := c.entries[handle]
	if entry.labels == nil && existed {
		entry.labels = previous.labels
	}
	if existed {
		for _, was := range previous.conditions {
			if !slices.Contains(entry.conditions, was) {
				cleared = append(cleared, was)
			}
		}
	}
	c.entries[handle] = entry
	return cleared
}

// forget removes a volume's entry immediately, independent of retainOnly's sweep-driven eviction.
// DeleteVolume calls this so a deleted volume's weka_csi_volume_health_status series doesn't have to
// wait for the next reconciler sweep to be pruned. Returns the entry's labels so the caller can
// delete the metric series, or nil if the handle was never cached or was cached without labels (a
// probe that never resolved an API client for it, see volumeConditionEntry.labels).
func (c *volumeConditionCache) forget(handle string) []string {
	c.Lock()
	defer c.Unlock()
	entry, ok := c.entries[handle]
	delete(c.entries, handle)
	if !ok {
		return nil
	}
	return entry.labels
}

// setVolumeConditionSeries and deleteVolumeConditionSeries are the only places the condition label is
// appended to a volume's label values, so the order stays in step with the metric's declaration -
// LabelsForCsiVolumes followed by "condition". Both no-op on a volume with no labels, which is a
// volume whose credentials never resolved and so has no series to key on.
func setVolumeConditionSeries(labels []string, conditions []string) {
	for _, condition := range conditions {
		if values := volumeConditionLabelValues(labels, condition); values != nil {
			controllerMetrics.VolumeHealth.Conditions.WithLabelValues(values...).Set(1)
		}
	}
}

func deleteVolumeConditionSeries(labels []string, conditions []string) {
	for _, condition := range conditions {
		if values := volumeConditionLabelValues(labels, condition); values != nil {
			controllerMetrics.VolumeHealth.Conditions.DeleteLabelValues(values...)
		}
	}
}

// volumeConditionLabelValues copies rather than appending in place: labels is the slice held in the
// cache entry, and append would write the condition into its spare capacity, so two conditions on
// one volume would overwrite each other's series key.
func volumeConditionLabelValues(labels []string, condition string) []string {
	if labels == nil {
		return nil
	}
	values := make([]string, 0, len(labels)+1)
	values = append(values, labels...)
	return append(values, condition)
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
	labels     []string
	value      float64
	conditions []string
}

// removedVolume is a volume that has gone away, and everything needed to delete its series.
type removedVolume struct {
	labels     []string
	conditions []string
}

// retainOnly drops entries for volumes that no longer exist, so the cache tracks the fleet rather
// than growing forever with deleted volumes, and in the same locked pass reports the current
// weka_csi_volume_health_status sample for every volume that remains. Re-deriving every live
// sample here - not only for the volumes a sweep actually probed - is what turns a volume stuck
// failing probes into "unknown" once its last good result ages past volumeHealthMaxAge, instead of
// leaving its gauge parked at a stale value forever. Doing both in one pass, rather than a second
// full lock-and-scan after this one, matters at a fleet size in the tens of thousands.
func (c *volumeConditionCache) retainOnly(handles map[string]struct{}) (remaining int, removed []removedVolume, live []volumeHealthStatusSample) {
	c.Lock()
	defer c.Unlock()
	for handle, entry := range c.entries {
		if _, ok := handles[handle]; !ok {
			if entry.labels != nil {
				removed = append(removed, removedVolume{labels: entry.labels, conditions: entry.conditions})
			}
			delete(c.entries, handle)
			continue
		}
		if entry.labels != nil {
			live = append(live, volumeHealthStatusSample{
				labels:     entry.labels,
				value:      classifyVolumeHealth(entry.known && !entry.stale(), entry.abnormal),
				conditions: entry.conditions,
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

	driverName := r.cs.driverName()

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

	var healthy, abnormal, unknown, failed, quotaMissing, quotaMismatch, noApiClient, backfilled, backfillSkipped int64
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
			volume, condition, vol, health, labels, err := r.cs.describeVolume(ctx, pv, filesystems)

			// Deliberately outside the counters lock: this can issue Weka API calls, and holding the
			// lock across them would serialise the whole sweep behind one volume.
			created, backfillErr := r.backfillMissingQuota(ctx, vol, pv, health)
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
			entry := volumeConditionEntry{
				capacity:   volume.CapacityBytes,
				probedAt:   time.Now(),
				labels:     labels,
				conditions: health.Conditions(),
			}
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
			// The quota tallies are independent of the health status above: a volume with no quota is
			// reported abnormal only when the driver is configured to, so it can be counted here while
			// still classifying as healthy.
			if health != nil && health.QuotaMissing {
				quotaMissing++
			}
			if health != nil && health.QuotaMismatch {
				quotaMismatch++
			}
			if health != nil && health.NoApiClient {
				noApiClient++
			}
			switch {
			case created:
				backfilled++
			case backfillErr != nil:
				backfillSkipped++
			}
			counters.Unlock()

			cleared := r.cache.store(handle, entry)
			deleteVolumeConditionSeries(entry.labels, cleared)
			return nil
		})
	}
	_ = probes.Wait()

	cached, removedLabels, liveStatuses := r.cache.retainOnly(live)
	for _, gone := range removedLabels {
		controllerMetrics.VolumeHealth.Status.DeleteLabelValues(gone.labels...)
		deleteVolumeConditionSeries(gone.labels, gone.conditions)
	}
	for _, sample := range liveStatuses {
		controllerMetrics.VolumeHealth.Status.WithLabelValues(sample.labels...).Set(sample.value)
		setVolumeConditionSeries(sample.labels, sample.conditions)
	}

	duration := time.Since(started)
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "healthy").Set(float64(healthy))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "abnormal").Set(float64(abnormal))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "unknown").Set(float64(unknown))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "failed").Set(float64(failed))
	// Condition counts, reported whatever the reportAs...Abnormal settings are - the same reasoning
	// as the per-volume conditions series. These overlap the statuses above rather than partitioning
	// them: with the flags off a volume counted under no_quota is also counted as healthy.
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, volumeConditionNoQuota).Set(float64(quotaMissing))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, volumeConditionQuotaMismatch).Set(float64(quotaMismatch))
	controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, volumeConditionNoApiClient).Set(float64(noApiClient))
	controllerMetrics.VolumeHealth.SweepDuration.WithLabelValues(driverName).Observe(duration.Seconds())
	controllerMetrics.VolumeHealth.LastSweepTimestamp.WithLabelValues(driverName).Set(float64(time.Now().Unix()))

	logger.Info().
		Int("volumes", len(pvs)).
		Int("cached", cached).
		Int64("healthy", healthy).
		Int64("abnormal", abnormal).
		Int64("unknown", unknown).
		Int64("failed", failed).
		Int64("quotas_missing", quotaMissing).
		Int64("quota_mismatches", quotaMismatch).
		Int64("volumes_without_api_client", noApiClient).
		Int64("quotas_created", backfilled).
		Int64("quotas_not_created", backfillSkipped).
		Dur("duration", duration).
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

// quotaGracePeriodFromPv reports the grace period a soft quota for this volume should carry, from
// the same persisted StorageClass parameters as the enforcement mode. Absent means 0, matching
// provisioning.
func quotaGracePeriodFromPv(pv *v1.PersistentVolume) (uint64, error) {
	if pv == nil || pv.Spec.CSI == nil {
		return 0, nil
	}
	return getQuotaGracePeriodParam(pv.Spec.CSI.VolumeAttributes)
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

	// Hard or soft comes from the volume itself, never from a default here: a volume provisioned
	// with capacityEnforcement=SOFT must not be quietly given a hard quota, which would start
	// failing writes that the volume was deliberately allowed to make.
	enforceCapacity, err := quotaEnforcementFromPv(pv)
	if err != nil {
		logger.Warn().Err(err).Msg("Volume has an unusable capacityEnforcement, not setting a quota")
		return false, err
	}

	// A soft quota is only as useful as its grace period, and that comes from the same persisted
	// parameters. setQuota reads it off the volume, which was built from an ID and so never had the
	// StorageClass parameters applied - leaving it at 0, which means "never block" rather than the
	// period that was asked for.
	graceSeconds, err := quotaGracePeriodFromPv(pv)
	if err != nil {
		logger.Warn().Err(err).Msg("Volume has an unusable quotaGracePeriod, not setting a quota")
		return false, err
	}
	vol.quotaGracePeriodSeconds = graceSeconds

	// Everything above comes from the PersistentVolume, so a volume that is misconfigured fails
	// without a round trip to the cluster. Only now ask the cluster anything.
	if vol.apiClient == nil {
		err := errors.New("volume is not bound to a Weka API client")
		logger.Warn().Err(err).Msg("Cannot create quota for volume")
		return false, err
	}

	// Attempt it rather than predicting whether it can succeed.
	//
	// Creating a quota over a directory that already holds data makes the cluster walk the whole
	// tree stamping the quota ID onto every file - the colouring task - which a data services
	// container runs in the background. An EMPTY directory needs no colouring at all and succeeds on
	// any cluster, and whether this directory is empty is something only the cluster knows: finding
	// out here would mean mounting the volume, which this reconciler never does. So the request goes
	// out, and the cluster reports what it could not do.
	logger.Info().Int64("capacity", capacity).Bool("enforce_capacity", enforceCapacity).
		Uint64("grace_seconds", graceSeconds).
		Msg("Volume has no quota, creating one from its PersistentVolume")
	if _, err := vol.setQuota(ctx, &enforceCapacity, uint64(capacity)); err != nil {
		err = fmt.Errorf("%w. %s", err, r.quotaFailureRemedy(ctx, vol, capacity))
		logger.Error().Err(err).Msg("Failed to create quota for volume")
		return false, err
	}
	logger.Info().Int64("capacity", capacity).Bool("enforce_capacity", enforceCapacity).
		Uint64("grace_seconds", graceSeconds).Msg("Created quota for volume")
	return true, nil
}

// quotaFailureRemedy explains what to do about a quota that could not be created, based on what the
// cluster is able to do. It is only ever called after a failure, so the cost of asking is paid once
// per unrepairable volume rather than once per volume.
//
// The likeliest cause is a directory that already holds data on a cluster that cannot colour it in
// the background, and each version of that has a different fix - saying only "failed" would send an
// operator looking in the wrong place.
func (r *volumeHealthReconciler) quotaFailureRemedy(ctx context.Context, vol *Volume, capacity int64) string {
	support, err := vol.apiClient.SupportsQuotaOnNonEmptyDirectory(ctx)
	if err != nil {
		return "The Weka cluster could not be asked whether it can set a quota on a directory that already holds data"
	}
	return quotaBackfillRemedy(support, vol.FilesystemName, capacity)
}

// quotaBackfillRemedy turns the reason a backfill cannot happen into something an operator can act
// on. Each case has a different fix, and saying only "unsupported" would send them looking in the
// wrong place.
func quotaBackfillRemedy(support apiclient.QuotaOnNonEmptyDirectorySupport, filesystemName string, capacity int64) string {
	switch support {
	case apiclient.QuotaOnNonEmptyDirectoryNoContainer:
		return "If the directory already holds data, this needs a data services container on the Weka " +
			"cluster to colour the existing files, and the cluster has none - deploy one and the quota " +
			"will be created on a later sweep"
	case apiclient.QuotaOnNonEmptyDirectoryVersionTooOld:
		return fmt.Sprintf("If the directory already holds data, this needs a data services container to "+
			"colour the existing files, and the Weka cluster is older than %s so it cannot run one. Either "+
			"upgrade it, or set the quota externally from a host with the Weka client and the filesystem "+
			"mounted: weka fs quota set <path> --filesystem %s --type directory --hard %d",
			apiclient.MinimumSupportedWekaVersions.DataServicesContainer, filesystemName, capacity)
	default:
		return "The Weka cluster reports that it can colour an existing directory, so this is not a " +
			"data services problem"
	}
}
