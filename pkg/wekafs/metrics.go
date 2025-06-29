package wekafs

import (
	"github.com/prometheus/client_golang/prometheus"
	"slices"
)

var (
	CsiCommonLabels                             = []string{"csi_driver_name"}
	CsiControllerConcurrencyMetricsLabels       = []string{"status"}
	CsiControllerVolumeOperationMetricsLabels   = []string{"status", "backing_type"}
	CsiControllerSnapshotOperationMetricsLabels = []string{"status"}
	CsiNodeConcurrencyMetricsLabels             = []string{"status"}
	CsiNodeVolumeOperationMetricsLabels         = []string{"status"}
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

func NewControllerOperationMetrics(volumeLabels, snapshotLabels []string) *ControllerOperationMetrics {
	return &ControllerOperationMetrics{
		CreateVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_total",
				Help:      "Total number of ControllerCreateVolume calls",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		CreateVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_duration_seconds",
				Help:      "Duration of ControllerCreateVolume calls in seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		CreateVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes created by ControllerCreateVolume in bytes",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		DeleteVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_total",
				Help:      "Total number of ControllerDeleteVolume calls",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		DeleteVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_duration_seconds",
				Help:      "Duration of ControllerDeleteVolume calls in seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		DeleteVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes deleted by ControllerDeleteVolume in bytes",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		ExpandVolumeCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_total",
				Help:      "Total number of ControllerExpandVolume calls",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		ExpandVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_duration_seconds",
				Help:      "Duration of ControllerExpandVolume calls in seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		ExpandVolumeTotalCapacity: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "expand_volume_total_capacity_bytes",
				Help:      "Total capacity of volumes expanded by ControllerExpandVolume in bytes",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		CreateSnapshotCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_snapshot_total",
				Help:      "Total number of ControllerCreateSnapshot calls",
			},
			slices.Concat(CsiCommonLabels, snapshotLabels),
		),
		CreateSnapshotDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "create_snapshot_duration_seconds",
				Help:      "Duration of ControllerCreateSnapshot calls in seconds",
			},
			slices.Concat(CsiCommonLabels, snapshotLabels),
		),
		DeleteSnapshotCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_snapshot_total",
				Help:      "Total number of ControllerDeleteSnapshot calls",
			},
			slices.Concat(CsiCommonLabels, snapshotLabels),
		),
		DeleteSnapshotDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "delete_snapshot_duration_seconds",
				Help:      "Duration of ControllerDeleteSnapshot calls in seconds",
			},
			slices.Concat(CsiCommonLabels, snapshotLabels),
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
			slices.Concat(CsiCommonLabels, labels),
		),
		DeleteVolume: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_volume",
				Help:      "Current number of concurrent ControllerDeleteVolume operations",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		ExpandVolume: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_expand_volume",
				Help:      "Current number of concurrent ControllerExpandVolume operations",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		CreateSnapshot: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_snapshot",
				Help:      "Current number of concurrent ControllerCreateSnapshot operations",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		DeleteSnapshot: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_snapshot",
				Help:      "Current number of concurrent ControllerDeleteSnapshot operations",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		CreateVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerCreateVolume semaphore in seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		DeleteVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerDeleteVolume semaphore in seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		ExpandVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_expand_volume_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerExpandVolume semaphore in seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		CreateSnapshotWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_create_snapshot_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerCreateSnapshot semaphore in seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		DeleteSnapshotWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "controller",
				Name:      "concurrency_delete_snapshot_wait_duration_seconds",
				Help:      "Duration of waiting for ControllerDeleteSnapshot semaphore in seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
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
	Concurrency *ControllerConcurrencyMetrics
	Operations  *ControllerOperationMetrics
}

func NewControllerServerMetrics() *ControllerServerMetrics {
	ret := &ControllerServerMetrics{
		Operations:  NewControllerOperationMetrics(CsiControllerVolumeOperationMetricsLabels, CsiControllerSnapshotOperationMetricsLabels),
		Concurrency: NewControllerConcurrencyMetrics(CsiControllerConcurrencyMetricsLabels),
	}
	ret.Init()
	return ret
}

