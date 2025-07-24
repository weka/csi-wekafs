package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/zerologr"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"go.uber.org/atomic"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"math"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	VolumeMetricsBufferSize = 10000
)

type SecretsStore struct {
	secrets map[int]*v1.Secret // map[secretName/secretNamespace]*v1.Secret
	sync.Mutex
}

func NewSecretsStore() *SecretsStore {
	return &SecretsStore{
		secrets: make(map[int]*v1.Secret),
	}
}

type MetricsServer struct {
	nodeID            string
	api               *ApiStore
	config            *DriverConfig
	driver            *WekaFsDriver
	secrets           *SecretsStore
	volumeMetrics     *VolumeMetrics
	prometheusMetrics *PrometheusMetrics
	running           bool

	manager               ctrl.Manager
	persistentVolumesChan chan *v1.PersistentVolume // channel for streaming PersistentVolume objects for further processing
	volumeMetricsChan     chan *VolumeMetric        // channel for incoming requests

	quotaMaps           *QuotaMapsPerFilesystem
	observedFilesystems *ObservedFilesystems // to track observed filesystem UIDs and their reference counts and API clients (for quota maps periodic updates)

	sync.Mutex
	wg sync.WaitGroup // WaitGroup to manage goroutines

	capacityFetchRunning bool
}

func (ms *MetricsServer) getMounter(ctx context.Context) AnyMounter {
	//TODO implement me
	panic("implement me")
}

func (ms *MetricsServer) getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	//TODO implement me
	panic("implement me")
}

func (ms *MetricsServer) getApiStore() *ApiStore {
	return ms.api
}

func (ms *MetricsServer) getConfig() *DriverConfig {
	return ms.config
}

func (ms *MetricsServer) getDefaultMountOptions() MountOptions {
	return getDefaultMountOptions().MergedWith(NewMountOptionsFromString(NodeServerAdditionalMountOptions), ms.getConfig().mutuallyExclusiveOptions)
}

func (ms *MetricsServer) getNodeId() string {
	return ms.driver.nodeID
}

// NewMetricsServer initializes a new MetricsServer instance
func NewMetricsServer(driver *WekaFsDriver) *MetricsServer {
	if driver == nil {
		panic("Driver is nil")
	}
	//goland:noinspection GoBoolExpressions
	ret := &MetricsServer{
		nodeID:                driver.nodeID,
		api:                   driver.api,
		config:                driver.config,
		driver:                driver,
		secrets:               NewSecretsStore(),
		volumeMetrics:         NewVolumeMetrics(),
		prometheusMetrics:     NewPrometheusMetrics(),
		persistentVolumesChan: make(chan *v1.PersistentVolume, MetricsServerVolumeLimit),
		wg:                    sync.WaitGroup{},

		quotaMaps: NewQuotaMapsPerFilesystem(),
	}
	ret.observedFilesystems = NewObservedFilesystems(ret)

	ret.prometheusMetrics.server.FetchMetricsFrequencySeconds.Set(ret.getConfig().metricsFetchInterval.Seconds())
	ret.prometheusMetrics.server.QuotaCacheValiditySeconds.Set(ret.getConfig().quotaCacheValidityDuration.Seconds())

	return ret

}

// GetK8sApi returns the Kubernetes API client from the driver or KUBECONFIG environment variable.
func (ms *MetricsServer) GetK8sApi() *kubernetes.Clientset {
	return ms.driver.GetK8sApiClient()
}

func (ms *MetricsServer) initManager(ctx context.Context) {
	logger := log.Ctx(ctx)
	config, err := rest.InClusterConfig()
	if err != nil {
		if errors.Is(err, rest.ErrNotInCluster) {
			log.Error().Msg("Not running in a Kubernetes cluster, trying to fetch default kubeconfig")
			// Fallback to using kubeconfig from the local environment
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				log.Error().Msg("KUBECONFIG environment variable is not set")
				Die("KUBECONFIG environment variable is not set, cannot run MetricsServer without it and not in cluster")
			}
			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create config from kubeconfig file")
				Die("Failed to create config from kubeconfig file, cannot run MetricsServer without it")
			}
		} else {
			log.Error().Err(err).Msg("Failed to create in-cluster config")
			Die("Failed to create in-cluster config, cannot run MetricsServer without it")
		}
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	pprofBindAddress := os.Getenv("PPROF_BIND_ADDRESS")
	if pprofBindAddress != "" {
		logger.Info().Str("pprof_bind_address", pprofBindAddress).Msg("Using PPROF_BIND_ADDRESS environment variable for pprof binding address")
	}

	namespace, err := getOwnNamespace()
	if err != nil {
		logger.Error().Msg("Namespace not detected and not set, not using Leader Election mechanism")
	}
	zerologr.NameFieldName = "logger"
	zerologr.NameSeparator = "/"
	var logrLog = zerologr.New(logger)

	ms.manager, err = ctrl.NewManager(config, ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          ms.getConfig().enableMetricsServerLeaderElection,
		LeaderElectionID:        "csimetricsad0b5146.weka.io",
		LeaderElectionNamespace: namespace,
		PprofBindAddress:        pprofBindAddress,
		HealthProbeBindAddress:  ":8081",
	})
	clog.SetLogger(logrLog)

	if err != nil {
		logger.Error().Err(err).Msg("unable to start manager")
		Die("unable to start manager, cannot run MetricsServer without it")
	}

}

