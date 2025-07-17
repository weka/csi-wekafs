package wekafs

import (
	"github.com/prometheus/client_golang/prometheus"
)

type PrometheusMetrics struct {
	Capacity   *prometheus.GaugeVec
	Used       *prometheus.GaugeVec
	Free       *prometheus.GaugeVec
	PvCapacity *prometheus.GaugeVec

	Reads           *prometheus.CounterVec
	Writes          *prometheus.CounterVec
	ReadBytes       *prometheus.CounterVec
	WriteBytes      *prometheus.CounterVec
	ReadDurationUs  *prometheus.CounterVec
	WriteDurationUs *prometheus.CounterVec

	// metricsserver metrics
	// Fetching PersistentVolume Objects from Kubernetes API. Refers to the number of batch requests made to fetch PVs.
	FetchPvBatchOperations            prometheus.Counter
	FetchPvBatchOperationFailureCount prometheus.Counter // total number of failed operations to fetch PVs
	FetchPvBatchOperationsDuration    prometheus.Counter
	FetchPvBatchOperationsHistogram   prometheus.Histogram
	FetchPvBatchSize                  prometheus.Gauge // total number of PVs fetched in the last batch

	// streaming Pv objects
	StreamPvOperations prometheus.Counter // total number of operations performed on streaming PVs

	// processing PersistentVolume Objects. Refers to the number of operations performed on single PV
	ProcessPvOperations          prometheus.Counter
	ProcessPvOperationsDuration  prometheus.Counter
	ProcessPvOperationsHistogram prometheus.Histogram
	ProcessPvQueueSize           prometheus.Gauge // total number of PVs in the queue for processing

	// fetching metric batches. refer to batches of periodic metrics fetch. Basically, this number should never be larger than fetch metrics interval
	FetchMetricsBatchOperations          prometheus.Counter
	FetchMetricsBatchOperationsDuration  prometheus.Counter
	FetchMetricsBatchOperationsHistogram prometheus.Histogram
	FetchMetricsBatchSize                prometheus.Gauge

	// fetching single metrics. refer to single metrics fetch from Weka cluster
	FetchSinglePvMetricsOperations          prometheus.Counter
	FetchSinglePvMetricsOperationsDuration  prometheus.Counter
	FetchSinglePvMetricsOperationsHistogram prometheus.Histogram
	FetchSinglePvMetricsQueueSize           prometheus.Gauge // total number of single metrics in the queue for processing

	PersistentVolumesAddedForMetricsCollection    prometheus.Counter
	PersistentVolumesRemovedFromMetricsCollection prometheus.Counter
	PersistentVolumesMonitored                    prometheus.Gauge

	PruneVolumesBatchOperations          prometheus.Counter
	PruneVolumesBatchOperationsDuration  prometheus.Counter
	PruneVolumesBatchOperationsHistogram prometheus.Histogram
	PruneVolumesBatchSize                prometheus.Gauge // total number of volumes pruned in the last batch

	PeriodicFetchMetricsInvokeCount  prometheus.Counter // total number of periodic fetch metrics invocations
	PeriodicFetchMetricsSkipCount    prometheus.Counter
	PeriodicFetchMetricsSuccessCount prometheus.Counter
	PeriodicFetchMetricsFailureCount prometheus.Counter
}

