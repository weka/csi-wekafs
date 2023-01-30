# CSI WekaFS Driver
![Version: 0.8.5](https://img.shields.io/badge/Version-0.8.5-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.8.5-sergeyfixcapacitycalcforexisti.50.1b13abd](https://img.shields.io/badge/AppVersion-v0.8.5--sergeyfixcapacitycalcforexisti.50.1b13abd-informational?style=flat-square)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Artifact HUB](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/csi-wekafs)](https://artifacthub.io/packages/search?repo=csi-wekafs)

This repository hosts the CSI WekaFS driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster of version 1.18 or later. Although older versions from 1.13 and up should work, they were not tested
- Access to terminal with `kubectl` installed
- Weka system pre-configured and Weka client installed and registered in cluster for each Kubernetes node

## Deployment
- [Helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [Helm-based local deployment](deploy/helm/csi-wekafsplugin/LOCAL.md)

## Usage
- [Deploy an Example application](docs/usage.md)
- [SELinux Support & Installation Notes](selinux/README.md)

## Additional Documentation
- [Official Weka CSI Plugin documentation](https://docs.weka.io/appendix/weka-csi-plugin)

## Building the binaries
If you want to build the driver yourself, you can do so with the following command from the root directory:

```console
make build
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| dynamicProvisionPath | string | `"csi-volumes"` | Directory in root of file system where dynamic volumes are provisioned |
| csiDriverName | string | `"csi.weka.io"` | Name of the driver (and provisioner) |
| csiDriverVersion | string | `"0.8.5-sergeyfixcapacitycalcforexisti.50.1b13abd"` | CSI driver version |
| images.livenessprobesidecar | string | `"k8s.gcr.io/sig-storage/livenessprobe:v2.6.0"` | CSI liveness probe sidecar image URL |
| images.attachersidecar | string | `"k8s.gcr.io/sig-storage/csi-attacher:v3.4.0"` | CSI attacher sidecar image URL |
| images.provisionersidecar | string | `"k8s.gcr.io/sig-storage/csi-provisioner:v3.1.0"` | CSI provisioner sidecar image URL |
| images.registrarsidecar | string | `"k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.5.0"` | CSI registrar sidercar |
| images.resizersidecar | string | `"k8s.gcr.io/sig-storage/csi-resizer:v1.4.0"` | CSI provisioner sidecar image URL |
| images.csidriver | string | `"quay.io/weka.io/csi-wekafs"` | CSI driver main image URL |
| images.csidriverTag | string | `"0.8.5-sergeyfixcapacitycalcforexisti.50.1b13abd"` | CSI driver tag |
| globalPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for all CSI driver components |
| controllerPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI controller component only (by default same as global) |
| nodePluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI node component only (by default same as global) |
| nodeSelector | object | `{}` | Optional nodeSelector for CSI plugin deployment on certain Kubernetes nodes only |
| logLevel | int | `5` | Log level of CSI plugin |
| legacyVolumeSecretName | string | `""` | for migration of pre-CSI 0.7.0 volumes only, default API secret. Must reside in same namespace as the plugin |
| priorityClassName | string | `""` | Optional CSI Plugin priorityClassName |
| selinuxSupport | string | `"off"` | Support SELinux labeling for Persistent Volumes, may be either `off`, `mixed`, `enforced` (default off)    In `enforced` mode, CSI node components will only start on nodes having a label `selinuxNodeLabel` below    In `mixed` mode, separate CSI node components will be installed on SELinux-enabled and regular hosts    In `off` mode, only non-SELinux-enabled node components will be run on hosts without label.    WARNING: if SELinux is not enabled, volume provisioning and publishing might fail! |
| selinuxNodeLabel | string | `"csi.weka.io/selinux_enabled"` | This label must be set to "true" on SELinux-enabled Kubernetes nodes,    e.g., to run the node server in secure mode on SELinux-enabled node, the node must have label    csi.weka.io/selinux_enabled="true" |
| kubeletPath | string | `"/var/lib/kubelet"` | kubelet path, in cases Kubernetes is installed not in default folder |
| metrics | object | `{"enabled":true,"port":9090}` | Prometheus Metrics configuration |
| metrics.enabled | bool | `true` | Enable Metrics |
| metrics.port | int | `9090` | Metrics port |
| tracingUrl | string | `""` | Tracing URL (For Jaeger tracing engine / OpenTelemetry), optional |
| pluginConfig.allowInsecureHttps | bool | `false` | Allow insecure HTTPS (skip TLS certificate verification) |
| pluginConfig.objectNaming.volumePrefix | string | `"csivol-"` | Prefix that will be added to names of Weka cluster filesystems / snapshots assocciated with CSI volume, must not exceed 7 symbols. |
| pluginConfig.objectNaming.snapshotPrefix | string | `"csisnp-"` | Prefix that will be added to names of Weka cluster snapshots assocciated with CSI snapshot, must not exceed 7 symbols. |
| pluginConfig.objectNaming.seedSnapshotPrefix | string | `"csisnp-seed-"` | Prefix that will be added to automatically created "seed" snapshot of empty filesytem, must not exceed 12 symbols. |
| pluginConfig.allowedOperations.autoCreateFilesystems | bool | `true` | Allow automatic provisioning of CSI volumes based on distinct Weka filesystem |
| pluginConfig.allowedOperations.autoExpandFilesystems | bool | `true` | Allow automatic expansion of filesystem on which Weka snapshot-based CSI volumes, e.g. in case a required volume capacity exceeds the size of filesystem. Note: the filesystem is not expanded automatically when a new directory-based volume is provisioned |
| pluginConfig.allowedOperations.autoCreateSeedSnapshot | bool | `true` | Allow automatic creation of "empty" snapshots for any provisioned filesystem, such snapshots could allow creation of additional empty CSI volumes to originate from the snapshot |
| pluginConfig.allowedOperations.snapshotDirectoryVolumes | bool | `false` | Create snapshots of legacy (dir/v1) volumes. By default disabled. Note: when enabled, for every legacy volume snapshot, a full filesystem snapshot will be created (wasteful) |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.11.0](https://github.com/norwoodj/helm-docs/releases/v1.11.0)