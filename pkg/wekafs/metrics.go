package wekafs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

const MetricsPrefix = "weka_csi"

type ControllerOperationMetrics struct {
	CreateVolumeCounter       *prometheus.CounterVec
	CreateVolumeDuration      *prometheus.HistogramVec
	CreateVolumeTotalCapacity *prometheus.CounterVec
	DeleteVolumeCounter       *prometheus.CounterVec
	DeleteVolumeDuration      *prometheus.HistogramVec
	DeleteVolumeTotalCapacity *prometheus.CounterVec
	ExpandVolumeCounter       *prometheus.CounterVec
	ExpandVolumeDuration      *prometheus.HistogramVec
	ExpandVolumeTotalCapacity *prometheus.CounterVec
	CreateSnapshotCounter     *prometheus.CounterVec
	CreateSnapshotDuration    *prometheus.HistogramVec
	DeleteSnapshotCounter     *prometheus.CounterVec
	DeleteSnapshotDuration    *prometheus.HistogramVec
}

func (c *ControllerOperationMetrics) Init() {
	// Initialize the metrics by registering them with Prometheus

	initMetrics([]prometheus.Collector{
		c.CreateVolumeCounter,
		c.CreateVolumeDuration,
		c.CreateVolumeTotalCapacity,
		c.DeleteVolumeCounter,
		c.DeleteVolumeDuration,
		c.DeleteVolumeTotalCapacity,
		c.ExpandVolumeCounter,
		c.ExpandVolumeDuration,
		c.ExpandVolumeTotalCapacity,
		c.CreateSnapshotCounter,
		c.CreateSnapshotDuration,
		c.DeleteSnapshotCounter,
		c.DeleteSnapshotDuration,
	})
}

func NewControllerOperationMetrics(labels []string) *ControllerOperationMetrics {
	return &ControllerOperationMetrics{
		CreateVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_total",
				Help:      "Total number of ControllerCreateVolume calls",
			},
			labels,
		),
		CreateVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_duration_seconds",
				Help:      "Duration of ControllerCreateVolume calls in seconds",
			},
			labels,
		),
		CreateVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes created by ControllerCreateVolume in bytes",
			},
			labels,
		),
		DeleteVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_total",
				Help:      "Total number of ControllerDeleteVolume calls",
			},
			labels,
		),
		DeleteVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_duration_seconds",
				Help:      "Duration of ControllerDeleteVolume calls in seconds",
			},
			labels,
		),
		DeleteVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes deleted by ControllerDeleteVolume in bytes",
			},
			labels,
		),
		ExpandVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_total",
				Help:      "Total number of ControllerExpandVolume calls",
			},
			labels,
		),
		ExpandVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_duration_seconds",
				Help:      "Duration of ControllerExpandVolume calls in seconds",
			},
			labels,
		),
		ExpandVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes expanded by ControllerExpandVolume in bytes",
			},
			labels,
		),
		CreateSnapshotCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_snapshot_total",
				Help:      "Total number of ControllerCreateSnapshot calls",
			},
			labels,
		),
		CreateSnapshotDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_snapshot_duration_seconds",
				Help:      "Duration of ControllerCreateSnapshot calls in seconds",
			},
			labels,
		),
		DeleteSnapshotCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_snapshot_total",
				Help:      "Total number of ControllerDeleteSnapshot calls",
			},
			labels,
		),
		DeleteSnapshotDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_snapshot_duration_seconds",
				Help:      "Duration of ControllerDeleteSnapshot calls in seconds",
			},
			labels,
		),
	}
}

type ControllerConcurrencyMetrics struct {
	CreateVolume               *prometheus.GaugeVec
	DeleteVolume               *prometheus.GaugeVec
	ExpandVolume               *prometheus.GaugeVec
	CreateSnapshot             *prometheus.GaugeVec
	DeleteSnapshot             *prometheus.GaugeVec
	CreateVolumeWaitDuration   *prometheus.HistogramVec
	DeleteVolumeWaitDuration   *prometheus.HistogramVec
	ExpandVolumeWaitDuration   *prometheus.HistogramVec
	CreateSnapshotWaitDuration *prometheus.HistogramVec
	DeleteSnapshotWaitDuration *prometheus.HistogramVec
}

func NewControllerConcurrencyMetrics(labels []string) *ControllerConcurrencyMetrics {
	return &ControllerConcurrencyMetrics{
		CreateVolume: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_volume",
				Help:      "Current number of concurrent ControllerCreateVolume operations",
			},
			labels,
		),
		DeleteVolume: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_volume",
				Help:      "Current number of concurrent ControllerDeleteVolume operations",
			},
			labels,
		),
		ExpandVolume: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_expand_volume",
				Help:      "Current number of concurrent ControllerExpandVolume operations",
			},
			labels,
		),
		CreateSnapshot: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_snapshot",
				Help:      "Current number of concurrent ControllerCreateSnapshot operations",
			},
			labels,
		),
		DeleteSnapshot: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_snapshot",
				Help:      "Current number of concurrent ControllerDeleteSnapshot operations",
			},
			labels,
		),
		CreateVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerCreateVolume semaphore in seconds",
			},
			labels,
		),
		DeleteVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerDeleteVolume semaphore in seconds",
			},
			labels,
		),
		ExpandVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_expand_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerExpandVolume semaphore in seconds",
			},
			labels,
		),
		CreateSnapshotWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_snapshot_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerCreateSnapshot semaphore in seconds",
			},
			labels,
		),
		DeleteSnapshotWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_snapshot_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerDeleteSnapshot semaphore in seconds",
			},
			labels,
		),
	}
}

func (c *ControllerConcurrencyMetrics) Init() {
	// Initialize the metrics by registering them with Prometheus
	initMetrics([]prometheus.Collector{
		c.CreateVolume,
		c.DeleteVolume,
		c.ExpandVolume,
		c.CreateSnapshot,
		c.DeleteSnapshot,
		c.CreateVolumeWaitDuration,
		c.DeleteVolumeWaitDuration,
		c.ExpandVolumeWaitDuration,
		c.CreateSnapshotWaitDuration,
		c.DeleteSnapshotWaitDuration,
	})
}

type ControllerServerMetrics struct {
	Concurrency  *ControllerConcurrencyMetrics
	Operations   *ControllerOperationMetrics
	CommonLabels []string
}

func NewControllerServerMetrics(labels []string) *ControllerServerMetrics {
	log.Info().Strs("labels", labels).Msg("Initializing controller server metrics")
	ret := &ControllerServerMetrics{
		Operations:   NewControllerOperationMetrics(labels),
		Concurrency:  NewControllerConcurrencyMetrics(labels),
		CommonLabels: labels,
	}
	ret.Init()
	return ret
}

func (m *ControllerServerMetrics) Init() {
	// Initialize the metrics by registering them with Prometheus
	m.Operations.Init()
	m.Concurrency.Init()
}

func initMetrics(c []prometheus.Collector) {
	// Register the metric with Prometheus
	prometheus.MustRegister(c...)
}
