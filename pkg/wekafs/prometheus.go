package wekafs

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics reported by the metrics server: one set describing each observed PersistentVolume, and
// one describing the server's own work fetching them.
//
// The volume metrics are timestamped (see timedmetrics.go) because their values are read from the
// Weka API on the server's own schedule and served from a cache in between, so the sample time is
// not the scrape time. The server's own metrics are ordinary collectors - they describe work
// happening as it is recorded.

var (
	// LabelsForCsiVolumes identifies one PersistentVolume. It is deliberately specific: the series
	// are per volume by design, and the identifiers are what makes a volume's metrics findable.
	// organization is the Weka tenant a volume belongs to, taken from the credentials its API client
	// authenticated with. Every volume has exactly one, so it adds no series - it only makes the
	// existing ones groupable by tenant.
	LabelsForCsiVolumes    = []string{"csi_driver_name", "pv_name", "cluster_guid", "storage_class_name", "filesystem_name", "volume_type", "organization", "pvc_name", "pvc_namespace", "pvc_uid"}
	LabelsForFilesystemOps = []string{"csi_driver_name", "cluster_guid", "filesystem_name"}

	HistogramDurationBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000}
)

const (
	MetricsServerSubsystem = "metricsserver"
	VolumesSubsystem       = "volume"
)

type PrometheusMetrics struct {
	volumes struct {
		CapacityBytes           *TimedGaugeVec
		UsedBytes               *TimedGaugeVec
		FreeBytes               *TimedGaugeVec
		PvReportedCapacityBytes *TimedGaugeVec

		ReadsTotal      *TimedCounterVec
		WritesTotal     *TimedCounterVec
		ReadBytesTotal  *TimedCounterVec
		WriteBytes      *TimedCounterVec
		ReadDurationUs  *TimedCounterVec
		WriteDurationUs *TimedCounterVec
	}

	server struct {
		// metricsserver metrics
		// Fetching PersistentVolume Objects from Kubernetes API. Refers to the number of batch requests made to fetch PVs.
		FetchPvBatchOperationsInvokeCount       prometheus.Counter
		FetchPvBatchOperationsSuccessCount      prometheus.Counter
		FetchPvBatchOperationFailureCount       prometheus.Counter // total number of failed operations to fetch PVs
		FetchPvBatchOperationsDurationSeconds   prometheus.Counter
		FetchPvBatchOperationsDurationHistogram prometheus.Histogram
		FetchPvBatchSize                        prometheus.Gauge

		// streaming Pv objects
		StreamPvOperationsCount prometheus.Counter // total number of operations performed on streaming PVs
		StreamPvBatchSize       prometheus.Gauge

		// processing PersistentVolume Objects. Refers to the number of operations performed on single PV
		ProcessPvOperationsCount             prometheus.Counter
		ProcessPvOperationsDurationSeconds   prometheus.Counter
		ProcessPvOperationsDurationHistogram prometheus.Histogram

		FetchMetricsBatchOperationsInvokeCount prometheus.Counter
		// fetching metric batches. refer to batches of periodic metrics fetch. Basically, this number should never be larger than fetch metrics interval
		FetchMetricsBatchOperationsSuccessCount      prometheus.Counter
		FetchMetricsBatchOperationsFailureCount      prometheus.Counter
		FetchMetricsBatchOperationsDurationSeconds   prometheus.Counter
		FetchMetricsBatchOperationsDurationHistogram prometheus.Histogram
		FetchMetricsBatchSize                        prometheus.Gauge
		FetchMetricsFrequencySeconds                 prometheus.Gauge // frequency of fetch metrics in seconds, taken from the configuration

		// fetching single metrics. refer to single metrics fetch from Weka cluster
		FetchSinglePvMetricsOperationsInvokeCount       prometheus.Counter
		FetchSinglePvMetricsOperationsSuccessCount      prometheus.Counter
		FetchSinglePvMetricsOperationsFailureCount      prometheus.Counter
		FetchSinglePvMetricsOperationsDurationSeconds   prometheus.Counter
		FetchSinglePvMetricsOperationsDurationHistogram prometheus.Histogram

		PersistentVolumeAdditionsCount  prometheus.Counter
		PersistentVolumeRemovalsCount   prometheus.Counter
		MonitoredPersistentVolumesGauge prometheus.Gauge

		PruneVolumesBatchInvokeCount       prometheus.Counter
		PruneVolumesBatchDurationSeconds   prometheus.Counter
		PruneVolumesBatchDurationHistogram prometheus.Histogram
		PruneVolumesBatchSize              prometheus.Gauge

		PeriodicFetchMetricsInvokeCount  prometheus.Counter // total number of periodic fetch metrics invocations
		PeriodicFetchMetricsSkipCount    prometheus.Counter
		PeriodicFetchMetricsSuccessCount prometheus.Counter
		PeriodicFetchMetricsFailureCount prometheus.Counter

		QuotaMapRefreshInvokeCount       *prometheus.CounterVec   // total number of quota map updates
		QuotaMapRefreshSuccessCount      *prometheus.CounterVec   // total number of successful quota map updates per filesystem
		QuotaMapRefreshFailureCount      *prometheus.CounterVec   // total number of quota map updates
		QuotaMapRefreshDurationSeconds   *prometheus.CounterVec   // total duration of quota map updates per filesystem in seconds
		QuotaMapRefreshDurationHistogram *prometheus.HistogramVec // histogram of durations for quota map updates per filesystem
		QuotaMapMissCount                *prometheus.CounterVec   // volumes absent from their filesystem's quota map, fetched one at a time instead

		QuotaUpdateBatchInvokeCount       prometheus.Counter   // total number of all quota updates
		QuotaUpdateBatchSuccessCount      prometheus.Counter   // total number of all quota updates
		QuotaUpdateBatchDurationSeconds   prometheus.Counter   // total duration of all quota updates in seconds
		QuotaUpdateBatchDurationHistogram prometheus.Histogram // histogram of durations for quota updates
		QuotaUpdateBatchSize              prometheus.Gauge     // total number of quotas updated in the last batch, or number of distinct observed filesystems
		QuotaCacheValiditySeconds         prometheus.Gauge     // frequency of quota updates in seconds, taken from the configuration

		ReportedMetricsSuccessCount prometheus.Counter // number of metrics reported to Prometheus across all . Should be equal to FetchSinglePvMetricsOperationsInvokeCount
		ReportedMetricsFailureCount prometheus.Counter // number of metrics that were not valid for reporting, e.g. appeared empty

	}
}

