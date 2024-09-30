# Usage Examples

## Dynamic Provision of Volumes

### General Information

The logic in dynamic provisioning is as following:

1. User creates a [secret](../examples/common/csi-wekafs-api-secret.yaml) that defines API endpoints and credentials
2. User creates a [snapshotClass](../examples/common/snapshotclass-csi-wekafs.yaml) to allow creation of volume
   snapshots
3. User creates a [storageClass](../examples/dynamic_directory/storageclass-wekafs-dir-api.yaml) that describes how new
   volumes should be provisioned
4. User creates a [PersistentVolumeClaim](../examples/dynamic_directory/pvc-wekafs-dir-api.yaml) that mentions the
   storageclass and
   provides additional info, such as desired capacity
5. Once PersistentVolumeClaim is configured, Kubernetes dynamically provisions a new PersistentVolume of desired
   capacity and binds the claim to the volume
6. User creates a [pod](../examples/dynamic_directory/csi-app-on-dir-api.yaml), that utilises the volume through
   PersistentVolumeClaim
7. Optionally, user could create snapshots of volumes and restore them to new volumes, or clone volumes directly

### Choosing the right volume type for your workload
Weka CSI plugin now supports multiple types of volumes, which basically differ by their representation on Weka cluster backend.  
Each volume type has its benefits and limitations. It is crucial to choose the right type of volume to achieve the best 
manageability and performance.

| Volume type                                 | Directory-backed volume                       | Snapshot-backed volume                                         | Filesystem-backed volume                                                     | Hybrid volume                                                  |
|---------------------------------------------|-----------------------------------------------|----------------------------------------------------------------|------------------------------------------------------------------------------|----------------------------------------------------------------|
| Representation on Weka storage              | Directory with attached quota                 | Writable snapshot with attached quota                          | Filesystem with attached quota                                               | Directory inside writable snapshot with attached quota         |
| `filesystemName` in storageClass            | Must be set                                   | Must be set                                                    |                                                                              | Storageclass is inherited from parent volume                   |
| `filesystemGroupName` in storageClass       | -                                             | -                                                              | Must be set                                                                  | -                                                              |
| Requires pre-configured filesystem          | Yes                                           | Yes                                                            | -                                                                            | Located on same filesystem where source content is             |
| Dynamic provision support                   | Yes                                           | Yes                                                            | Yes                                                                          | Yes                                                            |
| Static provision support                    | Yes                                           | Yes                                                            | Yes                                                                          | Yes                                                            |
| Max. number of volumes                      | Unlimited                                     | Limited by max. number of writable snapshots<sup>1</sup>       | Limited by max. number of filesystems<sup>2</sup>                            | Limited by max. number of writable snapshots<sup>1</sup>       |
| CSI Snapshot Support                        | Supported, Off by default<sup>3</sup>         | Supported                                                      | Supported                                                                    | Supported, Off by default<sup>3</sup>                          |
| CSI ContentSource: Snapshot                 | Supported                                     | Supported                                                      | Supported                                                                    | Supported                                                      |
| CSI ContentSource: Volume                   | Supported                                     | Supported                                                      | Supported                                                                    | Supported                                                      |
| Minimum supported Weka version              | 3.13 and up                                   | 3.14-4.1 with limitations<sup>4</sup>  <br>4.2 fully supported | 3.14 and up                                                                  | 3.14-4.1 with limitations<sup>4</sup>  <br>4.2 fully supported |
| Custom mountOptions                         | Per storage class                             | Per storage class                                              | Per storage class                                                            | Per storage class                                              |
| Shared caching (on Kubernetes worker)       | across all volumes on a filesystem            | across all volumes on a filesystem                             | per volume                                                                   | across all volumes on a filesystem                             |
| SELinux support                             | Yes                                           | Yes                                                            | Yes                                                                          | Yes                                                            |
| Volume expansion support                    | Yes                                           | Yes                                                            | Yes                                                                          | Yes                                                            |
| Capacity allocation                         | Shared<sup>5</sup>                            | Shared<sup>5</sup>                                             | Dedicated                                                                    | Shared<sup>5</sup>                                             |
| Can be backed up externally (E.g. snap2Obj) | Yes (a whole filesystem)                      | No                                                             | Yes<sup>6</sup>                                                              | No                                                             |
| Can be tiered to OBS (outside CSI)          | Yes (a whole filesystem)                      | No                                                             | Yes<sup>6</sup>                                                              | No                                                             |
| Snapshot can be uploaded to OBS             | Yes (a whole filesystem)                      | No                                                             | Yes<sup>6</sup>                                                              | No                                                             |
| Recommended usage pattern                   | Millions of small, thinly provisioned volumes | Thousands of volumes with mostly appended data                 | Hundreds of volumes with performance and consistent backup being key factors | Thousands of volumes created from same contentSource           |

