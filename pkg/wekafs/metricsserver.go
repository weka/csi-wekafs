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
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

const (
	// VolumeMetricsBufferSize sizes volumeMetricsChan, the queue of freshly fetched per-volume
	// statistics awaiting a report to Prometheus. Sized generously so a slow reporter (part 2) does
	// not immediately back-pressure the fetchers.
	VolumeMetricsBufferSize = 10000
	// MetricsServerVolumeLimit bounds how many PersistentVolumes a single streaming cycle will hand
	// off for processing, so one runaway cluster cannot grow the tracked set without limit. It can be
	// overridden via the MAXIMUM_PERSISTENT_VOLUME_COUNT environment variable.
	MetricsServerVolumeLimit = 100000
)

// SecretsStore caches the Kubernetes Secrets the metrics server has read, keyed by
// "namespace/name", so that streaming the same PersistentVolumes on every cycle does not mean
// re-reading their Secret from the apiserver every time.
type SecretsStore struct {
	secrets map[string]*v1.Secret
	sync.Mutex
}

func NewSecretsStore() *SecretsStore {
	return &SecretsStore{
		secrets: make(map[string]*v1.Secret),
	}
}

// secretCacheKey is the SecretsStore lookup key for a Secret reference.
func secretCacheKey(secretNamespace, secretName string) string {
	return secretNamespace + "/" + secretName
}

// MetricsServer discovers this driver's PersistentVolumes and keeps a live set of the volumes
// worth polling for capacity and performance statistics.
//
// This is the lifecycle and PersistentVolume discovery half of the metrics server: construction,
// the controller-runtime manager it rides on, and the streamer/processor pair that keeps
// volumeMetrics and observedFilesystems in sync with what Kubernetes has. Fetching statistics from
// Weka, refreshing quota maps, and reporting to Prometheus are a separate piece built on top of
// this one.
type MetricsServer struct {
	nodeID            string
	api               *ApiStore
	config            *DriverConfig
	driver            *WekaFsDriver
	secrets           *SecretsStore
	volumeMetrics     *VolumeMetrics
	prometheusMetrics *PrometheusMetrics

	// persistentVolumesChan carries PersistentVolumes from PersistentVolumeStreamer to
	// PersistentVolumeStreamProcessor.
	persistentVolumesChan chan *v1.PersistentVolume
	// volumeMetricsChan carries freshly fetched statistics from the fetchers (fetchSingleMetric,
	// GetMetricsFromQuotaMap) to MetricsReportStreamer, which is the only reader.
	volumeMetricsChan chan *VolumeMetric

	quotaMaps           *QuotaMapsPerFilesystem
	observedFilesystems *ObservedFilesystems // tracks observed filesystem UIDs, their reference counts, and API clients

	// capacityFetchRunning guards PeriodicSingleMetricsFetcher's fetch cycles against overlapping
	// invocations: if one cycle is still running when the next tick fires, the next tick is skipped
	// rather than piling another full fetch on top of it. It is an atomic.Bool because it is read and
	// written from more than one goroutine.
	capacityFetchRunning atomic.Bool
}

// getMounter satisfies AnyServer. The metrics server never mounts a filesystem directly - it only
// ever talks to the Weka API - so this returns nil rather than panicking. Volume.MountUnderlyingFS
// already turns a nil mounter into a clean error (see volume.go), so a volume whose inode cannot be
// resolved through the API on a metrics-server-driven cluster simply reports "inode unknown"
// instead of crashing the process.
func (ms *MetricsServer) getMounter(ctx context.Context) AnyMounter {
	return nil
}

// getMounterByTransport mirrors getMounter: the metrics server has no mounter for any transport.
func (ms *MetricsServer) getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	return nil
}

func (ms *MetricsServer) getApiStore() *ApiStore {
	return ms.api
}

func (ms *MetricsServer) getConfig() *DriverConfig {
	return ms.config
}

func (ms *MetricsServer) isInDevMode() bool {
	return ms.getConfig().isInDevMode()
}

func (ms *MetricsServer) getDefaultMountOptions() MountOptions {
	return getDefaultMountOptions().MergedWith(NewMountOptionsFromString(NodeServerAdditionalMountOptions), ms.getConfig().mutuallyExclusiveOptions)
}

func (ms *MetricsServer) getNodeId() string {
	return ms.driver.nodeID
}

// NewMetricsServer initializes a new MetricsServer for driver. It returns an error rather than
// panicking when driver is nil, since a construction failure here should be handled by the caller
// like any other startup error, not crash the process.
func NewMetricsServer(driver *WekaFsDriver) (*MetricsServer, error) {
	if driver == nil {
		return nil, errors.New("cannot create MetricsServer: driver is nil")
	}
	ret := &MetricsServer{
		nodeID:                driver.nodeID,
		api:                   driver.api,
		config:                driver.config,
		driver:                driver,
		secrets:               NewSecretsStore(),
		volumeMetrics:         NewVolumeMetrics(),
		prometheusMetrics:     NewPrometheusMetrics(),
		persistentVolumesChan: make(chan *v1.PersistentVolume, driver.config.metricsFetchConcurrentRequests),
		volumeMetricsChan:     make(chan *VolumeMetric, VolumeMetricsBufferSize),
		quotaMaps:             NewQuotaMapsPerFilesystem(),
	}
	ret.observedFilesystems = NewObservedFilesystems()

	ret.prometheusMetrics.server.FetchMetricsFrequencySeconds.Set(ret.getConfig().metricsFetchInterval.Seconds())
	ret.prometheusMetrics.server.QuotaCacheValiditySeconds.Set(ret.getConfig().quotaCacheValidityDuration.Seconds())

	return ret, nil
}

// MetricsServerCollectors returns the metrics server's own Prometheus collectors for main.go to
// register, or nil if the metrics server is disabled or failed to construct. Nothing in this
// package calls prometheus.MustRegister - that only ever happens in main.go, once for the whole
// process, so importing this package never has the side effect of registering anything.
func (driver *WekaFsDriver) MetricsServerCollectors() []prometheus.Collector {
	if driver.ms == nil {
		return nil
	}
	return driver.ms.prometheusMetrics.Collectors()
}

