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

```shell script
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
csi-wekafsplugin-controller-0  5/5     Running   0          1m10s
csi-wekafsplugin-node-6gk86    3/3     Running   0          1m11s
csi-wekafsplugin-node-sfmgd    3/3     Running   0          1m10s
```
> **Note**: 
> 1. the number of running `csi-wekafsplugin-node-*` pods should be equal to number of K8s nodes 
which are not tainted from `daemonset.apps` scheduling (e.g. in default configuration it would be all nodes except master)
> 1. the number of running `csi-wekafsplugin-controller-*` should always be 1

## Deployment Validation
Next, validate the deployment.  First, ensure all expected pods are running properly:

```shell script
$ kubectl get pods --namespace csi-wekafsplugin
NAME                           READY   STATUS    RESTARTS   AGE
csi-wekafsplugin-controller-0  5/5     Running   0          2m54s
csi-wekafsplugin-node-6gk86    3/3     Running   0          2m54s
csi-wekafsplugin-node-sfmgd    3/3     Running   0          2m54s
```