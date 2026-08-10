package wekafs

import (
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	CsiCommonLabels                             = []string{"csi_driver_name"}
	CsiControllerConcurrencyMetricsLabels       = []string{"status"}
	CsiControllerVolumeOperationMetricsLabels   = []string{"status", "backing_type"}
	CsiControllerSnapshotOperationMetricsLabels = []string{"status"}
	// The read-only controller RPCs carry no backing type: ListVolumes spans every backing type at
	// once, and ControllerGetVolume can fail long before the volume behind the handle is resolved.
	CsiControllerQueryOperationMetricsLabels = []string{"status"}
	CsiNodeConcurrencyMetricsLabels          = []string{"status"}
	CsiNodeVolumeOperationMetricsLabels      = []string{"status"}
	// CsiControllerVolumeHealthTallyMetricsLabels labels the per-sweep tally gauge, status being
	// healthy/abnormal/unknown/failed.
	CsiControllerVolumeHealthTallyMetricsLabels = []string{"status"}
)

const MetricsPrefix = "weka_csi"

// volumeHealthSubsystem namespaces the metrics the volume health reconciler emits. It is separate
// from "controller" (used by ControllerOperationMetrics/ControllerConcurrencyMetrics) so the metric
// names read as weka_csi_volume_health_*, matching what the reconciler actually measures rather
// than which server process happens to run it.
const volumeHealthSubsystem = "volume_health"

// Values the per-volume weka_csi_volume_health_status gauge is set to. -1 is deliberately outside
// the [0,1] range a boolean-ish gauge would suggest, so "unknown" cannot be mistaken for a
// weighted-average of healthy and abnormal by anyone graphing it.
const (
	volumeHealthStatusUnknown  = -1
	volumeHealthStatusAbnormal = 0
	volumeHealthStatusHealthy  = 1
)

// There is one controller server and one node server per process, so the metrics they record are
// process-wide, exactly like the API client's. Constructing them has no side effect; nothing is
// exported until something registers the collectors, which is what ControllerCollectors and
// NodeCollectors are for. Registering only the role this process actually serves keeps a node pod
// from exporting a permanently-zero set of controller series, and vice versa.
var (
	controllerMetrics = NewControllerServerMetrics()
	nodeMetrics       = NewNodeServerMetrics()
)

// The metric declarations below differ only in name, help and which labels they carry, so the
// shared opts live here rather than being spelled out 32 times.
func newCounterVec(subsystem, name, help string, labels []string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: MetricsPrefix, Subsystem: subsystem, Name: name, Help: help,
	}, slices.Concat(CsiCommonLabels, labels))
}

// buckets is optional: omitted, the histogram gets Prometheus's default buckets (up to 10s), which
// is fine for the request-duration histograms every other caller of this function builds. A caller
// measuring something that runs far longer - the volume health sweep - passes its own.
func newHistogramVec(subsystem, name, help string, labels []string, buckets ...float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: MetricsPrefix, Subsystem: subsystem, Name: name, Help: help, Buckets: buckets,
	}, slices.Concat(CsiCommonLabels, labels))
}

func newGaugeVec(subsystem, name, help string, labels []string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: MetricsPrefix, Subsystem: subsystem, Name: name, Help: help,
	}, slices.Concat(CsiCommonLabels, labels))
}

// recordOperation records one finished operation: its outcome on the counter and how long it took
// on the histogram, under the same labels. The two always move together, and keeping them in one
// place stops the pair drifting apart across the handlers that record them.
func recordOperation(counter *prometheus.CounterVec, duration *prometheus.HistogramVec, start time.Time, labels ...string) {
	counter.WithLabelValues(labels...).Inc()
	duration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
}

// netExpansion returns how many bytes a ControllerExpandVolume call actually added to a volume:
// zero unless it succeeded, the previous size was known, and the volume genuinely grew.
//
// Each guard matters, and none is correctable after the fact because counters only ever go up.
// Recording the resulting size would re-count every byte the volume already had on each subsequent
// expansion. Recording an attempt that failed after the old size was read - a rejected capacity
// reservation, or a Weka API error part-way through - would credit growth that never happened. A
// request that never got far enough to read the old size has nothing to compare against.
func netExpansion(succeeded bool, previousCapacity, capacity int64) float64 {
	if !succeeded || previousCapacity < 0 || capacity <= previousCapacity {
		return 0
	}
	return float64(capacity - previousCapacity)
}

