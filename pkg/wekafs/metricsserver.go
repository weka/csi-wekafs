package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"

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

type SecretsStore struct {
	secrets map[int]*v1.Secret // map[secretName/secretNamespace]*v1.Secret
	sync.Mutex
}

func NewSecretsStore() *SecretsStore {
	return &SecretsStore{
		secrets: make(map[int]*v1.Secret),
	}
}

type VolumeMetrics struct {
	sync.Mutex
	Metrics map[types.UID]*VolumeMetric
}

func (vms *VolumeMetrics) HasVolumeMetric(pvUID types.UID) bool {
	vms.Lock()
	defer vms.Unlock()
	_, exists := vms.Metrics[pvUID]
	return exists
}

func (vms *VolumeMetrics) GetVolumeMetric(pvUID types.UID) *VolumeMetric {
	vms.Lock()
	defer vms.Unlock()
	if _, exists := vms.Metrics[pvUID]; exists {
		return vms.Metrics[pvUID]
	}
	return nil
}

func (vms *VolumeMetrics) AddVolumeMetric(pvUID types.UID, metric *VolumeMetric) {
	vms.Lock()
	defer vms.Unlock()
	if vms.Metrics == nil {
		vms.Metrics = make(map[types.UID]*VolumeMetric)
	}
	vms.Metrics[pvUID] = metric
}

func (vms *VolumeMetrics) RemoveVolumeMetric(pvUID types.UID) {
	vms.Lock()
	defer vms.Unlock()
	delete(vms.Metrics, pvUID)
}

func NewVolumeMetrics() *VolumeMetrics {
	return &VolumeMetrics{
		Metrics: make(map[types.UID]*VolumeMetric),
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

// VolumeMetric represents the prometheusMetrics for a single Persistent Volume in Kubernetes
type VolumeMetric struct {
	persistentVolume *v1.PersistentVolume // object that represents the Kubernetes Persistent Volume
	volume           *Volume              // object that represents the Weka CSI Volume
	metrics          *PvStats             // Weka metrics for the volume including capacity, used, free, reads, writes, readBytes, writeBytes, writeThroughput
	secret           *v1.Secret           // Kubernetes Secret associated with the volume
	apiClient        *apiclient.ApiClient // reference to the Weka API client
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
		persistentVolumesChan: make(chan *v1.PersistentVolume, driver.config.wekaMetricsFetchConcurrentRequests),
		wg:                    sync.WaitGroup{},
	}
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
	zerologr.SetMaxV(1)
	var logrLog = zerologr.New(logger)

	ms.manager, err = ctrl.NewManager(config, ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          ms.getConfig().enableMetricsServerLeaderElection,
		LeaderElectionID:        "csimetricsad0b5146.weka.io",
		LeaderElectionNamespace: namespace,
		PprofBindAddress:        pprofBindAddress,
	})
	clog.SetLogger(logrLog)

	if err != nil {
		logger.Error().Err(err).Msg("unable to start manager")
		Die("unable to start manager, cannot run MetricsServer without it")
	}

	go func() {
		if err := ms.manager.Start(ctx); err != nil {
			logger.Error().Err(err).Msg("unable to start manager")
			Die("unable to start manager, cannot run MetricsServer without it")
		}
	}()

}

func (ms *MetricsServer) getFullListOfPersistentVolumes(ctx context.Context, out chan<- *v1.PersistentVolume) error {
	if ms.GetK8sApi() == nil {
		close(out)
		return errors.New("no k8s API client available")
	}

	// Get all PersistentVolumes
	pvList, err := ms.GetK8sApi().CoreV1().PersistentVolumes().List(
		ctx,
		metav1.ListOptions{})
	if err != nil {
		return err
	}
	go func() {
		defer close(out)
		for _, pv := range pvList.Items {
			if pv.Spec.CSI != nil &&
				pv.Spec.CSI.Driver == ms.driver.name &&
				pv.Spec.Capacity != nil &&
				pv.Spec.CSI.VolumeAttributes != nil &&
				slices.Contains(KnownVolTypes[:], VolumeType(pv.Spec.CSI.VolumeAttributes["volumeType"])) &&
				pv.Spec.CSI.NodePublishSecretRef != nil &&
				(pv.Status.Phase == v1.VolumeBound || pv.Status.Phase == v1.VolumeReleased) {

				select {
				case <-ctx.Done():
					return
				case out <- &pv:
				}
			}
		}
	}()
	return nil
}

