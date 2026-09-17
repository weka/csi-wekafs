# WEKA CSI Metrics Server
========================
Helm chart for Deployment of the WekaIO CSI Metrics Server, exporting per-volume WekaFS capacity and performance metrics to Prometheus

This is a standalone application that can be installed on top of the Weka CSI Plugin to provide metrics for Prometheus and Grafana
Note that WEKA CSI Plugin 3.0 has built-in metrics server, so this chart is not needed for WEKA CSI Plugin 3.0 and up

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Artifact HUB](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/csi-wekafs)](https://artifacthub.io/packages/search?repo=csi-wekafs)
![Version: 2.8.9](https://img.shields.io/badge/Version-2.8.9-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v2.8.9](https://img.shields.io/badge/AppVersion-v2.8.9-informational?style=flat-square)

## Homepage
https://github.com/weka/csi-wekafs

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| WekaIO, Inc. | <csi@weka.io> | <https://weka.io> |

## Pre-requisite
- Kubernetes cluster of version 1.18 and up, 1.19 and up recommended
- Helm v3 must be installed and configured properly
- Weka system pre-configured and Weka client installed and registered in cluster for each Kubernetes node
- Weka CSI Plugin installed and configured in the cluster.

> **NOTE:** This chart is not needed for WEKA CSI Plugin 3.0 and up, as it has built-in metrics server.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| csiDriverName | string | `"csi.weka.io"` | Name of the CSI driver whose PersistentVolumes to report on. Must match the csiDriverName of the    csi-wekafsplugin release that provisioned them, or no volumes will be discovered. |
| image.repository | string | `"quay.io/weka.io/csi-metricsserver"` | The metrics server has its own image, built by Dockerfile-metricsserver |
| image.pullPolicy | string | `"Always"` |  |
| image.tag | string | `""` | Image tag; defaults to v<chart version> when empty |
| imagePullSecret | string | `""` |  |
| hostNetwork | bool | `false` |  |
| priorityClassName | string | `""` | Optional priorityClassName for the metrics server, overridable with `metricsServer.priorityClassName` |
| metricsServer.replicas | int | `2` | Number of replicas for metrics server. More than one is only useful with enableLeaderElection,    which keeps exactly one of them collecting while the rest stand by. Standbys report Ready like    any other pod; which one holds leadership is visible in the Lease object, not in pod status.    A standby is not free: controller-runtime starts its cache on every replica, so each standby    holds a full PersistentVolume informer cache and a watch against the API server while idle |
| metricsServer.nodeSelector | object | `{}` | optional nodeSelector for metrics server only |
| metricsServer.affinity | object | `{}` | optional affinity for metrics server only |
| metricsServer.priorityClassName | string | `""` | optional priorityClassName for metrics server pods only, overriding the global `priorityClassName` |
| metricsServer.labels | object | `{}` | optional labels to add to metrics server deployment |
| metricsServer.podLabels | object | `{}` | optional labels to add to metrics server pods |
| metricsServer.tolerations | object | `{}` | tolerations for metrics server only |
| metricsServer.maxConcurrentRequests | int | `50` | concurrent requests for WEKA API (excluding quota) |
| metricsServer.metricsFetchIntervalSeconds | int | `60` | metrics fetch interval in seconds, default is 60 seconds.    Only expired metrics will be updated, set by quotaCacheValiditySeconds |
| metricsServer.terminationGracePeriodSeconds | int | `10` | termination grace period for metrics server pods |
| metricsServer.enableLeaderElection | bool | `true` | enable leader election for metrics server |
| metricsServer.quotaUpdateConcurrentRequests | int | `25` | number of concurrent requests for metrics server to update quotas |
| metricsServer.quotaCacheValiditySeconds | int | `300` | the time period for which quotaMap of a certain filesystem should be considered valid. usually should match metricsFetchIntervalSeconds,    but in deployments with thousands of PVCs this can be increased to reduce the load on the metrics server.    Metrics in such case will be updated less frequently. But for each metric, a last update time will be recorded |
| metricsServer.apiTimeoutSeconds | int | `180` | Timeout for a single WEKA API request, in seconds. Higher than the plugin's default because a    quota map fetch pulls every quota on a filesystem in one request |
| metricsServer.enableBatchModeForQuotaUpdates | bool | `true` | Fetch all quotas of a filesystem in one request instead of one request per volume.    On by default, because directory-backed volumes share a filesystem: a fleet of 5800 of them    spread over 5 filesystems costs 5 requests per cycle this way and roughly 5800 without.    Filesystem-backed volumes hold one volume per filesystem, so batching is no worse for them.    The trade-off is freshness. Quotas come from a cache kept for quotaCacheValiditySeconds, so a    value can be that old; each metric carries the timestamp of its own measurement rather than of    the scrape, which needs honorTimestamps on the Prometheus side (the PodMonitor sets it).    Set false to fetch every quota during collection instead, at one API request per volume. |
| metricsServer.scrapeInterval | string | `"60s"` | Scrape interval for the metrics server. Defaults to the fetch interval rather than the    chart-wide 30s: the gauges only move once per metricsFetchIntervalSeconds, and with    honorTimestamps a repeat scrape carries the same timestamp and is discarded as a duplicate |
| metricsServer.healthPort | int | `9196` | Port serving both the /healthz liveness probe and the /readyz readiness probe.    Deliberately not the controller's 8081: under hostNetwork the two share a node's network    namespace, and whichever bound second would fail |
| metricsServer.resources | object | `{"limits":{"cpu":2,"memory":"2Gi"},"requests":{"cpu":0.1,"memory":"256Mi"}}` | Resources for the metrics server container |
| logLevel | int | `4` | Log level of the metrics server |
| useJsonLogging | bool | `false` | Use JSON structured logging instead of human-readable logging format (for exporting logs to structured log parser) |
| metrics.metricsServerPort | int | `9096` | Metrics server metrics port. The metrics server always exports; exporting is its only job,    so there is no switch to turn it off |
| metrics.podMonitor.enabled | bool | `true` | Create a PodMonitor, if the Prometheus Operator CRDs are installed |
| metrics.podMonitor.interval | string | `"30s"` | Scrape interval for charts that do not override it |
| metrics.podMonitor.additionalLabels | object | `{}` | Additional labels, e.g. the release label your Prometheus selects PodMonitors by |
| pluginConfig.allowInsecureHttps | bool | `false` |  |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
