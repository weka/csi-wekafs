# CSI WekaFS Driver
![Version: 0.7.2](https://img.shields.io/badge/Version-0.7.2-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.7.2](https://img.shields.io/badge/AppVersion-v0.7.2-informational?style=flat-square)
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
| csiDriverVersion | string | `"0.7.2"` | CSI driver version |
| images.livenessprobesidecar | string | `"k8s.gcr.io/sig-storage/livenessprobe:v2.5.0"` | CSI liveness probe sidecar image URL |
| images.attachersidecar | string | `"k8s.gcr.io/sig-storage/csi-attacher:v3.3.0"` | CSI attacher sidecar image URL |
| images.provisionersidecar | string | `"k8s.gcr.io/sig-storage/csi-provisioner:v3.0.0"` | CSI provisioner sidecar image URL |
| images.registrarsidecar | string | `"k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.3.0"` | CSI registrar sidercar |
| images.resizersidecar | string | `"k8s.gcr.io/sig-storage/csi-resizer:v1.3.0"` | CSI provisioner sidecar image URL |
| images.csidriver | string | `"quay.io/weka.io/csi-wekafs"` | CSI driver main image URL |
| images.csidriverTag | string | `"0.7.2"` | CSI driver tag |
| globalPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for all CSI driver components |
| controllerPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI controller component only (by default same as global) |
| nodePluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI node component only (by default same as global) |
| nodeSelector | object | `{}` | Optional nodeSelector for CSI plugin deployment on certain Kubernetes nodes only |
| logLevel | int | `5` | Log level of CSI plugin |

