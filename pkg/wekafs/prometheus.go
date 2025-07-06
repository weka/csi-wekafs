package wekafs

import "github.com/prometheus/client_golang/prometheus"

type PrometheusMetrics struct {
	Capacity *prometheus.GaugeVec
	Used     *prometheus.GaugeVec
	Free     *prometheus.GaugeVec

	Reads           *prometheus.CounterVec
	Writes          *prometheus.CounterVec
	ReadBytes       *prometheus.CounterVec
	WriteBytes      *prometheus.CounterVec
	ReadDurationUs  *prometheus.CounterVec
	WriteDurationUs *prometheus.CounterVec
}

func (m *PrometheusMetrics) Init() {
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

	prometheus.MustRegister(m.Capacity, m.Used, m.Free, m.Reads, m.ReadBytes, m.ReadDurationUs, m.Writes, m.WriteBytes, m.WriteDurationUs)
}

func NewPrometheusMetrics() *PrometheusMetrics {
	metrics := &PrometheusMetrics{}
	metrics.Init()
	return metrics
}
