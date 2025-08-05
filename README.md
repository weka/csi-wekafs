# CSI WekaFS Driver
Helm chart for deploying the WEKA Container Storage Interface (CSI) plugin for WekaFS, the world's fastest filesystem.

![Version: 2.5.1-SNAPSHOT.44.0a35d77](https://img.shields.io/badge/Version-2.5.1--SNAPSHOT.44.0a35d77-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v2.5.1-SNAPSHOT.44.0a35d77](https://img.shields.io/badge/AppVersion-v2.5.1--SNAPSHOT.44.0a35d77-informational?style=flat-square)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Artifact HUB](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/csi-wekafs)](https://artifacthub.io/packages/search?repo=csi-wekafs)

## Homepage
https://github.com/weka/csi-wekafs

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| WekaIO, Inc. | <csi@weka.io> | <https://weka.io> |

## Source Code
* <https://github.com/weka/csi-wekafs/tree/v$CHART_VERSION/charts/csi-wekafsplugin>

## Pre-requisite
- Kubernetes cluster of version 1.20 or later is recommended. Minimum version is 1.17
- Access to terminal with `kubectl` installed
- WEKA system pre-configured and WEKA client installed and registered in cluster for each Kubernetes node
- Both AMD64 and ARM64 platforms are supported

## Deployment
- [Helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [Deployment and upgrade workflows](docs/deployment.md)
- [Helm-based local deployment](charts/csi-wekafsplugin/LOCAL.md)

## Usage
- [Deploy an Example application](docs/usage.md)
- [SELinux Support & Installation Notes](selinux/README.md)
- [WEKA CSI Plugin with NFS transport](docs/NFS.md)

## Building the binaries
To build the driver, run the following command from the root directory:

```console
make build
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| dynamicProvisionPath | string | `"csi-volumes"` | Directory in root of file system where dynamic volumes are provisioned |
| csiDriverName | string | `"csi.weka.io"` | Name of the driver (and provisioner) |
| csiDriverVersion | string | `"2.8.0-SNAPSHOT.186.sha.40d7d14"` | CSI driver version |
| images.livenessprobesidecar | string | `"registry.k8s.io/sig-storage/livenessprobe:v2.16.0"` | CSI liveness probe sidecar image URL |
| images.attachersidecar | string | `"registry.k8s.io/sig-storage/csi-attacher:v4.9.0"` | CSI attacher sidecar image URL |
| images.provisionersidecar | string | `"registry.k8s.io/sig-storage/csi-provisioner:v5.3.0"` | CSI provisioner sidecar image URL |
| images.registrarsidecar | string | `"registry.k8s.io/sig-storage/csi-node-driver-registrar:v2.14.0"` | CSI registrar sidercar |
| images.resizersidecar | string | `"registry.k8s.io/sig-storage/csi-resizer:v1.14.0"` | CSI resizer sidecar image URL |
| images.snapshottersidecar | string | `"registry.k8s.io/sig-storage/csi-snapshotter:v8.3.0"` | CSI snapshotter sidecar image URL |
| images.csidriver | string | `"quay.io/weka.io/csi-wekafs"` | CSI driver main image URL |
| images.csidriverTag | string | `"2.8.0-SNAPSHOT.186.sha.40d7d14"` | CSI driver tag |
| imagePullSecret | string | `""` | image pull secret required for image download. Must have permissions to access all images above.    Should be used in case of private registry that requires authentication |
| globalPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for all CSI driver components |
| controllerPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI controller component only (by default same as global) |
| nodePluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI node component only (by default same as global) |
| nodeSelector | object | `{}` | Optional nodeSelector for CSI plugin deployment on certain Kubernetes nodes only    This nodeselector will be applied to all CSI plugin components |
| affinity | object | `{}` | Optional affinity for CSI plugin deployment    This affinity will be applied to all CSI plugin components |
| machineConfigLabels | list | `["worker","master"]` | Optional setting for OCP platform only, which machineconfig pools to apply the Weka SELinux policy on    NOTE: by default, the policy will be installed both on workers and control plane nodes |
| controller.replicas | int | `2` | Controller number of replicas |
| controller.maxConcurrentRequests | int | `25` | Maximum concurrent requests from sidecars (for each sidecar) |
| controller.concurrency | object | `{"createSnapshot":10,"createVolume":25,"deleteSnapshot":10,"deleteVolume":25,"expandVolume":25}` | maximum concurrent operations per operation type |
| controller.grpcRequestTimeoutSeconds | int | `30` | Return GRPC Unavailable if request waits in queue for that long time (seconds) |
| controller.configureProvisionerLeaderElection | bool | `true` | Configure provisioner sidecar for leader election |
| controller.configureResizerLeaderElection | bool | `true` | Configure resizer sidecar for leader election |
| controller.configureSnapshotterLeaderElection | bool | `true` | Configure snapshotter sidecar for leader election |
| controller.configureAttacherLeaderElection | bool | `true` | Configure attacher sidecar for leader election |
| controller.nodeSelector | object | `{}` | optional nodeSelector for controller components only |
| controller.affinity | object | `{}` | optional affinity for controller components only |
| controller.labels | object | `{}` | optional labels to add to controller deployment |
| controller.podLabels | object | `{}` | optional labels to add to controller pods |
| controller.terminationGracePeriodSeconds | int | `10` | termination grace period for controller pods |
| node.maxConcurrentRequests | int | `5` | Maximum concurrent requests from sidecars (global) |
| node.concurrency | object | `{"nodePublishVolume":5,"nodeUnpublishVolume":5}` | maximum concurrent operations per operation type (to avoid API starvation) |
| node.grpcRequestTimeoutSeconds | int | `30` | Return GRPC Unavailable if request waits in queue for that long time (seconds) |
| node.nodeSelector | object | `{}` | optional nodeSelector for node components only |
| node.affinity | object | `{}` | optional affinity for node components only |
| node.labels | object | `{}` | optional labels to add to node daemonset |
| node.podLabels | object | `{}` | optional labels to add to node pods |
| node.terminationGracePeriodSeconds | int | `10` | termination grace period for node pods |
| metricsServer | object | `{"affinity":{},"apiClientTimeoutSeconds":180,"enableBatchModeForQuotaUpdates":false,"enableLeaderElection":true,"enabled":true,"labels":{},"logLevel":4,"maxConcurrentRequests":50,"metricsFetchIntervalSeconds":30,"nodeSelector":{},"podLabels":{},"quotaCacheValiditySeconds":240,"quotaUpdateConcurrentRequests":25,"replicas":2,"resources":{"requests":{"cpu":2,"memory":"4Gi"}},"terminationGracePeriodSeconds":10,"tolerations":[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]}` | Metrics server parameters, used for exposing WEKA metrics in Prometheus format |
| metricsServer.enabled | bool | `true` | Allow CSI plugin to report WEKA metrics in Prometheus format.    NOTE: this implies that the CSI plugin will get access to all Kubernetes PVs, and fetch their credentials, then query WEKA cluster for those metrics |
| metricsServer.replicas | int | `2` | Number of replicas for metrics server |
| metricsServer.nodeSelector | object | `{}` | optional nodeSelector for metrics server only |
| metricsServer.affinity | object | `{}` | optional affinity for metrics server only |
| metricsServer.labels | object | `{}` | optional labels to add to metrics server deployment |
| metricsServer.podLabels | object | `{}` | optional labels to add to metrics server pods |
| metricsServer.tolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | tolerations for metrics server only |
| metricsServer.maxConcurrentRequests | int | `50` | concurrent requests for WEKA API (excluding quota) |
| metricsServer.metricsFetchIntervalSeconds | int | `30` | metrics fetch interval in seconds, default is 60 seconds.    Only expired metrics will be updated, set by quotaCacheValiditySeconds |
| metricsServer.terminationGracePeriodSeconds | int | `10` | termination grace period for metrics server pods |
| metricsServer.enableLeaderElection | bool | `true` | enable leader election for metrics server |
| metricsServer.quotaUpdateConcurrentRequests | int | `25` | number of concurrent requests for metrics server to update quotas |
| metricsServer.quotaCacheValiditySeconds | int | `240` | the time period for which quotaMap of a certain filesystem should be considered valid. usually should match metricsFetchIntervalSeconds,    but in deployments with thousands of PVCs this can be increased to reduce the load on the metrics server.    Metrics in such case will be updated less frequently. But for each metric, a last update time will be recorded |
| metricsServer.apiClientTimeoutSeconds | int | `180` | Timeout for API client requests, in seconds. Default is 120 seconds. Increased to 180 seconds to allow for larger operations like quotamap fetching |
| metricsServer.enableBatchModeForQuotaUpdates | bool | `false` | Enable metrics server to fetch metrics from WEKA API using batches (all quotas for a filesystem in one request).    This is useful for large filesystems with many PVCs, as it reduces the number of requests to WEKA API.    This is disabled by default.    The drawback is that in such case, metrics will be reported less frequently, using cached values.    Report timestamp will be recorded for each metric, so you can see when the last update was.    This requires Prometheus server to honor timestamps.    When false, all quota values will be fetched instantaneously, during metrics collection.    Not recommended when having thousands of PVCs |
| metricsServer.resources | object | `{"requests":{"cpu":2,"memory":"4Gi"}}` | Resources for metrics server pods |
| metricsServer.logLevel | int | `4` | separate log level for metrics server, default is 4 (info) |
| logLevel | int | `5` | Log level of CSI plugin |
| useJsonLogging | bool | `false` | Use JSON structured logging instead of human-readable logging format (for exporting logs to structured log parser) |
| priorityClassName | string | `""` | Optional CSI Plugin priorityClassName |
| selinuxSupport | string | `"off"` | Support SELinux labeling for Persistent Volumes, may be either `off`, `mixed`, `enforced` (default off)    In `enforced` mode, CSI node components will only start on nodes having a label `selinuxNodeLabel` below    In `mixed` mode, separate CSI node components will be installed on SELinux-enabled and regular hosts    In `off` mode, only non-SELinux-enabled node components will be run on hosts without label.    WARNING: if SELinux is not enabled, volume provisioning and publishing might fail!    NOTE: SELinux support is enabled automatically on clusters recognized as RedHat OpenShift Container Platform |
| selinuxNodeLabel | string | `"csi.weka.io/selinux_enabled"` | This label must be set to `"true"` on SELinux-enabled Kubernetes nodes,    e.g., to run the node server in secure mode on SELinux-enabled node, the node must have label    `csi.weka.io/selinux_enabled="true"` |
| selinuxOcpRetainMachineConfig | bool | `false` | If true, the SELinux policy machine configuration will not be removed when uninstalling the plugin.    This is useful for OpenShift Container Platform clusters, to not cause machine config pool update on plugin reinstall |
| kubeletPath | string | `"/var/lib/kubelet"` | kubelet path, in cases Kubernetes is installed not in default folder |
| metrics.enabled | bool | `true` | Enable Prometheus Metrics |
| metrics.controllerPort | int | `9090` | Metrics port for Controller Server |
| metrics.provisionerPort | int | `9091` | Provisioner metrics port |
| metrics.resizerPort | int | `9092` | Resizer metrics port |
| metrics.snapshotterPort | int | `9093` | Snapshotter metrics port |
| metrics.nodePort | int | `9094` | Metrics port for Node Serer |
| metrics.attacherPort | int | `9095` | Attacher metrics port |
| metrics.metricsServerPort | int | `9096` | Metrics server metrics port |
| readinessProbe | object | `{"controllerServerPort":9190,"metricsServerPort":9196,"nodeServerPort":9191,"registrarPort":9195}` | Liveness probe parameters |
| readinessProbe.controllerServerPort | int | `9190` | Liveness probe port for Controller Server |
| readinessProbe.nodeServerPort | int | `9191` | Liveness probe port for Node Server |
| readinessProbe.metricsServerPort | int | `9196` | Liveness probe port for Metrics Server |
| readinessProbe.registrarPort | int | `9195` | Liveness probe port for Node Registrar |
| hostNetwork | bool | `false` | Set to true to use host networking. Will be always set to true when using NFS mount protocol |
| pluginConfig.fsGroupPolicy | string | `"File"` | WARNING: Changing this value might require uninstall and re-install of the plugin |
| pluginConfig.allowInsecureHttps | bool | `false` | Allow insecure HTTPS (skip TLS certificate verification) |
| pluginConfig.objectNaming.volumePrefix | string | `"csivol-"` | Prefix that will be added to names of Weka cluster filesystems / snapshots assocciated with CSI volume,    must not exceed 7 symbols. |
| pluginConfig.objectNaming.snapshotPrefix | string | `"csisnp-"` | Prefix that will be added to names of Weka cluster snapshots assocciated with CSI snapshot,    must not exceed 7 symbols. |
| pluginConfig.objectNaming.seedSnapshotPrefix | string | `"csisnp-seed-"` | Prefix that will be added to automatically created "seed" snapshot of empty filesytem,    must not exceed 12 symbols. |
| pluginConfig.allowedOperations.autoCreateFilesystems | bool | `true` | Allow automatic provisioning of CSI volumes based on distinct Weka filesystem |
| pluginConfig.allowedOperations.autoExpandFilesystems | bool | `true` | Allow automatic expansion of filesystem on which Weka snapshot-backed CSI volumes,    e.g. in case a required volume capacity exceeds the size of filesystem.    Note: the filesystem is not expanded automatically when a new directory-backed volume is provisioned |
| pluginConfig.allowedOperations.snapshotDirectoryVolumes | bool | `false` | Create snapshots of directory-backed (dir/v1) volumes. By default disabled.    Note: when enabled, every snapshot of a directory-backed volume creates a full filesystem snapshot (wasteful) |
| pluginConfig.allowedOperations.snapshotVolumesWithoutQuotaEnforcement | bool | `false` | Allow creation of snapshot-backed volumes even on unsupported Weka cluster versions, off by default    Note: On versions of Weka < v4.2 snapshot-backed volume capacity cannot be enforced |
| pluginConfig.allowedOperations.allowAsyncObjectDeletion | bool | `true` | Should the CSI plugin wait for object deletion before reporting completion.    If true, the plugin will report success on deletion of volumes while the actual deletion of objects will be done in the background.    If false, the plugin will report success only after the objects are deleted on WEKA cluster.    Usually, async deletion would drastically increase speed of volume deletions, since deletion is performed serially.    However, it may cause objects on Weka cluster to remain if the plugin crashes or is restarted before the deletion is completed. |
| pluginConfig.mutuallyExclusiveMountOptions[0] | string | `"readcache,writecache,coherent,forcedirect"` |  |
| pluginConfig.mutuallyExclusiveMountOptions[1] | string | `"sync,async"` |  |
| pluginConfig.mutuallyExclusiveMountOptions[2] | string | `"ro,rw"` |  |
| pluginConfig.encryption.allowEncryptionWithoutKms | bool | `false` | Allow encryption of Weka filesystems associated with CSI volumes without using external KMS server.    Should never be run in production, only for testing purposes |
| pluginConfig.mountProtocol.useNfs | bool | `false` | Use NFS transport for mounting Weka filesystems, off by default |
| pluginConfig.mountProtocol.allowNfsFailback | bool | `false` | Allow Failback to NFS transport if Weka client fails to mount filesystem using native protocol |
| pluginConfig.mountProtocol.interfaceGroupName | string | `""` | Specify name of NFS interface group to use for mounting Weka filesystems. If not set, first NFS interface group will be used |
| pluginConfig.mountProtocol.clientGroupName | string | `""` | Specify existing client group name for NFS configuration. If not set, "WekaCSIPluginClients" group will be created |
| pluginConfig.mountProtocol.nfsProtocolVersion | string | `"4.1"` | Specify NFS protocol version to use for mounting Weka filesystems. Default is "4.1", consult Weka documentation for supported versions |
| pluginConfig.skipGarbageCollection | bool | `false` | Skip garbage collection of deleted directory-backed volume contents and only move them to trash. Default false |
| pluginConfig.manageNodeTopologyLabels | bool | `true` | Allow CSI plugin to manage node topology labels. For Operator-managed clusters, this should be set to false. |
| pluginConfig.apiTimeoutSeconds | int | `60` | WEKA API timeout, default 60 seconds |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
