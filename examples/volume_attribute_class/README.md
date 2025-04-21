# Overview
## Example Intentions
1. This example concentrates on using VolumeAttributeClass to pass mount options for volumes.
2. Directory backed volume is provisioned with default mount options
3. Pod A is created with a volume mounted with default mount options
4. Pod B is created with a volume mounted with custom mount options that are set in csi-wekafs-forcedirect volumeattributeclass

## Configuration Requirements
Kubernetes of version 1.31 and up is required

## StorageClass Highlights
- Same storage class is used as in ../dynamic_directory/storageclass-wekafs-dir-api.yaml

## VolumeAttributeClass Highlights
1. Weka CSI Plugin comes preinstalled with 3 VolumeAttributeClasses:
   - `csi-wekafs-direct` - sets mount options to `forcedirect`, with all caching disabled
   - `csi-wekafs-performance` - enables both write and read caching by setting `writecache` mount options
   - `csi-wekafs-readcache` - enables read caching only by setting `readcache` mount options
2. If multiple instances of Weka CSI Plugin are installed, the VolumeAttributeClass names will match the Helm release name
3. Settings in VolumeAttributeClass override settings in StorageClass
4. PVC can be patched with a different VolumeAttributeClass, but existing mounts cannot be changed on the fly thus pods need to be restarted after the change

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-dir-api` (Located in [../dynamic_directory/storageclass-wekafs-dir-api.yaml](../dynamic_directory/storageclass-wekafs-dir-api.yaml))
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new directory-backed volume `pvc-wekafs-volume-attrs`
4. Create application pod that attaches this volume using default mount options: `csi-app-volume-attrs-a`
5. Connect to pod shell and run the command `mount -t wekafs | grep /data` and observe the mount options, they should look like this:
   ```cmd
   
   ```
5. Patch the PVC to use a different VolumeAttributeClass `csi-wekafs-forcedirect`
   > NOTE: this will not affect the already running pod
6. Create application pod that attaches to same volume with the newly applied volume attributes class: `csi-app-volume-attrs-b`
7. Create a new volume straight from original volume (e.g. clone volume): `pvc-wekafs-fs-clone`
8. Create application that tails content of `/data/temp.txt` from volume created from snapshot: `csi-app-on-fs-clone`
   - the file should exist and be accessible
   - the latest timestamp you are expected to see is the timestamp just before volume cloning
9. Optionally, create another application that access data in read-only mode: `csi-app-on-fs-api-readonly`