// PersistentVolumeStreamer streams PersistentVolumes from Kubernetes, sending them to the provided channel.
func (ms *MetricsServer) PersistentVolumeStreamer(ctx context.Context) {
	component := "PersistentVolumeStreamer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	ctx = context.WithValue(ctx, "start_time", time.Now())
	out := ms.persistentVolumesChan

	for {
		logger.Info().Msg("Fetching existing persistent volumes")
		pvList := &v1.PersistentVolumeList{}

		volumeLimit := MetricsServerVolumeLimit

		// override the maximum count of PersistentVolumes to fetch from environment variable if set
		maxCountStr := os.Getenv("MAXIMUM_PERSISTENT_VOLUME_COUNT")
		if maxCountStr != "" {
			maxCount, err := strconv.ParseInt(maxCountStr, 10, 64)
			if err == nil { // handle error (e.g., log or set a default value)
				volumeLimit = int(maxCount)
			}
		}

		err := ms.manager.GetClient().List(ctx, pvList)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch PersistentVolumes, no statistics will be available, will retry in 10 seconds")
			ms.prometheusMetrics.server.FetchPvBatchOperationFailureCount.Inc()
			time.Sleep(10 * time.Second)
			continue
		}

		d := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		ms.prometheusMetrics.server.FetchPvBatchOperationsInvokeCount.Inc()
		ms.prometheusMetrics.server.FetchPvBatchOperationsDurationSeconds.Add(d)
		ms.prometheusMetrics.server.FetchPvBatchOperationsDurationHistogram.Observe(d)
		ms.prometheusMetrics.server.MonitoredPersistentVolumesGauge.Set(float64(len(ms.volumeMetrics.Metrics)))

		logger.Info().Int("pv_count", len(pvList.Items)).Msg("Fetched list of PersistentVolumes, streaming them for processing")

		// Always sort the response so we get the volumes in same order for processing (especially if trimmed)
		slices.SortFunc(pvList.Items, func(a, b v1.PersistentVolume) int {
			if a.GetUID() < b.GetUID() {
				return -1
			}
			return 1
		},
		)

		var items []*v1.PersistentVolume

		for _, pv := range pvList.Items {
			// Validate the PersistentVolume validity
			err := ms.ensurePersistentVolumeValid(&pv)
			if err != nil {
				logger.Trace().Str("pv_name", pv.Name).Err(err).Msg("Skipping processing a PersistentVolume, not valid")
				continue
			}
			items = append(items, &pv)
		}

		// Limit the number of PersistentVolumes to the specified limit, but first sorting them so we always stream the same volumes in the same order
		if len(items) > volumeLimit {
			logger.Info().Int("pv_count", len(items)).Int("limit", volumeLimit).Msg("Trimming PersistentVolumes list to the limit")
			// Sort the PersistentVolumes by name to ensure consistent ordering
			items = items[:volumeLimit] // trim the list to the limit
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

		ms.pruneOldVolumes(ctx, items) // after all PVs are already streamed, prune old volumes (those that are not in the current list but were measured before)

		interval := ms.getConfig().metricsFetchInterval

		logger.Info().Int("pv_count_total", len(pvList.Items)).Int("pv_count_eligible", len(items)).Dur("wait_duration", interval).Msg("Sent all volumes to processing, waiting for next fetch")

		// refresh list of volumes every metricsFetchInterval
		time.Sleep(interval)
	}
}

func (ms *MetricsServer) pruneOldVolumes(ctx context.Context, pvList []*v1.PersistentVolume) {
	component := "pruneOldVolumes"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = context.WithValue(ctx, "start_time", time.Now())
	logger := log.Ctx(ctx).With().Str("component", component).Str("span_id", span.SpanContext().SpanID().String()).Str("trace_id", span.SpanContext().TraceID().String()).Logger()
	logger.Debug().Msg("Pruning stale volumes from metrics collection")
	var pruneCount float64 = 0
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
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
	uids := ms.fetchMetricKeys(ctx)
	// Remove metrics for UIDs not present in the current PV list
	for _, uid := range uids {
		if _, exists := currentUIDs[uid]; !exists {
			vm := ms.volumeMetrics.GetVolumeMetric(uid)
			if vm == nil {
				logger.Debug().Str("pv_uid", string(uid)).Msg("No volume metric found for UID, skipping prune")
				continue // no metric to prune
			}
			pruneCount++
			ms.pruneVolumeMetric(ctx, uid)
		}
	}
}

func (ms *MetricsServer) fetchMetricKeys(ctx context.Context) []types.UID {
	ms.volumeMetrics.Lock()
	defer ms.volumeMetrics.Unlock()
	// obtain the current UIDs atomically and release the lock
	var keys []types.UID
	for k := range ms.volumeMetrics.Metrics {
		keys = append(keys, k)
	}
	return keys
}

