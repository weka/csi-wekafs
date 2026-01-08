# Complete Metrics Reference for Weka CSI Driver

This document provides a comprehensive reference for all metrics exposed by the Weka CSI driver and MetricsServer.

## Overview

The Weka CSI driver tracks **90 metrics** across seven main categories:

| Category | Count | Source File |
|----------|-------|-------------|
| Volume Metrics | 10 | `pkg/wekafs/prometheus.go` |
| Controller Operation Metrics | 13 | `pkg/wekafs/metrics.go` |
| Controller Concurrency Metrics | 10 | `pkg/wekafs/metrics.go` |
| Node Operation Metrics | 6 | `pkg/wekafs/metrics.go` |
| Node Concurrency Metrics | 4 | `pkg/wekafs/metrics.go` |
| MetricsServer Internal Metrics | 44 | `pkg/wekafs/prometheus.go` |
| API Client Metrics | 3 | `pkg/wekafs/apiclient/metrics.go` |

---

## 1. Volume Metrics (Per PersistentVolume)

These metrics are exposed per volume, with labels identifying the specific volume. Defined in `pkg/wekafs/prometheus.go:452-544`.

### Labels for all volume metrics

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `pv_name` | Kubernetes PersistentVolume name |
| `cluster_guid` | Weka cluster unique identifier |
| `storage_class_name` | Kubernetes StorageClass name |
| `filesystem_name` | Weka filesystem name |
| `volume_type` | Type of volume (e.g., directory, snapshot) |
| `pvc_name` | PersistentVolumeClaim name |
| `pvc_namespace` | PersistentVolumeClaim namespace |
| `pvc_uid` | PersistentVolumeClaim UID |

### Capacity Metrics

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_volume_capacity_bytes` | TimedGauge | **Total capacity** of the Weka PersistentVolume in bytes. This is the quota/hard limit set for this volume on the Weka filesystem. |
| `weka_csi_volume_used_bytes` | TimedGauge | **Used capacity** of the Weka PersistentVolume in bytes. Actual data stored in this volume. |
| `weka_csi_volume_free_bytes` | TimedGauge | **Free capacity** (capacity - used) of the volume in bytes. Available space for new writes. |
| `weka_csi_volume_pv_reported_capacity_bytes` | TimedGauge | **Kubernetes-reported capacity** from the PV object spec. May differ from actual Weka quota if manually modified. |

### I/O Performance Metrics

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_volume_reads_total` | TimedCounter | **Total read operations** count. Cumulative number of read I/O operations performed on this volume. |
| `weka_csi_volume_read_bytes_total` | TimedCounter | **Total bytes read** from this volume. Cumulative data transferred in read operations. |
| `weka_csi_volume_read_duration_us` | TimedCounter | **Total read latency** in microseconds. Sum of all read operation durations - divide by reads_total to get average latency. |
| `weka_csi_volume_writes_total` | TimedCounter | **Total write operations** count. Cumulative number of write I/O operations performed on this volume. |
| `weka_csi_volume_write_bytes_total` | TimedCounter | **Total bytes written** to this volume. Cumulative data transferred in write operations. |
| `weka_csi_volume_write_duration_us` | TimedCounter | **Total write latency** in microseconds. Sum of all write operation durations - divide by writes_total to get average latency. |

---

## 2. CSI Controller Operation Metrics

Track volume provisioning/deprovisioning operations. Defined in `pkg/wekafs/metrics.go:55-175`.

### Labels

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `status` | Operation result: `SUCCESS` or `FAILURE` |
| `backing_type` | For volume ops: type of backing storage |