// streamPersistentVolumes streams PersistentVolumes from Kubernetes, sending them to the provided channel.
func (ms *MetricsServer) streamPersistentVolumes(ctx context.Context) {
	component := "streamPersistentVolumes"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	out := ms.persistentVolumesChan

	for {
		logger.Info().Msg("Fetching existing persistent volumes")
		pvList := &v1.PersistentVolumeList{}
		err := ms.manager.GetClient().List(ctx, pvList, &client.ListOptions{})
		//err = restClient.Get()
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch PersistentVolumes, no statistics will be available, will retry in 10 seconds")
			time.Sleep(10 * time.Second)
			continue
		}
		logger.Info().Int("pv_count", len(pvList.Items)).Msg("Fetched list of PersistentVolumes")
		for _, pv := range pvList.Items {
			select {
			case <-ctx.Done():
				return
			case out <- &pv:
			}
		}
		ms.pruneOldVolumes(ctx, &(pvList.Items))
		dur := ms.getConfig().wekaMetricsFetchInterval

		logger.Info().Int("pv_count", len(pvList.Items)).Dur("wait_diration", dur).Msg("Sent all volumes to processing, waiting for next fetch")

		// refresh list of volumes every wekaMetricsFetchInterval
		time.Sleep(dur)
	}
}

func (ms *MetricsServer) pruneOldVolumes(ctx context.Context, pvList *[]v1.PersistentVolume) {
	currentUIDs := make(map[types.UID]struct{}, len(*pvList))
	for _, pv := range *pvList {
		currentUIDs[pv.UID] = struct{}{}
	}
	// Remove metrics for UIDs not present in the current PV list
	for uid := range ms.volumeMetrics.Metrics {
		if _, exists := currentUIDs[uid]; !exists {
			ctx, span := otel.Tracer(TracerName).Start(ctx, "pruneOldVolumes")
			ms.pruneVolumeMetric(ctx, uid)
			span.End()
		}
	}
}

func (ms *MetricsServer) removePrometheusMetricsForLabels(ctx context.Context, metric *VolumeMetric) {
	logger := log.Ctx(ctx)
	logger.Trace().Str("pv_name", metric.persistentVolume.Name).Msg("Removing prometheus metrics labels for volume")
	labelValues := ms.createPrometheusLabelsForMetric(metric)
	ms.prometheusMetrics.Capacity.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Used.DeleteLabelValues(labelValues...)
	ms.prometheusMetrics.Free.DeleteLabelValues(labelValues...)
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

	metric := ms.volumeMetrics.GetVolumeMetric(pvUUID)
	ms.volumeMetrics.RemoveVolumeMetric(pvUUID)
	ms.removePrometheusMetricsForLabels(ctx, metric)
	logger.Info().Str("pv_uid", string(pvUUID)).Msg("Removed persistent volume from metric collection")
}

func (ms *MetricsServer) processStreamedPersistentVolumes(ctx context.Context) {
	component := "processStreamedPersistentVolumes"
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

	logger.Info().Str("pv_name", pv.Name).Str("phase", string(pv.Status.Phase)).Msg("Received a new PersistentVolume for processing")

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

	volume, err := NewVolumeFromId(ctx, pv.Spec.CSI.VolumeHandle, apiClient, ms)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create Volume from ID")
		return
	}
	volume.persistentVol = pv // Set the PersistentVolume reference in the Volume object
	// Create a new VolumeMetric instance
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
	if secret, exists := ms.secrets.secrets[hash]; exists {
		ms.secrets.Unlock()
		logger.Trace().Str("namespace", secretNamespace).Str("name", secretName).Msg("Using a secret from cache")
		return secret, nil // Return cached secret if available
	}
	ms.secrets.Unlock()
	logger.Debug().Str("namespace", secretNamespace).Str("name", secretName).Msg("Fetching Secret")
	if ms.GetK8sApi() == nil {
		return nil, errors.New("no k8s API client available")
	}
	secret, err := ms.GetK8sApi().CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch secret %s/%s: %w", secretNamespace, secretName, err)
	}
	ms.secrets.Lock()
	defer ms.secrets.Unlock()
	ms.secrets.secrets[hash] = secret // Cache the secret
	return secret, nil
}