// PersistentVolumeStreamer periodically lists this driver's PersistentVolumes and streams the
// eligible ones to persistentVolumesChan for PersistentVolumeStreamProcessor to pick up, then prunes
// tracked volumes that disappeared from the list.
func (ms *MetricsServer) PersistentVolumeStreamer(ctx context.Context) {
	component := "PersistentVolumeStreamer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	out := ms.persistentVolumesChan

	for {
		cycleStart := time.Now()
		logger.Info().Msg("Fetching existing persistent volumes")
		pvList := &v1.PersistentVolumeList{}

		volumeLimit := MetricsServerVolumeLimit
		// override the maximum count of PersistentVolumes to fetch from environment variable if set
		if maxCountStr := os.Getenv("MAXIMUM_PERSISTENT_VOLUME_COUNT"); maxCountStr != "" {
			if maxCount, err := strconv.ParseInt(maxCountStr, 10, 64); err == nil {
				volumeLimit = int(maxCount)
			}
		}

		err := ms.driver.manager.GetClient().List(ctx, pvList)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch PersistentVolumes, no statistics will be available, will retry in 10 seconds")
			ms.prometheusMetrics.server.FetchPvBatchOperationFailureCount.Inc()
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}

		d := time.Since(cycleStart).Seconds()
		ms.prometheusMetrics.server.FetchPvBatchOperationsInvokeCount.Inc()
		ms.prometheusMetrics.server.FetchPvBatchOperationsDurationSeconds.Add(d)
		ms.prometheusMetrics.server.FetchPvBatchOperationsDurationHistogram.Observe(d)
		ms.prometheusMetrics.server.MonitoredPersistentVolumesGauge.Set(float64(ms.volumeMetrics.Len()))

		logger.Info().Int("pv_count", len(pvList.Items)).Msg("Fetched list of PersistentVolumes, streaming them for processing")

		// Always sort the response so we get the volumes in same order for processing (especially if trimmed)
		slices.SortFunc(pvList.Items, func(a, b v1.PersistentVolume) int {
			if a.GetUID() < b.GetUID() {
				return -1
			}
			return 1
		})

		items := make([]*v1.PersistentVolume, 0, len(pvList.Items))
		for i := range pvList.Items {
			pv := &pvList.Items[i]
			if err := ms.ensurePersistentVolumeValid(pv); err != nil {
				logger.Trace().Str("pv_name", pv.Name).Err(err).Msg("Skipping processing a PersistentVolume, not valid")
				continue
			}
			items = append(items, pv)
		}

		// Limit the number of PersistentVolumes to the specified limit, having already sorted them so
		// the same volumes are streamed in the same order every cycle.
		if len(items) > volumeLimit {
			logger.Info().Int("pv_count", len(items)).Int("limit", volumeLimit).Msg("Trimming PersistentVolumes list to the limit")
			items = items[:volumeLimit]
		}
		ms.prometheusMetrics.server.StreamPvBatchSize.Set(float64(len(pvList.Items)))
		ms.prometheusMetrics.server.FetchPvBatchSize.Set(float64(len(items)))

		for _, pv := range items {
			select {
			case <-ctx.Done():
				return
			case out <- pv:
				ms.prometheusMetrics.server.StreamPvOperationsCount.Inc()
			}
		}

		// after all PVs are already streamed, prune old volumes (those that are not in the current
		// list but were measured before)
		ms.pruneOldVolumes(ctx, items)

		interval := ms.getConfig().metricsFetchInterval
		logger.Info().Int("pv_count_total", len(pvList.Items)).Int("pv_count_eligible", len(items)).Dur("wait_duration", interval).Msg("Sent all volumes to processing, waiting for next fetch")

		// refresh list of volumes every metricsFetchInterval, but wake up early on shutdown
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// pruneOldVolumes removes tracked volumes whose PersistentVolume is no longer in pvList.
func (ms *MetricsServer) pruneOldVolumes(ctx context.Context, pvList []*v1.PersistentVolume) {
	component := "pruneOldVolumes"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Debug().Msg("Pruning stale volumes from metrics collection")

	startTime := time.Now()
	var pruneCount float64
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.server.PruneVolumesBatchInvokeCount.Inc()
		ms.prometheusMetrics.server.PruneVolumesBatchSize.Set(pruneCount)
		ms.prometheusMetrics.server.PruneVolumesBatchDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.PruneVolumesBatchDurationHistogram.Observe(dur)
		if pruneCount > 0 {
			logger.Info().Int("pruned_volumes", int(pruneCount)).Msg("Pruned stale PersistentVolumes from metrics collection")
		}
	}()

	currentUIDs := make(map[types.UID]struct{}, len(pvList))
	for _, pv := range pvList {
		currentUIDs[pv.UID] = struct{}{}
	}
	// Remove metrics for UIDs not present in the current PV list
	for _, uid := range ms.volumeMetrics.PvUIDs() {
		if _, exists := currentUIDs[uid]; exists {
			continue
		}
		if !ms.volumeMetrics.Has(uid) {
			continue // already pruned by another goroutine
		}
		pruneCount++
		ms.pruneVolumeMetric(ctx, uid)
	}
}

// pruneVolumeMetric stops tracking one PersistentVolume: it drops it from volumeMetrics, deletes its
// Prometheus label values so a removed volume's series stop being exported rather than going stale,
// and, if that was the last volume on its filesystem, releases the filesystem from
// observedFilesystems and forgets its cached quota map.
//
// Unlike the version this was ported from, this does not re-resolve the volume's filesystem through
// the Weka API to find what to release - the filesystem and inode a volume was tracked under are
// already known (VolumeMetrics.Add recorded them), so releasing it needs no API call and cannot fail
// because the cluster happens to be unreachable at prune time.
func (ms *MetricsServer) pruneVolumeMetric(ctx context.Context, pvUID types.UID) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "pruneVolumeMetric")
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	vm := ms.volumeMetrics.Get(pvUID)
	if vm == nil {
		return // nothing to remove, if it was already removed by another goroutine
	}
	defer ms.prometheusMetrics.server.PersistentVolumeRemovalsCount.Inc()

	ms.volumeMetrics.Remove(pvUID)
	if ms.observedFilesystems.DecRef(vm.key.filesystemUid) {
		ms.quotaMaps.Forget(vm.key.filesystemUid)
	}
	if vm.persistentVolume != nil {
		ms.removePrometheusMetricsForLabels(ctx, vm)
	}
	logger.Info().Str("pv_uid", string(pvUID)).Msg("Removed persistent volume from metric collection")
}

// PersistentVolumeStreamProcessor reads PersistentVolumes off persistentVolumesChan and processes up
// to metricsFetchConcurrentRequests of them at once.
func (ms *MetricsServer) PersistentVolumeStreamProcessor(ctx context.Context) {
	component := "PersistentVolumeStreamProcessor"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting processing of PersistentVolumes")
	sem := make(chan struct{}, ms.getConfig().metricsFetchConcurrentRequests)
	sampledLogger := logger.Sample(&zerolog.BasicSampler{N: 100})
	for {
		select {
		case <-ctx.Done():
			return
		case pv, ok := <-ms.persistentVolumesChan:
			if !ok || pv == nil {
				return
			}
			sem <- struct{}{} // acquire semaphore
			go func(pv *v1.PersistentVolume) {
				defer func() { <-sem }() // release semaphore
				ms.processSinglePersistentVolume(ctx, pv)
				ms.prometheusMetrics.server.FetchPvBatchOperationsSuccessCount.Inc()
				sampledLogger.Info().Str("pv_name", pv.Name).Msg("Processing persistent volume completed. This is sampled log")
			}(pv)
		}
	}
}

