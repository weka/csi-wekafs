# Usage Examples
## Dynamic Provision of Volumes
### General Information
The logic in dynamic provisioning is as following: 
1. User creates a [storageclass](../examples/dynamic/storageclass-wekafs-dir.yaml) that describes filesystem name on which volumes are going to be provisioned
1. User creates a [PersistentVolumeClaim](../examples/dynamic/pvc-wekafs-dir.yaml) that mentions the storageclass and provides additional info, such as desired capacity
1. Once PersistentVolumeClaim is configured, Kubernetes dynamically provisions a new PersistentVolume of desired capacity and binds to it
1. User creates a [pod](../examples/dynamic/csi-app-on-dir.yaml), that utilises the volume through PersistentVolumeClaim

The csi-wekafs driver is configured to create new directories inside the wekafs filesystem, which is specified in 
storageClass parameters.

Eventually, those directories are mounted as a PersistentVolume and available to one or more pods, 
across any number of nodes in the cluster (as long as this member is also a part of Weka cluster)

A file written in a properly mounted csi-wekafs volume inside an application should show up inside the filesystem,
under one of its subfolders, and can be accessed, for example, by other applications outside of Kubernetes cluster.
  
### Apply example configuration
From the root directory, deploy the application pods including a storage class, a PVC, and a pod which mounts a volume using the csi-wekafs driver found in directory `./examples`:

```shell script
$ for i in \
 ./examples/dynamic/storageclass-wekafs-dir.yaml \
 ./examples/dynamic/pvc-wekafs-dir.yaml \
 ./examples/dynamic/csi-app-on-dir.yaml \
 kubectl apply -f $i; 
done

storageclass.storage.k8s.io/storageclass-wekafs-dir created      
PersistentVolumeclaim/pvc-wekafs-dir created             
pod/csi-app-on-dir created
```

Let's validate the components are deployed as required:

```shell script
$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                         STORAGECLASS                   REASON   AGE
pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir            3m36s

$ kubectl get pvc
NAME                  STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS                   AGE
pvc-wekafs-dir   Bound    pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            storageclass-wekafs-dir   3m55s
```

$ kubectl get pods
csi-app-on-dir              1/1     Running   0          4m

Finally, inspect one of the application pods, e.g. `csi-app-on-dir`, that a WekaFS volume is correctly mounted:

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
[root@kwuster-kube-6 csi-wekafs] 2020-06-24 10:47:34 $ 
```

### Check example configuration 
The following steps confirms that csi-wekafs is working properly.  

1. mount the Weka filesystem on either Kubernetes node or other server connected to Weka Matrix.
1. First, let's see the folder structure on the filesystem:
```shell script 
ls -al
total 0
drwxrwxr-x 1 root root 0 Jun 24 10:41 .
drwxr-xr-x 3 root root 0 Jun 24 10:57 ..
dr-xr-xr-x 1 root root 0 Jun 16 13:31 .snapshots
drwxr-x--- 1 root root 0 Jun 24 10:55 csi-volumes
```
1. By default, the volume directories are stored in `csi-volumes` directory for convenience
```shell script
ls -al csi-volumes
total 0
drwxr-x--- 1 root root 0 Jun 24 10:55  pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc-8fb6c993522aab48a293d488bdcc2e432863d50f
```
1. Note the name of the produced folder `pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc-8fb6c993522aab48a293d488bdcc2e432863d50f`,
this is the folder that was created by CSI plugin. For convenience, the ASCII part of the volume name appears in
directory name, which is followed by SHA1 hash for the full directory path

1. The directory now should contain a `hello.txt` file:
```shell script
$ ls csi-volumes/8fb6c993522aab48a293d488bdcc2e432863d50f-pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc/
hello.txt
```



## Static provisioning of existing volumes
### General Information
In some cases, e.g. when user wants to populate pre-existing data to Kubernetes pods, it is convenient to use static 
provisioning of an existing directory as PeristentVolume

The logic, if so, could be a little bit different from dynamic provisioning:
The logic in dynamic provisioning is as following: 
1. User creates a generic [storageclass](../examples/static/storageclass-wekafs-dir-static.yaml), which doesn't need to specify filesystem
1. User creates a [PersistentVolume](../examples/static/pvc-wekafs-dir-static.yaml), which provides a specially crafted volumeHandle (see below)
1. User creates a [PersistentVolumeClaim](../examples/static/pvc-wekafs-dir-static.yaml) that refers to volume name directly
1. Kubernetes configures an existing path as a pre-existing PersistentVolume.
1. User can utilize produced PersistentVolumeClaim as in previous example 

> **NOTE:** in static provisioning, since an existing volume is implied, Kuberenetes does not request creating 
a new volume from CSI driver; it would be called only later, when the PersistentVolumeClaim has to be published on a node.  

### volumeHandle structure
The volumeHandle in static provisioning should be of the following format:
```shell script
dir/v1/<FILESYSTEM_NAME>/<INNER_PATH>
```
e.g. 
```shell script
dir/v1/my_awsome_filesystem/and/very/deep/path/inside/it
```
> **NOTE:**
> 1. The directory must exist in order to be able to bind PersistentVolume to PersistentVolumeClaim
> 1. Empty path (e.g. root directory of a filesystem) is not supported for `dir/v1` volumeType and cannot be provided

### Apply example configuration
From the root directory, deploy the application pods including a storage class, a PVC, and a pod which mounts a volume using the csi-wekafs driver found in directory `./examples`:

```shell script
$ for i in \
 ./examples/static/storageclass-wekafs-dir-static.yaml \
 ./examples/static/pvc-wekafs-dir-static.yaml \
 ./examples/static/csi-app-on-dir-static.yaml \
 kubectl apply -f $i; 
