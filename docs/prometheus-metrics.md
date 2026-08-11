# Prometheus metrics

This is a complete reference for every Prometheus metric the WEKA CSI plugin and its metrics
server expose, verified against branch `fix-ported-3.0-rebase-fixes`. All metric names are
`namespace_subsystem_name`, where the namespace is always `weka_csi` (`MetricsPrefix` in
`pkg/wekafs/metrics.go`).

Two binaries produce these metrics:

- **CSI plugin** (`cmd/wekafsplugin`) — run as `-csimode=controller` or `-csimode=node`. Each mode
  registers only its own metric family; a node pod never exports a permanently-zero copy of the
  controller's series, and vice versa.
- **Metrics server** (`cmd/metricsserver`, or the plugin run as `-csimode=metricsserver`) — a
  separate process that polls the WEKA REST API for per-volume capacity, independent of the CSI
  gRPC surface.

Both expose Prometheus text format on `GET /metrics`, on the port set by `-metricsport` (default
`9090`), started by `bootstrap.ServeMetrics` (`pkg/bootstrap/bootstrap.go`). Nothing is exposed
unless `-enablemetrics` is set.

There are **104 metrics** in total, in five families:

| # | Family | Component | Count |
|---|---|---|---|
| 1 | Metrics server — server operation | Metrics server | 48 |
| 2 | API client — WEKA REST API calls | Metrics server and CSI plugin (controller + node) | 2 |
| 3 | CSI plugin — controller and node operations | CSI plugin (controller + node) | 40 |
| 4 | Metrics server — per-volume capacity | Metrics server | 10 |
| 5 | CSI plugin — volume health | CSI plugin (controller only) | 4 |

The previous version of this document put the read-only controller RPCs and volume health under
the metrics server. They are not: both are produced entirely inside the CSI controller process
(`ControllerServer`), and are exposed on the controller's own `/metrics`, not the metrics server's.

---

## 1. Metrics server — server operation

**Exposed by:** the metrics server only, on its `/metrics`. Namespace/subsystem:
`weka_csi_metricsserver_*`. Defined in `pkg/wekafs/prometheus.go` (`PrometheusMetrics.server`),
populated from `pkg/wekafs/metricsserver.go`.

These describe the metrics server's own pipeline: listing PersistentVolumes, resolving each one to
a WEKA filesystem/inode, fetching quotas (per-volume or batched per filesystem), and reporting the
result to Prometheus. None of them carry per-volume labels — they describe the collector, not a
volume. The `quota_map_*` metrics are the exception: they are labeled per filesystem
(`LabelsForFilesystemOps` = `csi_driver_name`, `cluster_guid`, `filesystem_name`).

### Fetching PersistentVolumes from the Kubernetes API

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_fetch_pv_batch_operations_invoke_count` | Counter | none | Number of List calls made to fetch PersistentVolumes |
| `weka_csi_metricsserver_fetch_pv_batch_operations_success_count_total` | Counter | none | List calls that succeeded |
| `weka_csi_metricsserver_fetch_pv_batch_operations_failure_count_total` | Counter | none | List calls that failed |
| `weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds` | Counter | none | Cumulative time spent in List calls |
| `weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds_histogram` | Histogram | none | Distribution of List call durations |
| `weka_csi_metricsserver_fetch_pv_batch_size` | Gauge | none | Number of eligible PVs in the most recent fetch, after the volume-count limit is applied |

### Streaming PersistentVolumes for processing

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_stream_pv_operations_count_total` | Counter | none | PersistentVolumes handed off to the processor |
| `weka_csi_metricsserver_stream_pv_batch_size` | Gauge | none | Size of the raw PV list before the eligibility/limit filter, i.e. total PVs returned by the last fetch |