// processSinglePersistentVolume resolves one PersistentVolume into a tracked VolumeMetric: it reads
// the volume's Secret, builds an API client from it, resolves the backing filesystem and the
// volume's inode, and adds it to volumeMetrics. Any failure along the way simply skips the volume -
// it will be retried the next time PersistentVolumeStreamer cycles.
func (ms *MetricsServer) processSinglePersistentVolume(ctx context.Context, pv *v1.PersistentVolume) {
	component := "processSinglePersistentVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	startTime := time.Now()
	defer func() {
		dur := time.Since(startTime)
		ms.prometheusMetrics.server.ProcessPvOperationsCount.Inc()
		ms.prometheusMetrics.server.ProcessPvOperationsDurationSeconds.Add(dur.Seconds())
		ms.prometheusMetrics.server.ProcessPvOperationsDurationHistogram.Observe(dur.Seconds())
	}()

	// if volume was marked for deletion, do nothing - it will be pruned once it drops out of the list
	if pv.DeletionTimestamp != nil {
		return
	}

	// Check if the PersistentVolume is already being processed
	if vm := ms.volumeMetrics.Get(pv.UID); vm != nil {
		vm.persistentVolume = pv // Update the PersistentVolume reference in the existing VolumeMetric
		return
	}

	logger.Debug().Str("pv_name", pv.Name).Str("phase", string(pv.Status.Phase)).Msg("Received a PersistentVolume for processing")

	// ensurePersistentVolumeValid (run by the streamer before this volume was ever queued) already
	// guarantees CSI and NodePublishSecretRef are set; guarded again here since nothing enforces that
	// this is only ever called after that check.
	if pv.Spec.CSI == nil || pv.Spec.CSI.NodePublishSecretRef == nil {
		logger.Trace().Str("pv_name", pv.Name).Msg("PersistentVolume has no NodePublishSecretRef, skipping")
		return
	}

	secret, err := ms.fetchSecret(ctx, pv.Spec.CSI.NodePublishSecretRef.Name, pv.Spec.CSI.NodePublishSecretRef.Namespace)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to fetch secret for PersistentVolume, skipping")
		return
	}
	secretData := make(map[string]string, len(secret.Data))
	for key, value := range secret.Data {
		secretData[key] = string(value)
	}
	if endpoints := os.Getenv("OVERRIDE_API_ENDPOINTS"); endpoints != "" {
		// Override API endpoints from environment variable if set
		secretData["endpoints"] = endpoints
	}
	apiClient, err := ms.getApiStore().fromSecrets(ctx, secretData, ms.nodeID)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create API client from secret, skipping PersistentVolume")
		return
	}

	volume, err := NewVolumeFromId(ctx, pv.Spec.CSI.VolumeHandle, apiClient, ms)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create Volume from ID")
		return
	}

	fsObj, err := volume.apiClient.CachedGetFileSystemByName(ctx, volume.FilesystemName, ms.getConfig().quotaCacheValidityDuration)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, skipping PersistentVolume")
		return
	}

	// we still want to validate the object; by-UID is faster than by-name
	volume.fileSystemObject = fsObj
	ensuredFsObj := &apiclient.FileSystem{}
	if err := volume.apiClient.GetFileSystemByUid(ctx, fsObj.Uid, ensuredFsObj, false); err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, skipping PersistentVolume")
		return
	}
	volume.fileSystemObject = ensuredFsObj

	// Prepopulate the inode id so metrics can be matched to a quota later without resolving it
	// again. A volume whose inode cannot be resolved is still tracked, with an unknown inode: it
	// reports the capacity Kubernetes knows about, and tracking it is what stops the streamer
	// rediscovering and reprocessing it - at four API calls a time - on every cycle.
	inodeId, err := volume.getInodeId(ctx)
	if err != nil {
		logger.Trace().Err(err).Str("pv_name", pv.Name).Msg("Failed to resolve inode ID for volume, tracking it without one")
		inodeId = 0
	}

	// The reference is taken only once the volume is definitely going to be tracked, since it is
	// released when the volume is pruned. Taking it earlier and then returning would leave a
	// reference nothing ever drops, and the filesystem would be polled forever.
	ms.observedFilesystems.IncRef(ensuredFsObj, apiClient)

	metric := &VolumeMetric{
		persistentVolume: pv,
		volume:           volume,
		metrics:          nil,
		secret:           secret,
		apiClient:        apiClient,
	}
	ms.volumeMetrics.Add(pv.UID, volumeKey{filesystemUid: ensuredFsObj.Uid, inodeId: inodeId}, metric)
	ms.prometheusMetrics.server.PersistentVolumeAdditionsCount.Inc()
	logger.Debug().Str("pv_name", pv.Name).Dur("duration", time.Since(startTime)).Msg("Added PersistentVolume for metrics processing")
}

// ensurePersistentVolumeValid reports why a PersistentVolume is not a candidate for metrics
// collection, or nil if it is.
func (ms *MetricsServer) ensurePersistentVolumeValid(pv *v1.PersistentVolume) error {
	// Filter for Weka CSI volumes of current driver only
	if pv.Spec.CSI == nil {
		return errors.New("pv is not a CSI volume")
	}
	if pv.Spec.CSI.NodePublishSecretRef == nil {
		return errors.New("pv is not valid, NodePublishSecretRef is not provided")
	}
	if pv.Spec.CSI.Driver != ms.driver.name {
		return errors.New("pv is not a WEKA CSI volume or not belonging to this driver")
	}
	if len(pv.Spec.Capacity) == 0 {
		return errors.New("pv has a zero capacity, half-baked volume possible")
	}
	if len(pv.Spec.CSI.VolumeAttributes) == 0 {
		return errors.New("pv is missing volumeAttributes")
	}
	if !slices.Contains(KnownVolTypes[:], VolumeType(pv.Spec.CSI.VolumeAttributes["volumeType"])) {
		return errors.New("pv is missing volumeType or has an unsupported volumeType")
	}
	if pv.Status.Phase != v1.VolumeBound && pv.Status.Phase != v1.VolumeReleased {
		return fmt.Errorf("pv is not in a valid phase: %s", pv.Status.Phase)
	}
	return nil // Valid PersistentVolume
}

// fetchSecret retrieves a Kubernetes Secret by name and namespace, caching it for future use. The
// apiserver read (through the manager's uncached API reader, so no Secret informer is started) is
// deliberately made without holding ms.secrets' lock, so one slow read cannot stall lookups of a
// different, already-cached Secret.
func (ms *MetricsServer) fetchSecret(ctx context.Context, secretName, secretNamespace string) (*v1.Secret, error) {
	component := "fetchSecret"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("secret_name", secretName).Str("secret_namespace", secretNamespace).Logger()

	if secretName == "" || secretNamespace == "" {
		return nil, errors.New("secret name and namespace must be provided")
	}
	key := secretCacheKey(secretNamespace, secretName)

	ms.secrets.Lock()
	secret, exists := ms.secrets.secrets[key]
	ms.secrets.Unlock()
	if exists {
		logger.Trace().Msg("Using a secret from cache")
		return secret, nil
	}

	logger.Debug().Msg("Fetching Secret")
	fetched := &v1.Secret{}
	// Deliberately an uncached read: going through the manager's cached client would start an
	// informer that mirrors every Secret in the cluster into this process's memory.
	nsName := types.NamespacedName{Name: secretName, Namespace: secretNamespace}
	if err := ms.driver.manager.GetAPIReader().Get(ctx, nsName, fetched); err != nil {
		return nil, fmt.Errorf("failed to fetch secret %s/%s: %w", secretNamespace, secretName, err)
	}

	ms.secrets.Lock()
	ms.secrets.secrets[key] = fetched
	ms.secrets.Unlock()
	return fetched, nil
}