### Volume Operations

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_controller_create_volume_total` | Counter | **Total CreateVolume calls**. Count of volume provisioning requests received by the controller. Labeled by status to distinguish successful vs failed. |
| `weka_csi_controller_create_volume_duration_seconds` | Histogram | **CreateVolume duration distribution**. How long provisioning takes. Useful for detecting slow provisioning. |
| `weka_csi_controller_create_volume_total_capacity_bytes` | Counter | **Cumulative capacity provisioned**. Total bytes of storage provisioned across all CreateVolume calls. Track provisioning growth over time. |
| `weka_csi_controller_delete_volume_total` | Counter | **Total DeleteVolume calls**. Count of volume deletion requests. |
| `weka_csi_controller_delete_volume_duration_seconds` | Histogram | **DeleteVolume duration distribution**. How long deletions take. |
| `weka_csi_controller_delete_volume_total_capacity_bytes` | Counter | **Cumulative capacity deleted**. Total bytes of storage removed across all DeleteVolume calls. |
| `weka_csi_controller_expand_volume_total` | Counter | **Total ExpandVolume calls**. Count of volume expansion requests. |
| `weka_csi_controller_expand_volume_duration_seconds` | Histogram | **ExpandVolume duration distribution**. How long expansions take. |
| `weka_csi_controller_expand_volume_total_capacity_bytes` | Counter | **Cumulative capacity expanded**. Total additional bytes added through expansions. |

### Snapshot Operations

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_controller_create_snapshot_total` | Counter | **Total CreateSnapshot calls**. Count of snapshot creation requests. |
| `weka_csi_controller_create_snapshot_duration_seconds` | Histogram | **CreateSnapshot duration distribution**. How long snapshot creation takes. |
| `weka_csi_controller_delete_snapshot_total` | Counter | **Total DeleteSnapshot calls**. Count of snapshot deletion requests. |
| `weka_csi_controller_delete_snapshot_duration_seconds` | Histogram | **DeleteSnapshot duration distribution**. How long snapshot deletions take. |

---

## 3. CSI Controller Concurrency Metrics

Track concurrent operations and semaphore wait times. Defined in `pkg/wekafs/metrics.go:177-283`.

### Labels

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `status` | Typically `ACTIVE` for current concurrent operations |

### Concurrency Gauges

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_controller_concurrency_create_volume` | Gauge | **Current concurrent CreateVolume operations**. How many volumes are being provisioned right now. Spike indicates high provisioning load. |
| `weka_csi_controller_concurrency_delete_volume` | Gauge | **Current concurrent DeleteVolume operations**. How many volumes are being deleted right now. |
| `weka_csi_controller_concurrency_expand_volume` | Gauge | **Current concurrent ExpandVolume operations**. How many volumes are being expanded right now. |
| `weka_csi_controller_concurrency_create_snapshot` | Gauge | **Current concurrent CreateSnapshot operations**. How many snapshots are being created right now. |
| `weka_csi_controller_concurrency_delete_snapshot` | Gauge | **Current concurrent DeleteSnapshot operations**. How many snapshots are being deleted right now. |

### Semaphore Wait Duration Histograms

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_controller_concurrency_create_volume_wait_duration_seconds` | Histogram | **Semaphore wait time for CreateVolume**. Time spent waiting for a slot to become available. High values indicate the semaphore is saturated. |
| `weka_csi_controller_concurrency_delete_volume_wait_duration_seconds` | Histogram | **Semaphore wait time for DeleteVolume**. Time spent waiting before deletion can start. |
| `weka_csi_controller_concurrency_expand_volume_wait_duration_seconds` | Histogram | **Semaphore wait time for ExpandVolume**. Time spent waiting before expansion can start. |
| `weka_csi_controller_concurrency_create_snapshot_wait_duration_seconds` | Histogram | **Semaphore wait time for CreateSnapshot**. Time spent waiting before snapshot creation can start. |
| `weka_csi_controller_concurrency_delete_snapshot_wait_duration_seconds` | Histogram | **Semaphore wait time for DeleteSnapshot**. Time spent waiting before snapshot deletion can start. |

---

## 4. CSI Node Operation Metrics

Track volume mount/unmount operations on nodes. Defined in `pkg/wekafs/metrics.go:375-447`.

### Labels

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `status` | Operation result: `SUCCESS` or `FAILURE` |