// ControllerCollectors returns the controller server metrics for the caller to register.
func ControllerCollectors() []prometheus.Collector { return controllerMetrics.Collectors() }

// NodeCollectors returns the node server metrics for the caller to register.
func NodeCollectors() []prometheus.Collector { return nodeMetrics.Collectors() }

type ControllerOperationMetrics struct {
	CreateVolumeCounter       *prometheus.CounterVec
	CreateVolumeDuration      *prometheus.HistogramVec
	CreateVolumeTotalCapacity *prometheus.CounterVec
	DeleteVolumeCounter       *prometheus.CounterVec
	DeleteVolumeDuration      *prometheus.HistogramVec
	ExpandVolumeCounter       *prometheus.CounterVec
	ExpandVolumeDuration      *prometheus.HistogramVec
	ExpandVolumeTotalCapacity *prometheus.CounterVec
	CreateSnapshotCounter     *prometheus.CounterVec
	CreateSnapshotDuration    *prometheus.HistogramVec
	DeleteSnapshotCounter     *prometheus.CounterVec
	DeleteSnapshotDuration    *prometheus.HistogramVec

	// The read-only RPCs. On a large fleet these are the entire steady-state controller workload -
	// the external health monitor calls GetVolume and ListVolumes continuously - so without them a
	// busy driver exports nothing at all between provisioning events.
	GetVolumeCounter                   *prometheus.CounterVec
	GetVolumeDuration                  *prometheus.HistogramVec
	ListVolumesCounter                 *prometheus.CounterVec
	ListVolumesDuration                *prometheus.HistogramVec
	ValidateVolumeCapabilitiesCounter  *prometheus.CounterVec
	ValidateVolumeCapabilitiesDuration *prometheus.HistogramVec
}

// Collectors returns the metrics for the caller to register.
func (c *ControllerOperationMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		c.CreateVolumeCounter,
		c.CreateVolumeDuration,
		c.CreateVolumeTotalCapacity,
		c.DeleteVolumeCounter,
		c.DeleteVolumeDuration,
		c.ExpandVolumeCounter,
		c.ExpandVolumeDuration,
		c.ExpandVolumeTotalCapacity,
		c.CreateSnapshotCounter,
		c.CreateSnapshotDuration,
		c.DeleteSnapshotCounter,
		c.DeleteSnapshotDuration,
		c.GetVolumeCounter,
		c.GetVolumeDuration,
		c.ListVolumesCounter,
		c.ListVolumesDuration,
		c.ValidateVolumeCapabilitiesCounter,
		c.ValidateVolumeCapabilitiesDuration,
	}
}

