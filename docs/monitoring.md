# Monitoring the WEKA CSI Plugin

## Overview
The WEKA CSI Plugin exposes Prometheus metrics from every component, and ships a **metrics server**
that reports the capacity of each provisioned volume by asking the WEKA cluster directly. Two Grafana
dashboards and a set of alert rules are included for both.

| Component | Exposes | Deployed by |
|---|---|---|
| Controller server | CSI gRPC operations, concurrency, WEKA API calls | always |
| Node server | CSI gRPC operations, concurrency, WEKA API calls | always |
| Metrics server | Per-volume used / free / total capacity, and its own collection health | `metricsServer.enabled=true`, or the separate `csi-metricsserver` chart |

The controller and node servers report only on work they perform. Volume capacity is not among that
work - the CSI specification has no "tell me how full every volume is" call - which is why capacity
reporting is a component of its own.

This page is the operational guide: how to deploy the metrics server, what to tune, and how to read
the dashboards. For the exhaustive list of every metric, its type and its labels, see
[Prometheus metrics](prometheus-metrics.md).

---

## The Metrics Server

### What it does
The metrics server watches every `PersistentVolume` provisioned by this driver, resolves each one to
its WEKA filesystem and directory, and reads its quota over the WEKA REST API. From the quota it
derives the volume's capacity (the quota's hard limit), its used bytes, and its free bytes, and
publishes all three to Prometheus labelled with enough Kubernetes identity to be findable:
PersistentVolume name, PersistentVolumeClaim name and namespace, StorageClass, WEKA filesystem, WEKA
organization, and cluster GUID.

Credentials come from the API secret each PersistentVolume references, so a fleet spread across
several WEKA clusters or several organizations is handled without extra configuration.

### Deployment
Two options, and you should pick exactly one:

```console
# alongside the CSI plugin (recommended)
helm upgrade --install csi-wekafs charts/csi-wekafsplugin --set metricsServer.enabled=true

# or standalone, for example to run it in a different namespace or on a different schedule
helm upgrade --install csi-metricsserver charts/csi-metricsserver
```

  > **NOTE**: do not run both against the same WEKA cluster. Each collector polls every volume's
  > quota independently, so a second one doubles the WEKA API load without reporting any additional
  > data.

### Permissions
Enabling the metrics server creates its **own ServiceAccount**, separate from the controller's, and
grants it cluster-wide read of two resources:

| Resource | Verbs | Why |
|---|---|---|
| `persistentvolumes` | `get`, `list`, `watch` | to discover which volumes to report on |
| `secrets` | `get`, `list` | to read the WEKA API credentials each volume references |

No write verb is granted on either. Namespaced, it additionally needs `leases` (for leader election)
and `events`. Read of Secrets cluster-wide is a real widening of what the release can see - it is the
reason the metrics server is off by default.

### High availability
The metrics server runs with `metricsServer.replicas: 2` and leader election on. Exactly one replica
collects; the others hold a warm PersistentVolume cache and stand by. This matters for how it is
monitored:

- **Only the leader publishes volume metrics.** A standby publishes none, so scraping both replicas
  yields exactly one copy of every volume - there is nothing to de-duplicate and no need to put a
  proxy in front of the pods to select the leader.
- **Standbys are not free.** Each runs its own PersistentVolume informer cache and holds a watch
  against the Kubernetes API server while idle. On a fleet of thousands of volumes that is real
  memory.
- **A standby reports `Ready`.** There is deliberately no readiness probe: gating readiness on
  leadership would hold every standby `NotReady` for its whole life, which restarts nothing but
  raises `KubePodNotReady` and `KubeDeploymentReplicasMismatch` permanently on any cluster running
  the kube-prometheus stack. Which replica holds leadership is visible in the `Lease` object, and on
  the `/readyz` endpoint, not in pod status.

### How quotas are fetched
Two modes, controlled by `metricsServer.enableBatchModeForQuotaUpdates`:

| Mode | Requests per cycle | When it wins |
|---|---|---|
| **Batch** (default, `true`) | one quota map per *filesystem* | Directory-backed volumes, which share a filesystem. A fleet of 5800 directory volumes over 5 filesystems costs 5 requests instead of 5800 |
| Per-volume (`false`) | one per *volume* | Nothing at scale. Retained as an escape hatch |

Filesystem-backed volumes hold one volume per filesystem, so batching is no worse for them.

Measured on a 10,000-volume fleet, switching to batch mode took the WEKA API load from **~32
requests/second to effectively zero**.

### Freshness, and the one thing that trips people up
Quotas are cached for `metricsServer.quotaCacheValiditySeconds` (default `300`). Rather than
pretending a cached reading is fresh, **each metric is published with the timestamp of its own
measurement**, and the PodMonitor sets `honorTimestamps: true` so Prometheus records it at that time.

The consequence is not obvious: Prometheus discards a repeated scrape of an unchanged timestamp as a
duplicate, so **a volume produces roughly one sample per cache period, not one per scrape**. Any
query window over these metrics must therefore be larger than `quotaCacheValiditySeconds`:

