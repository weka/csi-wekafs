# CSI WekaFS Driver
![Version: 0.7.2](https://img.shields.io/badge/Version-0.7.2-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.7.2](https://img.shields.io/badge/AppVersion-v0.7.2-informational?style=flat-square)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Artifact HUB](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/csi-wekafs)](https://artifacthub.io/packages/search?repo=csi-wekafs)

This repository hosts the CSI WekaFS driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster of version 1.18 and up, 1.19 and up recommended, 1.13 and up should work but were not tested.
- Helm v3 must be installed and configured properly
- Weka system pre-configured and Weka client installed and registered in cluster for each Kubernetes node

## Deployment
```shell
helm repo add csi-wekafs https://weka.github.io/csi-wekafs
helm install csi-wekafsplugin csi-wekafs/csi-wekafsplugin --namespace csi-wekafsplugin --create-namespace
```

> **NOTE:** Since version 0.7.0, Weka CSI plugin transitions to API-based deployment model which requires API
> connectivity and credentials parameters to be set in Storage Class.
>
> Kubernetes does not allow storage class modification for existing volumes, hence the
> recommended upgrade process is re-deploying new persistent volumes based on new storage class format.
>
> However, for sake of more convenient migration, a `legacySecretName` parameter can be set that will
> bind existing legacy volumes to a Weka cluster API and allow volume expansion.
>
> For further information, refer [Official Weka CSI Plugin documentation](https://docs.weka.io/appendix/weka-csi-plugin)

## Usage
- [Deploy an Example application](https://github.com/weka/csi-wekafs/blob/master/docs/usage.md)

## Additional Documentation
- [Official Weka CSI Plugin documentation](https://docs.weka.io/appendix/weka-csi-plugin)

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| controllerPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI controller component only (by default same as global) |
| csiDriverName | string | `"csi.weka.io"` | Name of the driver (and provisioner) |
| csiDriverVersion | string | `"0.7.2"` | CSI driver version |
| dynamicProvisionPath | string | `"csi-volumes"` | Directory in root of file system where dynamic volumes are provisioned |
| globalPluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for all CSI driver components |
| images.attachersidecar | string | `"k8s.gcr.io/sig-storage/csi-attacher:v3.3.0"` | CSI attacher sidecar image URL |
| images.csidriver | string | `"quay.io/weka.io/csi-wekafs"` | CSI driver main image URL |
| images.csidriverTag | string | `"0.7.2"` | CSI driver tag |
| images.livenessprobesidecar | string | `"k8s.gcr.io/sig-storage/livenessprobe:v2.5.0"` | CSI liveness probe sidecar image URL |
| images.provisionersidecar | string | `"k8s.gcr.io/sig-storage/csi-provisioner:v3.0.0"` | CSI provisioner sidecar image URL |
| images.registrarsidecar | string | `"k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.3.0"` | CSI registrar sidercar |
| images.resizersidecar | string | `"k8s.gcr.io/sig-storage/csi-resizer:v1.3.0"` | CSI provisioner sidecar image URL |
| logLevel | int | `5` | Log level of CSI plugin |
| nodePluginTolerations | list | `[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"}]` | Tolerations for CSI node component only (by default same as global) |
| nodeSelector | object | `{}` | Optional nodeSelector for CSI plugin deployment on certain Kubernetes nodes only |

