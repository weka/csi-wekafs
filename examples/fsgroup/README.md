# Overview
## Example Intentions
1. This example concentrates on setting fsGroup on Weka CSI volume
2. The example makes a use of a directory-backed volume, but the feature is functional on any other type of volume backings

## Weka CSI Plugin Upgrade Implications
Since CSIDriver objects are immutable, adding support for fsGroup requires the plugin to be uninstalled and reinstalled.
> **NOTE:** Existing persistent volumes or workloads using them will not be affected

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-snap-api` (Located in [../dynamic_directory/storageclass-wekafs-dir-api.yaml](../dynamic_directory/storageclass-wekafs-dir-api.yaml))
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new volume `pvc-wekafs-fsgroup`
4. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-fsgroup` and has different non-root permissions
5. Attach to the application and validate filesystem contents and permissions by issuing 
   ```
   kubectl exec csi-app-fsgroup -- ls -al /data
   ```
   The output should resemble this:
   ```
   $ ls -al /data
   total 8
   drwxrws--- 1 root 2000    0 Feb 12 11:46 .
   drwxr-xr-x 1 root root 4096 Feb 12 11:46 ..
   -rw-r--r-- 1 2000 2000 2345 Feb 12 11:57 temp.txt
   ```