// InvalidateSecret removes a Secret from the cache. Call this after getting an authentication error
// from an API client built from it - likely because the Secret was rotated - so the next volume that
// needs it re-reads the current contents instead of the stale, cached ones.
func (ms *MetricsServer) InvalidateSecret(ctx context.Context, secretName, secretNamespace string) {
	if secretName == "" || secretNamespace == "" {
		return
	}
	key := secretCacheKey(secretNamespace, secretName)
	ms.secrets.Lock()
	defer ms.secrets.Unlock()
	delete(ms.secrets.secrets, key)
}

// quotaToUsageStats turns one filesystem quota into the capacity view a PersistentVolume reports.
// Free is derived rather than reported by the API, since a quota describes a hard limit rather than
// a used/free split.
func quotaToUsageStats(q *apiclient.Quota, ts time.Time) *UsageStats {
	used := q.UsedBytes
	return &UsageStats{
		Capacity:  int64(q.HardLimitBytes),
		Used:      int64(used),
		Free:      int64(q.HardLimitBytes - used),
		Timestamp: ts,
	}
}

// fetchPvUsageStatsFromWeka fetches one volume's usage directly: resolve its inode, then ask the
// Weka API for the quota at that inode. This is one API round trip per volume, which is fine at
// small scale but is exactly what the quota-map path (fetchSinglePvUsageStatsFromQuotaMap) exists to
// avoid at larger scale.
func (ms *MetricsServer) fetchPvUsageStatsFromWeka(ctx context.Context, vm *VolumeMetric) (*UsageStats, error) {
	v := vm.volume
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get inode ID for PersistentVolume %s: %w", vm.pvName(), err)
	}
	fsObj, err := v.getFilesystemObj(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem object for PersistentVolume %s: %w", vm.pvName(), err)
	}
	if fsObj == nil {
		return nil, fmt.Errorf("failed to get filesystem object for PersistentVolume %s", vm.pvName())
	}
	quotaEntry, err := v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch quota for inode ID %d: %w", inodeId, err)
	}
	if quotaEntry == nil {
		return nil, fmt.Errorf("no quota entry found for inode ID %d", inodeId)
	}
	// apiclient.Quota carries no per-quota fetch timestamp on this branch (dev's quotaEntry had
	// LastUpdateTime; here that field only exists at the QuotaMap level), so the reading is stamped
	// with the time it was actually fetched.
	return quotaToUsageStats(quotaEntry, time.Now()), nil
}

// fetchPvUsageStatsFromWekaWithCache is fetchPvUsageStatsFromWeka with a per-volume cache in front of
// it, so a volume whose last reading is still within quotaCacheValidityDuration is served from
// memory instead of costing another API round trip.
func (ms *MetricsServer) fetchPvUsageStatsFromWekaWithCache(ctx context.Context, vm *VolumeMetric) (*UsageStats, error) {
	v := vm.volume
	if v.lastUsageStats == nil || time.Since(v.lastUsageStats.Timestamp) > ms.getConfig().quotaCacheValidityDuration {
		usageStats, err := ms.fetchPvUsageStatsFromWeka(ctx, vm)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch usage stats from Weka for PersistentVolume %s: %w", vm.pvName(), err)
		}
		v.lastUsageStats = usageStats
	}
	return v.lastUsageStats, nil
}

// fetchSinglePvUsageStatsFromQuotaMap resolves one volume's usage from its filesystem's cached quota
// map instead of a per-volume API call. The map itself has to already be cached - refreshing it here
// on a miss would turn every volume's read into the very per-volume API traffic this path exists to
// avoid; refreshing it is batchRefreshQuotaMaps' job.
func (ms *MetricsServer) fetchSinglePvUsageStatsFromQuotaMap(ctx context.Context, vm *VolumeMetric) (*UsageStats, error) {
	v := vm.volume
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get inode ID for PersistentVolume %s: %w", vm.pvName(), err)
	}
	fsObj, err := v.getFilesystemObj(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem object for PersistentVolume %s: %w", vm.pvName(), err)
	}
	if fsObj == nil {
		return nil, fmt.Errorf("failed to get filesystem object for PersistentVolume %s", vm.pvName())
	}
	quotaMap, err := ms.GetQuotaMapForFilesystem(ctx, fsObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get quota map for filesystem %s: %w", fsObj.Name, err)
	}
	quotaEntry := quotaMap.GetQuotaForInodeId(inodeId)
	if quotaEntry == nil {
		return nil, fmt.Errorf("no quota entry found for inode ID %d in cached quota map for filesystem %s", inodeId, fsObj.Name)
	}
	return quotaToUsageStats(quotaEntry, quotaMap.LastUpdate), nil
}

// FetchPvStatsFromQuotaMap is FetchPvStats' implementation for useQuotaMapsForMetrics=true.
func (ms *MetricsServer) FetchPvStatsFromQuotaMap(ctx context.Context, vm *VolumeMetric) (*PvStats, error) {
	usageStats, err := ms.fetchSinglePvUsageStatsFromQuotaMap(ctx, vm)
	if err != nil {
		return nil, err
	}
	return &PvStats{Usage: usageStats}, nil
}

// FetchPvStatsFromWeka is FetchPvStats' implementation for useQuotaMapsForMetrics=false.
func (ms *MetricsServer) FetchPvStatsFromWeka(ctx context.Context, vm *VolumeMetric) (*PvStats, error) {
	usageStats, err := ms.fetchPvUsageStatsFromWekaWithCache(ctx, vm)
	if err != nil {
		return nil, err
	}
	return &PvStats{Usage: usageStats}, nil
}

// FetchPvStats fetches one volume's usage statistics, routing to the filesystem-wide quota map or to
// a direct per-volume API call depending on how the driver is configured.
func (ms *MetricsServer) FetchPvStats(ctx context.Context, vm *VolumeMetric) (*PvStats, error) {
	if ms.getConfig().useQuotaMapsForMetrics {
		return ms.FetchPvStatsFromQuotaMap(ctx, vm)
	}
	return ms.FetchPvStatsFromWeka(ctx, vm)
}

// fetchSingleMetric fetches statistics for one tracked volume and, on success, hands it to
// MetricsReportStreamer over volumeMetricsChan.
func (ms *MetricsServer) fetchSingleMetric(ctx context.Context, vm *VolumeMetric) error {
	component := "fetchSingleMetric"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Str("pv_name", vm.pvName()).Logger().WithContext(ctx)

	startTime := time.Now()
	ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsInvokeCount.Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsDurationHistogram.Observe(dur)
	}()

	pvStats, err := ms.FetchPvStats(ctx, vm)
	if err != nil {
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsFailureCount.Inc()
		return fmt.Errorf("failed to fetch metric for persistent volume %s: %w", vm.pvName(), err)
	}

	vm.metrics = pvStats
	select {
	case ms.volumeMetricsChan <- vm:
	case <-ctx.Done():
		return ctx.Err()
	}
	ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsSuccessCount.Inc()
	return nil
}

