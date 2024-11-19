# Upgrade legacy persistent volumes for capacity enforcement

## Bind legacy volumes to API

Capacity enforcement and integration with the WEKA filesystem directory quotas require the following prerequisites:

* WEKA CSI Plugin version **0.7.0** and up.
* WEKA software version **v3.13.0** and up.
* WEKA CSI Plugin can communicate with the WEKA filesystem using REST API and correlate between a specific persistent volume and the WEKA cluster serving this volume.

In the API-based communication model, Kubernetes StorageClass refers to a secret that specifies all the required parameters for API calls to the WEKA cluster. However, this is different from the situation in the legacy communication model, where the storage class doesn't specify the API credentials. For details, see [API-based communication model] (../docs/storage-class-configurations.md) 

Kubernetes does not allow modifying the StorageClass parameters; hence every volume created with the legacy-model storage class never reports its credentials.

WEKA CSI Plugin **0.7.0** provides a unique configuration mode in which legacy volumes can be bound to a single secret, referring to a single WEKA cluster API connection parameters. In this configuration mode, every request to serve, such as create, delete, and expand, a legacy Persistent Volume (or Persistent Volume Claim) that originates from a Legacy Storage Class (without reference to an API secret) communicates to that cluster.

**Note**:
Volumes provisioned by the CSI Plugin of version **0.7.0** in the API-based communication model, but on older versions of the WEKA cluster (below version **3.13.0**), are still in legacy mode. However, because the storage class already contains the secret reference, specifying the `legacyVolumeSecretName` parameter is unnecessary, and you can safely skip to the **Migrate legacy volumes** procedure below.

To bind legacy volumes to a single secret, perform the following:

1. Create a Kubernetes secret that describes the API communication parameters for legacy volumes. Adhere to the following:
   * The format of the secret is identical to the secret.
   * This secret must be located in the same Kubernetes namespace of the WEKA CSI Plugin.
2. Set the `legacyVolumeSecretName` parameter to match the secret's name above during the plugin upgrade or installation. Do one of the following:
   * You can modify the `values.yaml` directly.
   * Create the Kubernetes secret and only then set the `legacyVolumeSecretName` parameter during the Helm upgrade as follows:

```
helm upgrade csi-wekafs --namespace csi-wekafs csi-wekafs/csi-wekafsplugin \
 --set legacyVolumeSecretName="csi-wekafs-api-secret"

```

**Warning**:
If you do not create the Kubernetes secret before executing the helm upgrade, the CSI Plugin components remain `Pending` after the upgrade.

## Migrate legacy volumes

Once you bind legacy volumes to a single secret procedure, you can migrate the volumes by binding a new WEKA filesystem directory quota object to an existing persistent volume.

WEKA provides a migration script that automates the process.

Run the migration procedure only once from any Linux server connected to the same WEKA cluster. Additional script runs migrate only those volumes created in legacy mode after migration. It is safe to run the migration script multiple times, although usually, this is not required.

The migration process might take significant time and depends on the number of persistent volumes and their capacity. The migration process is transparent and does not require downtime.

#### Before you begin

The migration script requires several dependencies, which must be installed in advance: `jq`, `xattr`, `getfattr`, and `setfattr.`

Refer to the specific OS package management documentation to install the necessary packages.

#### Procedure

1. Check the `csi-wekafs` repository from any server connected to the WEKA cluster:

```
git clone https://github.com/weka/csi-wekafs.git
```

2. Run the migration script using the following command line (for multiple filesystems, run the command line for each filesystem):

```
$ sudo migration/migrate-legacy-csi-volumes.sh <filesystem_name> [--csi-volumes-dir <csi_volumes_dir>] [--endpoint-address BACKEND_IP_ADDRESS:BACKEND_PORT]
```

Where:

* `<filesystem_name>`: Specifies the filesystem name on which the  CSI volumes are located.
* `<csi_volumes_dir>`: Optional parameter. Specifies the directory in the filesystem where the CSI volumes are stored. Set this parameter only if the directory differs from default values.

**Note**:
On a stateless client, you must specify the `--endpoint-address` to successfully mount a filesystem. However, if the container is part of the WEKA cluster (either client or backend), this is not necessary.

Example:

```
$ ./migrate-legacy-csi-volumes.sh default
Weka CSI Volume migration utility. Copyright 2021 Weka
[2021-11-04 14:33:04] NOTICE     Initializing volume migration for filesystem default
[2021-11-04 14:33:04] NOTICE     Successfully mounted filesystem default
[2021-11-04 14:33:04] NOTICE     Starting Persistent Volume migration
[2021-11-04 14:33:04] INFO       Processing directory 'pvc-e5379b17-4612-4fa3-aa57-64d5b37d7f57-1025f14ca92d2e18dd92a05efadf15a4972675f0'
[2021-11-04 14:33:04] INFO       Creating quota of 1073741824 bytes for directory pvc-e5379b17-4612-4fa3-aa57-64d5b37d7f57-1025f14ca92d2e18dd92a05efadf15a4972675f0
[2021-11-04 14:33:05] INFO       Quota was successfully set for directory pvc-e5379b17-4612-4fa3-aa57-64d5b37d7f57-1025f14ca92d2e18dd92a05efadf15a4972675f0
[2021-11-04 14:33:05] NOTICE     Migration process complete!
[2021-11-04 14:33:05] NOTICE     1 directories migrated successfully
[2021-11-04 14:33:05] NOTICE     0 directories skipped
```
