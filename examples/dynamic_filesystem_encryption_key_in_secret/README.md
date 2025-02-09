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

## Notes regarding object deletion:
1. Filesystem-backed volume maps directly to Weka filesystem. 
2. Filesystem is encrypted
3. Encryption key is stored in a KMS server
4. The encryption key identifier, KMS namespace, role ID and secret ID must be specified in the API secret
5. Only kmsKeyIdentifier and kmsNamespace

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fs-encryption-key-in-secret`
2. Create CSI secret `csi-wekafs-api-secret-kms-encryption-key-in-secret` 
3. Provision a new filesystem volume `pvc-wekafs-fs-encryption-key-in-secret`
4. Create a pod `csi-app-on-fs-encryption-key-in-secret` that uses the volume