// FetchMetricsOneByOne fetches statistics for every currently tracked volume, one Weka API call at a
// time per volume, bounded to quotaFetchConcurrentRequests concurrent fetches. It is the
// useQuotaMapsForMetrics=false counterpart to batchRefreshQuotaMaps.
//
// Unlike the version this was ported from, this actually waits for every fetch to finish before
// returning: that version created a WaitGroup but never called Add on it, so Wait returned
// immediately and the duration/success metrics recorded in the caller measured how long it took to
// launch the fetches, not how long they took to complete.
func (ms *MetricsServer) FetchMetricsOneByOne(ctx context.Context) error {
	component := "FetchMetricsOneByOne"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	startTime := time.Now()
	vms := ms.volumeMetrics.All()
	ms.prometheusMetrics.server.FetchMetricsBatchSize.Set(float64(len(vms)))
	ms.prometheusMetrics.server.FetchMetricsBatchOperationsInvokeCount.Inc()

	var succeeded atomic.Bool
	succeeded.Store(true)
	defer func() {
		dur := time.Since(startTime).Seconds()
		if succeeded.Load() {
			ms.prometheusMetrics.server.FetchMetricsBatchOperationsSuccessCount.Inc()
		} else {
			ms.prometheusMetrics.server.FetchMetricsBatchOperationsFailureCount.Inc()
		}
		ms.prometheusMetrics.server.FetchMetricsBatchOperationsDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.FetchMetricsBatchOperationsDurationHistogram.Observe(dur)
		if dur > ms.getConfig().metricsFetchInterval.Seconds() {
			logger.Warn().Int("pv_count", len(vms)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetching metrics took longer than the configured interval, consider increasing metricsFetchInterval or metricsFetchConcurrentRequests")
		} else {
			logger.Info().Int("pv_count", len(vms)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetched metrics for PersistentVolumes")
		}
	}()

	logger.Info().Int("pv_count", len(vms)).Msg("Starting to fetch metrics for PersistentVolumes")
	sem := make(chan struct{}, ms.getConfig().quotaFetchConcurrentRequests)
	var wg sync.WaitGroup
	for _, vm := range vms {
		if vm == nil || vm.persistentVolume == nil {
			// could happen if it was already pruned while this loop was building its snapshot
			continue
		}
		sem <- struct{}{} // acquire a slot in the semaphore
		wg.Add(1)
		go func(vm *VolumeMetric) {
			defer wg.Done()
			defer func() { <-sem }() // release the slot in the semaphore
			if err := ms.fetchSingleMetric(ctx, vm); err != nil {
				succeeded.Store(false)
				logger.Warn().Err(err).Str("pv_name", vm.pvName()).Msg("Failed to fetch metric for persistent volume")
			}
		}(vm)
	}
	wg.Wait()
	return nil
}

// GetMetricsFromQuotaMap updates every tracked volume on qm's filesystem from a freshly fetched
// quota map, and hands each of them to MetricsReportStreamer over volumeMetricsChan.
//
// It walks the map's own distinct inodes rather than the filesystem's tracked volumes, and for each
// one calls VolumeMetrics.ForInode to reach every PersistentVolume sharing it: the version this was
// ported from assumed one PersistentVolume per inode and updated only the first match, so a
// filesystem where two PVs point at the same static path (ordinary under static provisioning) would
// silently stop reporting for one of them.
func (ms *MetricsServer) GetMetricsFromQuotaMap(ctx context.Context, qm *apiclient.QuotaMap) {
	component := "GetMetricsFromQuotaMap"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if qm == nil {
		logger.Error().Msg("QuotaMap is nil, cannot update volume metrics from it")
		return
	}

	// ForFilesystem is only used to enumerate the distinct inodes this filesystem's tracked volumes
	// sit at; ForInode below is what actually looks up who to update at each one, since several
	// PersistentVolumes can share an inode.
	seenInodes := make(map[uint64]struct{})
	for _, vm := range ms.volumeMetrics.ForFilesystem(qm.FileSystemUid) {
		inodeId := vm.key.inodeId
		if inodeId == 0 {
			logger.Trace().Str("pv_name", vm.pvName()).Msg("Volume has no known inode ID, skipping")
			continue
		}
		if _, ok := seenInodes[inodeId]; ok {
			continue
		}
		seenInodes[inodeId] = struct{}{}

		q := qm.GetQuotaForInodeId(inodeId)
		if q == nil {
			// Not necessarily an error: a quota that lives in a snapshot view is absent from the
			// filesystem-wide quota list, while a direct per-inode lookup still finds it. That is
			// exactly the shape of a snapshot-backed volume, so skipping here would drop every one
			// of them from reporting - silently, since nothing else on this path counts a failure.
			// Falling back keeps batch mode a superset of the per-volume path, at a cost bounded by
			// the number of volumes the map cannot serve, which is normally zero.
			ms.reportVolumesMissingFromQuotaMap(ctx, qm, inodeId)
			continue
		}
		stats := &PvStats{Usage: quotaToUsageStats(q, qm.LastUpdate)}

		for _, target := range ms.volumeMetrics.ForInode(qm.FileSystemUid, inodeId) {
			target.metrics = stats
			select {
			case ms.volumeMetricsChan <- target:
			case <-ctx.Done():
				return
			}
		}
	}
}

// reportVolumesMissingFromQuotaMap fetches, one at a time, the volumes sitting at an inode the
// filesystem's quota map has no entry for, and hands them to MetricsReportStreamer.
//
// The per-volume fetch it falls back to is cached for quotaCacheValidityDuration exactly as it is on
// the non-batch path, so a volume permanently absent from the map costs one API request per cache
// period rather than one per cycle.
func (ms *MetricsServer) reportVolumesMissingFromQuotaMap(ctx context.Context, qm *apiclient.QuotaMap, inodeId uint64) {
	logger := log.Ctx(ctx)
	targets := ms.volumeMetrics.ForInode(qm.FileSystemUid, inodeId)
	if len(targets) == 0 {
		return
	}
	// LabelsForFilesystemOps, the same three the other quota-map counters carry: driver, cluster,
	// filesystem. WithLabelValues panics on a count mismatch rather than returning an error, and
	// this path only runs when a map is actually missing an entry, so a wrong count here is a crash
	// that no amount of ordinary running would reveal.
	clusterGuid := ""
	if apiClient := ms.observedFilesystems.GetApiClient(qm.FileSystemUid); apiClient != nil {
		clusterGuid = apiClient.ClusterGuid.String()
	}
	for _, target := range targets {
		ms.prometheusMetrics.server.QuotaMapMissCount.
			WithLabelValues(ms.driver.name, clusterGuid, target.volume.FilesystemName).Inc()
		usage, err := ms.fetchPvUsageStatsFromWekaWithCache(ctx, target)
		if err != nil {
			// Logged with the PersistentVolume name, not just the inode: an inode ID on its own
			// cannot be traced back to a volume without querying the cluster.
			logger.Warn().Err(err).Uint64("inode_id", inodeId).Str("pv_name", target.pvName()).
				Msg("Volume is absent from its filesystem's quota map and could not be fetched directly")
			continue
		}
		target.metrics = &PvStats{Usage: usage}
		select {
		case ms.volumeMetricsChan <- target:
		case <-ctx.Done():
			return
		}
	}
}

// MetricsReportStreamer reads freshly fetched statistics off volumeMetricsChan and reports them to
// Prometheus.
func (ms *MetricsServer) MetricsReportStreamer(ctx context.Context) {
	component := "MetricsReportStreamer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting to report metrics for PersistentVolumes")
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Context cancelled, stopping reporting of metrics")
			return
		case metric, ok := <-ms.volumeMetricsChan:
			if !ok {
				logger.Info().Msg("volumeMetricsChan closed, stopping reporting of metrics")
				return
			}
			// metric.persistentVolume is guarded here too (not just where it is added to
			// volumeMetricsChan): createPrometheusLabelsForMetric dereferences it, and a nil check at
			// the one call site that fills the channel today is easy to lose track of as more
			// producers are added.
			if metric == nil || metric.metrics == nil || metric.persistentVolume == nil {
				continue
			}
			u := metric.metrics.Usage
			p := metric.metrics.Performance
			labelValues := ms.createPrometheusLabelsForMetric(metric)

			if u != nil {
				logger.Trace().Str("pv_name", metric.pvName()).Msg("Reporting metrics for PersistentVolume")
				ms.prometheusMetrics.volumes.CapacityBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Capacity), u.Timestamp)
				ms.prometheusMetrics.volumes.UsedBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Used), u.Timestamp)
				ms.prometheusMetrics.volumes.FreeBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Free), u.Timestamp)
			}
			if p != nil {
				ms.prometheusMetrics.volumes.ReadsTotal.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.Reads), p.Timestamp)
				ms.prometheusMetrics.volumes.WritesTotal.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.Writes), p.Timestamp)
				ms.prometheusMetrics.volumes.ReadBytesTotal.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.ReadBytes), p.Timestamp)
				ms.prometheusMetrics.volumes.WriteBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.WriteBytes), p.Timestamp)
				ms.prometheusMetrics.volumes.ReadDurationUs.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.ReadLatencyUs), p.Timestamp)
				ms.prometheusMetrics.volumes.WriteDurationUs.WithLabelValues(labelValues...).SetWithTimestamp(float64(p.WriteLatencyUs), p.Timestamp)
			}
			if u != nil || p != nil {
				ms.prometheusMetrics.server.ReportedMetricsSuccessCount.Inc()
			} else {
				ms.prometheusMetrics.server.ReportedMetricsFailureCount.Inc()
			}
		}
	}
}