Comments:
1. Number of writable snapshots differs between different versions of Weka software. Please refer to documentation for your particular installed version for additional information
2. Number of filesystems differs between different versions of Weka software. Please refer to documentation for your particular installed version for additional information
3. CSI snapshot of directory-backed volume creates a snapshot of the whole filesystem on which the directory is located.  
   As a result, the capacity required by such snapshot would significantly depend on data usage pattern of all CSI directory-backed volumes on same filesystem, and much larger than the volume size.  
   Hence, snapshot creation is prohibited by default, but can be enabled. Refer to Weka CSI Plugin Helm chart documentation for additional information
4. In Weka versions prior to 4.2, quota is not enforced inside filesystem snapshots. As a result, capacity enforcement is not supported for this type of volume.  
   If capacity enforcement is crucial for your workload, use directory-backed volumes or upgrade to latest Weka software
5. Filesystem size and used capacity is not monitoried by CSI plugin. The administrator has to make sure enough capacity is allocated for the filesystem.  
   Filesystems created dynamically (via filesystem-backed volume) can be set with initial size to accomodate future volumes, refer to Weka CSI Plugin Helm chart documentation for additional information
6. Weka CSI plugin does not support automatic configuration of tiering for filesystem-backed volumes, but those can be set externally.

#### Directory-backed volumes
Directory-backed volumes, also called "legacy", are represented by single directory inside a dedicated filesystem.  
Since multiple directory-backed volumes may reside on a single filesystem, their maximal number is only limited by max number of directory quotas.
Snapshots of those volumes, however, are less efficient capacity-wise, since each CSI volume snapshot basically means a snapshot of a whole filesystem

#### Snapshot-backed volumes
Snapshot-backed volumes utilize Weka writable snapshots mechanism for storage. This basically means that a filesystem must be created, on top of which  
writable snapshots can be taken and presented as CSI volumes. The advantages of snapshot-backed volumes on top of directory-backed volumes:
- a new volume is basically a Weka snapshot, hence creating a (CSI) snapshot of it and provisioning as a new (CSI) volume would be very fast and efficient
- deletion of such volumes is much faster, since it is done by deleting the Weka snapshot immediately and reclaiming space in background (unlike in directory-backed volume, where
  deletion is performed in-band by the CSI plugin)

However, number of snapshot-backed volumes is limited by max number of writable snapshots supported by your current Weka software version

#### Filesystem-backed volumes
Filesystem-backed volumes stand for entire filesystem provisioned as a CSI volume. 
This in particular means simpler DR scenarios, better caching, tiering definitions etc.
> **NOTE:** those settings can be done on the filesystem directly, Weka CSI plugin doesn't support extended configuration.

> **WARNING:** in current version of Weka CSI plugin, `.snapshots` directory can be accessed from within root of filesystem-backed volume. 
> 
> This, basically, allows the pod attached to the filesystem-backed volume to access the snapshots of the filesystem - and any other 
> snapshot-backed volumes made on top of same filesystem.
> 
> Hence, it is not recommended to provision additional snapshot-backed volumes on top of same filesystem if strict data isolation is required between workloads

Although those are limited to max number of filesystems supported by your current Weka software, it is recommended to use
filesystem-backed volumes for critical workflows, where maximum performance and dedicated caching is required.

For additional information regarding different volume types and how to use them, refer to the following documentation:

### Examples of provisioning
- Dynamic provisioning of [directory-backed volumes](../examples/dynamic_directory/README.md)
- Dynamic provisioning of [filesystem-backed volumes](../examples/dynamic_filesystem/README.md)
- Dynamic provisioning of [snapshot-backed volumes](../examples/dynamic_snapshot/README.md)

### Apply example configuration

1. From the root directory, deploy the application pods including a storage class, a PVC, and a pod which mounts a
   volume using the csi-wekafs driver found in directory `./examples/dynamic_<VOLUME_TYPE>`, e.g. :

    ```shell script
    $ for i in \
     ./examples/common/csi-wekafs-secret.yaml \
     ./examples/common/snapshotcass-csi-wekafs.yaml \
     ./examples/dynamic_directory/storageclass-wekafs-dir.yaml \
     ./examples/dynamic_directory/pvc-wekafs-dir.yaml \
     ./examples/dynamic_directory/csi-app-on-dir.yaml ; do
     kubectl apply -f $i; 
    done
    
    secret/csi-wekafs-secret created
    volumesnapshotclass.snapshot.storage.k8s.io/snapshotcass-csi-wekafs created
    storageclass.storage.k8s.io/storageclass-wekafs-dir created      
    PersistentVolumeclaim/pvc-wekafs-dir created             
    pod/csi-app-on-dir created
    ```

