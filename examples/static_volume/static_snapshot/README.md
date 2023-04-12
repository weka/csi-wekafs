# Overview
This example covers a way to provision a Weka filesystem as a static PersistentVolume.

## Example Intentions
1. This example concentrates on Weka snapshot-backed volume with quota, and its derivatives
2. This mode requires an existing Weka filesystem
3. Snapshots of such CSI volumes can be done as well
4. However, since the volume is not created by the CSI controller, its quota is not set. If needed, please set quota manually.

## StorageClass Highlights
- Storage class specifies the filesystemName to provision the directories in
- volumeType set to `weka/v2`
- Filesystem name, if set, is disregarded within storageClass definition

## Prerequisites
The example assumes the following operations were performed on Weka storage prior to execution:
1. Filesystem `testfs` was created
2. A writable snapshot was created on that filesystem, having name "test-snap" and access-point "test-snap-access-point"
3. A quota was set on directory using Weka GUI or CLI
> **NOTE:** When statically provisioning volumes, their capacity is not set via CSI. Hence, if quota is not created, volume capacity enforcement will not operate.
> 
> After setting the quota externally on the `./snapshots/$SNAP_ACCESS_POINT` directory of the filesystem, capacity enforcement will be enabled and CSI volume resizing will be allowed

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fssnap-static-api` 
   - Make sure to set `filesystemName` to valid existing Weka filesystem
   - Make sure `volumeType` is set to `weka/v2`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../../common/csi-wekafs-api-secret.yaml](../../common/csi-wekafs-api-secret.yaml))
3. Create a static snapshot-backed PersistentVolume entry `pv-wekafs-fssnap-static-api`
4. Create a PersistentVolumeClaim that binds the volume above `pvc-wekafs-fssnap-static-api`
5. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-fssnap-static-api`
6. Mount filesystem externally and ensure that file called `temp.txt` was created under its root directory
