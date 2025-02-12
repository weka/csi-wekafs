# Overview
## Example Intentions
1. This example concentrates on Weka Filesystem-backed volume backed by Encrypted filesystem
2. Filesystem is provisioned automatically when volume is created, and encryption is enabled for the filesystem

## Configuration Requirements
This example introduces automatic provisioning of encrypted filesystems without KMS server configuration.
> **WARNING**: This configuration is for testing purposes only and is not supported for production environments.

Since using encryption without external KMS server is not considered safe, this behavior is prohibited by default in WEKA CSI Plugin.
For this functionality to work, the plugin needs to be installed with explicit configuration parameter
```
.Values.pluginConfig.encryption.allowEncryptionWithoutKms = "true"
```

If the plugin is not installed with this configuration, the following error will be displayed when trying to provision a PVC using encryption:
```
Creating encrypted filesystems without KMS server configuration is prohibited
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

## StorageClass Highlights
- Refer to highlights described in [../dynamic_filesystem/storageclass-wekafs-api.yaml]
- Storage class includes a parameter `parameters.encryptionEnabled` that defines whether the filesystem should be encrypted or not. This is a string and not boolean
    - `"true"` - filesystem is encrypted using cluster-wide encryption key defined via KMS server configuration.
    - `"false"` - filesystem is not encrypted

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-fs-encrypted-nokms-api`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new filesystem volume `pvc-wekafs-fs-encrypted-nokms-api`
4. Create a pod `csi-app-on-fs-encrypted-nokms-api` that uses the volume

Since no significant differences are present in the workflow compared to the [../dynamic_filesystem_encrypted/README.md](../dynamic_filesystem_encrypted/README.md), 
the rest of the workflow is omitted here.
