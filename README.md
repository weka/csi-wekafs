# CSI WekaFS Driver

This repository hosts the CSI WekaFS driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster
- Running version 1.18 or later. Although older versions should work, they were not tested
- Access to terminal with `kubectl` installed
- WekaFS system pre-configured and Weka client is installed on each Kubernetes node 

## Deployment
- [Deployment for Kubernetes 1.18 and later](docs/deploy.md)

## Building the binaries
If you want to build the driver yourself, you can do so with the following command from the root directory:

```shell
make
```
