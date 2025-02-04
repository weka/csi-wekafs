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

> **NOTE:** Encryption is only supported for filesystem-backed volumes

## StorageClass Highlights
- Refer to highlights described in [../dynamic_filesystem/storageclass-wekafs-api.yaml]
- Storage class includes a parameter `parameters.encryption` that defines whether the filesystem should be encrypted or not. This is a string and not boolean
    - `true` - filesystem is encrypted using WEKA-managed encryption keys.
    - `false` - filesystem is not encrypted
- Storage class includes a parameter `parameters.manageEncryptionKeys` that is currently not supported. This parameter is reserved for future use

## Notes regarding object deletion:
1. Filesystem-backed volume maps directly to Weka filesystem. 
2. Filesystem is encrypted

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fs-encrypted-api`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new filesystem volume `pvc-wekafs-fs-encrypted-api`
4. Create a pod `csi-app-on-fs-encrypted-api` that uses the volume