### Metrics

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_node_publish_volume_total` | Counter | **Total NodePublishVolume calls**. Count of volume mount operations on nodes. Called when a pod needs to access a volume. |
| `weka_csi_node_publish_volume_duration_seconds` | Histogram | **NodePublishVolume duration distribution**. How long mounts take. High values may indicate Weka mount issues. |
| `weka_csi_node_unpublish_volume_total` | Counter | **Total NodeUnpublishVolume calls**. Count of volume unmount operations. Called when a pod no longer needs a volume. |
| `weka_csi_node_unpublish_volume_duration_seconds` | Histogram | **NodeUnpublishVolume duration distribution**. How long unmounts take. |
| `weka_csi_node_get_volume_stats_total` | Counter | **Total NodeGetVolumeStats calls**. Count of volume stats requests. Called by kubelet to get capacity/usage for PVC metrics. |
| `weka_csi_node_get_volume_stats_duration_seconds` | Histogram | **NodeGetVolumeStats duration distribution**. How long stats collection takes. |

---

## 5. CSI Node Concurrency Metrics

Track concurrent node operations. Defined in `pkg/wekafs/metrics.go:321-372`.

### Labels

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `status` | Typically `ACTIVE` for current concurrent operations |

### Metrics

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_node_concurrency_node_publish_volume` | Gauge | **Current concurrent NodePublishVolume operations**. How many volumes are being mounted right now on this node. |
| `weka_csi_node_concurrency_node_unpublish_volume` | Gauge | **Current concurrent NodeUnpublishVolume operations**. How many volumes are being unmounted right now. |
| `weka_csi_node_concurrency_node_publish_volume_wait_duration_seconds` | Histogram | **Semaphore wait time for NodePublishVolume**. Time spent waiting for mount slot. |
| `weka_csi_node_concurrency_node_unpublish_volume_wait_duration_seconds` | Histogram | **Semaphore wait time for NodeUnpublishVolume**. Time spent waiting for unmount slot. |

---

## 6. MetricsServer Internal Metrics

Track the health and performance of the metrics collection system itself. Defined in `pkg/wekafs/prometheus.go:549-991`.

### PersistentVolume Fetching from Kubernetes API

These track how the MetricsServer discovers PVs.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_fetch_pv_batch_operations_invoke_count` | Counter | **Total PV fetch batch attempts**. How many times the server tried to fetch PVs from Kubernetes API. |
| `weka_csi_metricsserver_fetch_pv_batch_operations_success_count_total` | Counter | **Successful PV fetch batches**. Successful Kubernetes API calls. |
| `weka_csi_metricsserver_fetch_pv_batch_operations_failure_count_total` | Counter | **Failed PV fetch batches**. Failed Kubernetes API calls. Non-zero indicates connectivity or permission issues. |
| `weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds` | Counter | **Cumulative PV fetch duration**. Total time spent fetching PVs. |
| `weka_csi_metricsserver_fetch_pv_batch_operations_duration_seconds_histogram` | Histogram | **PV fetch duration distribution**. How long each batch fetch takes. |
| `weka_csi_metricsserver_fetch_pv_batch_size` | Gauge | **Last batch size**. Number of PVs returned in the most recent fetch. |

### PV Streaming

Track how PVs are streamed internally for processing.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_stream_pv_operations_count_total` | Counter | **Total PVs streamed**. Count of individual PVs sent through the internal stream for processing. |
| `weka_csi_metricsserver_stream_pv_batch_size` | Gauge | **Current stream batch size**. Number of PVs in the current streaming batch. |

### PV Processing

Track individual PV processing operations.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_process_pv_operations_count_total` | Counter | **Total PVs processed**. Count of individual PV processing operations completed. |
| `weka_csi_metricsserver_process_pv_operations_duration_seconds` | Counter | **Cumulative PV processing duration**. Total time spent processing PVs. |
| `weka_csi_metricsserver_process_pv_operations_duration_seconds_histogram` | Histogram | **PV processing duration distribution**. How long each PV takes to process. |

### Metrics Fetching from Weka (Batch)

Track batch operations to fetch metrics from Weka clusters.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_fetch_metrics_batch_operations_invoke_count_total` | Counter | **Total metrics batch fetch attempts**. How many batch metric collection cycles were started. |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_success_count_total` | Counter | **Successful metrics batch fetches**. Batches that completed without errors. |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_failure_count_total` | Counter | **Failed metrics batch fetches**. Batches that encountered errors. |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds` | Counter | **Cumulative batch fetch duration**. Total time spent in batch fetches. |
| `weka_csi_metricsserver_fetch_metrics_batch_operations_duration_seconds_histogram` | Histogram | **Batch fetch duration distribution**. How long each batch takes. |
| `weka_csi_metricsserver_fetch_metrics_batch_size` | Gauge | **Last batch size**. Number of metrics fetched in the most recent batch. |
| `weka_csi_metricsserver_fetch_metrics_frequency_seconds` | Gauge | **Configured fetch interval**. The `-wekametricsfetchintervalseconds` setting. Too high = stale metrics, too low = API overload. |

### Metrics Fetching from Weka (Single PV)

Track individual PV metrics fetches.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_fetch_single_pv_metrics_invoke_count_total` | Counter | **Total single PV metrics fetches**. Count of individual PV metrics requests to Weka API. |
| `weka_csi_metricsserver_fetch_single_pv_metrics_success_count_total` | Counter | **Successful single PV fetches**. Individual fetches that succeeded. |
| `weka_csi_metricsserver_fetch_single_pv_metrics_failure_count_total` | Counter | **Failed single PV fetches**. Individual fetches that failed. Non-zero indicates volume or API issues. |
| `weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds` | Counter | **Cumulative single fetch duration**. Total time spent fetching individual PV metrics. |
| `weka_csi_metricsserver_fetch_single_pv_metrics_operations_duration_seconds_histogram` | Histogram | **Single fetch duration distribution**. How long each individual PV metrics fetch takes. |

