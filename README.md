# CSI WekaFS Driver

This repository hosts the CSI WekaFS driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster
- Running version 1.18 or later. Although older versions from 1.13 and up should work, they were not tested
- Access to terminal with `kubectl` installed
- Weka system pre-configured and Weka client installed and registered in cluster for each Kubernetes node

## Deployment
- [Deployment for Kubernetes 1.18 and later](docs/deploy.md)
- Helm deployment (experimental)

## Building the binaries
If you want to build the driver yourself, you can do so with the following command from the root directory:

```shell
make
```
