# Overview
## Example Intentions
1. This example concentrates on Weka Filesystem-backed volume and its derivatives
2. Filesystem is provisioned automatically when volume is created
3. On filesystem, a "seed snapshot", which is basically a snapshot of the empty filesystem, is created
4. From here, basically, a new snapshot-based volumes may be created on same filesystem (described in [dynamic_snapshot](../dynamic_snapshot))
5. When a CSI snapshot is created from filesystem-backed volume, it is a read-only snapshot of the filesystem
6. When a new volume is created sourced from the CSI snapshot, this is basically another snapshot of the same filesystem, 
   but this time the snapshot is writable and contains all information that is preserved in the original Weka snapshot
7. When a new volume is cloned, the produced result is another snapshot-backed volume, but now it originates directly
   from the filesystem


## Configuration Requirements
This example introduces automatic provisioning of filesystems. For this functionality to work, the following configuration must be set:
```
.Values.pluginConfig.allowedOperations.autoCreateFilesystems = true  # to allow provisioning of filesystem-backed volumes
.Values.pluginConfig.allowedOperations.autoExpandFilesystems = true  # to allow resizing of filesystem if snapshot-backed volume is of larger size
```
> **NOTE**: Those values are set by default

## StorageClass Highlights
- Storage class specifies the filesystemGroup to provision the filesystems in
- volumeType set to `weka/v2`. This configuration is becoming default and can be ommitted.


## Notes regarding object deletion:
1. Filesystem-backed volume maps directly to Weka filesystem. 
2. This eventually means that all snapshots and volumes derived from this filesystem are Weka snapshot objects.
3. Deletion of filesystem in this case would render all CSI volumes and snapshots backed by the filesystem to become void.
4. Hence, Weka CSI plugin will not allow deletion of filesystem-backed volume as long as the backing filesystem has at least one (Weka) snapshot  
5. Seed snapshot (empty snapshot created automatically during volume provisioning) is the only snapshot that does not prevent deletion of volume

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fs-api`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Create snapshotclass `snapshotclass-csi-wekafs` (Located in [../common/snapshotclass-csi-wekafs.yaml](../common/snapshotclass-csi-wekafs.yaml))
4. Provision a new filesystem volume `pvc-wekafs-fs-api`
5. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-fs-api`
6. Create a snapshot of the PVC: `snapshot-pvc-wekafs-fs-api`
7. Create a new volume from snapshot: `pvc-wekafs-fs-snapshot`
8. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-snapshot`
   - the file should exist and be accessible
   - the latest timestamp you are expected to see is the timestamp just before creation of snapshot
9. Create a new volume straight from original volume (e.g. clone volume): `pvc-wekafs-fs-clone`
10. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-clone`
    - the file should exist and be accessible
    - the latest timestamp you are expected to see is the timestamp just before volume cloning
