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

	// running guards Start/Stop against being invoked more than once concurrently, and against Stop
	// running before Start (or twice). It is an atomic.Bool rather than a field read and written
	// under an ad hoc lock, so the two can race safely.
	running atomic.Bool

	// persistentVolumesChan carries PersistentVolumes from PersistentVolumeStreamer to
	// PersistentVolumeStreamProcessor.
	persistentVolumesChan chan *v1.PersistentVolume
	// volumeMetricsChan carries freshly fetched statistics to the (not yet ported) Prometheus
	// reporter. It is declared here because Stop needs something to close, but nothing in this file
	// writes to it - that arrives with the metrics-fetch half of the metrics server.
	volumeMetricsChan chan *VolumeMetric

	quotaMaps           *QuotaMapsPerFilesystem
	observedFilesystems *ObservedFilesystems // tracks observed filesystem UIDs, their reference counts, and API clients

	// wg tracks the leader-elected Runnable started in Start, so Wait blocks for exactly as long as
	// that Runnable's context remains live (i.e. until leadership is lost or the manager shuts down).
	wg sync.WaitGroup

	// capacityFetchRunning will guard the periodic single-metrics fetch loop (part 2) against
	// overlapping invocations. Declared now, alongside `running`, as an atomic.Bool rather than a
	// plain bool: both fields are read and written from more than one goroutine.
	capacityFetchRunning atomic.Bool
}

// getBackgroundTasksWg returns the WaitGroup tracking the metrics server's background work.
func (ms *MetricsServer) getBackgroundTasksWg() *sync.WaitGroup {
	return &ms.wg
}

// getMounter satisfies AnyServer. The metrics server never mounts a filesystem directly - it only
// ever talks to the Weka API - so this returns nil rather than panicking. Volume.MountUnderlyingFS
// already turns a nil mounter into a clean error (see volume.go), so a volume whose inode cannot be
// resolved through the API on a metrics-server-driven cluster simply reports "inode unknown"
// instead of crashing the process.
func (ms *MetricsServer) getMounter() AnyMounter {
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
		quotaMaps:             NewQuotaMapsPerFilesystem(),
	}
	ret.observedFilesystems = NewObservedFilesystems()

	ret.prometheusMetrics.server.FetchMetricsFrequencySeconds.Set(ret.getConfig().metricsFetchInterval.Seconds())
	ret.prometheusMetrics.server.QuotaCacheValiditySeconds.Set(ret.getConfig().quotaCacheValidityDuration.Seconds())

	return ret, nil
}

// initManager wires the metrics server's health and readiness checks into the controller-runtime
// manager. It returns an error instead of killing the process, so Start can decide how to react.
func (ms *MetricsServer) initManager(ctx context.Context) error {
	if ms.driver.manager == nil {
		return errors.New("metrics server has no controller-runtime manager, cannot start")
	}

	logger := log.Ctx(ctx).With().Str("component", "MetricsServer").Logger()
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
	return nil
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

// pruneVolumeMetric stops tracking one PersistentVolume: it drops it from volumeMetrics and, if
// that was the last volume on its filesystem, releases the filesystem from observedFilesystems and
// forgets its cached quota map.
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
	// Reporting - e.g. deleting the Prometheus label values for this volume - belongs to the metrics
	// fetch/report half of the metrics server, not to PersistentVolume discovery, so it is not done
	// here.
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

// Start brings the metrics server up: it wires health/readiness checks into the controller-runtime
// manager, then registers a Runnable that only does its work while this pod holds leadership (or
// unconditionally, if leader election is disabled for the manager itself), and starts the manager.
//
// Only the PersistentVolume discovery goroutines are started here. Fetching statistics from Weka,
// refreshing quota maps, and reporting to Prometheus are a separate piece not yet wired in.
func (ms *MetricsServer) Start(ctx context.Context) {
	component := "StartMetricsServer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if !ms.running.CompareAndSwap(false, true) {
		logger.Info().Msg("MetricsServer is already running")
		return
	}

	logger.Info().Msg("Starting MetricsServer")

	if err := ms.initManager(ctx); err != nil {
		logger.Error().Err(err).Msg("Failed to initialize MetricsServer manager, cannot start")
		ms.running.Store(false)
		return
	}

	// give the manager's cache a moment to sync before the first PersistentVolume fetch
	time.Sleep(1 * time.Second)
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes")

	ms.wg.Add(1)
	err := ms.driver.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		defer ms.wg.Done()
		logger.Info().Msg("Leader elected, starting MetricsServer processors")

		go ms.PersistentVolumeStreamer(ctx)
		go ms.PersistentVolumeStreamProcessor(ctx)
		// The metrics-fetch, quota-map-refresh, and Prometheus-reporting loops are a separate piece
		// of the metrics server and are deliberately not started here.

		<-ctx.Done()
		logger.Info().Msg("Leadership lost or shutdown, stopping...")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		ms.wg.Done()
		ms.running.Store(false)
		return
	}

	go func() {
		if err := ms.driver.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("MetricsServer manager exited with error")
		}
	}()
}

// Wait blocks until the metrics server's leader-elected work has stopped, i.e. until leadership is
// lost or the manager shuts down.
func (ms *MetricsServer) Wait() {
	ms.wg.Wait()
}

// Stop tears down the metrics server's channels. It is safe to call on a nil receiver, and safe to
// call more than once or before Start - both are no-ops.
func (ms *MetricsServer) Stop(ctx context.Context) {
	if ms == nil {
		return // Nothing to stop
	}
	if !ms.running.CompareAndSwap(true, false) {
		return // already stopped, or never started
	}
	// The channels are deliberately not closed. Their producers run until the context is cancelled
	// and send inside a select on ctx.Done, and a select gives no protection against sending on a
	// closed channel - closing one under a live producer panics. The consumers already exit on
	// ctx.Done, so closing buys nothing, and the channels are collected once unreferenced.
	log.Ctx(ctx).Info().Msg("Metrics server stopped")
}