func (m *ControllerServerMetrics) Init() {
	// Initialize the metrics by registering them with Prometheus
	m.Operations.Init()
	m.Concurrency.Init()
}

type NodeServerConcurrencyMetrics struct {
	PublishVolume               *prometheus.CounterVec
	UnpublishVolume             *prometheus.CounterVec
	PublishVolumeWaitDuration   *prometheus.HistogramVec
	UnpublishVolumeWaitDuration *prometheus.HistogramVec
}

func (m *NodeServerConcurrencyMetrics) Init(labels []string) {
	// Initialize the metrics by registering them with Prometheus
	// Currently, no metrics are defined for NodeServer
	initMetrics([]prometheus.Collector{
		m.PublishVolume,
		m.UnpublishVolume},
	)
}

func NewNodeConcurrencyMetrics(labels []string) *NodeServerConcurrencyMetrics {
	// Currently, no metrics are defined for NodeServer concurrency
	return &NodeServerConcurrencyMetrics{
		PublishVolume: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "concurrency_node_publish_volume",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		UnpublishVolume: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "concurrency_node_unpublish_volume",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		PublishVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "concurrency_node_publish_volume_wait_duration_seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
		UnpublishVolumeWaitDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "concurrency_node_unpublish_volume_wait_duration_seconds",
			},
			slices.Concat(CsiCommonLabels, labels),
		),
	}
}

type NodeServerOperationMetrics struct {
	PublishVolume           *prometheus.CounterVec
	PublishVolumeDuration   *prometheus.HistogramVec
	UnpublishVolume         *prometheus.CounterVec
	UnpublishVolumeDuration *prometheus.HistogramVec
	GetVolumeStats          *prometheus.CounterVec
	GetVolumeStatsDuration  *prometheus.HistogramVec
}

func (m *NodeServerOperationMetrics) Init(labels []string) {
	// Initialize the metrics by registering them with Prometheus
	// Currently, no metrics are defined for NodeServer
	initMetrics([]prometheus.Collector{
		m.PublishVolume,
		m.UnpublishVolume,
		m.PublishVolumeDuration,
		m.UnpublishVolumeDuration,
	})
}

func NewNodeOperationMetrics(volumeLabels []string) *NodeServerOperationMetrics {
	// Currently, no metrics are defined for NodeServer operations
	return &NodeServerOperationMetrics{
		PublishVolume: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "publish_volume_total",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		PublishVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "publish_volume_duration_seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		UnpublishVolume: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "unpublish_volume_total",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		UnpublishVolumeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "unpublish_volume_duration_seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		GetVolumeStats: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "get_volume_stats_total",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
		GetVolumeStatsDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: MetricsPrefix,
				Subsystem: "node",
				Name:      "get_volume_stats_duration_seconds",
			},
			slices.Concat(CsiCommonLabels, volumeLabels),
		),
	}
}

type NodeServerMetrics struct {
	Concurrency *NodeServerConcurrencyMetrics
	Operations  *NodeServerOperationMetrics
}

func (m *NodeServerMetrics) Init() {
	// Initialize the metrics by registering them with Prometheus
	// Currently, no metrics are defined for NodeServer
	m.Concurrency.Init(CsiNodeConcurrencyMetricsLabels)
	m.Operations.Init(CsiNodeVolumeOperationMetricsLabels)
}

func NewNodeServerMetrics() *NodeServerMetrics {
	ret := &NodeServerMetrics{
		Operations:  NewNodeOperationMetrics(CsiNodeVolumeOperationMetricsLabels),
		Concurrency: NewNodeConcurrencyMetrics(CsiNodeConcurrencyMetricsLabels),
	}
	ret.Init()
	return ret
}

func initMetrics(c []prometheus.Collector) {
	// Register the metric with Prometheus
	prometheus.MustRegister(c...)
}