2. Let's validate the components are deployed as required:

    ```shell script
    $ kubectl get pv
    NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                         STORAGECLASS                   REASON   AGE
    pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir            3m36s
    
    $ kubectl get pvc
    NAME                  STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS                   AGE
    pvc-wekafs-dir   Bound    pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            storageclass-wekafs-dir   3m55s
    
    $ kubectl get pods
    csi-app-on-dir              1/1     Running   0          4m
    ```

3. Finally, inspect one of the application pods, e.g. `csi-app-on-dir`, that a WekaFS volume is correctly mounted:

    ```shell script
    Name:         csi-app-on-dir
    Namespace:    default
    Priority:     0
    Node:         kwuster-kube-7/172.31.43.190
    Start Time:   Wed, 24 Jun 2020 10:41:03 +0300
    Labels:       <none>
    Annotations:  cni.projectcalico.org/podIP: 192.168.228.138/32
                  cni.projectcalico.org/podIPs: 192.168.228.138/32
    Status:       Running
    IP:           192.168.228.138
    IPs:
      IP:  192.168.228.138
    Containers:
      my-frontend:
        Container ID:  docker://9d457f0a702f08b58779b78055bc1f1e7bf965b692b0b70d205a98c15c96ebb9
        Image:         busybox
        Image ID:      docker-pullable://busybox@sha256:95cf004f559831017cdf4628aaf1bb30133677be8702a8c5f2994629f637a209
        Port:          <none>
        Host Port:     <none>
        Command:
          /bin/sh
        Args:
          -c
          while true; do echo `date` hello >> /data/temp.txt; sleep 10;done
        State:          Running
          Started:      Wed, 24 Jun 2020 10:41:22 +0300
        Ready:          True
        Restart Count:  0
        Environment:    <none>
        Mounts:
          /data from my-csi-volume (rw)
          /var/run/secrets/kubernetes.io/serviceaccount from default-token-sb67k (ro)
    Conditions:
      Type              Status
      Initialized       True
      Ready             True
      ContainersReady   True
      PodScheduled      True
    Volumes:
      my-csi-volume:
        Type:       PersistentVolumeClaim (a reference to a PersistentVolumeClaim in the same namespace)
        ClaimName:  pvc-wekafs-dir
        ReadOnly:   false
      default-token-sb67k:
        Type:        Secret (a volume populated by a Secret)
        SecretName:  default-token-sb67k
        Optional:    false
    QoS Class:       BestEffort
    Node-Selectors:  <none>
    Events:
      Type     Reason            Age                    From                     Message
      ----     ------            ----                   ----                     -------
      Normal   Scheduled         6m31s                  default-scheduler        Successfully assigned default/csi-app-on-dir to kwuster-kube-7
      Normal   Pulling           6m15s                  kubelet, kwuster-kube-7  Pulling image "busybox"
      Normal   Pulled            6m13s                  kubelet, kwuster-kube-7  Successfully pulled image "busybox"
      Normal   Created           6m12s                  kubelet, kwuster-kube-7  Created container my-frontend
      Normal   Started           6m12s                  kubelet, kwuster-kube-7  Started container my-frontend
    ```

### Check example configuration

The following steps confirms that csi-wekafs is working properly.

1. mount the Weka filesystem on either Kubernetes node or other server connected to Weka cluster.
2. First, let's see the folder structure on the filesystem:
    ```shell script 
    ls -al
    total 0
    drwxrwxr-x 1 root root 0 Jun 24 10:41 .
    drwxr-xr-x 3 root root 0 Jun 24 10:57 ..
    dr-xr-xr-x 1 root root 0 Jun 16 13:31 .snapshots
    drwxr-x--- 1 root root 0 Jun 24 10:55 csi-volumes
    ```
3. By default, the volume directories are stored in `csi-volumes` directory for convenience
    ```shell script
    ls -al csi-volumes
    total 0
    drwxr-x--- 1 root root 0 Jun 24 10:55  pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc-8fb6c993522aab48a293d488bdcc2e432863d50f
    ```
   > **NOTE:**  the name of the produced
   folder `pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc-8fb6c993522aab48a293d488bdcc2e432863d50f`,
   this is the folder that was created by CSI plugin. The truncated ASCII part of the volume name appears in
   directory name for simpler navigation, which is followed by SHA1 hash for the full directory path