### Processing individual PersistentVolumes

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_process_pv_operations_count_total` | Counter | none | PersistentVolumes processed (secret read, API client built, filesystem/inode resolved) |
| `weka_csi_metricsserver_process_pv_operations_duration_seconds` | Counter | none | Cumulative processing time |
| `weka_csi_metricsserver_process_pv_operations_duration_seconds_histogram` | Histogram | none | Distribution of per-PV processing time |

### Fetching metrics from WEKA — batch cycle (`FetchMetricsOneByOne`)

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_fetch_metrics_batch_operations_invoke_count_total` | Counter | none | Fetch cycles started |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_success_count_total` | Counter | none | Fetch cycles where every volume succeeded |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_failure_count_total` | Counter | none | Fetch cycles where at least one volume failed |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds` | Counter | none | Cumulative cycle duration |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds_histogram` | Histogram | none | Distribution of cycle durations |
| `weka_csi_metricsserver_fetch_metrics_batch_size` | Gauge | none | Number of tracked volumes at the start of the last cycle |
| `weka_csi_metricsserver_fetch_metrics_frequency_seconds` | Gauge | none | Configured `metricsFetchInterval`, as a constant series for reference in queries |

### Fetching a single volume's metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_fetch_single_pv_metrics_invoke_count_total` | Counter | none | Per-volume fetches attempted |
| `weka_csi_metricsserver_fetch_single_pv_metrics_success_count_total` | Counter | none | Per-volume fetches that succeeded |
| `weka_csi_metricsserver_fetch_single_pv_metrics_failure_count_total` | Counter | none | Per-volume fetches that failed |
| `weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds` | Counter | none | Cumulative per-volume fetch time |
| `weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds_histogram` | Histogram | none | Distribution of per-volume fetch time |

### PersistentVolumes entering/leaving monitoring

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_pv_additions_count_total` | Counter | none | PersistentVolumes newly tracked |
| `weka_csi_metricsserver_pv_removals_count_total` | Counter | none | PersistentVolumes pruned (no longer in the PV list) |
| `weka_csi_metricsserver_monitored_persistent_volumes_gauge` | Gauge | none | Currently tracked PersistentVolume count |

### Pruning stale volumes

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_prune_volumes_batch_invoke_count_total` | Counter | none | Prune passes run |
| `weka_csi_metricsserver_prune_volumes_batch_duration_seconds` | Counter | none | Cumulative prune-pass duration |
| `weka_csi_metricsserver_prune_volumes_batch_duration_seconds_histogram` | Histogram | none | Distribution of prune-pass duration |
| `weka_csi_metricsserver_prune_volumes_batch_size` | Gauge | none | Volumes removed in the last prune pass |

### Periodic fetch scheduler

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_periodic_fetch_metrics_invoke_count_total` | Counter | none | Ticks of the 30s scheduler that drives per-volume fetching (only runs when `useQuotaMapsForMetrics=false`) |
| `weka_csi_metricsserver_periodic_fetch_metrics_skip_count_total` | Counter | none | Ticks skipped because the previous cycle was still running |
| `weka_csi_metricsserver_periodic_fetch_metrics_success_count_total` | Counter | none | Cycles that completed without error |
| `weka_csi_metricsserver_periodic_fetch_metrics_failure_count_total` | Counter | none | Cycles that returned an error |

### Quota map refresh, per filesystem

Only relevant when `useQuotaMapsForMetrics=true` (the `enableBatchModeForQuotaUpdates` Helm value,
default on).

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_quota_map_refresh_invoke_count_total` | CounterVec | `csi_driver_name`, `cluster_guid`, `filesystem_name` | Refreshes attempted for this filesystem |
| `weka_csi_metricsserver_quota_map_refresh_success_count_total` | CounterVec | same | Refreshes that succeeded |
| `weka_csi_metricsserver_quota_map_refresh_failure_count_total` | CounterVec | same | Refreshes that failed |
| `weka_csi_metricsserver_quota_map_refresh_duration_seconds` | CounterVec | same | Cumulative refresh duration |
| `weka_csi_metricsserver_quota_map_refresh_duration_seconds_histogram` | HistogramVec | same | Distribution of refresh duration |
| `weka_csi_metricsserver_quota_map_miss_count_total` | CounterVec | same | Volume readings that were absent from the filesystem-wide quota map and had to fall back to a per-volume request. Non-zero only for snapshot-backed volumes, whose quota lives in the snapshot's view and is not returned when listing a filesystem's quotas |

