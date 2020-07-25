## Deployment
The easiest way to test the csi-wekafs driver is to run the `deploy.sh` script for the Kubernetes version used by
the cluster as shown below for Kubernetes 1.18. This creates the deployment that is maintained specifically for that
release of Kubernetes. However, other deployments might also work.

```
# deploy csi-wekafs driver
$ deploy/kubernetes-latest/deploy.sh
```

You should see an output similar to the following printed on the terminal showing the application of RBAC rules and the
result of deploying the csi-wekafs driver. Note that the following output is from Kubernetes 1.18:

```shell
$ ./deploy/kubernetes-latest/deploy.sh
creating wekafsplugin namespace
namespace/csi-wekafsplugin created
deploying wekafs components
   ./deploy/kubernetes-latest/wekafs/csi-wekafs-plugin.yaml
        using           image: quay.io/k8scsi/csi-node-driver-registrar:v1.3.0
        using           image: quay.io/weka.io/csi-wekafs:v0.0.2-3-g5d0362b
        using           image: quay.io/k8scsi/livenessprobe:v1.1.0
        using           image: quay.io/k8scsi/csi-provisioner:v1.6.0
        using           image: quay.io/k8scsi/csi-attacher:v3.0.0-rc1
csidriver.storage.k8s.io/csi.weka.io unchanged
serviceaccount/csi-wekafsplugin created
clusterrole.rbac.authorization.k8s.io/csi-wekafsplugin-cluster-role created
clusterrolebinding.rbac.authorization.k8s.io/csi-wekafsplugin-cluster-role-binding created
role.rbac.authorization.k8s.io/csi-wekafsplugin-role created
rolebinding.rbac.authorization.k8s.io/csi-wekafsplugin-role-binding created
daemonset.apps/csi-wekafsplugin created
10:29:07 waiting for wekafs deployment to complete, attempt #0
10:29:17 deployment completed successfully
10:29:17 2 plugin pods are running:
csi-wekafsplugin-6gk86   5/5     Running   0          10s
csi-wekafsplugin-sfmgd   5/5     Running   0          10s
```
**Note**: the number of running `csi-wekafsplugin-*` pods should be equal to number of K8s nodes 
which are not tainted from `daemonset.apps` scheduling (e.g. in default configuration it would be all nodes except master) 

## Run example application and validate

Next, validate the deployment.  First, ensure all expected pods are running properly:

```shell
$ kubectl get pods --namespace csi-wekafsplugin
NAME                     READY   STATUS    RESTARTS   AGE
csi-wekafsplugin-6gk86   5/5     Running   0          2m54s
csi-wekafsplugin-sfmgd   5/5     Running   0          2m54s
```

From the root directory, deploy the application pods including a storage class, a PVC, and a pod which mounts a volume using the csi-wekafs driver found in directory `./examples`:

```shell
$ for i in \
 ./examples/storageclass-wekafs-dir.yaml \
 ./examples/pvc-wekafs-dir.yaml \
 ./examples/csi-app-on-dir.yaml \
 ./examples/csi-daemonset.app-on-dir.yaml; do 
 kubectl apply -f $i; 
done

storageclass.storage.k8s.io/storageclass-wekafs-dir created      
persistentvolumeclaim/pvc-wekafs-dir created             
pod/my-csi-app created
daemonset.apps/csi-wekafs-test created                                                                                                               
```

Let's validate the components are deployed as required:

```shell
$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                         STORAGECLASS                   REASON   AGE
pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            Delete           Bound    default/pvc-wekafs-dir   storageclass-wekafs-dir            3m36s

$ kubectl get pvc
NAME                  STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS                   AGE
pvc-wekafs-dir   Bound    pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc   1Gi        RWX            storageclass-wekafs-dir   3m55s
```

$ kubectl get pods
my-csi-app              1/1     Running   0          4m
csi-wekafs-test-6zbgg   1/1     Running   0          4m
csi-wekafs-test-x89sl   1/1     Running   0          4m

Finally, inspect one of the application pods, e.g. `my-csi-app`, that a WekaFS volume is correctly mounted:

```shell
Name:         my-csi-app
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
Tolerations:     node.kubernetes.io/not-ready:NoExecute for 300s
                 node.kubernetes.io/unreachable:NoExecute for 300s
Events:
  Type     Reason            Age                    From                     Message
  ----     ------            ----                   ----                     -------
  Normal   Scheduled         6m31s                  default-scheduler        Successfully assigned default/my-csi-app to kwuster-kube-7
  Normal   Pulling           6m15s                  kubelet, kwuster-kube-7  Pulling image "busybox"
  Normal   Pulled            6m13s                  kubelet, kwuster-kube-7  Successfully pulled image "busybox"
  Normal   Created           6m12s                  kubelet, kwuster-kube-7  Created container my-frontend
  Normal   Started           6m12s                  kubelet, kwuster-kube-7  Started container my-frontend
[root@kwuster-kube-6 csi-wekafs] 2020-06-24 10:47:34 $ 
```

## Confirm csi-wekafs driver works
The csi-wekafs driver is configured to create new directories inside the wekafs filesystem, which is specified in 
[storageclass](../examples/dir/storageclass-wekafs-dir.yaml) parameters.

Eventually, those directories are mounted as a persistentVolume and available to one or more pods, 
across any number of nodes in the cluster (as long as this member is also a part of Weka cluster)

A file written in a properly mounted csi-wekafs volume inside an application should show up inside the filesystem,
under one of its subfolders.

The following steps confirms that csi-wekafs is working properly.  

First, create a file from the application pod as shown:

```shell
$ kubectl exec -it my-csi-app /bin/sh
/ # touch /data/hello-world
/ # exit
```

Next, mount the Weka filesystem on either Kubernetes node or other server connected to Weka Matrix

First, let's see the folder structure on the filesystem:
```shell 
ls -al
total 0
drwxrwxr-x 1 root root 0 Jun 24 10:41 .
drwxr-xr-x 3 root root 0 Jun 24 10:57 ..
dr-xr-xr-x 1 root root 0 Jun 16 13:31 .snapshots
drwxr-x--- 1 root root 0 Jun 24 10:55 8fb6c993522aab48a293d488bdcc2e432863d50f-pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc
```
Note the name of the produced folder `8fb6c993522aab48a293d488bdcc2e432863d50f-pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc`,
this is the folder that was created by CSI plugin. For convenience, the ASCII part of the volume name appears in
directory name.

The directory now should contain our `hello-world` file:
```shell
$ ls 8fb6c993522aab48a293d488bdcc2e432863d50f-pvc-382ebb8c-1f8c-4a06-b1e3-0d5e166ebacc/
hello-world
```