func (ms *MetricsServer) removePrometheusMetricsForLabels(ctx context.Context, metric *VolumeMetric) {
	logger := log.Ctx(ctx)
	logger.Trace().Str("pv_name", metric.persistentVolume.Name).Msg("Removing prometheus metrics labels for volume")
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

func (ms *MetricsServer) pruneVolumeMetric(ctx context.Context, pvUUID types.UID) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "pruneVolumeMetric")
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	metric := ms.volumeMetrics.GetVolumeMetric(pvUUID)
	if metric == nil {
		return // nothing to remove, if it was already removed by another goroutine
	}

	defer ms.prometheusMetrics.server.PersistentVolumeRemovalsCount.Inc()

	// decrease refcounter to a filesystem
	fsObj, err := metric.volume.getFilesystemObj(ctx, true)
	if err != nil {
		logger.Error().Err(err).Str("pv_uid", string(pvUUID)).Msg("Failed to get filesystem object for volume metric, skipping removal")
	}

	ms.observedFilesystems.decRef(fsObj) // actually decrease refcounter
	ms.volumeMetrics.RemoveVolumeMetric(ctx, pvUUID)
	ms.removePrometheusMetricsForLabels(ctx, metric)
	logger.Info().Str("pv_uid", string(pvUUID)).Msg("Removed persistent volume from metric collection")
}

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
				defer func() {
					<-sem // release semaphore
				}()
				ms.processSinglePersistentVolume(ctx, pv)
				ms.prometheusMetrics.server.FetchPvBatchOperationsSuccessCount.Inc()
				sampledLogger.Info().Str("pv_name", pv.Name).Msg("Processing persistent volume completed. This is sampled log")
			}(pv)
		}
	}
}

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

	// if volume was marked for deletion, do nothing
	if pv.DeletionTimestamp != nil {
		// do nothing here, we will prune it later
		return
	}

	// Check if the PersistentVolume is already being processed
	if ms.volumeMetrics.HasVolumeMetric(pv.UID) {
		ms.volumeMetrics.GetVolumeMetric(pv.UID).persistentVolume = pv // Update the PersistentVolume reference in the existing VolumeMetric
		return
	}

	logger.Debug().Str("pv_name", pv.Name).Str("phase", string(pv.Status.Phase)).Msg("Received a PersistentVolume for processing")

	secret, err := ms.fetchSecret(ctx, pv.Spec.CSI.NodePublishSecretRef.Name, pv.Spec.CSI.NodePublishSecretRef.Namespace)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to fetch secret for PersistentVolume, skipping")
		return
	}
	secretData := make(map[string]string)
	for key, value := range secret.Data {
		secretData[key] = string(value)
	}
	if os.Getenv("OVERRIDE_API_ENDPOINTS") != "" {
		// Override API endpoints from environment variable if set
		endpoints := os.Getenv("OVERRIDE_API_ENDPOINTS")
		if endpoints != "" {
			secretData["endpoints"] = endpoints
		}
	}
	apiClient, err := ms.getApiStore().fromSecrets(ctx, secretData, ms.nodeID)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create API client from secret, skipping PersistentVolume")
		return
	}
	apiClient.RotateEndpointOnEachRequest = true // Rotate endpoint on each request to ensure we spread the load across all endpoints

	volume, err := NewVolumeFromId(ctx, pv.Spec.CSI.VolumeHandle, apiClient, ms)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create Volume from ID")
		return
	}
	volume.persistentVol = pv // Set the PersistentVolume reference in the Volume object
	// Create a new VolumeMetric instance

	fsObj, err := volume.apiClient.CachedGetFileSystemByName(ctx, volume.FilesystemName, ms.getConfig().quotaCacheValidityDuration)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, skipping PersistentVolume")
		return
	}

	// we still want to validate the object, by UID call is faster than by name
	volume.fileSystemObject = fsObj
	ensuredFsObj := &apiclient.FileSystem{}
	if err := volume.apiClient.GetFileSystemByUid(ctx, fsObj.Uid, ensuredFsObj, false); err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, skipping PersistentVolume")
		return
	}

	volume.fileSystemObject = ensuredFsObj // ensure the filesystem object is valid

	ms.observedFilesystems.incRef(fsObj, apiClient) // Add the filesystem to the observed list

	// prepopulate the inode ID for the volume, this will be used to fetch metrics later to avoid it during AddMetric
	_, err = volume.getInodeId(ctx)
	if err != nil {
		// this could happen if the volume is not yet created or is in an invalid state
		logger.Trace().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem inode ID for volume, skipping PersistentVolume")
		return
	}

	metric := &VolumeMetric{
		persistentVolume: pv,
		volume:           volume,
		metrics:          nil,
		secret:           secret,
		apiClient:        apiClient,
	}
	ms.volumeMetrics.AddVolumeMetric(ctx, pv.UID, metric)
	ms.prometheusMetrics.server.PersistentVolumeAdditionsCount.Inc()
	logger.Debug().Str("pv_name", pv.Name).Dur("duration", time.Since(startTime)).Msg("Added PersistentVolume for metrics processing")

}

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
	if pv.Spec.Capacity == nil || len(pv.Spec.Capacity) == 0 {
		return errors.New("pv has a zero capacity, half-baked volume possible")
	}
	if pv.Spec.CSI.VolumeAttributes == nil || len(pv.Spec.CSI.VolumeAttributes) == 0 {
		return errors.New("pv is missing volumeAttributes")
	}
	if !slices.Contains(KnownVolTypes[:], VolumeType(pv.Spec.CSI.VolumeAttributes["volumeType"])) {
		return errors.New("pv is missing volumeType or has an unsupported volumeType")
	}
	if pv.Status.Phase != v1.VolumeBound && pv.Status.Phase != v1.VolumeReleased {
		return errors.New(fmt.Sprintf("pv is not in a valid phase: %s", pv.Status.Phase))
	}
	return nil // Valid PersistentVolume
}

