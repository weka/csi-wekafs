# Overview
## Example Intentions
1. This example concentrates on Weka snapshot-backed volume and its derivatives
2. Upon first CSI volume creation, a new read-only snapshot of empty filesystem is produced, called a _seed snapshot_
3. Since that moment:
   1. every new blank CSI volume maps to a new writable snapshot of the seed snapshot
   2. every snapshot of the volume maps to a new read-only snapshot of itself
   3. every volume created from snapshot maps to a new writable snapshot of the originating CSI snapshot (which is a Weka read-only snapshot)
   4. every cloned volume maps to a new writable snapshot of the originating CSI volume (which is a Weka writable snapshot)

> **WARNING**: Filesystem provided for Weka CSI Plugin must be empty at least at the moment of first CSI volume provisioning.
  This is required for the plugin to be able to create a _seed snapshot_ (a snapshot of the filesystem in empty state)  

## Configuration Requirements
This example introduces automatic provisioning of filesystems. For this functionality to work, the following configuration must be set:
```
.Values.pluginConfig.allowedOperations.autoCreateSeedSnapshot = true  # to allow creation of the initial empty snapshot
.Values.pluginConfig.allowedOperations.autoExpandFilesystems = true  # to allow resizing of filesystem if snapshot-backed volume is of a larger size
```
> **NOTE**: Those values are set by default

### Special Consideration for Weka Software Versions Below v4.1
Weka software of version below 4.1 does not support enforcement of quotas on filesystem snapshots.
Hence, CSI plugin forbids creation of snapshot-based volumes on clusters having an older version by default.
As a result, provisioning a snapshot-based volume on such cluster will fail with a message similar to this:
```text
failed to provision volume with StorageClass "storageclass-wekafs-snap-api": rpc error: code = FailedPrecondition desc = Quota enforcement is not supported for snapshot-based volumes by current Weka software version, please upgrade Weka cluster
```
This behavior may be adjusted, so snapshot-based volumes will be allowed on older versions of Weka as well, by setting
```
.Values.pluginConfig.allowedOperations.snapshotVolumesWithoutQuotaEnforcement=true
```

> **WARNING**: Capacity will not be enforced for such volumes until Weka software is upgraded to supported version

> **NOTE**: No user action is required to enable capacity enforcement upon the storage cluster upgrade

## StorageClass Highlights
- Storage class specifies the filesystemName to provision the filesystems in
- volumeType set to `weka/v2` or is unset at all

> **NOTE**: It is important to mention that the difference from [directory-based storageClass](../dynamic_directory/storageclass-wekafs-dir-api.yaml) 
> is only the volumeType


# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-snap-api`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Create snapshotclass `snapshotclass-csi-wekafs` (Located in [../common/snapshotclass-csi-wekafs.yaml](../common/snapshotclass-csi-wekafs.yaml))
4. Provision a new filesystem volume `pvc-wekafs-snap-api`
5. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-snap-api`
6. Create a snapshot of the PVC: `snapshot-pvc-wekafs-snap-api`
7. Create a new volume from snapshot: `pvc-wekafs-snap-snapshot`
8. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-snap-snapshot`
   - the file should exist and be accessible
   - the latest timestamp you are expected to see is the timestamp just before creation of snapshot
9. Create a new volume straight from original volume (e.g. clone volume): `pvc-wekafs-snap-clone`
10. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-clone`
    - the file should exist and be accessible
    - the latest timestamp you are expected to see is the timestamp just before volume cloning