// createPrometheusLabelsForMetric builds the LabelsForCsiVolumes label values for one volume.
// organizationLabel names the Weka tenant a volume belongs to. Credentials leave Organization empty
// to mean the root organization, so that is spelled out rather than exported as a blank label, which
// would be indistinguishable from "not known" in a query.
func organizationLabel(client *apiclient.ApiClient) string {
	if client == nil || client.Credentials.Organization == "" {
		return apiclient.RootOrganizationName
	}
	return client.Credentials.Organization
}

func (ms *MetricsServer) createPrometheusLabelsForMetric(metric *VolumeMetric) []string {
	return csiVolumeLabelValues(
		ms.driver.name,
		metric.persistentVolume,
		metric.apiClient.ClusterGuid.String(),
		metric.volume.FilesystemName,
		string(metric.volume.GetBackingType()),
		organizationLabel(metric.apiClient),
	)
}

// removePrometheusMetricsForLabels deletes every Prometheus series for one volume, e.g. once it is
// pruned. Without this, a removed volume's last-reported values (and, for the plain, non-timed
// PvReportedCapacityBytes gauge, its stale value) would keep being exported forever.
func (ms *MetricsServer) removePrometheusMetricsForLabels(ctx context.Context, metric *VolumeMetric) {
	log.Ctx(ctx).Trace().Str("pv_name", metric.pvName()).Msg("Removing Prometheus metric labels for volume")
	labelValues := ms.createPrometheusLabelsForMetric(metric)
	ms.prometheusMetrics.volumes.CapacityBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.UsedBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.FreeBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.PvReportedCapacityBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.ReadsTotal.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.WritesTotal.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.ReadBytesTotal.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.WriteBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.ReadDurationUs.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.volumes.WriteDurationUs.DeleteLabelValues(labelValues...)
}

// PeriodicPersistentVolumeCapacityReporter periodically re-reports every tracked volume's
// Kubernetes-known capacity. It is a fallback, independent of whether Weka statistics are being
// fetched successfully: if the cluster is unreachable, or the useQuotaMapsForMetrics quota map
// hasn't refreshed yet, PvReportedCapacityBytes still reflects what the PersistentVolume claims.
func (ms *MetricsServer) PeriodicPersistentVolumeCapacityReporter(ctx context.Context) {
	component := "PeriodicPersistentVolumeCapacityReporter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting periodic reporting of PersistentVolume capacities once a minute. This is a fallback mechanism to ensure capacities are reported even if metrics are not fetched from the Weka API for some reason.")
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Context cancelled, stopping periodic reporting of PersistentVolume capacities")
			return
		case <-ticker.C:
			ms.reportOnlyPvCapacities(ctx)
		}
	}
}

// reportOnlyPvCapacities reports every tracked volume's Kubernetes-known capacity to
// PvReportedCapacityBytes. Unlike the version this was ported from, which re-looked-up each volume
// by UID one at a time, this takes one snapshot of the tracked set up front via VolumeMetrics.All -
// there is no exported key list to iterate on this branch, and a snapshot is what that call was
// approximating anyway.
func (ms *MetricsServer) reportOnlyPvCapacities(ctx context.Context) {
	component := "reportOnlyPvCapacities"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	vms := ms.volumeMetrics.All()
	logger.Info().Int("pv_count", len(vms)).Msg("Starting to report only PersistentVolume capacities")
	if len(vms) == 0 {
		logger.Info().Msg("No PersistentVolumes found, nothing to report")
		return
	}

	for _, vm := range vms {
		if vm == nil || vm.persistentVolume == nil {
			// could happen if it was already pruned while this loop was building its snapshot
			continue
		}
		r := vm.persistentVolume.Spec.Capacity.Storage()
		if r == nil {
			logger.Warn().Str("pv_name", vm.pvName()).Msg("PersistentVolume capacity is nil, skipping")
			continue
		}
		labels := ms.createPrometheusLabelsForMetric(vm)
		ms.prometheusMetrics.volumes.PvReportedCapacityBytes.WithLabelValues(labels...).Set(float64(r.Value()))
	}
	logger.Info().Int("pv_count", len(vms)).Msg("Finished reporting PersistentVolume capacities")
}

// GetQuotaMapForFilesystem returns the cached quota map for a filesystem, or an error if none has
// been fetched yet. It never fetches: populating the cache is refreshQuotaMapPerFilesystem's job,
// invoked from batchRefreshQuotaMaps on its own schedule.
func (ms *MetricsServer) GetQuotaMapForFilesystem(ctx context.Context, fs *apiclient.FileSystem) (*apiclient.QuotaMap, error) {
	if fs == nil {
		return nil, errors.New("filesystem is nil")
	}
	if fs.Uid == uuid.Nil {
		return nil, errors.New("filesystem UID is empty")
	}
	quotaMap := ms.quotaMaps.GetQuotaMap(fs.Uid)
	if quotaMap == nil {
		return nil, errors.New("quota map not found for filesystem")
	}
	return quotaMap, nil
}