### Quota update batch (across all filesystems)

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_quota_update_batch_invoke_count_total` | Counter | none | Batch refresh cycles run |
| `weka_csi_metricsserver_quota_update_batch_success_count_total` | Counter | none | Batch cycles completed |
| `weka_csi_metricsserver_quota_update_batch_duration_seconds` | Counter | none | Cumulative batch duration |
| `weka_csi_metricsserver_quota_update_batch_duration_seconds_histogram` | Histogram | none | Distribution of batch duration |
| `weka_csi_metricsserver_quota_update_batch_size` | Gauge | none | Distinct filesystems refreshed in the last batch |
| `weka_csi_metricsserver_quota_cache_validity_seconds` | Gauge | none | Configured `quotaCacheValiditySeconds`, as a constant series for reference in queries |

### Reporting to Prometheus

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_metricsserver_reported_metrics_success_count_total` | Counter | none | Volume readings successfully written to the family-4 gauges/counters |
| `weka_csi_metricsserver_reported_metrics_failure_count_total` | Counter | none | Readings that arrived with neither usage nor performance data to report |

---

## 2. API client — WEKA REST API calls

**Exposed by:** both components — whichever process makes the request labels it with its own
`csi_driver_name` and `cluster_guid`, on that process's own `/metrics`. Namespace/subsystem:
`weka_csi_api_*`. Defined in `pkg/wekafs/apiclient/metrics.go`.

The `url` label is the request path with UUIDs and numeric IDs replaced by `{guid}`/`{id})`
(`generalizeUrlPathForMetrics`), so calls against different objects share one series instead of one
series per filesystem/snapshot/inode.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_api_request_count` | CounterVec | `csi_driver_name`, `cluster_guid`, `endpoint`, `method`, `url`, `status` | Total requests to the WEKA API |
| `weka_csi_api_request_duration_seconds` | HistogramVec | same | Request duration, buckets `0.1, 0.25, 0.5, 1, 2.5, 5, 7.5, 10, 15, 30, 60, 120, 300` seconds |

---

## 3. CSI plugin — controller and node operations

**Exposed by:** the CSI plugin — controller metrics only in `-csimode=controller`, node metrics
only in `-csimode=node` — on that process's own `/metrics`. Defined in `pkg/wekafs/metrics.go`.

This includes the read-only controller RPCs (`get_volume`, `list_volumes`,
`validate_volume_capabilities`) and `node_get_info`. On a large fleet these are the bulk of
steady-state controller traffic — the external CSI health monitor sidecar calls `GetVolume` and
`ListVolumes` continuously — so without them a busy driver would appear to export nothing between
provisioning events. They carry only `status` (`CsiControllerQueryOperationMetricsLabels`), not
`backing_type`: `ListVolumes` spans every backing type at once, and `ControllerGetVolume` can fail
before the volume behind the handle is even resolved.

### Controller — volume and snapshot operations

Namespace/subsystem: `weka_csi_controller_*`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_controller_create_volume_total` | Counter | `csi_driver_name`, `status`, `backing_type` | `ControllerCreateVolume` calls |
| `weka_csi_controller_create_volume_duration_seconds` | Histogram | same | Duration of `ControllerCreateVolume` |
| `weka_csi_controller_create_volume_total_capacity_bytes` | Counter | same | Total capacity of volumes created |
| `weka_csi_controller_delete_volume_total` | Counter | same | `ControllerDeleteVolume` calls |
| `weka_csi_controller_delete_volume_duration_seconds` | Histogram | same | Duration of `ControllerDeleteVolume` |
| `weka_csi_controller_expand_volume_total` | Counter | same | `ControllerExpandVolume` calls |
| `weka_csi_controller_expand_volume_duration_seconds` | Histogram | same | Duration of `ControllerExpandVolume` |
| `weka_csi_controller_expand_volume_total_capacity_bytes` | Counter | same | Net bytes added by expansions — zero unless the call succeeded, the previous size was known, and the volume actually grew (`netExpansion`) |
| `weka_csi_controller_create_snapshot_total` | Counter | `csi_driver_name`, `status` | `ControllerCreateSnapshot` calls |
| `weka_csi_controller_create_snapshot_duration_seconds` | Histogram | same | Duration of `ControllerCreateSnapshot` |
| `weka_csi_controller_delete_snapshot_total` | Counter | same | `ControllerDeleteSnapshot` calls |
| `weka_csi_controller_delete_snapshot_duration_seconds` | Histogram | same | Duration of `ControllerDeleteSnapshot` |
| `weka_csi_controller_get_volume_total` | Counter | `csi_driver_name`, `status` | `ControllerGetVolume` calls |
| `weka_csi_controller_get_volume_duration_seconds` | Histogram | same | Duration of `ControllerGetVolume` |
| `weka_csi_controller_list_volumes_total` | Counter | same | `ControllerListVolumes` calls |
| `weka_csi_controller_list_volumes_duration_seconds` | Histogram | same | Duration of `ControllerListVolumes` |
| `weka_csi_controller_validate_volume_capabilities_total` | Counter | same | `ValidateVolumeCapabilities` calls |
| `weka_csi_controller_validate_volume_capabilities_duration_seconds` | Histogram | same | Duration of `ValidateVolumeCapabilities` |

