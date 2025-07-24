package wekafs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"go.uber.org/atomic"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

var (
	LabelsForCsiVolumes    = []string{"csi_driver_name", "pv_name", "cluster_guid", "storage_class_name", "filesystem_name", "volume_type", "pvc_name", "pvc_namespace", "pvc_uid"}
	LabelsForFilesystemOps = []string{"csi_driver_name", "cluster_guid", "filesystem_name"}

	HistogramDurationBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000}
)

const (
	MetricsNamespace       = "weka_csi"
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

		FetchSinglePvMetricsOperationsInvokeCount  prometheus.Counter
		FetchSinglePvMetricsOperationsSuccessCount prometheus.Counter
		// fetching single metrics. refer to single metrics fetch from Weka cluster
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

		QuotaUpdateBatchInvokeCount       prometheus.Counter   // total number of all quota updates
		QuotaUpdateBatchSuccessCount      prometheus.Counter   // total number of all quota updates
		QuotaUpdateBatchDurationSeconds   prometheus.Counter   // total duration of all quota updates in seconds
		QuotaUpdateBatchDurationHistogram prometheus.Histogram // histogram of durations for quota updates
		QuotaUpdateBatchSize              prometheus.Gauge     // total number of quotas updated in the last batch, or number of distinct observed filesystems
		QuotaUpdateFrequencySeconds       prometheus.Gauge     // frequency of quota updates in seconds, taken from the configuration

		ReportedMetricsSuccessCount prometheus.Counter // number of metrics reported to Prometheus across all . Should be equal to FetchSinglePvMetricsOperationsInvokeCount
		ReportedMetricsFailureCount prometheus.Counter // number of metrics that were not valid for reporting, e.g. appeared empty

	}
}

func labelsHash(values []string) uint64 {
	h := fnv.New64a()
	for _, v := range values {
		h.Write([]byte(v))
		h.Write([]byte{0}) // separator
	}
	return h.Sum64()
}

// TimedGauge is a gauge that records the value and the last timestamp it was set.
type TimedGauge struct {
	desc   *prometheus.Desc
	val    *atomic.Float64
	lastTs *atomic.Time
	labels []string
}

func (tg *TimedGauge) Set(v float64) { tg.val.Store(v); tg.lastTs.Store(time.Now()) }

func NewTimedGauge(opts prometheus.GaugeOpts) *TimedGauge {
	return &TimedGauge{
		desc:   prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", nil, nil),
		val:    atomic.NewFloat64(0),
		lastTs: atomic.NewTime(time.Time{}),
	}
}

func (tg *TimedGauge) SetWithTimestamp(v float64, ts time.Time) *prometheus.Desc {
	tg.val.Store(v)
	if ts.IsZero() {
		tg.lastTs.Store(time.Now())
	} else {
		tg.lastTs.Store(ts)
	}
	return tg.desc
}

func (tg *TimedGauge) Describe(ch chan<- *prometheus.Desc) { ch <- tg.desc }

func (tg *TimedGauge) Collect(ch chan<- prometheus.Metric) {
	ts := tg.lastTs.Load()
	v := tg.val.Load()
	if ts.IsZero() {
		ch <- prometheus.MustNewConstMetric(tg.desc, prometheus.GaugeValue, v, tg.labels...)
		return
	}
	ch <- prometheus.NewMetricWithTimestamp(ts,
		prometheus.MustNewConstMetric(tg.desc, prometheus.CounterValue, v, tg.labels...),
	)
}

// TimedCounter is a counter that records the value and the last timestamp it was set.
type TimedCounter struct {
	opts   *prometheus.CounterOpts
	desc   *prometheus.Desc
	val    *atomic.Float64
	lastTs *atomic.Time
	labels []string
}

func NewTimedCounter(opts prometheus.CounterOpts) *TimedCounter {
	return &TimedCounter{
		desc:   prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", nil, nil),
		val:    atomic.NewFloat64(0),
		lastTs: atomic.NewTime(time.Time{}),
	}

}

func (tc *TimedCounter) Inc() { tc.Add(1) }

func (tc *TimedCounter) Add(v float64) { tc.val.Add(v); tc.lastTs.Store(time.Now()) }

func (tc *TimedCounter) AddWithTimestamp(v float64, ts time.Time) {
	tc.Add(v)
	tc.lastTs.Store(ts)
}

