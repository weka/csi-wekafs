package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/go-logr/zerologr"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"slices"
	"sync"
	"time"
)

var MetricsLabels = []string{"csi_driver_name", "pv_name", "cluster_guid", "storage_class_name", "filesystem_name", "volume_type", "pvc_name", "pvc_namespace", "pvc_uid"}
var QuotaLabels = []string{"csi_driver_name", "cluster_guid", "filesystem_name"}

const (
	PVStreamChannelSize     = 100000 // Size of the channel for streaming PersistentVolume objects
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

	quotaMaps              *QuotaMapsPerFilesystem
	observedFilesystemUids *ObservedFilesystemUids // to track observed filesystem UIDs and their reference counts and API clients (for quota maps periodic updates)

	firstStreamCompleted  bool // flag to indicate if the first stream of PersistentVolumes has been completed so we can start processing them
	firstQuotaMapsFetched bool // flag to indicate if the first quota maps have been fetched so fetching metrics can start
	sync.Mutex
	wg sync.WaitGroup // WaitGroup to manage goroutines
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
		persistentVolumesChan: make(chan *v1.PersistentVolume, PVStreamChannelSize),
		wg:                    sync.WaitGroup{},

		quotaMaps:              NewQuotaMapsPerFilesystem(),
		observedFilesystemUids: NewObservedFilesystemUids(),
	}
	ret.prometheusMetrics.FetchMetricsFrequencySeconds.Set(ret.getConfig().wekaMetricsFetchInterval.Seconds())
	ret.prometheusMetrics.QuotaUpdateFrequencySeconds.Set(float64(ret.getConfig().wekaMetricsFetchInterval.Seconds()))
	ret.prometheusMetrics.QuotaUpdateConcurrentRequests.Set(float64(ret.getConfig().wekaMetricsQuotaUpdateConcurrentRequests))

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
		err := ms.manager.GetClient().List(ctx, pvList, &client.ListOptions{})
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch PersistentVolumes, no statistics will be available, will retry in 10 seconds")
			ms.prometheusMetrics.FetchPvBatchOperationFailureCount.Inc()
			time.Sleep(10 * time.Second)
			continue
		}

		d := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		ms.prometheusMetrics.FetchPvBatchOperations.Inc()
		ms.prometheusMetrics.FetchPvBatchOperationsDuration.Add(d)
		ms.prometheusMetrics.FetchPvBatchSize.Set(float64(len(pvList.Items)))
		ms.prometheusMetrics.FetchPvBatchOperationsHistogram.Observe(d)

		logger.Info().Int("pv_count", len(pvList.Items)).Msg("Fetched list of PersistentVolumes")

		for _, pv := range pvList.Items {
			select {
			case <-ctx.Done():
				return
			case out <- &pv:
			}
			ms.prometheusMetrics.StreamPvOperations.Inc()
		}

		ms.pruneOldVolumes(ctx, pvList.Items) // after all PVs are already streamed, prune old volumes (those that are not in the current list but were measured before)

		ms.firstStreamCompleted = true
		dur := ms.getConfig().wekaMetricsFetchInterval

		logger.Info().Int("pv_count", len(pvList.Items)).Dur("wait_duration", dur).Msg("Sent all volumes to processing, waiting for next fetch")

		// refresh list of volumes every wekaMetricsFetchInterval
		time.Sleep(dur)
	}
}