// The declarations below differ only in name and help, so the shared opts live here.

func newVolumeGauge(name, help string) *TimedGaugeVec {
	return NewTimedGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsPrefix, Subsystem: VolumesSubsystem, Name: name, Help: help,
	}, LabelsForCsiVolumes)
}

func newVolumeCounter(name, help string) *TimedCounterVec {
	return NewTimedCounterVec(prometheus.CounterOpts{
		Namespace: MetricsPrefix, Subsystem: VolumesSubsystem, Name: name, Help: help,
	}, LabelsForCsiVolumes)
}

func newServerCounter(name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: MetricsPrefix, Subsystem: MetricsServerSubsystem, Name: name, Help: help,
	})
}

func newServerGauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: MetricsPrefix, Subsystem: MetricsServerSubsystem, Name: name, Help: help,
	})
}

func newServerHistogram(name, help string) prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: MetricsPrefix, Subsystem: MetricsServerSubsystem, Name: name, Help: help,
		Buckets: HistogramDurationBuckets,
	})
}

func newFsCounterVec(name, help string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsPrefix, Subsystem: MetricsServerSubsystem, Name: name, Help: help,
	}, LabelsForFilesystemOps)
}

func newFsHistogramVec(name, help string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: MetricsPrefix, Subsystem: MetricsServerSubsystem, Name: name, Help: help,
		Buckets: HistogramDurationBuckets,
	}, LabelsForFilesystemOps)
}