// Set sets the counter to value. Although breaks prometheus logics,
// it is used when values are already accumulated externally
func (tc *TimedCounter) Set(v float64) {
	tc.val.Store(v)
	tc.lastTs.Store(time.Now())
}

// SetWithTimestamp sets the counter to value and timestamp. Although breaks prometheus logics,
// it is used when values are already accumulated externally
func (tc *TimedCounter) SetWithTimestamp(v float64, ts time.Time) *prometheus.Desc {
	tc.val.Store(v)
	if ts.IsZero() {
		tc.lastTs.Store(time.Now())
	} else {
		tc.lastTs.Store(ts)
	}
	return tc.desc
}

func (tc *TimedCounter) Describe(ch chan<- *prometheus.Desc) { ch <- tc.desc }
func (tc *TimedCounter) Collect(ch chan<- prometheus.Metric) {
	ts := tc.lastTs.Load()
	v := tc.val.Load()
	if ts.IsZero() {
		ch <- prometheus.MustNewConstMetric(tc.desc, prometheus.CounterValue, v, tc.labels...)
		return
	}
	ch <- prometheus.NewMetricWithTimestamp(ts,
		prometheus.MustNewConstMetric(tc.desc, prometheus.CounterValue, v, tc.labels...),
	)
}

// TimedGaugeVec is a vector of gauges that records the value and the last timestamp it was set for each label combination.
type TimedGaugeVec struct {
	sync.RWMutex

	opts       *prometheus.GaugeOpts
	desc       *prometheus.Desc
	lastValues map[uint64]*TimedGauge
	labels     []string
}

func (tg *TimedGaugeVec) DeleteLabelValues(labelValues ...string) {
	tg.Lock()
	defer tg.Unlock()
	delete(tg.lastValues, labelsHash(labelValues))
}

func NewTimedGaugeVec(opts prometheus.GaugeOpts, labels []string) *TimedGaugeVec {
	return &TimedGaugeVec{
		desc:       prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", labels, nil),
		lastValues: make(map[uint64]*TimedGauge),
		labels:     labels,
		opts:       &opts,
	}
}

func (tg *TimedGaugeVec) WithLabelValues(lv ...string) *TimedGauge {
	key := labelsHash(lv)
	tg.Lock()
	defer tg.Unlock()
	if values, ok := tg.lastValues[key]; !ok || values == nil {
		tg.lastValues[key] = &TimedGauge{
			desc:   tg.desc,
			val:    atomic.NewFloat64(0),
			lastTs: atomic.NewTime(time.Time{}),
			labels: lv,
		}
	}
	return tg.lastValues[key]
}

func (tg *TimedGaugeVec) Describe(ch chan<- *prometheus.Desc) { ch <- tg.desc }
func (tg *TimedGaugeVec) Collect(ch chan<- prometheus.Metric) {
	tg.RLock()

	defer tg.RUnlock()
	for _, val := range tg.lastValues {
		val.Collect(ch)
	}
}

// TimedCounterVec is a vector of counters that records the value and the last timestamp it was set for each label combination.
type TimedCounterVec struct {
	sync.RWMutex

	opts       *prometheus.CounterOpts
	desc       *prometheus.Desc
	lastValues map[uint64]*TimedCounter
	labels     []string
}

func NewTimedCounterVec(opts prometheus.CounterOpts, labels []string) *TimedCounterVec {
	return &TimedCounterVec{
		desc:       prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", labels, nil),
		lastValues: make(map[uint64]*TimedCounter),
	}
}

func (tc *TimedCounterVec) DeleteLabelValues(labelValues ...string) {
	tc.Lock()
	defer tc.Unlock()
	delete(tc.lastValues, labelsHash(labelValues))
}

func (tc *TimedCounterVec) WithLabelValues(lv ...string) *TimedCounter {
	tc.Lock()
	defer tc.Unlock()
	key := labelsHash(lv)

	if val, ok := tc.lastValues[key]; !ok || val == nil {
		tc.lastValues[key] = &TimedCounter{
			desc:   tc.desc,
			val:    atomic.NewFloat64(0),
			lastTs: atomic.NewTime(time.Time{}),
			labels: lv,
			opts:   tc.opts,
		}
	}
	return tc.lastValues[key]
}