### Controller — concurrency

Labels: `csi_driver_name`, `status` (`CsiControllerConcurrencyMetricsLabels`).

| Metric | Type | Meaning |
|---|---|---|
| `weka_csi_controller_concurrency_create_volume` | Gauge | Concurrent `ControllerCreateVolume` operations in flight |
| `weka_csi_controller_concurrency_delete_volume` | Gauge | Concurrent `ControllerDeleteVolume` operations in flight |
| `weka_csi_controller_concurrency_expand_volume` | Gauge | Concurrent `ControllerExpandVolume` operations in flight |
| `weka_csi_controller_concurrency_create_snapshot` | Gauge | Concurrent `ControllerCreateSnapshot` operations in flight |
| `weka_csi_controller_concurrency_delete_snapshot` | Gauge | Concurrent `ControllerDeleteSnapshot` operations in flight |
| `weka_csi_controller_concurrency_create_volume_wait_duration_seconds` | Histogram | Time spent waiting for the `ControllerCreateVolume` concurrency semaphore |
| `weka_csi_controller_concurrency_delete_volume_wait_duration_seconds` | Histogram | Wait time for the `ControllerDeleteVolume` semaphore |
| `weka_csi_controller_concurrency_expand_volume_wait_duration_seconds` | Histogram | Wait time for the `ControllerExpandVolume` semaphore |
| `weka_csi_controller_concurrency_create_snapshot_wait_duration_seconds` | Histogram | Wait time for the `ControllerCreateSnapshot` semaphore |
| `weka_csi_controller_concurrency_delete_snapshot_wait_duration_seconds` | Histogram | Wait time for the `ControllerDeleteSnapshot` semaphore |

### Node — operations

Namespace/subsystem: `weka_csi_node_*`. Labels: `csi_driver_name`, `status`
(`CsiNodeVolumeOperationMetricsLabels`).

| Metric | Type | Meaning |
|---|---|---|
| `weka_csi_node_publish_volume_total` | Counter | `NodePublishVolume` calls |
| `weka_csi_node_publish_volume_duration_seconds` | Histogram | Duration of `NodePublishVolume` |
| `weka_csi_node_unpublish_volume_total` | Counter | `NodeUnpublishVolume` calls |
| `weka_csi_node_unpublish_volume_duration_seconds` | Histogram | Duration of `NodeUnpublishVolume` |
| `weka_csi_node_get_volume_stats_total` | Counter | `NodeGetVolumeStats` calls |
| `weka_csi_node_get_volume_stats_duration_seconds` | Histogram | Duration of `NodeGetVolumeStats` |
| `weka_csi_node_get_info_total` | Counter | `NodeGetInfo` calls. Called once per node at registration, so this is a low-rate counter useful for spotting nodes that fail to register, not for its rate |
| `weka_csi_node_get_info_duration_seconds` | Histogram | Duration of `NodeGetInfo` |

### Node — concurrency

Labels: `csi_driver_name`, `status` (`CsiNodeConcurrencyMetricsLabels`).