func (ms *MetricsServer) pruneOldVolumes(ctx context.Context, pvList []v1.PersistentVolume) {
	component := "pruneOldVolumes"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = context.WithValue(ctx, "start_time", time.Now())
	var pruneCount float64 = 0
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		ms.prometheusMetrics.PruneVolumesBatchOperations.Inc()
		ms.prometheusMetrics.PruneVolumesBatchSize.Set(pruneCount)
		ms.prometheusMetrics.PruneVolumesBatchOperationsDuration.Add(dur)
		ms.prometheusMetrics.PruneVolumesBatchOperationsHistogram.Observe(dur)
	}()

	logger := log.Ctx(ctx).With().Str("component", component).Str("span_id", span.SpanContext().SpanID().String()).Str("trace_id", span.SpanContext().TraceID().String()).Logger()
	logger.Trace().Msg("Pruning old volumes from metrics collection")
	currentUIDs := make(map[types.UID]struct{}, len(pvList))
	for _, pv := range pvList {
		currentUIDs[pv.UID] = struct{}{}
	}
	// Remove metrics for UIDs not present in the current PV list
	for _, uid := range ms.fetchMetricKeys(ctx) {
		if _, exists := currentUIDs[uid]; !exists {
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
	ms.prometheusMetrics.Capacity.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Used.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Free.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.PvCapacity.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Reads.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Writes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.ReadBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.WriteBytes.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.ReadDurationUs.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.WriteDurationUs.DeleteLabelValues(labelValues...)
}

func (ms *MetricsServer) pruneVolumeMetric(ctx context.Context, pvUUID types.UID) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "pruneVolumeMetric")
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	defer ms.prometheusMetrics.PersistentVolumesRemovedFromMetricsCollection.Inc()

	metric := ms.volumeMetrics.GetVolumeMetric(pvUUID)

	// decrease refcounter to a filesystem
	fsObj, err := metric.volume.getFilesystemObj(ctx, true)
	if err != nil {
		logger.Error().Err(err).Str("pv_uid", string(pvUUID)).Msg("Failed to get filesystem object for volume metric, skipping removal")
	}

	ms.observedFilesystemUids.decRef(fsObj) // actually decrease refcounter
	ms.volumeMetrics.RemoveVolumeMetric(pvUUID)
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

	sem := make(chan struct{}, ms.getConfig().wekaMetricsFetchConcurrentRequests)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case pv, ok := <-ms.persistentVolumesChan:
			if !ok || pv == nil {
				wg.Wait()
				return
			}
			sem <- struct{}{} // acquire semaphore
			wg.Add(1)
			go func(pv *v1.PersistentVolume) {
				defer func() {
					<-sem // release semaphore
					wg.Done()
				}()
				ms.processSinglePersistentVolume(ctx, pv)
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

	ctx = context.WithValue(ctx, "start_time", time.Now())
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		ms.prometheusMetrics.ProcessPvOperations.Inc()
		ms.prometheusMetrics.ProcessPvOperationsDuration.Add(dur)
		ms.prometheusMetrics.ProcessPvOperationsHistogram.Observe(dur)
	}()

	// Validate the PersistentVolume validity
	err := ms.ensurePersistentVolumeValid(pv)
	if err != nil {
		logger.Trace().Str("pv_name", pv.Name).Err(err).Msg("Skipping processing a PersistentVolume, not valid")
		return
	}

	// Check if the PersistentVolume is already being processed
	if ms.volumeMetrics.HasVolumeMetric(pv.UID) {
		ms.volumeMetrics.GetVolumeMetric(pv.UID).persistentVolume = pv // Update the PersistentVolume reference in the existing VolumeMetric
		return
	}

	logger.Debug().Str("pv_name", pv.Name).Str("phase", string(pv.Status.Phase)).Msg("Received a new PersistentVolume for processing")

	ms.prometheusMetrics.PersistentVolumesAddedForMetricsCollection.Inc()

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

	fsObj, err := volume.getFilesystemObj(ctx, false)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, skipping PersistentVolume")
		return
	}
	if fsObj == nil {
		logger.Error().Str("pv_name", pv.Name).Msg("Failed to get filesystem object for volume, filesystem is nil, skipping PersistentVolume")
		return
	}
	ms.observedFilesystemUids.incRef(fsObj, apiClient) // Add the filesystem to the observed list

	metric := &VolumeMetric{
		persistentVolume: pv,
		volume:           volume,
		metrics:          nil,
		secret:           secret,
		apiClient:        apiClient,
	}
	logger.Trace().Str("pv_name", pv.Name).Msg("Adding PersistentVolume for metrics processing")
	ms.volumeMetrics.AddVolumeMetric(pv.UID, metric)
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
	logger := log.Ctx(ctx)
	ctx = context.WithValue(ctx, "start_time", time.Now())
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		ms.prometheusMetrics.FetchSinglePvMetricsOperations.Inc()
		ms.prometheusMetrics.FetchSinglePvMetricsOperationsDuration.Add(dur)
		ms.prometheusMetrics.FetchSinglePvMetricsOperationsHistogram.Observe(dur)
	}()

	// Fetch prometheusMetrics for a single persistent volume
	logger.Trace().Str("pv_name", vm.persistentVolume.Name).Msg("Fetching Metric")
	defer logger.Trace().Str("pv_name", vm.persistentVolume.Name).Msg("Fetching Metric completed")
	qosMetric, err := ms.FetchPvStats(ctx, vm.volume)
	if err != nil {
		return fmt.Errorf("failed to fetch metric for persistent volume %s: %w", vm.persistentVolume.Name, err)
	}
	if qosMetric == nil {
		return fmt.Errorf("no metric data available for persistent volume %s", vm.persistentVolume.Name)
	}

	vm.metrics = qosMetric
	ms.volumeMetricsChan <- vm // Send the metric to the MetricsServer's incoming requests channel
	return nil
}