func NewControllerOperationMetrics(volumeLabels, snapshotLabels, queryLabels []string) *ControllerOperationMetrics {
	return &ControllerOperationMetrics{
		CreateVolumeCounter:       newCounterVec("controller", "create_volume_total", "Total number of ControllerCreateVolume calls", volumeLabels),
		CreateVolumeDuration:      newHistogramVec("controller", "create_volume_duration_seconds", "Duration of ControllerCreateVolume calls in seconds", volumeLabels),
		CreateVolumeTotalCapacity: newCounterVec("controller", "create_volume_total_capacity_bytes", "Total capacity of volumes created by ControllerCreateVolume in bytes", volumeLabels),
		DeleteVolumeCounter:       newCounterVec("controller", "delete_volume_total", "Total number of ControllerDeleteVolume calls", volumeLabels),
		DeleteVolumeDuration:      newHistogramVec("controller", "delete_volume_duration_seconds", "Duration of ControllerDeleteVolume calls in seconds", volumeLabels),
		ExpandVolumeCounter:       newCounterVec("controller", "expand_volume_total", "Total number of ControllerExpandVolume calls", volumeLabels),
		ExpandVolumeDuration:      newHistogramVec("controller", "expand_volume_duration_seconds", "Duration of ControllerExpandVolume calls in seconds", volumeLabels),
		ExpandVolumeTotalCapacity: newCounterVec("controller", "expand_volume_total_capacity_bytes", "Total capacity added to volumes by ControllerExpandVolume in bytes, counting only the increase over the previous size", volumeLabels),
		CreateSnapshotCounter:     newCounterVec("controller", "create_snapshot_total", "Total number of ControllerCreateSnapshot calls", snapshotLabels),
		CreateSnapshotDuration:    newHistogramVec("controller", "create_snapshot_duration_seconds", "Duration of ControllerCreateSnapshot calls in seconds", snapshotLabels),
		DeleteSnapshotCounter:     newCounterVec("controller", "delete_snapshot_total", "Total number of ControllerDeleteSnapshot calls", snapshotLabels),
		DeleteSnapshotDuration:    newHistogramVec("controller", "delete_snapshot_duration_seconds", "Duration of ControllerDeleteSnapshot calls in seconds", snapshotLabels),

		GetVolumeCounter:                   newCounterVec("controller", "get_volume_total", "Total number of ControllerGetVolume calls", queryLabels),
		GetVolumeDuration:                  newHistogramVec("controller", "get_volume_duration_seconds", "Duration of ControllerGetVolume calls in seconds", queryLabels),
		ListVolumesCounter:                 newCounterVec("controller", "list_volumes_total", "Total number of ControllerListVolumes calls", queryLabels),
		ListVolumesDuration:                newHistogramVec("controller", "list_volumes_duration_seconds", "Duration of ControllerListVolumes calls in seconds", queryLabels),
		ValidateVolumeCapabilitiesCounter:  newCounterVec("controller", "validate_volume_capabilities_total", "Total number of ValidateVolumeCapabilities calls", queryLabels),
		ValidateVolumeCapabilitiesDuration: newHistogramVec("controller", "validate_volume_capabilities_duration_seconds", "Duration of ValidateVolumeCapabilities calls in seconds", queryLabels),
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
		CreateVolume:               newGaugeVec("controller", "concurrency_create_volume", "Current number of concurrent ControllerCreateVolume operations", labels),
		DeleteVolume:               newGaugeVec("controller", "concurrency_delete_volume", "Current number of concurrent ControllerDeleteVolume operations", labels),
		ExpandVolume:               newGaugeVec("controller", "concurrency_expand_volume", "Current number of concurrent ControllerExpandVolume operations", labels),
		CreateSnapshot:             newGaugeVec("controller", "concurrency_create_snapshot", "Current number of concurrent ControllerCreateSnapshot operations", labels),
		DeleteSnapshot:             newGaugeVec("controller", "concurrency_delete_snapshot", "Current number of concurrent ControllerDeleteSnapshot operations", labels),
		CreateVolumeWaitDuration:   newHistogramVec("controller", "concurrency_create_volume_wait_duration_seconds", "Duration of waiting for ControllerCreateVolume semaphore in seconds", labels),
		DeleteVolumeWaitDuration:   newHistogramVec("controller", "concurrency_delete_volume_wait_duration_seconds", "Duration of waiting for ControllerDeleteVolume semaphore in seconds", labels),
		ExpandVolumeWaitDuration:   newHistogramVec("controller", "concurrency_expand_volume_wait_duration_seconds", "Duration of waiting for ControllerExpandVolume semaphore in seconds", labels),
		CreateSnapshotWaitDuration: newHistogramVec("controller", "concurrency_create_snapshot_wait_duration_seconds", "Duration of waiting for ControllerCreateSnapshot semaphore in seconds", labels),
		DeleteSnapshotWaitDuration: newHistogramVec("controller", "concurrency_delete_snapshot_wait_duration_seconds", "Duration of waiting for ControllerDeleteSnapshot semaphore in seconds", labels),
	}
}

// Collectors returns the metrics for the caller to register.
func (c *ControllerConcurrencyMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
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
	}
}