// fetchSecret retrieves a Kubernetes Secret by name and namespace, caching it for future use.
func (ms *MetricsServer) fetchSecret(ctx context.Context, secretName, secretNamespace string) (*v1.Secret, error) {
	component := "fetchSecret"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("secret_name", secretName).Str("secret_namespace", secretNamespace).Logger()

	// Fetch the secret from Kubernetes
	if secretName == "" || secretNamespace == "" {
		return nil, errors.New("secret name and namespace must be provided")
	}
	secretKey := fmt.Sprintf("%s/%s", secretNamespace, secretName)
	hash := hashString(secretKey, math.MaxInt)
	ms.secrets.Lock()
	defer ms.secrets.Unlock()
	if secret, exists := ms.secrets.secrets[hash]; exists {
		logger.Trace().Str("namespace", secretNamespace).Str("name", secretName).Msg("Using a secret from cache")
		return secret, nil // Return cached secret if available
	}
	logger.Debug().Str("namespace", secretNamespace).Str("name", secretName).Msg("Fetching Secret")
	if ms.GetK8sApi() == nil {
		return nil, errors.New("no k8s API client available")
	}
	secret, err := ms.GetK8sApi().CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch secret %s/%s: %w", secretNamespace, secretName, err)
	}
	ms.secrets.secrets[hash] = secret // Cache the secret
	return secret, nil
}

// fetchSingleMetric fetches the prometheusMetrics for a single Persistent Volume and sends it to the MetricsServer's incoming requests channel.
func (ms *MetricsServer) fetchSingleMetric(ctx context.Context, vm *VolumeMetric) error {
	component := "fetchSingleMetric"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.Ctx(ctx).With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	StartTime := time.Now()

	ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsInvokeCount.Inc()
	defer func() {
		dur := time.Since(StartTime).Seconds()
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsDurationHistogram.Observe(dur)
	}()

	// Fetch prometheusMetrics server.for a single persistent volume
	qosMetric, err := ms.FetchPvStats(ctx, vm.volume)
	if err != nil {
		ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsFailureCount.Inc()
		return fmt.Errorf("failed to fetch metric for persistent volume %s: %w", vm.persistentVolume.Name, err)
	}
	vm.metrics = qosMetric
	ms.volumeMetricsChan <- vm // Send the metric to the MetricsServer's incoming requests channel
	ms.prometheusMetrics.server.FetchSinglePvMetricsOperationsSuccessCount.Inc()
	return nil
}

func (ms *MetricsServer) FetchMetricsOneByOne(ctx context.Context) error {
	component := "FetchMetricsOneByOne"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()

	logger := log.Ctx(ctx).With().Str("component", component).Str("span_id", span.SpanContext().SpanID().String()).Str("trace_id", span.SpanContext().TraceID().String()).Logger()

	ctx = context.WithValue(ctx, "start_time", time.Now())
	wg := &sync.WaitGroup{}
	sem := make(chan struct{}, ms.getConfig().quotaFetchConcurrentRequests)
	keys := ms.fetchMetricKeys(ctx)
	ms.prometheusMetrics.server.FetchMetricsBatchSize.Set(float64(len(keys)))
	ms.prometheusMetrics.server.FetchMetricsBatchOperationsInvokeCount.Inc()
	succeeded := true
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		if succeeded {
			ms.prometheusMetrics.server.FetchMetricsBatchOperationsSuccessCount.Inc()
		} else {
			ms.prometheusMetrics.server.FetchMetricsBatchOperationsFailureCount.Inc()
		}
		ms.prometheusMetrics.server.FetchMetricsBatchOperationsDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.FetchMetricsBatchOperationsDurationHistogram.Observe(dur)
		if dur > float64(ms.getConfig().metricsFetchInterval.Seconds()) {
			logger.Warn().Int("pv_count", len(keys)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetching metrics took longer than the configured interval, consider increasing metricsFetchInterval or metricsFetchConcurrentRequests")
		} else {
			logger.Info().Int("pv_count", len(keys)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetched metrics for PersistentVolumes")
		}
	}()

	logger.Info().Int("pv_count", len(keys)).Msg("Starting to fetch prometheusMetrics for PersistentVolumes")
	defer logger.Info().Int("pv_count", len(keys)).Msg("Finished to fetch prometheusMetrics for PersistentVolumes")
	for _, key := range keys {
		vm := ms.volumeMetrics.GetVolumeMetric(key)
		sem <- struct{}{} // Acquire a slot in the semaphore

		go func(vm *VolumeMetric) {
			if vm == nil || vm.persistentVolume == nil {
				// could happen if was already pruned while waiting
				logger.Trace().Str("pv_uid", string(key)).Msg("VolumeMetric or PersistentVolume is nil, skipping")
				<-sem // Release the slot in the semaphore
				return
			}
			defer func() { <-sem }() // Release the slot in the semaphore

			err := ms.fetchSingleMetric(ctx, vm) // Actually fetch the prometheusMetrics for the persistent volume
			if err != nil {
				succeeded = false
			}
		}(vm)
	}

	wg.Wait()
	return nil
}

func (ms *MetricsServer) GetMetricsFromQuotaMap(ctx context.Context, qm *apiclient.QuotaMap) {
	component := "GetMetricsFromQuotaMap"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if qm == nil {
		logger.Error().Msg("QuotaMap is nil, cannot fetch vms")
		return
	}
	vms := ms.volumeMetrics.GetAllMetricsByFilesystemUid(ctx, qm.FileSystemUid)
	for _, vm := range vms {
		inodeId := vm.volume.inodeId
		if inodeId == 0 {
			logger.Error().Uint64("inode_id", inodeId).Msg("Failed to get inode ID for volume, skipping")
			continue
		}
		q := qm.GetQuotaForInodeId(inodeId)
		if q == nil {
			logger.Warn().Uint64("inode_id", inodeId).Msg("No quota entry found for inode ID in cached quota object, skipping")
			continue
		}
		vm.metrics = &PvStats{
			Usage: &UsageStats{
				Capacity:  int64(q.HardLimitBytes),
				Used:      int64(q.UsedBytes),
				Free:      int64(q.HardLimitBytes - q.UsedBytes),
				Timestamp: q.LastUpdateTime,
			},
			Performance: nil,
		}
		ms.volumeMetricsChan <- vm // Send the metric to the MetricsServer's incoming requests channel
	}
}

func (ms *MetricsServer) fetchPvUsageStatsFromWeka(ctx context.Context, v *Volume) (*UsageStats, error) {
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get inode ID for volume %s: %w", v.persistentVol.Name, err)
	}
	fsObj, err := v.getFilesystemObj(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem object for volume %s: %w", v.persistentVol.Name, err)
	}
	if fsObj == nil {
		return nil, fmt.Errorf("failed to get filesystem object for volume %s", v.persistentVol.Name)
	}
	quotaEntry, err := v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch quota for inode ID %d: %w", inodeId, err)
	}
	if quotaEntry == nil {
		return nil, fmt.Errorf("no quota entry found for inode ID %d", inodeId)
	}
	return &UsageStats{

		Capacity:  int64(quotaEntry.HardLimitBytes),
		Used:      int64(quotaEntry.UsedBytes),
		Free:      int64(quotaEntry.HardLimitBytes - quotaEntry.UsedBytes),
		Timestamp: quotaEntry.LastUpdateTime,
	}, nil
}