done

storageclass.storage.k8s.io/storageclass-wekafs-dir-static created      
PersistentVolumeclaim/pvc-wekafs-dir-static created             
pod/csi-app-on-dir-static created
```

Let's validate the components are deployed as required:

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

## Expanding a PersistentVolumeClaim
Note: Currently, Weka CSI plugin does not enforce actual capacity limits for PersistentVolumes

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

In order to perform expansion of the volume, the existing PersistentVolumeClaim has to be edited:
Change the value of `spec.resources.requests.storage` to desired capacity. 
The resize will be usually performed within seconds, depending on size and capabilities of your Kubernetes clusetr   
```shell script

$ kubectl edit pvc pvc-wekafs-dir
<REPLACE spec.resources.requests.storage value with 4Gi>
```

Check that configuration was applied
```shell script
$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                    STORAGECLASS               REASON   AGE
pvc-ee54de25-14f3-4024-98d0-12225e4b8215   4Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir             2d2h 

$ kubectl get pvc
NAME               STATUS   VOLUME         CAPACITY   ACCESS MODES   STORAGECLASS        dir  AGE
pvc-wekafs-dir     Bound    pv-wekafs-dir  1Gi        RWX            storageclass-wekafs-dir  7d16h
```
Note: pod restart could be required if feature gate `ExpandInUsePersistentVolumes` is not true, or if pod does not support it
 
## Deleting a PersistentVolumeClaim
Weka supports both Retain and Delete reclaim policy for dynamically provisioned volumes. 
In example configuration, a Delete reclaimPolicy is defined, which means that the volume will be completely removed, 
and all it's data will be destroyed once PersistentVolumeClaim is deleted.

Deletion of volumes is performed asynchronously, and could take time if a volume contains high number of directory entries.
The free capacity on a filesystem is reclaimed automatically

> **WARNING**:  PersistentVolumes which were provisioned using static provisioning mode, are handled by Kubernetes differently.
Since the static provisioning is stated for "provisioning a pre-existing volume", Kubernetes does not initiate
`ControllerDeleteVolume` request when deleting such a volume, so the CSI plugin does not perform the deletion.
>
>However, this behavior is undocumented, and it might change in future versions. For this reason, it is always
>recommended using `ReclaimPolicy: Retain` for statically-provisioned volumes.   

regardless the `RetainPolicy` configuration for those volumes, they are never deleted automatically by the cluster.

In the example below, we remove the PersistentVolume by deleting the PersistentVolumeClaim, due to "Delete" reclaimPolicy:  

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
1. Delete pod that consumes PersistentVolumeClaim
```shell script
$ kubectl delete pod csi-app-on-dir
pod "csi-app-on-dir" deleted
```
1. Delete PersistentVolumeClaim
```shell script
$ kubectl delete pvc pvc-wekafs-dir
PersistentVolumeclaim "pvc-wekafs-dir" deleted
```
1. Inspect result:
```shell script
$ kubectl get pv
NAME    CAPACITY    ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM   STORAGECLASS    REASON   AGE
```