```promql
# with quotaCacheValiditySeconds=300, this reports a fraction of the fleet, varying over time
count(count_over_time(weka_csi_volume_used_bytes[5m]))

# this reports all of it
count(count_over_time(weka_csi_volume_used_bytes[10m]))
```

Symptoms of getting this wrong are a graph that periodically loses volumes, or - in batch mode, where
a whole filesystem's volumes share one timestamp and move together - one that drops the entire fleet
at once and recovers. Neither is a collection failure. Raise the window, or lower
`quotaCacheValiditySeconds` and accept the extra API load.

`weka_csi_volume_pv_reported_capacity_bytes` is the exception: it comes from the Kubernetes
PersistentVolume object rather than from WEKA, needs no API call, and carries no measurement
timestamp, so it behaves like an ordinary gauge.

### Tuning
| Value | Default | Effect |
|---|---|---|
| `metricsServer.metricsFetchIntervalSeconds` | `60` | How often a collection cycle runs. Only expired readings are refetched |
| `metricsServer.quotaCacheValiditySeconds` | `300` | How long a reading stays fresh. Raise on large fleets to cut API load; every query window must stay larger than this |
| `metricsServer.enableBatchModeForQuotaUpdates` | `true` | Whole-filesystem quota fetch instead of per-volume |
| `metricsServer.maxConcurrentRequests` | `50` | Concurrent WEKA API requests, excluding quota |
| `metricsServer.quotaUpdateConcurrentRequests` | `25` | Concurrent quota requests |
| `metricsServer.apiTimeoutSeconds` | `180` | Per-request timeout. Higher than the plugin-wide default because a quota map pulls every quota on a filesystem in one request |
| `metricsServer.scrapeInterval` | `60s` | PodMonitor interval. Defaults to the fetch interval, since the gauges do not move faster than that |
| `metricsServer.logLevel` | `4` | Deliberately below the chart-wide default: at `5` the collector logs a line per volume per cycle |

### Labels
Volume metrics carry `csi_driver_name`, `pv_name`, `cluster_guid`, `storage_class_name`,
`filesystem_name`, `volume_type`, `organization`, `pvc_name`, `pvc_namespace`, `pvc_uid`.

The PodMonitor deliberately strips `pod` (and `instance`, `job`, `namespace`, `endpoint`,
`container`) from `weka_csi_volume_*`. A volume metric describes a volume, not the replica that
happened to observe it, and because the collector is leader-elected, leaving `pod` on would fork a
new series for every volume on each rollout and each failover - graphs would draw one line per pod a
volume had ever been reported by, and any query summing across pods would double-count during a
handover. `pod` is preserved on `weka_csi_metricsserver_*`, where the pod is precisely what is being
described.

If you scrape these metrics some other way, apply the same rule, or aggregate `pod` away in your
queries.

---

## Grafana Dashboards
The `dashboards/` directory holds ready-to-import dashboards in Grafana export format, so they are
portable across installations.

| File | Shows |
|---|---|
| `metricsserver-health.json` | Metrics server health: WEKA API request rate and latency, fetch cycles, quota cache behaviour, PersistentVolumes entering and leaving monitoring |
| `volume-capacity.json` | Per-volume used, free and total capacity, plus rankings of the largest volumes, the fullest volumes, and usage by filesystem and by tenant |
| `plugin-health.json` | The CSI driver itself: controller and node RPC rates, error rates and latency, concurrency and semaphore waits, WEKA API load, plus Kubernetes workload health (replicas, DaemonSet coverage, restarts, CrashLoopBackOff, leader-election lease holders) |
| `volume-health.json` | Per-volume health condition: how many volumes are healthy, abnormal or unknown, which ones they are, breakdowns by filesystem, storage class and tenant, and the reconciler's own sweep duration and staleness |

Import through **Dashboards -> New -> Import**, which prompts for the Prometheus datasource.

  > **NOTE**: importing over the HTTP API requires `/api/dashboards/import` with an `inputs` entry
  > for `DS_PROMETHEUS`. `/api/dashboards/db` reports success but leaves the datasource unresolved,
  > and every panel then renders empty.

### volume-capacity.json
Four per-volume graphs - used, total, free and utilisation - each capped at the **top 100 series**,
because a fleet of thousands is neither readable nor cheap to render. Narrow the scope with the
Tenant, Filesystem, Storage class, Namespace and PersistentVolume variables rather than raising the
cap. Below them are four rankings: largest volumes, fullest volumes, usage by filesystem, and usage
by tenant.

The **Value max age** variable is the one to understand. It sets the freshness window described
above, and **must stay larger than `metricsServer.quotaCacheValiditySeconds`**. It defaults to `10m`
against the default 300s cache; keep roughly that ratio if you change either.

Utilisation panels mark 85% and 95%, matching the alert rules below.