### PV Lifecycle Tracking

Track PVs being added/removed from monitoring.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_pv_additions_count_total` | Counter | **Total PVs added**. How many PVs have been discovered and added to monitoring since startup. |
| `weka_csi_metricsserver_pv_removals_count_total` | Counter | **Total PVs removed**. How many PVs have been removed from monitoring (deleted from cluster). |
| `weka_csi_metricsserver_monitored_persistent_volumes_gauge` | Gauge | **Current monitored PV count**. How many PVs are currently being monitored. Should match total PVs provisioned by this CSI driver. |

### Pruning Stale Volumes

Track cleanup of stale volume metrics.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_prune_volumes_batch_invoke_count_total` | Counter | **Total prune operations**. How many times the prune process has run to clean up metrics for deleted PVs. |
| `weka_csi_metricsserver_prune_volumes_batch_duration_seconds` | Counter | **Cumulative prune duration**. Total time spent in prune operations. |
| `weka_csi_metricsserver_prune_volumes_batch_duration_seconds_histogram` | Histogram | **Prune duration distribution**. How long each prune batch takes. |
| `weka_csi_metricsserver_prune_volumes_batch_size` | Gauge | **Last prune batch size**. Number of volumes pruned in the most recent batch. |

### Periodic Fetch Metrics

Track the periodic metrics collection scheduler.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_periodic_fetch_metrics_invoke_count_total` | Counter | **Total periodic fetch invocations**. How many times the periodic fetch timer has fired. |
| `weka_csi_metricsserver_periodic_fetch_metrics_skip_count_total` | Counter | **Skipped periodic fetches**. Fetches skipped (e.g., previous fetch still running). High count may indicate fetch is slower than interval. |
| `weka_csi_metricsserver_periodic_fetch_metrics_success_count_total` | Counter | **Successful periodic fetches**. Periodic fetches that completed successfully. |
| `weka_csi_metricsserver_periodic_fetch_metrics_failure_count_total` | Counter | **Failed periodic fetches**. Periodic fetches that failed. Non-zero needs investigation. |

### Quota Map Operations (Per Filesystem)

Track quota cache refresh operations. These are labeled per filesystem.

**Labels:**
| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `cluster_guid` | Weka cluster identifier |
| `filesystem_name` | Weka filesystem name |

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_quota_map_refresh_invoke_count_total` | CounterVec | **Total quota map refresh attempts per filesystem**. How many times quota data was refreshed for each filesystem. |
| `weka_csi_metricsserver_quota_map_refresh_success_count_total` | CounterVec | **Successful quota refreshes per filesystem**. Refreshes that succeeded. |
| `weka_csi_metricsserver_quota_map_refresh_failure_count_total` | CounterVec | **Failed quota refreshes per filesystem**. Refreshes that failed. Check Weka connectivity if non-zero. |
| `weka_csi_metricsserver_quota_map_refresh_duration_seconds` | CounterVec | **Cumulative refresh duration per filesystem**. Total time spent refreshing quota maps. |
| `weka_csi_metricsserver_quota_map_refresh_duration_seconds_histogram` | HistogramVec | **Refresh duration distribution per filesystem**. How long each quota refresh takes. |