func (ms *MetricsServer) FetchMetrics(ctx context.Context) error {
	component := "FetchMetrics"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()

	logger := log.Ctx(ctx).With().Str("component", component).Str("span_id", span.SpanContext().SpanID().String()).Str("trace_id", span.SpanContext().TraceID().String()).Logger()

	ctx = context.WithValue(ctx, "start_time", time.Now())
	wg := &sync.WaitGroup{}
	sem := make(chan struct{}, ms.getConfig().wekaMetricsFetchConcurrentRequests)
	keys := ms.fetchMetricKeys(ctx)
	ms.prometheusMetrics.FetchMetricsBatchSize.Set(float64(len(keys)))
	ms.prometheusMetrics.FetchMetricsBatchOperationsInvoked.Inc()
	succeeded := true
	defer func() {
		dur := time.Since(ctx.Value("start_time").(time.Time)).Seconds()
		if succeeded {
			ms.prometheusMetrics.FetchMetricsBatchOperationsSucceeded.Inc()
		} else {
			ms.prometheusMetrics.FetchMetricsBatchOperationsFailed.Inc()
		}
		ms.prometheusMetrics.FetchMetricsBatchOperationsDuration.Add(dur)
		ms.prometheusMetrics.FetchMetricsBatchOperationsHistogram.Observe(dur)
		if dur > float64(ms.getConfig().wekaMetricsFetchInterval.Seconds()) {
			logger.Warn().Int("pv_count", len(keys)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetching metrics took longer than the configured interval, consider increasing wekaMetricsFetchInterval or wekaMetricsFetchConcurrentRequests")
		} else {
			logger.Info().Int("pv_count", len(keys)).Dur("fetch_duration", time.Duration(dur*float64(time.Second))).Msg("Fetched metrics for PersistentVolumes")
		}
	}()

	logger.Info().Int("pv_count", len(keys)).Msg("Starting to fetch prometheusMetrics for PersistentVolumes")
	defer logger.Info().Int("pv_count", len(keys)).Msg("Finished to fetch prometheusMetrics for PersistentVolumes")
	for _, key := range keys {
		vm := ms.volumeMetrics.GetVolumeMetric(key)
		wg.Add(1)
		sem <- struct{}{} // Acquire a slot in the semaphore

		go func(vm *VolumeMetric) {
			if vm == nil || vm.persistentVolume == nil {
				logger.Error().Str("pv_uid", string(key)).Msg("VolumeMetric or PersistentVolume is nil, skipping")
				wg.Done()
				<-sem // Release the slot in the semaphore
				return
			}
			defer wg.Done()
			defer func() { <-sem }() // Release the slot in the semaphore

			err := ms.fetchSingleMetric(ctx, vm) // Actually fetch the prometheusMetrics for the persistent volume
			if err != nil {
				succeeded = false
				logger.Error().Err(err).Str("pv_name", vm.persistentVolume.Name).Msg("Failed to fetch prometheusMetrics for persistent volume")
			}
		}(vm)
	}

	wg.Wait()
	return nil
}

func (ms *MetricsServer) fetchPvUsageStats(ctx context.Context, v *Volume) (*UsageStats, error) {
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
		return &UsageStats{}, fmt.Errorf("no quota entry found for inode ID %d in cached quota object for filesystem %s", inodeId, fsObj.Name)
	}
	return &UsageStats{
		Capacity: int64(quotaEntry.HardLimitBytes),
		Used:     int64(quotaEntry.UsedBytes),
		Free:     int64(quotaEntry.HardLimitBytes - quotaEntry.UsedBytes),
	}, nil
}