func (ms *MetricsServer) fetchPvUsageStatsFromWekaWithCache(ctx context.Context, v *Volume) (*UsageStats, error) {
	if v.lastUsageStats == nil || time.Since(v.lastUsageStats.Timestamp) < ms.getConfig().quotaCacheValidityDuration {
		usageStats, err := ms.fetchPvUsageStatsFromWeka(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch usage stats from Weka for volume %s: %w", v.persistentVol.Name, err)
		}
		v.lastUsageStats = usageStats
	}
	return v.lastUsageStats, nil
}

func (ms *MetricsServer) fetchSingePvUsageStatsFromQuotaMap(ctx context.Context, v *Volume) (*UsageStats, error) {
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get inode ID for volume %s: %w", v.persistentVol.Name, err)
	}
	fsObj, err := v.getFilesystemObj(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem object for volume %s: %w", v.persistentVol.Name, err)
	}
	if fsObj == nil {
		return nil, fmt.Errorf("failed to get filesystem object for volume %s", v.persistentVol.Name)
	}
	quotaMap, err := ms.GetQuotaMapForFilesystem(ctx, fsObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get quota map for filesystem %s: %w", fsObj.Name, err)
	}
	if quotaMap == nil {
		return nil, fmt.Errorf("quota map is nil for filesystem %s", fsObj.Name)
	}
	// Find the quota entry for the inode ID
	quotaEntry := quotaMap.GetQuotaForInodeId(inodeId)
	if quotaEntry == nil {
		return nil, fmt.Errorf("no quota entry found for inode ID %d in cached quota object for filesystem %s", inodeId, fsObj.Name)
	}
	return &UsageStats{

		Capacity:  int64(quotaEntry.HardLimitBytes),
		Used:      int64(quotaEntry.UsedBytes),
		Free:      int64(quotaEntry.HardLimitBytes - quotaEntry.UsedBytes),
		Timestamp: quotaEntry.LastUpdateTime,
	}, nil
}

func (ms *MetricsServer) FetchPvStatsFromQuotaMap(ctx context.Context, v *Volume) (*PvStats, error) {
	ret := &PvStats{}
	usageStats, err := ms.fetchSingePvUsageStatsFromQuotaMap(ctx, v)
	ret.Usage = usageStats
	return ret, err
}

func (ms *MetricsServer) FetchPvStatsFromWeka(ctx context.Context, v *Volume) (*PvStats, error) {
	ret := &PvStats{}
	usageStats, err := ms.fetchPvUsageStatsFromWekaWithCache(ctx, v)
	if err != nil {
		return nil, err
	}
	ret.Usage = usageStats
	return ret, nil
}

func (ms *MetricsServer) FetchPvStats(ctx context.Context, v *Volume) (*PvStats, error) {
	if ms.getConfig().useQuotaMapsForMetrics {
		return ms.FetchPvStatsFromQuotaMap(ctx, v)
	}
	return ms.FetchPvStatsFromWeka(ctx, v)
}

// MetricsReportStreamer listens on the volumeMetricsChan channel and reports prometheusMetrics to Prometheus.
func (ms *MetricsServer) MetricsReportStreamer(ctx context.Context) {
	component := "MetricsReportStreamer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting to report prometheusMetrics for PersistentVolumes")
	// function listens on volumeMetricsChan channel and reports prometheusMetrics
	for {
		select {
		case metric, ok := <-ms.volumeMetricsChan:
			if !ok {
				logger.Info().Msg("volumeMetricsChan closed, stopping reporting metrics")
				return
			}
			if metric == nil || metric.metrics == nil {
				continue
			}
			u := metric.metrics.Usage
			p := metric.metrics.Performance
			// enrich labels with persistent volume claim information
			labelValues := ms.createPrometheusLabelsForMetric(metric)

			if u != nil {
				logger.Trace().Str("pv_name", metric.persistentVolume.Name).Msg("Reporting prometheusMetrics for PersistentVolume")
				ms.prometheusMetrics.volumes.CapacityBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Capacity), u.Timestamp)
				ms.prometheusMetrics.volumes.UsedBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Used), u.Timestamp)
				ms.prometheusMetrics.volumes.FreeBytes.WithLabelValues(labelValues...).SetWithTimestamp(float64(u.Free), u.Timestamp)
			}
			if p != nil {
				// Report performance metrics if available
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
		case <-ctx.Done():
			logger.Info().Msg("Context cancelled, stopping reporting metrics")
			return
		}
	}
}