### Quota Update Batch Operations

Track overall quota update batches (across all filesystems).

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_quota_update_batch_invoke_count_total` | Counter | **Total quota batch updates**. How many batch quota update operations have been performed. |
| `weka_csi_metricsserver_quota_update_batch_success_count_total` | Counter | **Successful quota batch updates**. Batches that completed successfully. |
| `weka_csi_metricsserver_quota_update_batch_duration_seconds` | Counter | **Cumulative quota batch duration**. Total time spent in quota batch updates. |
| `weka_csi_metricsserver_quota_update_batch_duration_seconds_histogram` | Histogram | **Quota batch duration distribution**. How long each quota batch takes. |
| `weka_csi_metricsserver_quota_update_batch_size` | Gauge | **Last batch filesystem count**. Number of distinct filesystems processed in the most recent quota batch. |
| `weka_csi_metricsserver_quota_cache_validity_seconds` | Gauge | **Configured quota cache validity**. The `-wekametricsquotacachevalidityseconds` setting. How long cached quota data is considered fresh. |

### Metrics Reporting

Track success/failure of reporting metrics to Prometheus.

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_metricsserver_reported_metrics_success_count_total` | Counter | **Successfully reported metrics**. Count of metrics successfully recorded to Prometheus. Should equal fetch_single_pv_metrics_invoke_count. |
| `weka_csi_metricsserver_reported_metrics_failure_count_total` | Counter | **Failed metric reports**. Metrics that couldn't be reported (e.g., empty/invalid data from Weka). |

---

## 7. API Client Metrics

Track all HTTP requests to Weka API. Defined in `pkg/wekafs/apiclient/metrics.go:21-56`.

### Labels

| Label | Description |
|-------|-------------|
| `csi_driver_name` | Name of the CSI driver |
| `cluster_guid` | Weka cluster identifier |
| `endpoint` | Weka API endpoint IP/hostname |
| `method` | HTTP method (GET, POST, etc.) |
| `url` | API path being called |
| `status` | Result: `success`, `transport_error`, `response_parse_error`, etc. |

### Metrics

| Metric Name | Type | Description |
|-------------|------|-------------|
| `weka_csi_api_endpoints_count` | GaugeVec | **API endpoint count**. Number of configured API endpoints per cluster. Note: This metric is defined but not registered (not exposed). |
| `weka_csi_api_request_count` | CounterVec | **Total API requests**. Count of all HTTP requests to Weka API, broken down by endpoint, method, URL path, and status. Use this to monitor API traffic patterns and errors. |
| `weka_csi_api_request_duration_seconds` | HistogramVec | **API request duration distribution**. How long API calls take, with buckets from 0.1s to 300s. Use this to detect slow API responses or timeouts. |

---

## Custom Timed Collectors

The codebase implements custom Prometheus collector types (`pkg/wekafs/prometheus.go:118-434`) that wrap standard metrics to include timestamps:

| Type | Description |
|------|-------------|
| `TimedGauge` / `TimedGaugeVec` | Gauges that track when they were last updated |
| `TimedCounter` / `TimedCounterVec` | Counters that track update timestamps |
| `TimedHistogram` / `TimedHistogramVec` | Histograms with timestamp tracking |

This is useful because volume metrics are fetched periodically from Weka, and the timestamp indicates the freshness of the data. Prometheus can use these timestamps for staleness handling.

---

## Metrics Endpoint Configuration

Both the CSI plugin and MetricsServer expose metrics via HTTP:

```go
http.Handle("/metrics", promhttp.Handler())
http.ListenAndServe(fmt.Sprintf(":%s", *metricsPort), nil)
```

**Default port:** 9090 (configurable via `-metricsport` flag)

### MetricsServer Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-enablemetrics` | `false` | Enable Prometheus metrics endpoint |
| `-metricsport` | `9090` | HTTP port to expose metrics on |
| `-wekametricsfetchintervalseconds` | `60` | Interval to fetch metrics from Weka cluster |
| `-wekametricsquotacachevalidityseconds` | `60` | Duration quota map is valid before refresh |
| `-fetchquotasinbatchmode` | `false` | Use batch mode for fetching quotas |