func (ms *MetricsServer) FetchPvStats(ctx context.Context, v *Volume) (*PvStats, error) {
	ret := &PvStats{}
	usageStats, err := ms.fetchPvUsageStats(ctx, v)
	ret.Usage = usageStats
	return ret, err
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

			var pDiff *apiclient.PerfStats
			if p != nil {
				lastStat := metric.volume.lastStats
				if lastStat == nil {
					pDiff = p
				} else {
					pDiff = &apiclient.PerfStats{
						Reads:          p.Reads - lastStat.Reads,
						Writes:         p.Writes - lastStat.Writes,
						ReadBytes:      p.ReadBytes - lastStat.ReadBytes,
						WriteBytes:     p.WriteBytes - lastStat.WriteBytes,
						ReadLatencyUs:  p.ReadLatencyUs - lastStat.ReadLatencyUs,
						WriteLatencyUs: p.WriteLatencyUs - lastStat.WriteLatencyUs,
					}
				}
				metric.volume.lastStats = p
			}
			// enrich labels with persistent volume claim information
			labelValues := ms.createPrometheusLabelsForMetric(metric)

			logger.Trace().Str("pv_name", metric.persistentVolume.Name).Msg("Reporting prometheusMetrics for PersistentVolume")
			ms.prometheusMetrics.Capacity.WithLabelValues(labelValues...).Set(float64(u.Capacity))
			ms.prometheusMetrics.Used.WithLabelValues(labelValues...).Set(float64(u.Used))
			ms.prometheusMetrics.Free.WithLabelValues(labelValues...).Set(float64(u.Free))
			if pDiff != nil {
				// Report performance metrics if available
				ms.prometheusMetrics.Reads.WithLabelValues(labelValues...).Add(float64(pDiff.Reads))
				ms.prometheusMetrics.Writes.WithLabelValues(labelValues...).Add(float64(pDiff.Writes))
				ms.prometheusMetrics.ReadBytes.WithLabelValues(labelValues...).Add(float64(pDiff.ReadBytes))
				ms.prometheusMetrics.WriteBytes.WithLabelValues(labelValues...).Add(float64(pDiff.WriteBytes))
				ms.prometheusMetrics.ReadDurationUs.WithLabelValues(labelValues...).Add(float64(pDiff.ReadLatencyUs))
				ms.prometheusMetrics.WriteDurationUs.WithLabelValues(labelValues...).Add(float64(pDiff.WriteLatencyUs))
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
			logger.Warn().Str("pv_uid", string(key)).Msg("VolumeMetric or PersistentVolume is nil, skipping")
			continue
		}

		r := metric.persistentVolume.Spec.Capacity.Storage()
		if r == nil {
			logger.Warn().Str("pv_name", metric.persistentVolume.Name).Msg("PersistentVolume capacity is nil, skipping")
			continue
		}
		capacity := r.Value()
		labels := ms.createPrometheusLabelsForMetric(metric)
		ms.prometheusMetrics.PvCapacity.WithLabelValues(labels...).Set(float64(capacity))

	}
	logger.Info().Int("pv_count", len(keys)).Msg("Finished to report only PersistentVolume capacities")
}

func (ms *MetricsServer) batchUpdateQuotaMaps(ctx context.Context) {

	component := "batchUpdateQuotaMaps"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	startTime := time.Now()

	sem := make(chan struct{}, ms.getConfig().wekaMetricsQuotaUpdateConcurrentRequests) // limit concurrent goroutines
	uids := ms.observedFilesystemUids.GetUids()
	logger.Info().Int("batch_count", len(uids)).Msg("Starting to update quota maps")

	// update prometheusMetrics for batchUpdateQuotaMaps batches
	ms.prometheusMetrics.QuotaUpdateBatchCount.Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.QuotaUpdateBatchDuration.Add(dur)
		ms.prometheusMetrics.QuotaUpdateBatchDurationHistogram.Observe(dur)
		ms.prometheusMetrics.QuotaUpdateBatchSize.Set(float64(len(uids)))
		if time.Since(startTime) > ms.getConfig().wekaMetricsFetchInterval {
			logger.Error().Int("batch_count", len(uids)).Dur("batch_duration_ms", time.Since(startTime)).
				Msg("Finished to update quota maps, took longer than configured interval, consider increasing wekaMetricsFetchInterval or wekaMetricsQuotaUpdateConcurrentRequests")
		} else {
			logger.Info().Int("batch_count", len(uids)).Dur("batch_duration_ms", time.Since(startTime)).Msg("Finished to update quota maps on time")
		}
	}()

	for fsUid, qm := range uids {
		apiClient := qm.apiClient
		fsObj := &apiclient.FileSystem{}
		err := apiClient.GetFileSystemByUid(ctx, fsUid, fsObj, false)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to get filesystem object for quota map update")
			continue
		}

		sem <- struct{}{} // acquire a slot
		go func(fsObj *apiclient.FileSystem) {
			defer func() { <-sem }() // release the slot
			err = ms.updateQuotaMapPerFilesystem(ctx, fsObj)
			if err != nil {
				logger.Error().Err(err).Str("filesystem_name", fsObj.Name).Msg("Failed to update quota map for filesystem")
			}
		}(fsObj)
	}
}