4. The directory now should contain a `hello.txt` file:
    ```shell script
    $ ls csi-volumes/8fb6c993522aab48a293d488bdcc2e432863d50f-pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc/
    hello.txt
    ```

## Static provisioning of existing volumes

### General Information

In some cases, e.g. when user wants to populate pre-existing data to Kubernetes pods, it is convenient to use static
provisioning of an existing directory as PersistentVolume

The static provisioning logic, if so, is slightly different from dynamic provisioning:

1. User creates a generic [storageclass](../examples/static_volume/static_directory/storageclass-wekafs-dir-static-api.yaml), 
   which doesn't need to specify filesystem name
2. User creates a [PersistentVolume](../examples/static_volume/static_directory/pv-wekafs-dir-static-api.yaml), which provides a
   specially crafted volumeHandle (see below)
3. User creates a [PersistentVolumeClaim](../examples/static_volume/static_directory/pvc-wekafs-dir-static-api.yaml) that refers to
   volume name directly
4. Kubernetes automatically binds the PersistentVolumeClaim to the PersistentVolume representation
5. User can attach the bound PersistentVolumeClaim to pod 

> **NOTE:** in static provisioning, since an existing volume is implied, Kuberenetes does not request creating
> a new volume from CSI driver; it would be called only later, when the PersistentVolumeClaim has to be published on a
> node. Hence, quota objects cannot be created for such volumes and as a result, capacity cannot be enforced.

For additional information regarding different volume types and how to use them, refer to the following documentation:

- Static provisioning of [volumes](../examples/static_volume/README.md)
- Static provisioning of [snapshots](../examples/static_snapshot/README.md)

### volumeHandle structure

The volumeHandle in static provisioning should be of the following format:

#### For directory-backed volumes (as in previous versions of Weka CSI Plugin):

```shell script
dir/v1/<FILESYSTEM_NAME>/<INNER_PATH>
```

e.g.

```shell script
dir/v1/my_awsome_filesystem/and/very/deep/path/inside/it
```

#### For unified volumes (including directory volume as a subset):

```shell script
weka/v2/<FILESYSTEM_NAME>[:SNAPSHOT_ACCESS_POINT][/INNER_PATH]
```

Snapshot access point and inner path are optional, hence all the examples below represent valid volume IDs:

```shell
weka/v2/my_awesome_filesystem
weka/v2/my_awesome_filesystem:snap02
weka/v2/my_awesome_filesystem/charming/directory
weka/v2/my_awesome_filesystem:snap02/another/awsome/path
```

> **NOTES:**
> 1. The filesystem, snapshot access point and inner path must exist in order to be able to bind PersistentVolume to
     PersistentVolumeClaim
> 2. Empty path is not supported for `dir/v1` volumeType, but is supported for `weka/v2`
> 3. Please note that Weka snapshot is identified not by its name, but by its access point.
> 4. However, the snapshot name must match the pattern `csivol-<SNAPSHOT_ACCESS_POINT>`, e.g. `csivol-snap02` for the
     example above

### Apply example configuration

1. From the root directory, deploy the application pods including a storage class, a PVC, and a pod which mounts a
   volume using the csi-wekafs driver found in directory `./examples`:

    ```shell script
    $ for i in \
     ./examples/static/storageclass-wekafs-dir-static.yaml \
     ./examples/static/pvc-wekafs-dir-static.yaml \
     ./examples/static/csi-app-on-dir-static.yaml ; do
       kubectl apply -f $i; 
    done
    
    storageclass.storage.k8s.io/storageclass-wekafs-dir-static created      
    PersistentVolumeclaim/pvc-wekafs-dir-static created             
    pod/csi-app-on-dir-static created
    ```

2. Let's validate the components are deployed as required:

    ```shell script
    $ kubectl get pv
    NAME                  CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                             STORAGECLASS                    REASON   AGE
    pv-wekafs-dir-static  1Gi        RWX            Retain           Bound    default/pvc-wekafs-dir-static     storageclass-wekafs-dir-static           7d16h
    
    $ kubectl get pvc
    NAME                      STATUS   VOLUME                  CAPACITY   ACCESS MODES   STORAGECLASS                    AGE
    pvc-wekafs-dir-static     Bound    pv-wekafs-dir-static    1Gi        RWX            storageclass-wekafs-dir-static  7d16h
    
    kubectl get pod
    NAME                    READY   STATUS     RESTARTS   AGE
    csi-app-on-dir-static   1/1     Running    0          44h
    ```