// NewPrometheusMetrics builds the metrics without registering them, so that constructing a metrics
// server has no effect on any registry. Collectors returns them for the caller to register.
func NewPrometheusMetrics() *PrometheusMetrics {
	m := &PrometheusMetrics{}
	// initialize the Prometheus metrics for volume statistics
	m.volumes.CapacityBytes = newVolumeGauge("capacity_bytes", "Total capacity of the WEKA PersistentVolume in bytes")

	m.volumes.UsedBytes = newVolumeGauge("used_bytes", "Used capacity of the WEKA PersistentVolume in bytes")

	m.volumes.FreeBytes = newVolumeGauge("free_bytes", "Free capacity of the WEKA PersistentVolume in bytes")

	// Reported capacity of the WEKA PersistentVolume in bytes, taken from Kubernetes PV object
	m.volumes.PvReportedCapacityBytes = newVolumeGauge("pv_reported_capacity_bytes", "Reported capacity of the WEKA PersistentVolume in bytes (from Kubernetes PV object)")

	m.volumes.ReadsTotal = newVolumeCounter("reads_total", "Total READ Operations of the WEKA PersistentVolume")

	m.volumes.ReadBytesTotal = newVolumeCounter("read_bytes_total", "Total READ BYTES from the WEKA PersistentVolume")

	m.volumes.ReadDurationUs = newVolumeCounter("read_duration_us", "Total READ DURATION from the WEKA PersistentVolume in microseconds")

	m.volumes.WritesTotal = newVolumeCounter("writes_total", "Total WRITE Operations of the WEKA PersistentVolume")

	m.volumes.WriteBytes = newVolumeCounter("write_bytes_total", "Total WRITE BYTES to the WEKA PersistentVolume")

	m.volumes.WriteDurationUs = newVolumeCounter("write_duration_us", "Total WRITE DURATION to the WEKA PersistentVolume in microseconds")

	// metricsserver own metrics

	// metrics for fetching PersistentVolume objects from Kubernetes API
	m.server.FetchPvBatchOperationsInvokeCount = newServerCounter("fetch_pv_batch_operations_invoke_count", "Total number of operations to fetch PersistentVolume objects from Kubernetes API")

	m.server.FetchPvBatchOperationsSuccessCount = newServerCounter("fetch_pv_batch_operations_success_count_total", "Total number of operations to fetch PersistentVolume objects from Kubernetes API that succeeded")

	m.server.FetchPvBatchOperationFailureCount = newServerCounter("fetch_pv_batch_operations_failure_count_total", "Total number of failed operations to fetch PersistentVolume objects from Kubernetes API")

	m.server.FetchPvBatchOperationsDurationSeconds = newServerCounter("fetch_pv_batch_operations_duration_seconds", "Total duration of operations to fetch PersistentVolume objects from Kubernetes API in seconds")

	m.server.FetchPvBatchOperationsDurationHistogram = newServerHistogram("fetch_pv_batch_operations_duration_seconds_histogram", "Histogram of durations for fetching PersistentVolume objects from Kubernetes API")

	m.server.FetchPvBatchSize = newServerGauge("fetch_pv_batch_size", "Size of the batch of PersistentVolume objects fetched from Kubernetes API")

	// metrics for streaming PersistentVolume objects
	m.server.StreamPvOperationsCount = newServerCounter("stream_pv_operations_count_total", "Total number of operations performed on streaming PersistentVolume objects")

	m.server.StreamPvBatchSize = newServerGauge("stream_pv_batch_size", "Size of the batch of streaming PersistentVolume objects")

	// metrics for processing PersistentVolume objects
	m.server.ProcessPvOperationsCount = newServerCounter("process_pv_operations_count_total", "Total number of processed PersistentVolume objects")

	m.server.ProcessPvOperationsDurationSeconds = newServerCounter("process_pv_operations_duration_seconds", "Total duration of processing PersistentVolume objects in seconds")

	m.server.ProcessPvOperationsDurationHistogram = newServerHistogram("process_pv_operations_duration_seconds_histogram", "Histogram of durations for processing PersistentVolume objects")

	// metrics for fetching metrics from Weka cluster
	m.server.FetchMetricsBatchOperationsInvokeCount = newServerCounter("fetch_metrics_batch_operations_invoke_count_total", "Total number of fetch metrics batches from Weka cluster that were invoked")

	m.server.FetchMetricsBatchOperationsSuccessCount = newServerCounter("fetch_metrics_batch_operations_success_count_total", "Total number of fetch metrics batches from Weka cluster that were completed successfully")

	m.server.FetchMetricsBatchOperationsFailureCount = newServerCounter("fetch_metrics_batch_operations_failure_count_total", "Total number of fetch metrics batches from Weka cluster that failed")

	m.server.FetchMetricsBatchOperationsDurationSeconds = newServerCounter("fetch_metrics_batch_operations_duration_seconds", "Total duration of fetch metrics batches from Weka cluster in seconds")

	m.server.FetchMetricsBatchOperationsDurationHistogram = newServerHistogram("fetch_metrics_batch_operations_duration_seconds_histogram", "Histogram of durations for fetching metrics batches from Weka cluster")

	m.server.FetchMetricsBatchSize = newServerGauge("fetch_metrics_batch_size", "Size of the batch of metrics fetched from Weka cluster")

	m.server.FetchMetricsFrequencySeconds = newServerGauge("fetch_metrics_frequency_seconds", "Frequency, or interval of fetching metrics from Weka cluster in seconds, taken from the configuration. Too high value may lead to stale metrics or API overload")

	m.server.FetchSinglePvMetricsOperationsInvokeCount = newServerCounter("fetch_single_pv_metrics_invoke_count_total", "Total number of single metrics fetch operations from Weka cluster")

	m.server.FetchSinglePvMetricsOperationsSuccessCount = newServerCounter("fetch_single_pv_metrics_success_count_total", "Total number of single metrics fetch operations from Weka cluster that were completed successfully")

	m.server.FetchSinglePvMetricsOperationsFailureCount = newServerCounter("fetch_single_pv_metrics_failure_count_total", "Total number of single metrics fetch operations from Weka cluster that failed")

	m.server.FetchSinglePvMetricsOperationsDurationSeconds = newServerCounter("fetch_single_pv_metrics_operations_duration_seconds", "Total duration of single metrics fetch operations from Weka cluster in seconds")

	m.server.FetchSinglePvMetricsOperationsDurationHistogram = newServerHistogram("fetch_single_pv_metrics_operations_duration_seconds_histogram", "Histogram of durations for fetching single metrics from Weka cluster")

	// metrics for PersistentVolumes added/removed from metrics collection
	m.server.PersistentVolumeAdditionsCount = newServerCounter("pv_additions_count_total", "Total number of PersistentVolumes added for metrics collection")

	m.server.PersistentVolumeRemovalsCount = newServerCounter("pv_removals_count_total", "Total number of PersistentVolumes removed from metrics collection")

	// metrics for PersistentVolumes currently monitored by the metrics server
	m.server.MonitoredPersistentVolumesGauge = newServerGauge("monitored_persistent_volumes_gauge", "Total number of PersistentVolumes currently monitored by the metrics server, should eventually be equal to the number of PVs in the metrics server cache")

	// metrics for pruning volumes batch
	m.server.PruneVolumesBatchInvokeCount = newServerCounter("prune_volumes_batch_invoke_count_total", "Total number of prune volumes batch operations invoked")

	m.server.PruneVolumesBatchDurationSeconds = newServerCounter("prune_volumes_batch_duration_seconds", "Total duration of prune volumes batch operations in seconds")

	m.server.PruneVolumesBatchDurationHistogram = newServerHistogram("prune_volumes_batch_duration_seconds_histogram", "Histogram of durations for prune volumes batch operations")

	m.server.PruneVolumesBatchSize = newServerGauge("prune_volumes_batch_size", "Total number of volumes pruned in the last batch")

	// metrics for periodic fetch metrics
	m.server.PeriodicFetchMetricsInvokeCount = newServerCounter("periodic_fetch_metrics_invoke_count_total", "Total number of periodic fetch metrics invocations")

	m.server.PeriodicFetchMetricsSkipCount = newServerCounter("periodic_fetch_metrics_skip_count_total", "Total number of periodic fetch metrics invocations that were skipped")

	m.server.PeriodicFetchMetricsSuccessCount = newServerCounter("periodic_fetch_metrics_success_count_total", "Total number of successful periodic fetch metrics invocations")

	m.server.PeriodicFetchMetricsFailureCount = newServerCounter("periodic_fetch_metrics_failure_count_total", "Total number of failed periodic fetch metrics invocations")

	// metrics for quota map updates
	m.server.QuotaMapRefreshInvokeCount = newFsCounterVec("quota_map_refresh_invoke_count_total", "Total number of quota map updates per filesystem")

	m.server.QuotaMapRefreshSuccessCount = newFsCounterVec("quota_map_refresh_success_count_total", "Total number of successful quota map updates per filesystem")

	m.server.QuotaMapRefreshFailureCount = newFsCounterVec("quota_map_refresh_failure_count_total", "Total number of failed quota map updates per filesystem")

	m.server.QuotaMapRefreshDurationSeconds = newFsCounterVec("quota_map_refresh_duration_seconds", "Total duration of quota map updates per filesystem in seconds")

	m.server.QuotaMapRefreshDurationHistogram = newFsHistogramVec("quota_map_refresh_duration_seconds_histogram", "Histogram of durations for quota map updates per filesystem")

	m.server.QuotaMapMissCount = newFsCounterVec("quota_map_miss_count_total", "Total number of volume readings that could not be served from the filesystem-wide quota map and fell back to a per-volume API request. Expected to be non-zero only for snapshot-backed volumes, whose quota lives in a snapshot view and is not returned when listing a filesystem's quotas")

	// metrics for quota update batches
	m.server.QuotaUpdateBatchInvokeCount = newServerCounter("quota_update_batch_invoke_count_total", "Total number of all quota update batches performed")

	m.server.QuotaUpdateBatchSuccessCount = newServerCounter("quota_update_batch_success_count_total", "Total number of all quota update batches completed")

	m.server.QuotaUpdateBatchDurationSeconds = newServerCounter("quota_update_batch_duration_seconds", "Total duration of all quota update batches in seconds")

	m.server.QuotaUpdateBatchDurationHistogram = newServerHistogram("quota_update_batch_duration_seconds_histogram", "Histogram of durations for quota update batches")

	m.server.QuotaUpdateBatchSize = newServerGauge("quota_update_batch_size", "Total number of distinct observed filesystems in the last quota update batch")

	m.server.QuotaCacheValiditySeconds = newServerGauge("quota_cache_validity_seconds", "Time period in which fetched quota is considered valid so no new requests are performed. Higher value may lead to stale quota information, lower value may lead to quota API overload")

	m.server.ReportedMetricsSuccessCount = newServerCounter("reported_metrics_success_count_total", "Total number of metrics reported to Prometheus across all PersistentVolumes. Should be equal to FetchSinglePvMetricsOperationsInvokeCount")

	m.server.ReportedMetricsFailureCount = newServerCounter("reported_metrics_failure_count_total", "Total number of metrics that were not valid for reporting, e.g. appeared empty")
	return m
}

