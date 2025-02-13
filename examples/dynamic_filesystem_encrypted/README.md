# Overview
## Example Intentions
1. This example concentrates on Weka Filesystem-backed volume backed by Encrypted filesystem
2. Filesystem is provisioned automatically when volume is created, and encryption is enabled for the filesystem

## Configuration Requirements
This example introduces automatic provisioning of encrypted filesystems. 
For this functionality to work, the WEKA cluster must be configured with a valid KMS server.
If KMS server is not configured, volume will not be created and the following error will be displayed:
```
Encryption is not enabled on the cluster
```
If WEKA cluster does not support encryption of filesystems, the following error will be displayed:
```
Encryption is not supported on the cluster
```

## Example Highlights
1. Encryption is only supported for filesystem-backed volumes.
2. Volumes created from snapshots or directories inherit the encryption setting from the underlying filesystem.
3. However, if encryption is enabled in storageClass, the driver will validate that the underlying filesystem is encrypted.
   If the filesystem does not have encryption enabled, an appropriate error will be returned.
4. The configuration of the KMS server is not part of this example. Please refer to the Weka documentation for more information.

## StorageClass Highlights
- Refer to highlights described in [../dynamic_filesystem/storageclass-wekafs-api.yaml]
- Storage class includes a parameter `parameters.encryptionEnabled` that defines whether the filesystem should be encrypted or not. 
  This is a string and not boolean.
    - `"true"` - filesystem is encrypted using WEKA-managed encryption keys.
    - `"false"` - filesystem is not encrypted

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fs-encrypted-api`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new filesystem volume `pvc-wekafs-fs-encrypted-api`
4. Create a pod `csi-app-on-fs-encrypted-api` that uses the volume
5. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-on-fs-encrypted-api`
6. Create a snapshot of the PVC: `snapshot-pvc-wekafs-fs-encrypted-api`
7. Create a new volume from snapshot: `pvc-wekafs-fs-snapshot-encrypted-api`
8. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-snapshot-encrypted-api`
  - the file should exist and be accessible
  - the latest timestamp you are expected to see is the timestamp just before creation of snapshot
9. Create a new volume straight from original volume (e.g. clone volume): `pvc-wekafs-fs-clone-encrypted-api`
10. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-clone-encrypted-api`
  - the file should exist and be accessible
  - the latest timestamp you are expected to see is the timestamp just before volume cloning
