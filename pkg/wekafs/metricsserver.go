package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"math"
	"os"
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
	if vms.HasVolumeMetric(pvUID) {
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
	if vms.HasVolumeMetric(pvUID) {
		delete(vms.Metrics, pvUID)
	}
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
	ctx := context.Background()

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
	ret.Start(ctx)
	return ret

}

// GetK8sApi returns the Kubernetes API client from the driver or KUBECONFIG environment variable.
func (ms *MetricsServer) GetK8sApi() *kubernetes.Clientset {
	return ms.driver.GetK8sApiClient()
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
	if ms.GetK8sApi() == nil {
		logger.Error().Msg("no k8s API client available, cannot stream PersistentVolumes, no statistics will be available")
	}

	logger.Info().Msg("Fetching existing persistent volumes")
	// Fetch initial list
	pvList, err := ms.GetK8sApi().CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error().Msg("Failed to fetch PersistentVolumes, no statistics will be available")
	}
	logger.Info().Int("pv_count", len(pvList.Items)).Msg("Fetched initial list of PersistentVolumes")
	for _, pv := range pvList.Items {
		logger.Trace().Str("pv_name", pv.Name).Msg("Sending PersistentVolume to channel for processing")
		select {
		case <-ctx.Done():
			return
		case out <- &pv:
		}
	}

	logger.Info().Msg("Initial persistent volumes fetched, starting watch")
	go func() {
		logger.Info().Msg("Starting watch for PersistentVolumes")
		defer logger.Info().Msg("PersistentVolumes watch goroutine stopped")
		for {
			watcher, err := ms.GetK8sApi().CoreV1().PersistentVolumes().Watch(ctx, metav1.ListOptions{})
			if err != nil {
				logger.Error().Err(err).Msg("Failed to start PersistentVolumes watch, retrying in 5s")
				select {
				case <-ctx.Done():
					logger.Info().Msg("Context cancelled, stopping PersistentVolumes watch")
					return
				case <-time.After(5 * time.Second):
					continue
				}
			}
			for {
				select {
				case <-ctx.Done():
					logger.Info().Msg("Context cancelled, stopping PersistentVolumes watch")
					watcher.Stop()
					return
				case event, ok := <-watcher.ResultChan():
					if !ok {
						logger.Warn().Msg("PersistentVolumes watch channel closed, restarting watch")
						watcher.Stop()
						// Optionally, update resourceVersion here if needed
						time.Sleep(1 * time.Second)
						break
					}
					pv, ok := event.Object.(*v1.PersistentVolume)
					if !ok {
						logger.Error().Msg("Received non-PersistentVolume object from watch channel, skipping")
						continue
					}
					logger.Trace().Str("pv_name", pv.Name).Str("event_type", string(event.Type)).Msg("Received PersistentVolume event")
					out <- pv
				}
			}
		}
	}()
}

func (ms *MetricsServer) pruneVolumeMetric(ctx context.Context, pvUUID types.UID) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "pruneVolumeMetric")
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	ms.volumeMetrics.RemoveVolumeMetric(pvUUID)
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
				logger.Trace().Str("pv_name", pv.Name).Str("phase", string(pv.Status.Phase)).Msg("Received PersistentVolume for processing")
				if pv.DeletionTimestamp != nil {
					logger.Trace().Str("pv_name", pv.Name).Msg("PersistentVolume is being deleted, pruning metrics")
					ms.pruneVolumeMetric(ctx, pv.UID)
					return
				}
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
		logger.Trace().Str("pv_name", pv.Name).Err(err).Msg("Skipping PersistentVolume, not valid for processing")
		return
	}

	// Check if the PersistentVolume is already being processed
	if ms.volumeMetrics.HasVolumeMetric(pv.UID) {
		logger.Debug().Str("pv_name", pv.Name).Msg("PersistentVolume already being processed, updating")
		ms.volumeMetrics.GetVolumeMetric(pv.UID).persistentVolume = pv // Update the PersistentVolume reference in the existing VolumeMetric
		return
	}

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
	client, err := ms.getApiStore().fromSecrets(ctx, secretData, ms.nodeID)
	if err != nil {
		logger.Error().Err(err).Str("pv_name", pv.Name).Msg("Failed to create API client from secret, skipping PersistentVolume")
		return
	}

	volume, err := NewVolumeFromId(ctx, pv.Spec.CSI.VolumeHandle, client, ms)
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
		apiClient:        client,
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
	logger.Debug().Msg("Fetching Secret")

	// Fetch the secret from Kubernetes
	if secretName == "" || secretNamespace == "" {
		return nil, errors.New("secret name and namespace must be provided")
	}
	secretKey := fmt.Sprintf("%s/%s", secretNamespace, secretName)
	hash := hashString(secretKey, math.MaxInt)
	if secret, exists := ms.secrets.secrets[hash]; exists {
		return secret, nil // Return cached secret if available
	}
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
			pvName := metric.persistentVolume.Name
			guid := metric.apiClient.ClusterGuid.String()
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
				logger.Trace().Int64("writes", metric.volume.lastStats.Reads).Msg("Updated last stats for volume")
			}
			// enrich labels with persistent volume claim information
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

			logger.Trace().Str("pv_name", pvName).Msg("Reporting prometheusMetrics for PersistentVolume")
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

	// Initialize the incoming requests channel and report all incoming prometheusMetrics
	go func() {
		ms.wg.Add(1)

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
	ms.wg.Wait()
}

func (ms *MetricsServer) Stop(ctx context.Context) {
	component := "StopMetricsServer"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	ms.Lock()
	defer ms.Unlock()
	if !ms.running {
		return // Already stopped
	}
	ms.running = false
	close(ms.volumeMetricsChan)     // Close the channel to stop reporting prometheusMetrics
	close(ms.persistentVolumesChan) // Close the channel to stop streaming PersistentVolumes
	logger.Info().Msg("Metrics server stopped")
}