func (tc *TimedCounterVec) Describe(ch chan<- *prometheus.Desc) { ch <- tc.desc }
func (tc *TimedCounterVec) Collect(ch chan<- prometheus.Metric) {
	for _, val := range tc.lastValues {
		val.Collect(ch)
	}
}

type bucketCounters struct {
	counters map[float64]*atomic.Uint64
}

func newBucketCounters(buckets []float64) *bucketCounters {
	ret := &bucketCounters{
		counters: make(map[float64]*atomic.Uint64, len(buckets)),
	}
	for _, b := range buckets {
		ret.counters[b] = atomic.NewUint64(0) // initialize bucket if it does not exist
	}
	return ret
}

func (bc *bucketCounters) AsMap() map[float64]uint64 {
	ret := make(map[float64]uint64, len(bc.counters))
	for b, c := range bc.counters {
		ret[b] = c.Load()
	}
	return ret
}

// TimedHistogram is a histogram that records the value and the last timestamp it was set, along with bucket counts.
type TimedHistogram struct {
	sync.RWMutex
	opts       *prometheus.HistogramOpts
	desc       *prometheus.Desc
	buckets    *bucketCounters
	sum        *atomic.Float64
	count      *atomic.Uint64
	lastTs     *atomic.Time
	bucketDefs []float64
	labels     []string
}

func NewTimedHistogram(opts prometheus.HistogramOpts) *TimedHistogram {
	return &TimedHistogram{
		desc:       prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", nil, nil),
		buckets:    newBucketCounters(opts.Buckets),
		bucketDefs: opts.Buckets,
		sum:        atomic.NewFloat64(0),
		count:      atomic.NewUint64(0),
		lastTs:     atomic.NewTime(time.Time{}),
	}
}
func (th *TimedHistogram) Observe(v float64) {
	th.count.Inc()
	th.sum.Add(v)

	for _, b := range th.bucketDefs {
		if v <= b {
			th.buckets.counters[b].Inc()
		}
	}
	th.lastTs.Store(time.Now())
}

func (th *TimedHistogram) Describe(ch chan<- *prometheus.Desc) { ch <- th.desc }
func (th *TimedHistogram) Collect(ch chan<- prometheus.Metric) {

	ts := th.lastTs.Load()
	c := th.count.Load()
	s := th.sum.Load()
	buckets := th.buckets.AsMap()

	if ts.IsZero() {
		ch <- prometheus.MustNewConstHistogram(th.desc, c, s, buckets, th.labels...)
		return
	}
	h := prometheus.MustNewConstHistogram(th.desc, c, s, buckets, th.labels...)
	ch <- prometheus.NewMetricWithTimestamp(ts, h)
}

type TimedHistogramVec struct {
	sync.Mutex
	opts       *prometheus.HistogramOpts
	desc       *prometheus.Desc
	lastValues map[uint64]*TimedHistogram
	labels     [][]string
	bucketDefs []float64
}

func (thv *TimedHistogramVec) DeleteLabelValues(labelValues ...string) {
	delete(thv.lastValues, labelsHash(labelValues))
}

func NewTimedHistogramVec(opts prometheus.HistogramOpts, labels []string) *TimedHistogramVec {
	return &TimedHistogramVec{
		opts:       &opts,
		desc:       prometheus.NewDesc(prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name), opts.Help+" (timestamped)", labels, nil),
		lastValues: make(map[uint64]*TimedHistogram),
		bucketDefs: opts.Buckets,
	}
}
func (thv *TimedHistogramVec) WithLabelValues(lv ...string) *TimedHistogram {
	thv.Lock()
	defer thv.Unlock()
	key := labelsHash(lv)
	if thv.lastValues[key] == nil {
		thv.lastValues[key] = &TimedHistogram{
			desc:       thv.desc,
			opts:       thv.opts,
			buckets:    newBucketCounters(thv.bucketDefs),
			bucketDefs: thv.bucketDefs,
			labels:     lv,
			sum:        atomic.NewFloat64(0),
			count:      atomic.NewUint64(0),
			lastTs:     atomic.NewTime(time.Time{}),
		}
	}
	return thv.lastValues[key]
}

func (thv *TimedHistogramVec) Describe(ch chan<- *prometheus.Desc) { ch <- thv.desc }
func (thv *TimedHistogramVec) Collect(ch chan<- prometheus.Metric) {
	for _, th := range thv.lastValues {
		th.Collect(ch)
	}
}

