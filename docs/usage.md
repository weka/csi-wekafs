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

> **NOTE:** The example above is based on directory-backed CSI volume, but same logic applies also to other volume
> types.

For additional information regarding different volume types and how to use them, refer to the following documentation:

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
provisioning of an existing directory as PeristentVolume

The static provisioning logic, if so, is slightly different from dynamic provisioning:

1. User creates a generic [storageclass](../examples/static_volume/storageclass-wekafs-dir-static-api.yaml), which
   doesn't need to
   specify filesystem
2. User creates a [PersistentVolume](../examples/static_volume/pvc-wekafs-dir-static-api.yaml), which provides a
   specially crafted
   volumeHandle (see below)
3. User creates a [PersistentVolumeClaim](../examples/static_volume/pvc-wekafs-dir-static-api.yaml) that refers to
   volume name
   directly
4. Kubernetes configures an existing path as a pre-existing PersistentVolume.
5. User can utilize produced PersistentVolumeClaim as in previous example

> **NOTE:** in static provisioning, since an existing volume is implied, Kuberenetes does not request creating
> a new volume from CSI driver; it would be called only later, when the PersistentVolumeClaim has to be published on a
> node.

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
> **NOTE:** Currently, Weka CSI plugin does not enforce actual capacity limits for PersistentVolumes

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

1. Check that configuration was applied
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