func (m *PrometheusMetrics) Init() {
	// initialize the Prometheus metrics for volume statistics
	m.Capacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "weka_csi_volume_capacity_bytes",
			Help: "Total capacity of the WEKA PersistentVolume in bytes",
		},
		MetricsLabels,
	)

	m.Used = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "weka_csi_volume_used_bytes",
			Help: "Used capacity of the WEKA PersistentVolume in bytes",
		},
		MetricsLabels,
	)

	m.Free = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "weka_csi_volume_free_bytes",
			Help: "Free capacity of the WEKA PersistentVolume in bytes",
		},
		MetricsLabels,
	)

	m.PvCapacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "weka_csi_reported_pv_capacity_bytes",
			Help: "Reported capacity of the WEKA PersistentVolumes in bytes (from Kubernetes PV object)",
		},
		MetricsLabels,
	)

	m.Reads = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_reads_total",
			Help: "Total READ Operations of the WEKA PersistentVolume",
		},
		MetricsLabels,
	)

	m.ReadBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_read_bytes_total",
			Help: "Total READ BYTES from the WEKA PersistentVolume",
		},
		MetricsLabels,
	)

	m.ReadDurationUs = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_read_duration_us",
			Help: "Total READ DURATION from the WEKA PersistentVolume in microseconds",
		},
		MetricsLabels,
	)
	m.Writes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_writes_total",
			Help: "Total WRITE Operations of the WEKA PersistentVolume",
		},
		MetricsLabels,
	)
	m.WriteBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_write_bytes_total",
			Help: "Total WRITE BYTES to the WEKA PersistentVolume",
		},
		MetricsLabels,
	)
	m.WriteDurationUs = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weka_csi_volume_write_duration_us",
			Help: "Total WRITE DURATION to the WEKA PersistentVolume in microseconds",
		},
		MetricsLabels,
	)
	prometheus.MustRegister(m.Capacity, m.Used, m.Free, m.PvCapacity, m.Reads, m.ReadBytes, m.ReadDurationUs, m.Writes, m.WriteBytes, m.WriteDurationUs)

	// metricsserver own metrics
	m.FetchPvBatchOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_pv_batch_operations_total",
		Help: "Total number of operations to fetch PersistentVolume objects from Kubernetes API",
	})

	m.FetchPvBatchOperationFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_pv_batch_operations_failures_total",
		Help: "Total number of failed operations to fetch PersistentVolume objects from Kubernetes API",
	})

	m.FetchPvBatchOperationsDuration = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds",
		Help: "Total duration of operations to fetch PersistentVolume objects from Kubernetes API in seconds",
	})

	m.FetchPvBatchOperationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds_histogram",
		Help: "Histogram of durations for fetching PersistentVolume objects from Kubernetes API",
	})

	m.FetchPvBatchSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_fetch_pv_batch_size",
		Help: "Size of the batch of PersistentVolume objects fetched from Kubernetes API",
	})

	m.StreamPvOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_stream_pv_operations_total",
		Help: "Total number of operations performed on streaming PersistentVolume objects",
	})

	m.ProcessPvQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_process_pv_queue_size",
		Help: "Total number of PersistentVolumes in the queue for processing",
	})

	m.ProcessPvOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_process_pv_operations_total",
		Help: "Total number of processed PersistentVolume objects",
	})

	m.ProcessPvOperationsDuration = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_process_pv_operations_duration_seconds",
		Help: "Total duration of processing PersistentVolume objects in seconds",
	})

	m.ProcessPvOperationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "weka_csi_metricsserver_process_pv_operations_duration_seconds_histogram",
		Help: "Histogram of durations for processing PersistentVolume objects",
	})

	m.FetchMetricsBatchOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_metrics_batch_operations_total",
		Help: "Total number of fetch metrics batches from Weka cluster",
	})

	m.FetchMetricsBatchOperationsDuration = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds",
		Help: "Total duration of fetch metrics batches from Weka cluster in seconds",
	})

	m.FetchMetricsBatchOperationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds_histogram",
		Help: "Histogram of durations for fetching metrics batches from Weka cluster",
	})

	m.FetchMetricsBatchSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_fetch_metrics_batch_size",
		Help: "Size of the batch of metrics fetched from Weka cluster",
	})

	m.FetchSinglePvMetricsOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_single_pv_metrics_operations_total",
		Help: "Total number of single metrics fetch operations from Weka cluster",
	})

	m.FetchSinglePvMetricsOperationsDuration = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds",
		Help: "Total duration of single metrics fetch operations from Weka cluster in seconds",
	})

	m.FetchSinglePvMetricsOperationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds_histogram",
		Help: "Histogram of durations for fetching single metrics from Weka cluster",
	})

	m.FetchSinglePvMetricsQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_fetch_single_pv_metrics_queue_size",
		Help: "Total number of single metrics in the queue for processing",
	})

	m.PersistentVolumesAddedForMetricsCollection = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_pvs_added_for_metrics_collection_total",
		Help: "Total number of PersistentVolumes added for metrics collection",
	})

	m.PersistentVolumesRemovedFromMetricsCollection = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_pvs_removed_from_metrics_collection_total",
		Help: "Total number of PersistentVolumes removed from metrics collection",
	})

	m.PersistentVolumesMonitored = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_pvs_monitored",
		Help: "Total number of PersistentVolumes currently monitored by the metrics server",
	})

	m.PruneVolumesBatchOperations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_prune_volumes_batch_operations_total",
	})

	m.PruneVolumesBatchOperationsDuration = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_prune_volumes_batch_operations_duration_seconds",
		Help: "Total duration of prune volumes batch operations in seconds",
	})

	m.PruneVolumesBatchOperationsHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "weka_csi_metricsserver_prune_volumes_batch_operations_duration_seconds_histogram",
		Help: "Histogram of durations for prune volumes batch operations",
	})

	m.PruneVolumesBatchSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weka_csi_metricsserver_prune_volumes_batch_size",
		Help: "Total number of volumes pruned in the last batch",
	})

	m.PeriodicFetchMetricsInvokeCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_periodic_fetch_metrics_invoke_count_total",
		Help: "Total number of periodic fetch metrics invocations",
	})

	m.PeriodicFetchMetricsSkipCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_periodic_fetch_metrics_skip_count_total",
		Help: "Total number of periodic fetch metrics invocations that were skipped",
	})

	m.PeriodicFetchMetricsSuccessCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_periodic_fetch_metrics_success_count_total",
		Help: "Total number of successful periodic fetch metrics invocations",
	})

	m.PeriodicFetchMetricsFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "weka_csi_metricsserver_periodic_fetch_metrics_failure_count_total",
		Help: "Total number of failed periodic fetch metrics invocations",
	})

	prometheus.MustRegister(
		m.FetchPvBatchOperations,
		m.FetchPvBatchOperationFailureCount,
		m.FetchPvBatchOperationsDuration,
		m.FetchPvBatchOperationsHistogram,
		m.FetchPvBatchSize,
		m.StreamPvOperations,
		m.ProcessPvOperations,
		m.ProcessPvOperationsDuration,
		m.ProcessPvOperationsHistogram,
		m.ProcessPvQueueSize,
		m.FetchMetricsBatchOperations,
		m.FetchMetricsBatchOperationsDuration,
		m.FetchMetricsBatchOperationsHistogram,
		m.FetchMetricsBatchSize,
		m.FetchSinglePvMetricsOperations,
		m.FetchSinglePvMetricsOperationsDuration,
		m.FetchSinglePvMetricsOperationsHistogram,
		m.FetchSinglePvMetricsQueueSize,
		m.PersistentVolumesAddedForMetricsCollection,
		m.PersistentVolumesRemovedFromMetricsCollection,
		m.PersistentVolumesMonitored,
		m.PruneVolumesBatchOperations,
		m.PruneVolumesBatchOperationsDuration,
		m.PruneVolumesBatchOperationsHistogram,
		m.PruneVolumesBatchSize,
		m.PeriodicFetchMetricsInvokeCount,
		m.PeriodicFetchMetricsSkipCount,
		m.PeriodicFetchMetricsSuccessCount,
		m.PeriodicFetchMetricsFailureCount,
	)
}

func NewPrometheusMetrics() *PrometheusMetrics {
	metrics := &PrometheusMetrics{}
	metrics.Init()
	return metrics
}