func (ms *MetricsServer) createPrometheusLabelsForMetric(metric *VolumeMetric) []string {
	pvName := metric.persistentVolume.Name
	guid := metric.apiClient.ClusterGuid.String()

	labelValues := []string{ms.driver.name,
		pvName,
		guid,
		metric.persistentVolume.Spec.StorageClassName,
		metric.volume.FilesystemName,
		string(metric.volume.GetBackingType()),
	}
	if metric.persistentVolume.Spec.ClaimRef != nil {
		labelValues = append(labelValues,
			metric.persistentVolume.Spec.ClaimRef.Name,
			metric.persistentVolume.Spec.ClaimRef.Namespace,
			string(metric.persistentVolume.Spec.ClaimRef.UID))
	} else {
		labelValues = append(labelValues, "", "", "")
	}
	return labelValues
}

// InvalidateSecret removes a secret from the cache and its associated PerClientVolumes. To be called when getting error on API client which is likely due to secret rotation.
func (ms *MetricsServer) InvalidateSecret(ctx context.Context, secretName, secretNamespace string) {
	// Invalidate the secret by removing it from the cache
	secretKey := fmt.Sprintf("%s/%s", secretNamespace, secretName)
	hash := hashString(secretKey, math.MaxInt)
	ms.secrets.Lock()
	defer ms.secrets.Unlock()
	if _, exists := ms.secrets.secrets[hash]; exists {
		delete(ms.secrets.secrets, hash)
	}
}

func (ms *MetricsServer) PeriodicPersistentVolumeCapacityReporter(ctx context.Context) {
	component := "PeriodicPersistentVolumeCapacityReporter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting periodic reporting of PersistentVolume capacities once in a minute. This is the fallback mechanism to ensure that we report capacities even if the metrics are not fetched from Weka API for some reason.")
	ticker := time.NewTicker(1 * time.Minute) // Report every minute
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

func (ms *MetricsServer) reportOnlyPvCapacities(ctx context.Context) {
	component := "reportOnlyPvCapacities"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	keys := ms.fetchMetricKeys(ctx)
	logger.Info().Int("pv_count", len(keys)).Msg("Starting to report only PersistentVolume capacities")
	if len(keys) == 0 {
		logger.Info().Msg("No PersistentVolumes found, nothing to report")
		return
	}

	for _, key := range keys {
		metric := ms.volumeMetrics.GetVolumeMetric(key)
		if metric == nil || metric.persistentVolume == nil {
			// could happen if was already pruned while waiting
			logger.Trace().Str("pv_uid", string(key)).Msg("VolumeMetric or PersistentVolume is nil, skipping")
			continue
		}

		r := metric.persistentVolume.Spec.Capacity.Storage()
		if r == nil {
			logger.Warn().Str("pv_name", metric.persistentVolume.Name).Msg("PersistentVolume capacity is nil, skipping")
			continue
		}
		capacity := r.Value()
		labels := ms.createPrometheusLabelsForMetric(metric)
		ms.prometheusMetrics.volumes.PvReportedCapacityBytes.WithLabelValues(labels...).Set(float64(capacity))

	}
	logger.Info().Int("pv_count", len(keys)).Msg("Finished to report only PersistentVolume capacities")
}

// batchRefreshQuotaMaps refreshes the quota maps for all observed filesystems in batches.
// It limits the number of concurrent goroutines to avoid overwhelming the API server.
// It also updates asynchronously calls update of all volumeMetrics on that filesystem
func (ms *MetricsServer) batchRefreshQuotaMaps(ctx context.Context, force bool) {

	component := "batchRefreshQuotaMaps"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	startTime := time.Now()

	concurrency := ms.getConfig().quotaFetchConcurrentRequests
	sortedObservedFilesystems := ms.observedFilesystems.GetByQuotaUpdateTime()
	countNeverUpdated := 0
	countUpToDate := 0
	countExpired := 0
	for _, ofs := range sortedObservedFilesystems {
		ts := ofs.lastQuotaUpdate.Load()
		if ts.IsZero() {
			countNeverUpdated++
		} else if ofs.lastQuotaUpdate.Load().Before(time.Now().Add(-ms.getConfig().quotaCacheValidityDuration)) {
			countExpired++
		} else {
			countUpToDate++
		}
	}
	batchSize := len(sortedObservedFilesystems)
	logger := log.Ctx(ctx)
	logger.Info().Int("never_updated", countNeverUpdated).Int("up_to_date", countUpToDate).Int("expired", countExpired).Int("total", batchSize).Msg("Starting to update quota maps")
	if batchSize == 0 {
		logger.Info().Msg("No observed filesystems to update, skipping batch refresh")
		return
	}

	// update prometheusMetrics for batchRefreshQuotaMaps batches
	ms.prometheusMetrics.server.QuotaUpdateBatchInvokeCount.Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.server.QuotaUpdateBatchSuccessCount.Inc()
		ms.prometheusMetrics.server.QuotaUpdateBatchDurationSeconds.Add(dur)
		ms.prometheusMetrics.server.QuotaUpdateBatchDurationHistogram.Observe(dur)
		ms.prometheusMetrics.server.QuotaUpdateBatchSize.Set(float64(batchSize))
	}()
	duration := atomic.NewFloat64(0)
	countStarted := atomic.NewInt64(0)
	countSuccessful := atomic.NewInt64(0)
	countFailed := atomic.NewInt64(0)

	cycleStart := time.Now()
	sampledLogger := logger.Sample(&zerolog.BasicSampler{N: 50})
	concurrencySem := make(chan struct{}, concurrency) // limit concurrent goroutines

	for _, ofs := range sortedObservedFilesystems {
		fsObj := ofs.GetFileSystem(ctx, true)
		if fsObj == nil {
			logger.Error().Str("fs_uid", ofs.fsUid.String()).Msg("FileSystem object is nil, skipping")
			continue
		}
		concurrencySem <- struct{}{} // acquire a slot
		go func(fsObj *apiclient.FileSystem) {
			defer func() { <-concurrencySem }() // release the slot
			countStarted.Inc()
			start := time.Now()
			qm, err := ms.refreshQuotaMapPerFilesystem(ctx, fsObj, force)

			// Update the metrics with the quota map if the fetch was successful
			if qm != nil {
				go ms.GetMetricsFromQuotaMap(ctx, qm)
			}
			dur := time.Since(start)
			duration.Add(dur.Seconds())

			if err != nil {
				countFailed.Inc()
				logger.Error().Err(err).Str("filesystem_name", fsObj.Name).Msg("Failed to update quota map for filesystem")
			} else {
				countSuccessful.Inc()
				ofs.lastQuotaUpdate.Store(qm.LastUpdate)
			}
			sampledLogger.Info().Int64("complete_count", countSuccessful.Load()+countFailed.Load()).Dur("duration", dur).Msg("Quota maps batch refresh progress")

		}(fsObj)
	}
	cycleDuration := time.Since(cycleStart)
	countIncomplete := countStarted.Load() - countFailed.Load() - countSuccessful.Load()
	countComplete := countSuccessful.Load() + countFailed.Load()
	avgDurationEffective := duration.Load() / float64(countComplete)
	avgDurationSuccessful := duration.Load() / float64(countSuccessful.Load())
	parallelism := float64(countComplete) / cycleDuration.Seconds()

	logger.Info().Dur("cycle_duration", cycleDuration).
		Float64("concurrency", parallelism).
		Float64("avg_duration_effectie", avgDurationEffective).
		Float64("avg_duration_successful", avgDurationSuccessful).
		Int64("count_total", countStarted.Load()).
		Int64("count_successful", countSuccessful.Load()).
		Int64("count_failed", countFailed.Load()).
		Int64("count_incomplete", countIncomplete).
		Int64("count_completed", countComplete).
		Msg("BATCH ENDED")
}