// NormalizeLabelName replaces all invalid characters in a label name with underscores
func NormalizeLabelName(str string) string {
	str = strings.ReplaceAll(str, "/", "_")
	str = strings.ReplaceAll(str, "-", "_")
	str = strings.ReplaceAll(str, ".", "_")
	return str
}

func NormalizeLabelNames(labels []string) []string {
	normalized := make([]string, len(labels))
	for i, label := range labels {
		normalized[i] = NormalizeLabelName(label)
	}
	return normalized
}

func (m *PrometheusMetrics) Init() {
	// initialize the Prometheus metrics for volume statistics
	m.volumes.CapacityBytes = NewTimedGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Subsystem: VolumesSubsystem,
		Name:      "capacity_bytes",
		Help:      "Total capacity of the WEKA PersistentVolume in bytes",
	}, LabelsForCsiVolumes)

	m.volumes.UsedBytes = NewTimedGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Subsystem: VolumesSubsystem,
		Name:      "used_bytes",
		Help:      "Used capacity of the WEKA PersistentVolume in bytes",
	}, LabelsForCsiVolumes)

	m.volumes.FreeBytes = NewTimedGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsNamespace,
		Subsystem: VolumesSubsystem,
		Name:      "free_bytes",
		Help:      "Free capacity of the WEKA PersistentVolume in bytes",
	}, LabelsForCsiVolumes)

	// Reported capacity of the WEKA PersistentVolume in bytes, taken from Kubernetes PV object
	m.volumes.PvReportedCapacityBytes = NewTimedGaugeVec(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "pv_reported_capacity_bytes",
			Help:      "Reported capacity of the WEKA PersistentVolume in bytes (from Kubernetes PV object)",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.ReadsTotal = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "reads_total",
			Help:      "Total READ Operations of the WEKA PersistentVolume",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.ReadBytesTotal = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "read_bytes_total",
			Help:      "Total READ BYTES from the WEKA PersistentVolume",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.ReadDurationUs = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "read_duration_us",
			Help:      "Total READ DURATION from the WEKA PersistentVolume in microseconds",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.WritesTotal = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "writes_total",
			Help:      "Total WRITE Operations of the WEKA PersistentVolume",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.WriteBytes = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "write_bytes_total",
			Help:      "Total WRITE BYTES to the WEKA PersistentVolume",
		},
		LabelsForCsiVolumes,
	)

	m.volumes.WriteDurationUs = NewTimedCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: VolumesSubsystem,
			Name:      "write_duration_us",
			Help:      "Total WRITE DURATION to the WEKA PersistentVolume in microseconds",
		},
		LabelsForCsiVolumes,
	)

	// metricsserver own metrics

	// metrics for fetching PersistentVolume objects from Kubernetes API
	m.server.FetchPvBatchOperationsInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_invoke_count",
			Help:      "Total number of operations to fetch PersistentVolume objects from Kubernetes API",
		},
	)

	m.server.FetchPvBatchOperationsSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_success_count_total",
			Help:      "Total number of operations to fetch PersistentVolume objects from Kubernetes API",
		},
	)

	m.server.FetchPvBatchOperationFailureCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_failure_count_total",
			Help:      "Total number of failed operations to fetch PersistentVolume objects from Kubernetes API",
		},
	)

	m.server.FetchPvBatchOperationsDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_duration_seconds",
			Help:      "Total duration of operations to fetch PersistentVolume objects from Kubernetes API in seconds",
		},
	)

	m.server.FetchSinglePvMetricsOperationsDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_duration_seconds",
			Help:      "Total duration of operations to fetch PersistentVolume objects from Kubernetes API in seconds",
		},
	)

	m.server.FetchPvBatchOperationsDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_operations_duration_seconds_histogram",
			Help:      "Histogram of durations for fetching PersistentVolume objects from Kubernetes API",
			Buckets:   HistogramDurationBuckets,
		},
	)

	m.server.FetchPvBatchSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_pv_batch_size",
			Help:      "Size of the batch of PersistentVolume objects fetched from Kubernetes API",
		},
	)

	// metrics for streaming PersistentVolume objects
	m.server.StreamPvOperationsCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "stream_pv_operations_count_total",
			Help:      "Total number of operations performed on streaming PersistentVolume objects",
		},
	)

	m.server.StreamPvBatchSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "stream_pv_batch_size",
			Help:      "Size of the batch of streaming PersistentVolume objects",
		},
	)

	// metrics for processing PersistentVolume objects
	m.server.ProcessPvOperationsCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "process_pv_operations_count_total",
			Help:      "Total number of processed PersistentVolume objects",
		},
	)

	m.server.ProcessPvOperationsDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "process_pv_operations_duration_seconds",
			Help:      "Total duration of processing PersistentVolume objects in seconds",
		},
	)

	m.server.ProcessPvOperationsDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "process_pv_operations_duration_seconds_histogram",
			Help:      "Histogram of durations for processing PersistentVolume objects",
			Buckets:   HistogramDurationBuckets,
		},
	)

	// metrics for fetching metrics from Weka cluster
	m.server.FetchMetricsBatchOperationsInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_operations_invoke_count_total",
			Help:      "Total number of fetch metrics batches from Weka cluster that were invoked",
		},
	)

	m.server.FetchMetricsBatchOperationsSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_operations_success_count_total",
			Help:      "Total number of fetch metrics batches from Weka cluster that were completed successfully",
		},
	)

	m.server.FetchMetricsBatchOperationsFailureCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_operations_failure_count_total",
			Help:      "Total number of fetch metrics batches from Weka cluster that were completed successfully",
		},
	)

	m.server.FetchMetricsBatchOperationsDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_operations_duration_seconds",
			Help:      "Total duration of fetch metrics batches from Weka cluster in seconds",
		},
	)

	m.server.FetchMetricsBatchOperationsDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_operations_duration_seconds_histogram",
			Help:      "Histogram of durations for fetching metrics batches from Weka cluster",
			Buckets:   HistogramDurationBuckets,
		},
	)

	m.server.FetchMetricsBatchSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_batch_size",
			Help:      "Size of the batch of metrics fetched from Weka cluster",
		},
	)

	m.server.FetchMetricsFrequencySeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_metrics_frequency_seconds",
			Help:      "Frequency, or interval of fetching metrics from Weka cluster in seconds, taken from the configuration. Too high value may lead to stale metrics or API overload",
		},
	)

	m.server.FetchSinglePvMetricsOperationsInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_single_pv_metrics_invoke_count_total",
			Help:      "Total number of single metrics fetch operations from Weka cluster",
		},
	)

	m.server.FetchSinglePvMetricsOperationsSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_single_pv_metrics_success_count_total",
			Help:      "Total number of single metrics fetch operations from Weka cluster that were completed successfully",
		},
	)

	m.server.FetchSinglePvMetricsOperationsFailureCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_single_pv_metrics_failure_count_total",
			Help:      "Total number of single metrics fetch operations from Weka cluster that failed",
		},
	)

	m.server.FetchSinglePvMetricsOperationsDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_single_pv_metrics_operations_duration_seconds",
			Help:      "Total duration of single metrics fetch operations from Weka cluster in seconds",
		},
	)

	m.server.FetchSinglePvMetricsOperationsDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "fetch_single_pv_metrics_operations_duration_seconds_histogram",
			Help:      "Histogram of durations for fetching single metrics from Weka cluster",
			Buckets:   HistogramDurationBuckets,
		},
	)

	// metrics for PersistentVolumes added/removed from metrics collection
	m.server.PersistentVolumeAdditionsCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "pv_additions_count_total",
			Help:      "Total number of PersistentVolumes added for metrics collection",
		},
	)

	m.server.PersistentVolumeRemovalsCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "pv_removals_count_total",
			Help:      "Total number of PersistentVolumes removed from metrics collection",
		},
	)

	// metrics for PersistentVolumes currently monitored by the metrics server
	m.server.MonitoredPersistentVolumesGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "monitored_persistent_volumes_gauge",
			Help:      "Total number of PersistentVolumes currently monitored by the metrics server, should eventually be equal to the number of PVs in the metrics server cache",
		},
	)

	// metrics for pruning volumes batch
	m.server.PruneVolumesBatchInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "prune_volumes_batch_invoke_count_total",
			Help:      "Total number of prune volumes batch operations invoked",
		},
	)

	m.server.PruneVolumesBatchDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "prune_volumes_batch_duration_seconds",
			Help:      "Total duration of prune volumes batch operations in seconds",
		},
	)

	m.server.PruneVolumesBatchDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "prune_volumes_batch_duration_seconds_histogram",
			Help:      "Histogram of durations for prune volumes batch operations",
			Buckets:   HistogramDurationBuckets,
		},
	)

	m.server.PruneVolumesBatchSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "prune_volumes_batch_size",
			Help:      "Total number of volumes pruned in the last batch",
		},
	)

	// metrics for periodic fetch metrics
	m.server.PeriodicFetchMetricsInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "periodic_fetch_metrics_invoke_count_total",
			Help:      "Total number of periodic fetch metrics invocations",
		},
	)

	m.server.PeriodicFetchMetricsSkipCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "periodic_fetch_metrics_skip_count_total",
			Help:      "Total number of periodic fetch metrics invocations that were skipped",
		},
	)

	m.server.PeriodicFetchMetricsSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "periodic_fetch_metrics_success_count_total",
			Help:      "Total number of successful periodic fetch metrics invocations",
		},
	)

	m.server.PeriodicFetchMetricsFailureCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "periodic_fetch_metrics_failure_count_total",
			Help:      "Total number of failed periodic fetch metrics invocations",
		},
	)

	// metrics for quota map updates
	m.server.QuotaMapRefreshInvokeCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_map_refresh_invoke_count_total",
			Help:      "Total number of quota map updates per filesystem",
		},
		LabelsForFilesystemOps,
	)

	m.server.QuotaMapRefreshSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_map_refresh_success_count_total",
			Help:      "Total number of successful quota map updates per filesystem",
		},
		LabelsForFilesystemOps,
	)

	m.server.QuotaMapRefreshFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_map_refresh_failure_count_total",
			Help:      "Total number of failed quota map updates per filesystem",
		},
		LabelsForFilesystemOps,
	)

	m.server.QuotaMapRefreshDurationSeconds = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_map_refresh_duration_seconds",
			Help:      "Total duration of quota map updates per filesystem in seconds",
		},
		LabelsForFilesystemOps,
	)

	m.server.QuotaMapRefreshDurationHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_map_refresh_duration_seconds_histogram",
			Help:      "Histogram of durations for quota map updates per filesystem",
			Buckets:   HistogramDurationBuckets,
		},
		LabelsForFilesystemOps,
	)

	// metrics for quota update batches
	m.server.QuotaUpdateBatchInvokeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_batch_invoke_count_total",
			Help:      "Total number of all quota update batches performed",
		},
	)

	m.server.QuotaUpdateBatchSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_batch_success_count_total",
			Help:      "Total number of all quota update batches completed",
		},
	)

	m.server.QuotaUpdateBatchDurationSeconds = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_batch_duration_seconds",
			Help:      "Total duration of all quota update batches in seconds",
		},
	)

	m.server.QuotaUpdateBatchDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_batch_duration_seconds_histogram",
			Help:      "Histogram of durations for quota update batches",
			Buckets:   HistogramDurationBuckets,
		},
	)

	m.server.QuotaUpdateBatchSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_batch_size",
			Help:      "Total number of distinct observed filesystems in the last quota update batch",
		},
	)

	m.server.QuotaUpdateFrequencySeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "quota_update_frequency_seconds",
			Help:      "Frequency, or interval of per filesystem quota updates in seconds, taken from the configuration. Too high value may lead to stale quotas or API overload",
		},
	)

	m.server.ReportedMetricsSuccessCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "reported_metrics_success_count_total",
			Help:      "Total number of metrics reported to Prometheus across all PersistentVolumes. Should be equal to FetchSinglePvMetricsOperationsInvokeCount",
		},
	)

	m.server.ReportedMetricsFailureCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsServerSubsystem,
			Name:      "reported_metrics_failure_count_total",
			Help:      "Total number of metrics that were not valid for reporting, e.g. appeared empty",
		},
	)

	prometheus.MustRegister(
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
		m.server.QuotaUpdateBatchInvokeCount,
		m.server.QuotaUpdateBatchSuccessCount,
		m.server.QuotaUpdateBatchDurationSeconds,
		m.server.QuotaUpdateBatchDurationHistogram,
		m.server.QuotaUpdateBatchSize,
		m.server.QuotaUpdateFrequencySeconds,
		m.server.ReportedMetricsSuccessCount,
		m.server.ReportedMetricsFailureCount,
	)

	log.Debug().Msg("Prometheus metrics initialized")
}

func NewPrometheusMetrics() *PrometheusMetrics {
	metrics := &PrometheusMetrics{}
	metrics.Init()
	return metrics
}
