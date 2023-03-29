# Overview

## Example Intentions
In rare circumstances, additional mount options (e.g. different caching settings) are desired for a particular flow.

1. This example concentrates on setting custom mount options
2. The example makes a use of a filesystem-backed volume, but the feature is functional on any other type of volume backings

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-mountoptions`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new volume `pvc-wekafs-fs-mountoptions`
4. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-fs-mountoptions` 
5. Attach to the application and validate the options are added correctly 
   ```
   kubectl exec csi-app-fs-mountoptions -- mount -t wekafs
   ```
   The output should resemble this: 
   ```
   csivol-pvc-15a45f20-Z72GJXDCEWQ5 on /data type wekafs (rw,relatime,readcache,noatime,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0)
   ```