// refreshQuotaMapPerFilesystem fetches a fresh quota map for one filesystem and installs it, unless
// the cached one is still within quotaCacheValidityDuration and force is false.
//
// The per-filesystem lock (from QuotaMapsPerFilesystem.GetLock) is held across the API call rather
// than just around installing the result. That is a deliberate departure from "don't hold a lock
// across I/O": without it, two overlapping refreshes of the same filesystem - which
// batchRefreshQuotaMaps does not otherwise prevent if one cycle is still running when the next
// PeriodicQuotaMapUpdater tick fires - would both see the cached map as stale, both call the API, and
// the loser's response would overwrite the winner's regardless of which was actually newer. Holding
// the lock makes the second refresh observe the first one's fresh result and skip the API call
// entirely via the check below, rather than merely narrowing the window in which the double fetch can
// happen.
//
// Unlike the version this was ported from, the lock is released via defer immediately after it is
// acquired. That version locked, then called the API, and returned on the error path before the
// deferred unlock was ever registered - so a single failed fetch left the filesystem's lock held
// forever, and every later refresh of it (forever after) blocked with no chance of recovering.
func (ms *MetricsServer) refreshQuotaMapPerFilesystem(ctx context.Context, fs *apiclient.FileSystem, force bool) (*apiclient.QuotaMap, error) {
	component := "refreshQuotaMapPerFilesystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if fs == nil {
		return nil, errors.New("filesystem is nil")
	}

	logger.Debug().Str("filesystem", fs.Name).Msg("Updating QuotaMap for filesystem")

	maplock := ms.quotaMaps.GetLock(fs.Uid)
	maplock.Lock()
	defer maplock.Unlock()

	startTime := time.Now()
	defer func() {
		dur := time.Since(startTime)
		logger.Debug().Str("filesystem", fs.Name).Dur("duration", dur).Msg("Finished updating QuotaMap for filesystem")
		if dur > ms.getConfig().metricsFetchInterval {
			logger.Warn().Str("filesystem", fs.Name).Dur("duration", dur).Msg("Updating QuotaMap took longer than expected, consider increasing metricsFetchInterval")
		}
	}()

	// optimization: if the quota map is still valid, skip the update unless force is set
	if existing := ms.quotaMaps.GetQuotaMap(fs.Uid); existing != nil && !force && existing.LastUpdate.Add(ms.getConfig().quotaCacheValidityDuration).After(time.Now()) {
		logger.Debug().Str("filesystem", fs.Name).Msg("QuotaMap is up-to-date, skipping update")
		return existing, nil
	}

	apiClient := ms.observedFilesystems.GetApiClient(fs.Uid)
	if apiClient == nil {
		return nil, fmt.Errorf("no API client found for filesystem UID %s", fs.Uid)
	}

	labelValues := []string{ms.driver.name, apiClient.ClusterGuid.String(), fs.Name}
	ms.prometheusMetrics.server.QuotaMapRefreshInvokeCount.WithLabelValues(labelValues...).Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.server.QuotaMapRefreshDurationSeconds.WithLabelValues(labelValues...).Add(dur)
		ms.prometheusMetrics.server.QuotaMapRefreshDurationHistogram.WithLabelValues(labelValues...).Observe(dur)
	}()

	quotaMap, err := apiClient.GetQuotaMap(ctx, fs)
	if err != nil {
		ms.prometheusMetrics.server.QuotaMapRefreshFailureCount.WithLabelValues(labelValues...).Inc()
		return nil, fmt.Errorf("failed to fetch QuotaMap for filesystem %s: %w", fs.Name, err)
	}
	ms.prometheusMetrics.server.QuotaMapRefreshSuccessCount.WithLabelValues(labelValues...).Inc()

	ms.quotaMaps.SetQuotaMap(fs.Uid, quotaMap)
	return quotaMap, nil
}

// batchRefreshQuotaMaps refreshes the quota map for every observed filesystem, bounded to
// quotaFetchConcurrentRequests concurrent refreshes, then asynchronously pushes the results into
// volumeMetrics via GetMetricsFromQuotaMap. Filesystems are visited least-recently-refreshed first,
// so a batch that cannot get through all of them (more filesystems than the concurrency limit and
// interval allow) makes progress on the ones most overdue rather than starving them indefinitely in
// favor of ones already fresh.
//
// Unlike the version this was ported from, this waits (via a WaitGroup) for every refresh to finish
// before logging the cycle's summary. That version launched every refresh in its own goroutine and
// then immediately computed and logged "cycle duration" and per-filesystem averages from counters
// that had barely had a chance to be incremented - the numbers it reported described how long it took
// to launch the batch, not how long the batch took to run.
func (ms *MetricsServer) batchRefreshQuotaMaps(ctx context.Context, force bool) {
	component := "batchRefreshQuotaMaps"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	startTime := time.Now()
	concurrency := ms.getConfig().quotaFetchConcurrentRequests
	sortedObservedFilesystems := ms.observedFilesystems.ByQuotaUpdateTime()

	validity := ms.getConfig().quotaCacheValidityDuration
	var countNeverUpdated, countUpToDate, countExpired int
	for _, ofs := range sortedObservedFilesystems {
		switch ts := ofs.LastQuotaUpdate(); {
		case ts.IsZero():
			countNeverUpdated++
		case ts.Before(time.Now().Add(-validity)):
			countExpired++
		default:
			countUpToDate++
		}
	}
	batchSize := len(sortedObservedFilesystems)
	logger.Info().Int("never_updated", countNeverUpdated).Int("up_to_date", countUpToDate).Int("expired", countExpired).Int("total", batchSize).Msg("Starting to update quota maps")
	if batchSize == 0 {
		logger.Info().Msg("No observed filesystems to update, skipping batch refresh")
		return
	}

	ms.prometheusMetrics.server.QuotaUpdateBatchInvokeCount.Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.server.QuotaUpdateBatchSuccessCount.Inc()
		ms.prometheusMetrics.server.QuotaUpdateBatchDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.QuotaUpdateBatchDurationHistogram.Observe(dur)
		ms.prometheusMetrics.server.QuotaUpdateBatchSize.Set(float64(batchSize))
	}()

	var totalDurationNanos, countStarted, countSuccessful, countFailed atomic.Int64
	cycleStart := time.Now()
	sampledLogger := logger.Sample(&zerolog.BasicSampler{N: 50})
	concurrencySem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, ofs := range sortedObservedFilesystems {
		fsObj, err := ofs.GetFileSystem(ctx, time.Minute)
		if err != nil || fsObj == nil {
			logger.Error().Err(err).Str("fs_uid", ofs.Uid().String()).Msg("Failed to get filesystem object, skipping")
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case concurrencySem <- struct{}{}:
		}
		countStarted.Add(1)
		wg.Add(1)
		go func(fsObj *apiclient.FileSystem) {
			defer wg.Done()
			defer func() { <-concurrencySem }()
			start := time.Now()
			qm, err := ms.refreshQuotaMapPerFilesystem(ctx, fsObj, force)
			dur := time.Since(start)
			totalDurationNanos.Add(dur.Nanoseconds())

			if err != nil {
				countFailed.Add(1)
				logger.Error().Err(err).Str("filesystem_name", fsObj.Name).Msg("Failed to update quota map for filesystem")
			} else {
				countSuccessful.Add(1)
				ofs.MarkQuotaUpdated(qm.LastUpdate)
				go ms.GetMetricsFromQuotaMap(ctx, qm)
			}
			sampledLogger.Info().Int64("complete_count", countSuccessful.Load()+countFailed.Load()).Dur("duration", dur).Msg("Quota maps batch refresh progress")
		}(fsObj)
	}
	wg.Wait()

	cycleDuration := time.Since(cycleStart)
	countComplete := countSuccessful.Load() + countFailed.Load()
	var avgDurationEffective, avgDurationSuccessful, parallelism float64
	if countComplete > 0 {
		avgDurationEffective = time.Duration(totalDurationNanos.Load()).Seconds() / float64(countComplete)
		parallelism = float64(countComplete) / cycleDuration.Seconds()
	}
	if s := countSuccessful.Load(); s > 0 {
		avgDurationSuccessful = time.Duration(totalDurationNanos.Load()).Seconds() / float64(s)
	}

	logger.Info().Dur("cycle_duration", cycleDuration).
		Float64("parallelism", parallelism).
		Float64("avg_duration_effective", avgDurationEffective).
		Float64("avg_duration_successful", avgDurationSuccessful).
		Int64("count_total", countStarted.Load()).
		Int64("count_successful", countSuccessful.Load()).
		Int64("count_failed", countFailed.Load()).
		Int64("count_completed", countComplete).
		Msg("Quota maps batch refresh completed")
}