// ControllerVolumeHealthMetrics are emitted by the volume health reconciler
// (volumehealthreconciler.go), which only ever runs inside the controller process - so, unlike the
// rest of this file, these are not built from CsiCommonLabels via newGaugeVec/newHistogramVec:
// Status is labeled with LabelsForCsiVolumes, which already carries csi_driver_name, and
// concatenating CsiCommonLabels in front of it would register the same label name twice.
type ControllerVolumeHealthMetrics struct {
	// Status is one series per volume - see LabelsForCsiVolumes - holding the last value the
	// reconciler determined: volumeHealthStatusHealthy/Abnormal/Unknown. A volume whose credentials
	// cannot be resolved gets no series at all, the same as the metrics server's per-volume metrics,
	// since neither can build a label set without an API client.
	Status *prometheus.GaugeVec
	// Volumes is the per-sweep tally: one series per status among healthy/abnormal/unknown/failed,
	// set once at the end of every completed sweep. "failed" counts probes that errored during the
	// sweep, which is not one of the values Status ever takes - a failed probe leaves the previous
	// Status value in place rather than overwriting it with a guess.
	Volumes *prometheus.GaugeVec
	// SweepDuration is observed once per completed sweep. A sweep over a fleet of ~10k volumes runs
	// for minutes, so this reuses HistogramDurationBuckets (up to 1000s) rather than Prometheus's
	// default buckets, which top out at 10s and would put every real observation in the +Inf bucket.
	SweepDuration *prometheus.HistogramVec
	// LastSweepTimestamp is the unix time the last sweep completed, so a stalled reconciler (e.g. it
	// lost leadership, or a sweep is hanging) is alertable independent of whether any volume's status
	// actually changed.
	LastSweepTimestamp *prometheus.GaugeVec
}

func NewControllerVolumeHealthMetrics() *ControllerVolumeHealthMetrics {
	return &ControllerVolumeHealthMetrics{
		Status: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricsPrefix, Subsystem: volumeHealthSubsystem, Name: "status",
			Help: "Health condition of the CSI volume as last observed by the volume health reconciler: 1 = healthy, 0 = abnormal, -1 = unknown (including a probe result older than the reconciler's max age)",
		}, LabelsForCsiVolumes),
		Volumes: newGaugeVec(volumeHealthSubsystem, "volumes", "Number of volumes in each health status as of the last completed reconciliation sweep", CsiControllerVolumeHealthTallyMetricsLabels),
		SweepDuration: newHistogramVec(volumeHealthSubsystem, "sweep_duration_seconds",
			"Duration of a complete volume health reconciliation sweep in seconds", nil, HistogramDurationBuckets...),
		LastSweepTimestamp: newGaugeVec(volumeHealthSubsystem, "last_sweep_timestamp_seconds", "Unix timestamp when the volume health reconciler last completed a sweep", nil),
	}
}

// Collectors returns the metrics for the caller to register.
func (m *ControllerVolumeHealthMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.Status,
		m.Volumes,
		m.SweepDuration,
		m.LastSweepTimestamp,
	}
}

type ControllerServerMetrics struct {
	Concurrency  *ControllerConcurrencyMetrics
	Operations   *ControllerOperationMetrics
	VolumeHealth *ControllerVolumeHealthMetrics
}

func NewControllerServerMetrics() *ControllerServerMetrics {
	return &ControllerServerMetrics{
		Operations:   NewControllerOperationMetrics(CsiControllerVolumeOperationMetricsLabels, CsiControllerSnapshotOperationMetricsLabels, CsiControllerQueryOperationMetricsLabels),
		Concurrency:  NewControllerConcurrencyMetrics(CsiControllerConcurrencyMetricsLabels),
		VolumeHealth: NewControllerVolumeHealthMetrics(),
	}
}

// Collectors returns every controller metric for the caller to register.
func (m *ControllerServerMetrics) Collectors() []prometheus.Collector {
	return slices.Concat(m.Operations.Collectors(), m.Concurrency.Collectors(), m.VolumeHealth.Collectors())
}

type NodeServerConcurrencyMetrics struct {
	PublishVolume               *prometheus.GaugeVec
	UnpublishVolume             *prometheus.GaugeVec
	PublishVolumeWaitDuration   *prometheus.HistogramVec
	UnpublishVolumeWaitDuration *prometheus.HistogramVec
}