// fetchSingleMetric fetches the prometheusMetrics for a single Persistent Volume and sends it to the MetricsServer's incoming requests channel.
func (ms *MetricsServer) fetchSingleMetric(ctx context.Context, vm *VolumeMetric) error {
	component := "fetchSingleMetric"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	// Fetch prometheusMetrics for a single persistent volume
	logger.Trace().Str("persistent_volume", vm.persistentVolume.Name).Msg("Fetching Metric")

	qosMetric, err := vm.volume.FetchPvStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch metric for persistent volume %s: %w", vm.persistentVolume.Name, err)
	}

	vm.metrics = qosMetric
	ms.volumeMetricsChan <- vm // Send the metric to the MetricsServer's incoming requests channel
	return nil
}

func (ms *MetricsServer) FetchMetrics(ctx context.Context) error {
	component := "FetchMetrics"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	wg := &sync.WaitGroup{}
	sem := make(chan struct{}, ms.getConfig().wekaMetricsFetchConcurrentRequests)

	for _, vm := range ms.volumeMetrics.Metrics {
		wg.Add(1)
		sem <- struct{}{} // Acquire a slot in the semaphore

		go func(vm *VolumeMetric) {
			defer wg.Done()
			defer func() { <-sem }() // Release the slot in the semaphore

			err := ms.fetchSingleMetric(ctx, vm)
			if err != nil {
				fmt.Printf("failed to fetch prometheusMetrics for persistent volume %s: %v\n", vm.persistentVolume.Name, err)
			}
		}(vm)
	}

	wg.Wait()
	return nil
}

// reportMetricsStreamer listens on the volumeMetricsChan channel and reports prometheusMetrics to Prometheus.
func (ms *MetricsServer) reportMetricsStreamer(ctx context.Context) {
	component := "reportMetricsStreamer"
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

func (ms *MetricsServer) PeriodicFetchMetrics(ctx context.Context) {
	// Periodically fetch prometheusMetrics for all persistent volumes
	component := "reportMetricsStreamer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Info().Str("interval", ms.getConfig().wekaMetricsFetchInterval.String()).Msg("starting reporting metrics every defined interval")
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
			logger.Info().Int("pv_count", len(ms.volumeMetrics.Metrics)).Msg("Fetching prometheusMetrics for PersistentVolumes")
			err := ms.FetchMetrics(ctx)
			if err != nil {
				logger.Error().Err(err).Msg("Error fetching prometheusMetrics")
			}
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

	// TODO: make sure that we do not continue further till leader election lease is acquired

	time.Sleep(1 * time.Second) // to ensure the manager cache is fully started before fetching PersistentVolumes
	logger.Info().Msg("started manager, starting to fetch PersistentVolumes")

	// Initialize the incoming requests channel and report all incoming prometheusMetrics
	ms.wg.Add(1)
	go func() {

		ms.volumeMetricsChan = make(chan *VolumeMetric, ms.config.wekaMetricsFetchConcurrentRequests)
		for {
			select {
			case <-ctx.Done():
				logger.Info().Msg("Metrics server context cancelled, stopping...")
				ms.wg.Done()
				return
			default:
				// Keep the goroutine alive to listen for incoming requests
			}
		}
	}()

	// Start processing streamed PersistentVolumes
	go func() {
		ms.streamPersistentVolumes(ctx)
	}()

	// Start processing streamed PersistentVolumes
	go func() {
		ms.processStreamedPersistentVolumes(ctx)
	}()

	// Start reporting prometheusMetrics
	go func() {
		ms.reportMetricsStreamer(ctx) // Start reporting prometheusMetrics
	}()

	// Start periodic metrics fetching
	go func() {
		ms.PeriodicFetchMetrics(ctx)
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