// PeriodicQuotaMapUpdater periodically refreshes the quota maps for all observed filesystems. It is
// the useQuotaMapsForMetrics=true counterpart to PeriodicSingleMetricsFetcher.
func (ms *MetricsServer) PeriodicQuotaMapUpdater(ctx context.Context) {
	component := "PeriodicQuotaMapUpdater"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting PeriodicQuotaMapUpdater")
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("PeriodicQuotaMapUpdater context cancelled, stopping...")
			return
		case <-time.After(time.Minute):
			logger.Info().Msg("PeriodicQuotaMapUpdater cycle triggered")
			ms.batchRefreshQuotaMaps(ctx, false)
		}
	}
}

// runSingleMetricsFetchCycle is one tick of PeriodicSingleMetricsFetcher: it guards against
// overlapping with a still-running previous cycle via capacityFetchRunning, then runs
// FetchMetricsOneByOne.
//
// Unlike the version this was ported from, the success/failure counters below are the right way
// around. That version incremented PeriodicFetchMetricsSuccessCount when FetchMetricsOneByOne
// returned an error, and PeriodicFetchMetricsFailureCount when it did not.
func (ms *MetricsServer) runSingleMetricsFetchCycle(ctx context.Context) {
	component := "PeriodicSingleMetricsFetchCycle"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	startTime := time.Now()
	logger.Info().Msg("Periodic fetch metrics cycle triggered")
	ms.prometheusMetrics.server.PeriodicFetchMetricsInvokeCount.Inc()

	if !ms.capacityFetchRunning.CompareAndSwap(false, true) {
		logger.Warn().Msg("Capacity fetch is already running, skipping this cycle. This can happen if the fetch takes longer than the configured interval.")
		ms.prometheusMetrics.server.PeriodicFetchMetricsSkipCount.Inc()
		return
	}
	defer ms.capacityFetchRunning.Store(false)

	logger.Info().Int("pv_count", ms.volumeMetrics.Len()).Msg("Fetching metrics for PersistentVolumes")
	if err := ms.FetchMetricsOneByOne(ctx); err != nil {
		logger.Error().Err(err).Msg("Error fetching metrics")
		ms.prometheusMetrics.server.PeriodicFetchMetricsFailureCount.Inc()
	} else {
		ms.prometheusMetrics.server.PeriodicFetchMetricsSuccessCount.Inc()
	}
	logger.Info().Dur("duration", time.Since(startTime)).Msg("Periodic fetch metrics cycle completed")
}

// PeriodicSingleMetricsFetcher periodically fetches metrics for all tracked PersistentVolumes and
// reports them to Prometheus, one Weka API call per volume. It is only relevant when
// useQuotaMapsForMetrics is false.
//
// It ticks every 30 seconds rather than once per metricsFetchInterval so that outdated volumes are
// swept up gradually through the interval instead of all being fetched in one spike at its end -
// fetching outstanding volumes across several ticks spreads the load on the Weka API more evenly
// than one fetch of everything per interval would.
func (ms *MetricsServer) PeriodicSingleMetricsFetcher(ctx context.Context) {
	component := "PeriodicSingleMetricsFetcher"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Str("interval", ms.getConfig().metricsFetchInterval.String()).Msg("Starting collection of WEKA metrics for PVs")

	const tickInterval = 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Periodic fetch metrics context cancelled, stopping...")
			return
		case <-time.After(tickInterval):
			// Run each cycle in its own goroutine rather than inline, so a fetch that runs long does
			// not delay the next tick from being noticed - runSingleMetricsFetchCycle's own
			// capacityFetchRunning check is what actually prevents them piling up.
			go ms.runSingleMetricsFetchCycle(ctx)
		}
	}
}

// AddToManager wires the metrics server's health/readiness checks into the controller-runtime
// manager and registers a Runnable that only does its work while this pod holds leadership (or
// unconditionally, if leader election is disabled for the manager itself). It must be called before
// the manager is started - controller-runtime rejects Add calls afterward - so it never starts the
// manager itself; that is the driver's job, once every runnable has been registered.
func (ms *MetricsServer) AddToManager() error {
	if ms.driver.manager == nil {
		return errors.New("metrics server has no controller-runtime manager, cannot register")
	}

	logger := log.With().Str("component", "MetricsServer").Logger()

	if err := ms.driver.manager.AddReadyzCheck("leader", func(req *http.Request) error {
		if !ms.getConfig().enableMetricsServerLeaderElection {
			return nil // if leader election is not enabled, we are always ready
		}
		select {
		case <-ms.driver.manager.Elected():
			return nil // we are the leader
		default:
			return fmt.Errorf("not the leader yet")
		}
	}); err != nil {
		logger.Error().Err(err).Msg("Failed to add readiness check for leader election")
		return fmt.Errorf("failed to add readiness check for leader election: %w", err)
	}
	if err := ms.driver.manager.AddHealthzCheck("alwaysReady", healthz.Ping); err != nil {
		logger.Error().Err(err).Msg("Failed to add health check")
		return fmt.Errorf("failed to add health check: %w", err)
	}

	if err := ms.driver.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting MetricsServer processors")

		go ms.PersistentVolumeStreamer(ctx)
		go ms.PersistentVolumeStreamProcessor(ctx)
		go ms.MetricsReportStreamer(ctx)
		go ms.PeriodicPersistentVolumeCapacityReporter(ctx)

		// Depending on configuration, quotas are refreshed either in filesystem-wide batches or one
		// volume at a time; only the matching loop is started.
		if ms.getConfig().useQuotaMapsForMetrics {
			go ms.PeriodicQuotaMapUpdater(ctx)
		} else {
			go ms.PeriodicSingleMetricsFetcher(ctx)
		}

		<-ctx.Done()
		logger.Info().Msg("Leadership lost or shutdown, stopping...")
		return nil
	})); err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		return fmt.Errorf("failed to add MetricsServer runnable to manager: %w", err)
	}

	return nil
}