// PeriodicQuotaMapUpdater periodically updates the quota maps for all observed filesystems.
func (ms *MetricsServer) PeriodicQuotaMapUpdater(ctx context.Context) {
	component := "PeriodicQuotaMapUpdater"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting PeriodicQuotaMapUpdater")

	ticker := time.Minute
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("PeriodicQuotaMapUpdater context cancelled, stopping...")
			return
		case <-time.After(ticker):
			logger.Info().Msg("PeriodicQuotaMapUpdater cycle triggered")
			ms.batchRefreshQuotaMaps(ctx, false)
		}
	}
}

func (ms *MetricsServer) RollingQuotaMapUpdaterForDebug(ctx context.Context) {
	component := "RollingQuotaMapUpdaterForDebug"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Starting to update quota maps in debug mode")
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("RollingQuotaMapUpdaterForDebug context cancelled, stopping...")
			return
		default:
			logger.Info().Msg("Starting to update quota maps for all PersistentVolumes in debug mode")
			ms.batchRefreshQuotaMaps(ctx, true)
		}
	}
}

// PeriodicSingleMetricsFetcher periodically fetches metrics for all PersistentVolumes and reports them to Prometheus.
// It runs in a separate goroutine and is controlled by a ticker based on the configured interval.
// It is only relevant for the flow where metrics are fetched from WEKA one by one, not in batch.
func (ms *MetricsServer) PeriodicSingleMetricsFetcher(ctx context.Context) {
	// Periodically fetch prometheusMetrics for all persistent volumes
	component := "PeriodicSingleMetricsFetcher"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Str("interval", ms.getConfig().metricsFetchInterval.String()).Msg("Starting collection of WEKA metrics for PVs")

	ticker := ms.config.metricsFetchInterval
	if ticker <= 0 {
		ticker = time.Minute // Default to 1 minute if not set
	}
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("Periodic fetch prometheusMetrics context cancelled, stopping...")
			return
		case <-time.After(ticker):
			startTime := time.Now()
			logger.Info().Msg("Periodic fetch prometheusMetrics cycle triggered")
			ms.prometheusMetrics.server.PeriodicFetchMetricsInvokeCount.Inc()
			// Start the fetch in a goroutine to avoid blocking the periodic fetch
			go func() {
				// Start the fetch in a goroutine to avoid blocking the periodic fetch
				// Check if the fetch is already running to avoid concurrent fetches
				if ms.capacityFetchRunning {
					logger.Warn().Msg("Capacity fetch is already running, skipping this cycle. This can happen if the fetch takes longer than the configured interval.")
					ms.prometheusMetrics.server.PeriodicFetchMetricsSkipCount.Inc()
					return
				}

				ms.capacityFetchRunning = true
				defer func() { ms.capacityFetchRunning = false }()

				logger.Info().Int("pv_count", len(ms.volumeMetrics.Metrics)).Msg("Fetching prometheusMetrics for PersistentVolumes")
				err := ms.FetchMetricsOneByOne(ctx)
				if err != nil {
					logger.Error().Err(err).Msg("Error fetching prometheusMetrics")
					ms.prometheusMetrics.server.PeriodicFetchMetricsSuccessCount.Inc()
				} else {
					ms.prometheusMetrics.server.PeriodicFetchMetricsFailureCount.Inc()
				}
				dur := time.Since(startTime)
				logger.Info().Dur("duration", dur).Msg("Periodic fetch prometheusMetrics cycle completed")
			}()
		}
	}
}