func (ms *MetricsServer) PeriodicQuotaMapUpdater(ctx context.Context) {
	component := "PeriodicQuotaMapUpdater"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Waiting for the first batch of PersistentVolumes to be streamed before starting PeriodicQuotaMapUpdater")
	for !ms.firstStreamCompleted {
		time.Sleep(100 * time.Millisecond) // Wait until the first batch of PersistentVolumes is streamed
	}
	logger.Info().Str("interval", ms.getConfig().wekaMetricsFetchInterval.String()).Msg("Starting PeriodicQuotaMapUpdater every defined interval")

	ticker := ms.config.wekaMetricsFetchInterval
	if ticker <= 0 {
		ticker = time.Minute // Default to 1 minute if not set
	}
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("PeriodicQuotaMapUpdater context cancelled, stopping...")
			return
		case <-time.After(ticker):
			logger.Info().Msg("PeriodicQuotaMapUpdater cycle triggered")
			ms.batchUpdateQuotaMaps(ctx)
			ms.firstQuotaMapsFetched = true // Mark that processing has started, so that PeriodicMetricsFetcher can proceed
		}
	}
}

func (ms *MetricsServer) PeriodicMetricsFetcher(ctx context.Context) {
	// Periodically fetch prometheusMetrics for all persistent volumes
	component := "PeriodicMetricsFetcher"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	logger.Info().Msg("Waiting for the first batch of filesystem quota updates before starting PeriodicMetricsFetcher")
	for !ms.firstQuotaMapsFetched {
		time.Sleep(100 * time.Millisecond)
	}

	logger.Info().Str("interval", ms.getConfig().wekaMetricsFetchInterval.String()).Msg("Starting collection of WEKA metrics for PVs")

	ticker := ms.config.wekaMetricsFetchInterval
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
			go func() {
				ms.reportOnlyPvCapacities(ctx) // Report only capacities, assuming it will go faster and will not block the periodic fetch
			}()
			ms.prometheusMetrics.PeriodicFetchMetricsInvokeCount.Inc()
			ms.prometheusMetrics.ProcessPvQueueSize.Set(float64(len(ms.persistentVolumesChan)))
			ms.prometheusMetrics.FetchSinglePvMetricsQueueSize.Set(float64(len(ms.volumeMetricsChan)))
			logger.Info().Int("pv_count", len(ms.volumeMetrics.Metrics)).Msg("Fetching prometheusMetrics for PersistentVolumes")
			err := ms.FetchMetrics(ctx)
			if err != nil {
				logger.Error().Err(err).Msg("Error fetching prometheusMetrics")
				ms.prometheusMetrics.PeriodicFetchMetricsSuccessCount.Inc()
			} else {
				ms.prometheusMetrics.PeriodicFetchMetricsFailureCount.Inc()
			}
			dur := time.Since(startTime)
			logger.Info().Dur("duration", dur).Msg("Periodic fetch prometheusMetrics cycle completed")
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

	// Add a Runnable that only runs when this pod is elected leader
	// TODO: make sure that we do not continue further till leader election lease is acquired

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
		logger.Info().Msg("Leader elected, starting PersistentVolumeStreamer")

		go ms.PersistentVolumeStreamer(ctx)

		// Wait until leadership is lost or shutdown
		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping PersistentVolumeStreamer")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add PersistentVolumeStreamer to manager, cannot run MetricsServer without it")
	}

	err = ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting MetricsReportStreamer")

		go ms.MetricsReportStreamer(ctx)

		// Wait until leadership is lost or shutdown
		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping MetricsServer")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add MetricsReportStreamer to manager, cannot run MetricsServer without it")
	}

	err = ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting PersistentVolumeStreamProcessor")

		go ms.PersistentVolumeStreamProcessor(ctx)

		// Wait until leadership is lost or shutdown
		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping MetricsServer")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add PersistentVolumeStreamProcessor to manager, cannot run MetricsServer without it")
	}

	err = ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting PeriodicMetricsFetcher")

		go ms.PeriodicMetricsFetcher(ctx)

		// Wait until leadership is lost or shutdown
		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping MetricsServer")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add PeriodicMetricsFetcher to manager, cannot run MetricsServer without it")
	}

	err = ms.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		logger.Info().Msg("Leader elected, starting PeriodicQuotaMapUpdater")

		go ms.PeriodicQuotaMapUpdater(ctx)

		// Wait until leadership is lost or shutdown
		<-ctx.Done()
		log.Info().Msg("Leadership lost or shutdown, stopping MetricsServer")
		return nil
	}))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add Runnable to manager")
		Die("Failed to add batchUpdateQuotaMaps to manager, cannot run MetricsServer without it")
	}

	go func() {
		if err := ms.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("Cannot continue running MetricsServer")
			os.Exit(1)
		}
	}()

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