// Collectors returns the metrics for the caller to register.
func (m *NodeServerConcurrencyMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.PublishVolume,
		m.UnpublishVolume,
		m.PublishVolumeWaitDuration,
		m.UnpublishVolumeWaitDuration,
	}
}

func NewNodeConcurrencyMetrics(labels []string) *NodeServerConcurrencyMetrics {
	return &NodeServerConcurrencyMetrics{
		PublishVolume:               newGaugeVec("node", "concurrency_node_publish_volume", "Current number of concurrent NodePublishVolume operations", labels),
		UnpublishVolume:             newGaugeVec("node", "concurrency_node_unpublish_volume", "Current number of concurrent NodeUnpublishVolume operations", labels),
		PublishVolumeWaitDuration:   newHistogramVec("node", "concurrency_node_publish_volume_wait_duration_seconds", "Duration of waiting for NodePublishVolume semaphore in seconds", labels),
		UnpublishVolumeWaitDuration: newHistogramVec("node", "concurrency_node_unpublish_volume_wait_duration_seconds", "Duration of waiting for NodeUnpublishVolume semaphore in seconds", labels),
	}
}

type NodeServerOperationMetrics struct {
	PublishVolume           *prometheus.CounterVec
	PublishVolumeDuration   *prometheus.HistogramVec
	UnpublishVolume         *prometheus.CounterVec
	UnpublishVolumeDuration *prometheus.HistogramVec
	GetVolumeStats          *prometheus.CounterVec
	GetVolumeStatsDuration  *prometheus.HistogramVec
	// Called once per node at registration, so this is a low-rate counter whose value is in
	// spotting nodes that fail to register rather than in its rate.
	GetInfo         *prometheus.CounterVec
	GetInfoDuration *prometheus.HistogramVec
}

// Collectors returns the metrics for the caller to register.
func (m *NodeServerOperationMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.PublishVolume,
		m.UnpublishVolume,
		m.PublishVolumeDuration,
		m.UnpublishVolumeDuration,
		m.GetVolumeStats,
		m.GetVolumeStatsDuration,
		m.GetInfo,
		m.GetInfoDuration,
	}
}

func NewNodeOperationMetrics(volumeLabels []string) *NodeServerOperationMetrics {
	return &NodeServerOperationMetrics{
		PublishVolume:           newCounterVec("node", "publish_volume_total", "Total number of NodePublishVolume calls", volumeLabels),
		PublishVolumeDuration:   newHistogramVec("node", "publish_volume_duration_seconds", "Duration of NodePublishVolume calls in seconds", volumeLabels),
		UnpublishVolume:         newCounterVec("node", "unpublish_volume_total", "Total number of NodeUnpublishVolume calls", volumeLabels),
		UnpublishVolumeDuration: newHistogramVec("node", "unpublish_volume_duration_seconds", "Duration of NodeUnpublishVolume calls in seconds", volumeLabels),
		GetVolumeStats:          newCounterVec("node", "get_volume_stats_total", "Total number of NodeGetVolumeStats calls", volumeLabels),
		GetVolumeStatsDuration:  newHistogramVec("node", "get_volume_stats_duration_seconds", "Duration of NodeGetVolumeStats calls in seconds", volumeLabels),
		GetInfo:                 newCounterVec("node", "get_info_total", "Total number of NodeGetInfo calls", volumeLabels),
		GetInfoDuration:         newHistogramVec("node", "get_info_duration_seconds", "Duration of NodeGetInfo calls in seconds", volumeLabels),
	}
}

type NodeServerMetrics struct {
	Concurrency *NodeServerConcurrencyMetrics
	Operations  *NodeServerOperationMetrics
}

func NewNodeServerMetrics() *NodeServerMetrics {
	return &NodeServerMetrics{
		Operations:  NewNodeOperationMetrics(CsiNodeVolumeOperationMetricsLabels),
		Concurrency: NewNodeConcurrencyMetrics(CsiNodeConcurrencyMetricsLabels),
	}
}

// Collectors returns every node metric for the caller to register.
func (m *NodeServerMetrics) Collectors() []prometheus.Collector {
	return append(m.Operations.Collectors(), m.Concurrency.Collectors()...)
}