func (ms *MetricsServer) Start(ctx context.Context) {
	component := "StartMetricsServer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Info().Msg("Starting MetricsServer")
	ms.Lock()
	if ms.running {
		return // Already running
	}
	ms.running = true
	ms.Unlock()

	ms.initManager(ctx) // Initialize the controller-runtime manager

	time.Sleep(1 * time.Second) // to ensure the manager cache is fully started before fetching PersistentVolumes
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes")

	// Initialize the incoming requests channel and report all incoming prometheusMetrics
	ms.wg.Add(1)
	go func() {

		ms.volumeMetricsChan = make(chan *VolumeMetric, VolumeMetricsBufferSize)
		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Metrics server context cancelled, stopping...")
				ms.wg.Done()
				return
			default:
				time.Sleep(100 * time.Millisecond) // Prevent busy loop
			}
		}
	}()

	// Add a Runnable that only runs when this pod is elected leader
	err := ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting MetricsServer processors")

		go ms.PersistentVolumeStreamer(ctx)
		go ms.MetricsReportStreamer(ctx)
		go ms.PersistentVolumeStreamProcessor(ctx)
		go ms.PeriodicPersistentVolumeCapacityReporter(ctx)

		// depending on the configuration, start either the periodic quota map updater or the periodic single metrics fetcher
		if ms.getConfig().useQuotaMapsForMetrics {
			go ms.PeriodicQuotaMapUpdater(ctx)
		} else {
			go ms.PeriodicSingleMetricsFetcher(ctx)
		}

		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping...")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add processors to manager, cannot run MetricsServer without it")
	}

	go func() {
		if err := ms.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("Cannot continue running MetricsServer")
			os.Exit(1)
		}
	}()

}

// StartDebug starts the MetricsServer in debug mode, only fetching metrics from WEKA
func (ms *MetricsServer) StartDebugSingleQuotas(ctx context.Context) {
	component := "StartDebugSingleQuotas"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Info().Msg("Starting MetricsServer in debug mode, only fetching single quotas from WEKA")
	ms.Lock()
	if ms.running {
		return // Already running
	}
	ms.running = true
	ms.Unlock()

	ms.initManager(ctx) // Initialize the controller-runtime manager

	time.Sleep(1 * time.Second) // to ensure the manager cache is fully started before fetching PersistentVolumes
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes")

	// Initialize the incoming requests channel and report all incoming prometheusMetrics
	ms.wg.Add(1)
	go func() {

		ms.volumeMetricsChan = make(chan *VolumeMetric, VolumeMetricsBufferSize)
		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Metrics server context cancelled, stopping...")
				ms.wg.Done()
				return
			default:
				time.Sleep(100 * time.Millisecond) // Prevent busy loop
			}
		}
	}()

	// Add a Runnable that only runs when this pod is elected leader
	err := ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting MetricsServer processors")

		go ms.PersistentVolumeStreamer(ctx)
		go ms.PersistentVolumeStreamProcessor(ctx)

		<-ctx.Done()
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add processors to manager, cannot run MetricsServer without it")
	}

	go func() {
		if err := ms.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("Cannot continue running MetricsServer")
			os.Exit(1)
		}
	}()

}

func (ms *MetricsServer) StartDebugQuotaMaps(ctx context.Context) {
	component := "StartDebugQuotaMaps"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Info().Msg("Starting MetricsServer in debug mode, fetching whole filesystem QUOTA MAPS from WEKA")
	ms.Lock()
	if ms.running {
		return // Already running
	}
	ms.running = true
	ms.Unlock()

	ms.initManager(ctx) // Initialize the controller-runtime manager

	time.Sleep(1 * time.Second) // to ensure the manager cache is fully started before fetching PersistentVolumes
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes")

	// Initialize the incoming requests channel and report all incoming prometheusMetrics
	ms.wg.Add(1)
	go func() {

		ms.volumeMetricsChan = make(chan *VolumeMetric, VolumeMetricsBufferSize)
		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Metrics server context cancelled, stopping...")
				ms.wg.Done()
				return
			default:
				time.Sleep(100 * time.Millisecond) // Prevent busy loop
			}
		}
	}()

	// Add a Runnable that only runs when this pod is elected leader
	err := ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting MetricsServer processors")

		go ms.PersistentVolumeStreamer(ctx)
		go ms.PersistentVolumeStreamProcessor(ctx)

		<-ctx.Done()
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add processors to manager, cannot run MetricsServer without it")
	}

	go func() {
		if err := ms.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("Cannot continue running MetricsServer")
			os.Exit(1)
		}
	}()

	time.Sleep(10 * time.Second)
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes, no quota maps yet")

	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("RollingMetricsFetcherForDebug context cancelled, stopping...")
			return
		default:
			ms.batchRefreshQuotaMaps(ctx, true) // Force update quota maps in debug mode
		}
	}
}

func (ms *MetricsServer) Wait() {
	ms.wg.Wait()
}

func (ms *MetricsServer) Stop(ctx context.Context) {
	if ms == nil {
		return // Nothing to stop
	}
	ms.Lock()
	defer ms.Unlock()
	if !ms.running {
		return // Already stopped
	}
	close(ms.volumeMetricsChan)     // Close the channel to stop reporting prometheusMetrics
	close(ms.persistentVolumesChan) // Close the channel to stop streaming PersistentVolumes
	ms.wg.Done()
	ms.running = false
	log.Info().Msg("Metrics server stopped")
}
