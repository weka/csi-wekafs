# Overview
This example extends the example provided in previous version of the Weka CSI Plugin.

## Example Intentions
1. This example concentrates on Weka directory-backed volume with quota, and its derivatives
2. Although new functionality is added, this mode is fully compatible with volumes provisioned in previous versions of Weka CSI Plugin
3. This mode requires an existing Weka filesystem, on top of which directories will be provisioned, and attached to Weka quota object
4. Snapshots of such CSI volumes (including volumes created with previous versions of the plugin) can be done as well
5. However, since the snapshot is performed on Weka filesystem level, snapshot would include all changes done in either one of the volumes
   provisioned on top of that filesystem, hence would be very wasteful on allocated storage resources.
6. Due to the limitation above, creation of CSI snapshots originating from directory-backed volumes is prohibited by default configuration.
   Please refer to the section below for configuration required to allow this functionality.
7. When a CSI snapshot is created from directory-backed volume, the produced snapshot ID points on same directory inside the Weka read-only snapshot
8. When a new volume is created sourced from the produced CSI snapshot, this is basically another snapshot of the same filesystem, 
   but this time the snapshot is writable and contains all information that is preserved in the original Weka snapshot. However, new volume 
   inherits the inner path of original volume. 
9. Cloned volume will behave in similar manner, by creating a writable snapshot of a whole Weka filesystem and inner path pointer. 


## Configuration Requirements
This example introduces dynamic provisioning of directory-based CSI volumes using Weka CSI plugin, and subsequent creation of child objects.
Creating of snapshots from directory-based volumes basically means creation of a full filesystem snapshot.
This means, in particular, that creating a snapshot of a small volume or unfrequently changed volume that shares a filesystem with
another large / frequently might produce a snapshot of size much bigger than the original volume actual capacity.

This behavior may be adjusted by explicitly allowing creation of directory-based volume snapshots by the following configuration:
```
.Values.pluginConfig.allowedOperations.snapshotDirectoryVolumes = true
```
> **WARNING**: Those are settings are disabled by default and must be changed by reinstalling the Weka CSI Plugin with settings enabled 

## StorageClass Highlights
- Storage class specifies the filesystemName to provision the directories in
- volumeType set to `dir/v1`
> **NOTE:** For provisioning CSI volumes on multiple Weka filesystems, a separate storage class is required for each filesystem

> **NOTE:** If volumeType is unset or set to `weka/v2`, instead of directory-based, a [snapshot-based](../dynamic_snapshot) volumes 
  will be provisioned

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-dir-api`
   - Make sure to set `filesystemName` to valid existing Weka filesystem
   - Make sure `volumeType` is set to `dir/v1`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Create snapshotclass `snapshotclass-csi-wekafs` (Located in [../common/snapshotclass-csi-wekafs.yaml](../common/snapshotclass-csi-wekafs.yaml))
4. Provision a new directory-based volume `pvc-wekafs-dir-api`
5. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-dir-api`
6. Create a snapshot of the PVC: `snapshot-pvc-wekafs-dir-api`
7. Create a new volume from snapshot: `pvc-wekafs-dir-snapshot`
8. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-dir-snapshot`
   - the file should exist and be accessible
   - the latest timestamp you are expected to see is the timestamp just before creation of snapshot
9. Create a new volume straight from original volume (e.g. clone volume): `pvc-wekafs-dir-clone`
10. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-dir-clone`
    - the file should exist and be accessible
    - the latest timestamp you are expected to see is the timestamp just before volume cloning