### metricsserver-health.json
Scoped to a single metrics-server pod through its `$pod` variable, which resolves only to
metrics-server pods. The CSI controller and node servers are not visible in it, and on an idle
cluster most provisioning counters are silent - a `CounterVec` with no observed labels exports
nothing at all, so a panel reads as broken rather than as zero.

### plugin-health.json
Covers the driver rather than the metrics server. Two pod variables, `controller pod` and `node
pod`, are scoped to metrics each role actually exports, so neither resolves to metrics-server pods.
The first row is Kubernetes workload health from **kube-state-metrics**; if that is not installed
those panels stay empty while the rest of the dashboard still works.

Its `$namespace`, `$controller_deployment` and `$node_daemonset` variables deliberately carry **no
`allValue`**, unlike the CSI-metric-backed variables elsewhere. They are built on generic
kube-state-metrics series, so an `allValue` of `.*` would make "All" match every Deployment and
DaemonSet in the cluster rather than only the CSI ones.

The leader-election lease panel is intentionally **not** scoped to `$namespace`, so a lease held
across namespaces stays visible - that is exactly the failure this panel exists to catch.

### volume-health.json
Companion to `volume-capacity.json`, about condition rather than capacity. Counts are derived from
the per-volume `weka_csi_volume_health_status` series so they honour the Tenant, Filesystem, Storage
class, Namespace and PersistentVolume filters.

Two things to know about the numbers:

- **`healthy + abnormal + unknown` partitions the fleet.** `Probe failures` does **not** add to
  them - it counts probes that errored during the last sweep, and a failed probe leaves the volume's
  previous status in place. A volume counted there is still counted as healthy, abnormal or unknown.
  It is also the one tile that cannot honour the filters, since it comes from the fleet-wide tally
  which carries no per-volume labels.
- **Condition only refreshes once per reconciler sweep** (`volumeHealthReconcileInterval`, 5m by
  default, plus the sweep's own duration). A newly created volume therefore appears one full cycle
  after it is provisioned, and a deleted one disappears at the next sweep boundary - except on
  delete, where `DeleteVolume` drops the series immediately.

Entries older than `volumeHealthMaxAge` (30m) report as `unknown` (`-1`) rather than as a stale
healthy reading.

---

## Alerts
`dashboards/volume-capacity-alerts.yaml` is a `PrometheusRule` for clusters running the Prometheus
Operator:

```console
kubectl apply -n <prometheus-namespace> -f dashboards/volume-capacity-alerts.yaml
```

For a plain Prometheus, lift its `groups:` block into your own rule file.

| Rule | Severity | Fires when |
|---|---|---|
| `WekaCsiVolumeAlmostFull` | warning | A volume is between 85% and 95% full for 15m |
| `WekaCsiVolumeFull` | critical | A volume is at or above 95% full for 15m |
| `WekaCsiVolumeMetricsStale` | warning | Fewer than 90% of monitored volumes have reported in 15m |

`WekaCsiVolumeMetricsStale` is what makes the other two trustworthy. If collection stops, utilisation
freezes at its last value instead of going missing, and a volume can fill without either capacity
alert ever firing.

All three use a 15m window and a 15m `for:`, both comfortably larger than the default 300s quota
cache. **Raise them if you raise `quotaCacheValiditySeconds`**, or the alerts will flap.

---

## Troubleshooting

**No `weka_csi_volume_*` metrics at all.** Check that the metrics server is deployed and that its
PodMonitor was created - the chart only renders one if the `monitoring.coreos.com/v1` CRD is present
at install time, so installing the Prometheus Operator afterwards requires a `helm upgrade`. Then
confirm the pod is an `up` scrape target.

**Volumes appear and disappear from a panel.** The query window is at or below
`quotaCacheValiditySeconds`. See *Freshness* above.

**Two or three lines per volume on a graph.** The `pod` label is still present, either because the
PodMonitor relabeling is missing or because the metrics are scraped some other way. Aggregate `pod`
away, or add the relabeling.

**Snapshot-backed volumes.** A snapshot-backed volume's quota lives in the snapshot's view, and
WEKA does not return it when listing a filesystem's quotas - only a direct per-inode lookup finds it.
The metrics server detects this and falls back to a per-volume fetch for exactly those volumes, which
is counted by `weka_csi_metricsserver_quota_map_miss_count_total`. A non-zero value there is normal
if you provision volumes from snapshots; a value that tracks your whole fleet means the quota map is
not being built at all.

**A volume is monitored but never reports.** Compare
`weka_csi_metricsserver_monitored_persistent_volumes_gauge` against
`count(weka_csi_volume_capacity_bytes)`. A gap means the volume was discovered but its quota could
not be read - check `weka_csi_metricsserver_fetch_single_pv_metrics_failure_count_total` and the
collector's logs for that PersistentVolume name.

**High WEKA API load.** Confirm `enableBatchModeForQuotaUpdates` is on by checking the pod's args for
`--fetchquotasinbatchmode=true`, and watch the split between the `filesystems/{guid}/quota` and
`fileSystems/{guid}/quota/{id}` URLs in `weka_csi_api_request_count`. Traffic on the latter means
per-volume fetching.