// Collectors returns every metric the metrics server exports, for the caller to register.
func (m *PrometheusMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.volumes.CapacityBytes,
		m.volumes.UsedBytes,
		m.volumes.FreeBytes,
		m.volumes.PvReportedCapacityBytes,
		m.volumes.ReadsTotal,
		m.volumes.ReadBytesTotal,
		m.volumes.ReadDurationUs,
		m.volumes.WritesTotal,
		m.volumes.WriteBytes,
		m.volumes.WriteDurationUs,
		m.server.FetchPvBatchOperationsInvokeCount,
		m.server.FetchPvBatchOperationsSuccessCount,
		m.server.FetchPvBatchOperationFailureCount,
		m.server.FetchPvBatchOperationsDurationSeconds,
		m.server.FetchPvBatchOperationsDurationHistogram,
		m.server.FetchPvBatchSize,
		m.server.StreamPvOperationsCount,
		m.server.StreamPvBatchSize,
		m.server.ProcessPvOperationsCount,
		m.server.ProcessPvOperationsDurationSeconds,
		m.server.ProcessPvOperationsDurationHistogram,
		m.server.FetchMetricsBatchOperationsInvokeCount,
		m.server.FetchMetricsBatchOperationsSuccessCount,
		m.server.FetchMetricsBatchOperationsFailureCount,
		m.server.FetchMetricsBatchOperationsDurationSeconds,
		m.server.FetchMetricsBatchOperationsDurationHistogram,
		m.server.FetchMetricsBatchSize,
		m.server.FetchMetricsFrequencySeconds,
		m.server.FetchSinglePvMetricsOperationsInvokeCount,
		m.server.FetchSinglePvMetricsOperationsSuccessCount,
		m.server.FetchSinglePvMetricsOperationsFailureCount,
		m.server.FetchSinglePvMetricsOperationsDurationSeconds,
		m.server.FetchSinglePvMetricsOperationsDurationHistogram,
		m.server.PersistentVolumeAdditionsCount,
		m.server.PersistentVolumeRemovalsCount,
		m.server.MonitoredPersistentVolumesGauge,
		m.server.PruneVolumesBatchInvokeCount,
		m.server.PruneVolumesBatchDurationSeconds,
		m.server.PruneVolumesBatchDurationHistogram,
		m.server.PruneVolumesBatchSize,
		m.server.PeriodicFetchMetricsInvokeCount,
		m.server.PeriodicFetchMetricsSkipCount,
		m.server.PeriodicFetchMetricsSuccessCount,
		m.server.PeriodicFetchMetricsFailureCount,
		m.server.QuotaMapRefreshInvokeCount,
		m.server.QuotaMapRefreshSuccessCount,
		m.server.QuotaMapRefreshFailureCount,
		m.server.QuotaMapRefreshDurationSeconds,
		m.server.QuotaMapRefreshDurationHistogram,
		m.server.QuotaMapMissCount,
		m.server.QuotaUpdateBatchInvokeCount,
		m.server.QuotaUpdateBatchSuccessCount,
		m.server.QuotaUpdateBatchDurationSeconds,
		m.server.QuotaUpdateBatchDurationHistogram,
		m.server.QuotaUpdateBatchSize,
		m.server.QuotaCacheValiditySeconds,
		m.server.ReportedMetricsSuccessCount,
		m.server.ReportedMetricsFailureCount,
	}
}
