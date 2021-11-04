# CSI WekaFS Driver

This repository hosts the CSI WekaFS driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster
- Running version 1.18 or later. Although older versions from 1.13 and up should work, they were not tested
- Access to terminal with `kubectl` installed
- Weka system pre-configured and Weka client installed and registered in cluster for each Kubernetes node

## Deployment
- [Helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [Script-based deployment](docs/deploy.md)
- [Helm-based local deployment](deploy/helm/csi-wekafsplugin/LOCAL.md)

## Usage
- [Deploy an Example application](docs/usage.md)

## Additional Documentation
- [Official Weka CSI Plugin documentation](https://docs.weka.io/appendix/weka-csi-plugin)

## Building the binaries
If you want to build the driver yourself, you can do so with the following command from the root directory:

```shell
make build
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| controllerPluginTolerations[0].effect | string | `"NoSchedule"` |  |
| controllerPluginTolerations[0].key | string | `"node-role.kubernetes.io/master"` |  |
| controllerPluginTolerations[0].operator | string | `"Exists"` |  |
| csiDriverName | string | `"csi.weka.io"` |  |
| csiDriverVersion | string | `"0.6.6"` |  |
| dynamicProvisionPath | string | `"csi-volumes"` |  |
| globalPluginTolerations[0].effect | string | `"NoSchedule"` |  |
| globalPluginTolerations[0].key | string | `"node-role.kubernetes.io/master"` |  |
| globalPluginTolerations[0].operator | string | `"Exists"` |  |
| images.attachersidecar | string | `"quay.io/k8scsi/csi-attacher:v3.0.0-rc1"` |  |
| images.csidriver | string | `"quay.io/weka.io/csi-wekafs"` |  |
| images.csidriverTag | string | `"0.6.6"` |  |
| images.livenessprobesidecar | string | `"quay.io/k8scsi/livenessprobe:v2.2.0"` |  |
| images.provisionersidecar | string | `"quay.io/k8scsi/csi-provisioner:v1.6.0"` |  |
| images.registrarsidecar | string | `"quay.io/k8scsi/csi-node-driver-registrar:v1.3.0"` |  |
| images.resizersidecar | string | `"quay.io/k8scsi/csi-resizer:v0.5.0"` |  |
| nodePluginTolerations[0].effect | string | `"NoSchedule"` |  |
| nodePluginTolerations[0].key | string | `"node-role.kubernetes.io/master"` |  |
| nodePluginTolerations[0].operator | string | `"Exists"` |  |
| 