| Metric | Type | Meaning |
|---|---|---|
| `weka_csi_node_concurrency_node_publish_volume` | Gauge | Concurrent `NodePublishVolume` operations in flight |
| `weka_csi_node_concurrency_node_unpublish_volume` | Gauge | Concurrent `NodeUnpublishVolume` operations in flight |
| `weka_csi_node_concurrency_node_publish_volume_wait_duration_seconds` | Histogram | Wait time for the `NodePublishVolume` semaphore |
| `weka_csi_node_concurrency_node_unpublish_volume_wait_duration_seconds` | Histogram | Wait time for the `NodeUnpublishVolume` semaphore |

---

## 4. Metrics server — per-volume capacity metrics

**Exposed by:** the metrics server only, on its `/metrics`. Namespace/subsystem: `weka_csi_volume_*`
(note: `volume`, singular — distinct from the plugin's `volume_health` subsystem in family 5).
Defined in `pkg/wekafs/prometheus.go` (`PrometheusMetrics.volumes`), populated from
`pkg/wekafs/metricsserver.go`.

These are custom timed collectors (`TimedGaugeVec`/`TimedCounterVec`, see below), because their
values come from a WEKA API read on the collector's own schedule, not from the scrape itself.

### Labels

One series per PersistentVolume, identified by `LabelsForCsiVolumes` in `volumemetrics.go` — 10
labels:

| Label | Meaning |
|---|---|
| `csi_driver_name` | CSI driver name |
| `pv_name` | Kubernetes PersistentVolume name |
| `cluster_guid` | WEKA cluster GUID |
| `storage_class_name` | Kubernetes StorageClass name |
| `filesystem_name` | WEKA filesystem name |
| `volume_type` | Volume backing type |
| `organization` | WEKA tenant/organization the volume's API credentials authenticate as. Blank credentials resolve to the root organization name rather than an empty label value |
| `pvc_name` | PersistentVolumeClaim name (blank if the PV has no claim ref) |
| `pvc_namespace` | PersistentVolumeClaim namespace (blank if unbound) |
| `pvc_uid` | PersistentVolumeClaim UID (blank if unbound) |

The previous version of this table was missing `organization`.

### Metrics

| Metric | Type | Meaning |
|---|---|---|
| `weka_csi_volume_capacity_bytes` | TimedGaugeVec | Volume's quota hard limit, in bytes |
| `weka_csi_volume_used_bytes` | TimedGaugeVec | Bytes used against the quota |
| `weka_csi_volume_free_bytes` | TimedGaugeVec | `capacity - used` |
| `weka_csi_volume_pv_reported_capacity_bytes` | TimedGaugeVec | Capacity from the Kubernetes PersistentVolume spec, not from WEKA. Updated once a minute independent of WEKA API reachability, as a fallback so capacity is reported even when the cluster cannot be reached |
| `weka_csi_volume_reads_total` | TimedCounterVec | Cumulative read operations, mirrored from the WEKA cluster's own counter |
| `weka_csi_volume_read_bytes_total` | TimedCounterVec | Cumulative bytes read |
| `weka_csi_volume_read_duration_us` | TimedCounterVec | Cumulative read duration, microseconds |
| `weka_csi_volume_writes_total` | TimedCounterVec | Cumulative write operations |
| `weka_csi_volume_write_bytes_total` | TimedCounterVec | Cumulative bytes written |
| `weka_csi_volume_write_duration_us` | TimedCounterVec | Cumulative write duration, microseconds |

### The freshness trap

Quotas are cached for `quotaCacheValiditySeconds` (driver default 60s if unset; the Helm charts set
`300` for both `csi-wekafsplugin`'s `metricsServer.*` values and the standalone
`csi-metricsserver` chart). `capacity_bytes`/`used_bytes`/`free_bytes` are set via
`SetWithTimestamp` using the *measurement* time — when the quota was actually read from WEKA — not
the scrape time, and Prometheus (scraped with `honorTimestamps: true`, the PodMonitor default)
drops a repeated sample at the same timestamp as a duplicate.

**The result: a volume emits roughly one sample per cache period, not one per scrape.** A dashboard
panel or alert expression evaluated over a window shorter than `quotaCacheValiditySeconds` will see
gaps — a graph that periodically loses volumes, or, in batch quota-fetch mode where a whole
filesystem's volumes share one timestamp, one that drops and recovers its entire fleet at once.
Neither is a collection failure. **Every query window over these three metrics must be
comfortably larger than `quotaCacheValiditySeconds`.**

`weka_csi_volume_pv_reported_capacity_bytes` is the one exception in this family: it is set with
plain `Set()` (which internally stamps "now"), sourced from the Kubernetes object rather than a
WEKA quota read, so it behaves like an ordinary, always-fresh gauge.

The performance counters (`reads_total`, `writes_total`, and their byte/duration counterparts) are
likewise timed with the fetch's own timestamp and are subject to the same caveat.

---

## 5. CSI plugin — volume health

**Exposed by:** the CSI plugin, controller mode only (`ControllerCollectors()`), on the
controller's own `/metrics`. Namespace/subsystem: `weka_csi_volume_health_*`. Defined in
`pkg/wekafs/metrics.go` (`ControllerVolumeHealthMetrics`), populated by the background reconciler
in `pkg/wekafs/volumehealthreconciler.go`.

This family is deliberately not part of the metrics server: it runs entirely inside the controller
process, on a leader-elected background loop (`volumeHealthReconciler.Start`) that sweeps every
PersistentVolume belonging to this driver every `volumeHealthReconcileInterval` (5 minutes),
probing up to `volumeHealthProbeConcurrency` (10) volumes at once. `ControllerGetVolume` and
`ControllerListVolumes` serve their condition/capacity answers from this reconciler's cache rather
than probing WEKA inline, which is what keeps those RPCs cheap (see family 3).

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `weka_csi_volume_health_status` | GaugeVec | `LabelsForCsiVolumes` (the same 10 labels as family 4 — this metric does **not** add `csi_driver_name` on top, since that label is already part of the set) | Last health condition the reconciler determined for this volume: `1` = healthy, `0` = abnormal, `-1` = unknown, including a cached result older than `volumeHealthMaxAge` (30 minutes) |
| `weka_csi_volume_health_volumes` | GaugeVec | `csi_driver_name`, `status` (`healthy`/`abnormal`/`unknown`/`failed`) | Fleet-wide tally as of the last completed sweep. `failed` counts probes that errored during the sweep — it is not one of the values `status` on the per-volume gauge takes, and a failed probe leaves that volume's previous status in place rather than overwriting it |
| `weka_csi_volume_health_sweep_duration_seconds` | HistogramVec | `csi_driver_name` | Duration of one complete reconciliation sweep. Uses the wide `HistogramDurationBuckets` (up to 1000s), not Prometheus's 10s-capped defaults, since a sweep over a large fleet runs for minutes |
| `weka_csi_volume_health_last_sweep_timestamp_seconds` | GaugeVec | `csi_driver_name` | Unix time the last sweep completed. Lets a stalled reconciler (lost leadership, or a hung sweep) be alerted on independent of whether any volume's status changed |

`healthy + abnormal + unknown` (from `weka_csi_volume_health_status`) partitions the fleet;
`failed`, from the tally metric only, does not add to that partition.

---

## Custom timed collectors

`pkg/wekafs/timedmetrics.go` defines `TimedGauge`, `TimedCounter` and `TimedHistogram` (plus their
`*Vec` forms), used throughout family 4. A timed metric carries the timestamp of when its value was
actually measured, rather than the moment Prometheus happens to scrape it — necessary because the
metrics server reads most of what it reports from the WEKA API on its own schedule and serves it
from a cache in between.

- `Set`/`Inc`/`Add`/`Observe` (no explicit timestamp) records the value as measured *now*.
- `SetWithTimestamp`/`AddWithTimestamp`/`ObserveWithTimestamp` attach an explicit measurement time —
  what the metrics server uses when reporting a quota or performance reading, passing the time it
  was actually fetched (or the quota map's own `LastUpdate`).
- Internally these are implemented as custom `prometheus.Collector`s backed by lock-free atomics
  (`atomicFloat64`, `atomicTime`) so a concurrent scrape never blocks a collector goroutine, and
  they attach the timestamp via `prometheus.NewMetricWithTimestamp` at `Collect` time.
- This requires the scrape config to honor timestamps (`honorTimestamps: true`), which is
  Prometheus's default and is what the shipped PodMonitor sets explicitly.
- A metric that has never been written is exported with no timestamp at all, leaving Prometheus to
  stamp it at scrape time as usual — this only matters before the first successful reading.

---

## Dashboards

The `dashboards/` directory ships four Grafana dashboards and one alerting rule file, all in
Grafana's JSON export format (each carries an `__inputs` entry for a `DS_PROMETHEUS` datasource),
so they are portable across installations. Import via **Dashboards → New → Import**, which prompts
for the Prometheus datasource to bind `DS_PROMETHEUS` to. Importing over the HTTP API requires
`/api/dashboards/import` with an `inputs` entry for `DS_PROMETHEUS` — `/api/dashboards/db` reports
success but leaves the datasource unresolved and every panel renders empty.

| Dashboard | Reads | Shows |
|---|---|---|
| `plugin-health.json` | Family 2 (API client) + family 3 (controller/node operations, concurrency) | The CSI driver itself: controller and node RPC rates, error rates and latency (including the read-only `get_volume`/`list_volumes`/`validate_volume_capabilities`/`get_info` RPCs), concurrency and semaphore waits, WEKA API load. A first row of Kubernetes workload health (replicas, DaemonSet coverage, restarts, leader-election lease) comes from kube-state-metrics and stays empty if that isn't installed |
| `volume-health.json` | Family 5 (volume health) | Per-volume health condition: healthy/abnormal/unknown counts, breakdowns by filesystem/storage class/tenant, and the reconciler's own sweep duration and staleness |
| `volume-capacity.json` | Family 4 (per-volume capacity) | Per-volume used/free/total capacity and utilisation (top 100 series, narrowed by Tenant/Filesystem/Storage class/Namespace/PersistentVolume variables), plus rankings of the largest and fullest volumes and usage by filesystem/tenant. Its "Value max age" variable sets the query window described in the freshness trap above and must stay larger than `quotaCacheValiditySeconds` |
| `metricsserver-health.json` | Family 1 (metrics server operation) + family 2 (API client) + some of family 4 (`pv_reported_capacity_bytes`, `used_bytes`, for a monitored-vs-reporting comparison) | Metrics server health: WEKA API request rate and latency, fetch cycles, quota cache behaviour, PersistentVolumes entering/leaving monitoring. Scoped to a single metrics-server pod via a `$pod` variable |

`dashboards/volume-capacity-alerts.yaml` is a `PrometheusRule` (apply with `kubectl apply -f` on a
cluster running the Prometheus Operator; for plain Prometheus, lift its `groups:` block into your
own rule file) built entirely on family 4:

| Rule | Severity | Fires when |
|---|---|---|
| `WekaCsiVolumeAlmostFull` | warning | A volume is 85–95% full for 15m |
| `WekaCsiVolumeFull` | critical | A volume is ≥95% full for 15m |
| `WekaCsiVolumeMetricsStale` | warning | Fewer than 90% of monitored volumes have reported `weka_csi_volume_used_bytes` in 15m, compared against `weka_csi_metricsserver_monitored_persistent_volumes_gauge` |

All three rules use a 15-minute window and `for:`, comfortably larger than the 300s Helm-chart
default for `quotaCacheValiditySeconds` — raise them if you raise that setting, or the alerts will
flap.

See `docs/monitoring.md` for a full operational walkthrough (deployment modes, high availability,
troubleshooting) of the metrics server and these dashboards.

---

## Metric family totals

| Family | Count |
|---|---|
| 1. Metrics server — server operation | 48 |
| 2. API client | 2 |
| 3. CSI plugin — controller + node operations | 40 (18 controller ops + 10 controller concurrency + 8 node ops + 4 node concurrency) |
| 4. Metrics server — per-volume capacity | 10 |
| 5. CSI plugin — volume health | 4 |
| **Total** | **104** |

The previous version of this document stated "90 metrics"; that count is superseded by the above,
verified directly against each family's `Collectors()` method in the code.