> **NOTE:** Additional examples can be found in `examples` directory

## Expanding a PersistentVolumeClaim

Weka supports online or offline expansion of PersistentVolumeClaim.
Assuming that there is a PersistentVolumeClaim named `pvc-wekafs-dir`, which was defined to use a 1Gi capacity,
and we would like to expand it to 4Gi.

```shell script
$ kubectl get pvc
NAME               STATUS   VOLUME         CAPACITY   ACCESS MODES   STORAGECLASS        dir  AGE
pvc-wekafs-dir     Bound    pv-wekafs-dir  1Gi        RWX            storageclass-wekafs-dir  7d16h

$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                    STORAGECLASS               REASON   AGE
pvc-ee54de25-14f3-4024-98d0-12225e4b8215   4Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir             2d2h 
```

1. In order to perform expansion of the volume, the existing PersistentVolumeClaim has to be edited:
   Change the value of `spec.resources.requests.storage` to desired capacity.
   The resize usually happens within seconds, depending on size and capabilities of your Kubernetes cluster
    ```shell script
    $ kubectl edit pvc pvc-wekafs-dir
    <REPLACE spec.resources.requests.storage value with 4Gi>
    ```

2. Check that configuration was applied
    ```shell script
    $ kubectl get pv
    NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                    STORAGECLASS               REASON   AGE
    pvc-ee54de25-14f3-4024-98d0-12225e4b8215   4Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir             2d2h 
    
    $ kubectl get pvc
    NAME               STATUS   VOLUME         CAPACITY   ACCESS MODES   STORAGECLASS        dir  AGE
    pvc-wekafs-dir     Bound    pv-wekafs-dir  1Gi        RWX            storageclass-wekafs-dir  7d16h
    ```

> **NOTE:** pod restart could be required if feature gate `ExpandInUsePersistentVolumes` is not true, or if pod does not
> support it

## Deleting a PersistentVolumeClaim

Weka supports both Retain and Delete reclaim policy for dynamically provisioned volumes.
In example configuration, a `Delete` reclaimPolicy is defined, which means that the volume will be completely removed,
and all its data will be destroyed once `PersistentVolumeClaim` is deleted.

> **NOTE:** Weka CSI plugin processes volume deletions asynchronously, which means that actual deletion (and capacity
> reclamation) could take time if a volume contains high number of directory entries. Filesystems and snapshots are also
> deleted asynchronously.

It is recommended using `reclaimPolicy: Retain` configuration for statically provisioned,
so they are never deleted automatically by the Weka CSI Plugin.

> **WARNING**:  PersistentVolumes which were provisioned using static provisioning mode, are handled by
> Kubernetes differently. Since the static provisioning is stated for "provisioning a pre-existing volume",  
> Kubernetes does not initiate CSI `ControllerDeleteVolume` request when deleting such a volume;
> so the CSI plugin does not perform the deletion. However, this behavior is undocumented, and it might change  
> in future versions. For this reason, we recommende using `ReclaimPolicy: Retain` for statically-provisioned volumes.


In the example below, we remove the PersistentVolume by deleting the PersistentVolumeClaim, due to "Delete"
reclaimPolicy:

1. Assuming that we have a running pod that uses our volume:
    ```shell script
    $ kubectl get pod
    NAME               READY   STATUS    RESTARTS   AGE
    csi-app-on-dir     1/1     Running   0          43h
    
    $ kubectl get pvc
    NAME                      STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS               AGE
    pvc-wekafs-dir            Bound    pvc-9a0d7f8e-f29e-4762-871b-66652eed3ac4   1Gi        RWX            storageclass-wekafs-dir    7d16h
    
    $ kubectl get pv
    NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                             STORAGECLASS               REASON   AGE
    pvc-9a0d7f8e-f29e-4762-871b-66652eed3ac4   1Gi        RWX            Delete           Bound    default/pvc-wekafs-dir            storageclass-wekafs-dir             7d16h
    ```
2. Delete pod that consumes PersistentVolumeClaim
    ```shell script
    $ kubectl delete pod csi-app-on-dir
    pod "csi-app-on-dir" deleted
    ```
3. Delete PersistentVolumeClaim
    ```shell script
    $ kubectl delete pvc pvc-wekafs-dir
    PersistentVolumeclaim "pvc-wekafs-dir" deleted
    ```
4. Inspect result:
    ```shell script
    $ kubectl get pv
    NAME    CAPACITY    ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM   STORAGECLASS    REASON   AGE
    ```
