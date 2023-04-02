# Overview
This example covers a way to provision a Weka filesystem as a static PersistentVolume.

## Example Intentions
1. This example concentrates on static provisioning of CSI snapshot using Weka CSI Plugin
2. This mode requires an existing Weka filesystem, a snapshot and a quota on root directory of snapshot

## StorageClass Highlights
- Storage class must specify CSI secrets
- volumeType set to `weka/v2` but may be left blank.
- Filesystem name, if set, is disregarded within storageClass definition

## Prerequisites
The example assumes the following operations were performed on Weka storage prior to execution:
1. Filesystem `testfs` was created
2. A non-writable snapshot was created on that filesystem, having name "test-snap" and access-point "test-snap-access-point"
3. A quota must be set on a root directory of this snapshot

> **WARNING:** If quota was not created for the statically provisioned snapshot (regardless of its size), volumes restored from this snapshot will not have a capacity enforcement!
> **NOTE:** You will not be able to provision new volume from the snapshot if it is configured as writable


# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-snap-api` 
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Create snapshotclass `snapshotclass-csi-wekafs` (Located in [../common/snapshotclass-csi-wekafs.yaml](../common/snapshotclass-csi-wekafs.yaml))
4. Create a static VolumeSnapshotContent entry `snapshotcontent-wekafs-static-api`
5. Create a static VolumeSnapshot entry `snapshot-wekafs-static-api`
6. Create a PersistentVolumeClaim that creates a new dynamic volume sourced from snapshot `pvc-wekafs-snap-from-static-snap`
7. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-fssnap-static-api`

> **NOTE:** When statically provisioning volumes, their capacity is not set via CSI. Hence, quota should be created manually  
