# Deployment

## Prerequisites

Ensure the following prerequisites are met:

* The privileged mode must be allowed on the Kubernetes cluster.
* The following Kubernetes feature gates must be enabled: DevicePlugins, CSINodeInfo, CSIDriverRegistry, and ExpandCSIVolumes (all these gates are enabled by default).
* A WEKA cluster is installed.
* For snapshot and directory backing, a filesystem must be pre-defined on the WEKA cluster.
* For filesystem backing, a filesystem group must be pre-defined on the WEKA cluster.
* Your workstation has a valid connection to the Kubernetes worker nodes.
* The WEKA CSI container must run as the root user to enable the management of WEKA filesystem mounts at the Worker Node level.

### Prerequisites for using WekaFS transport

The WEKA client must be installed on the Kubernetes worker nodes. Follow these guidelines:

* It is recommended to use a WEKA persistent client (a client that is part of the cluster) rather than a stateless client. See [Add clients to an on-premises WEKA cluster](https://docs.weka.io/planning-and-installation/bare-metal/adding-clients-bare-metal).
* If the Kubernetes worker nodes are running on WEKA cluster backends (converged mode), ensure the WEKA processes are running before the `kubelet` process starts.

### **Prerequisites for Using NFS transport**

* The WEKA cluster must be installed and properly configured.
* The NFS protocol must be enabled on the WEKA cluster.
* An NFS interface group must be created on the WEKA cluster with at least one floating IP address. For optimal performance and load sharing, it's recommended to assign at least one IP address per protocol node in the cluster.
* NFS interface group IP addresses must be accessible from the Kubernetes cluster nodes.

### Guidelines for NFS Interface Groups and configuration

* **Setting the NFS Interface Group**\
  When defining multiple NFS interface groups on WEKA clusters, set the `pluginConfig.mountProtocol.interfaceGroupName` parameter in the `values.yaml` file to specify the desired NFS interface group name. Failure to do so will result in the use of an arbitrary NFS interface group, which may lead to performance or networking issues.
* **Configuring the NFS Client Group**\
  The WEKA CSI Plugin automatically creates an NFS Client group named `WekaCSIPluginClients`. During volume creation or publishing, the Kubernetes node IP address is added to this group. For larger deployments, instead of adding node IP addresses individually (which is more secure), consider defining a CIDR network range for the NFS Client group in the WEKA cluster. Use the `pluginConfig.mountProtocol.nfsClientGroupName` parameter in the `values.yaml` file to specify this group.
* **Manual IP Address Specification**\
  In cloud or on-premises deployments where virtual IP addresses cannot be assigned to the interface group, the WEKA CSI Plugin can still be used with NFS transport. In this case, manually specify the IP addresses of the NFS protocol nodes as NFS target IP addresses (using API secret). Note that automatic failover for NFS connections will not be available, and the failure of a protocol node may disrupt NFS connections.

## Installation

### Install CSI Snapshot Controller and Snapshot CRDs

To enable Kubernetes-controlled snapshots, install the CSI Snapshot Controller and the CSI external-snapshotter CRD manifests.
**Note**: The CSI external-snapshotter is a sidecar container in Kubernetes. It monitors for VolumeSnapshot and VolumeSnapshotContent objects and triggers snapshot operations against a CSI endpoint. It’s part of the Kubernetes’ CSI implementation.

**Note**: On RedHat OpenShift Container Platform (OCP) those definitions might be preinstalled.

1. On the workstation you manage Kubernetes from, clone the CSI external-snapshotter from its GitHub repository.

```
git clone https://github.com/kubernetes-csi/external-snapshotter  
```

2. Switch to the directory created by the Git clone.

```
cd external-snapshotter 
```

3. Create and deploy the proper Custom Resource Definitions for the CSI external-snapshotter.

```
kubectl -n kube-system kustomize deploy/kubernetes/snapshot-controller | kubectl create -f -
kubectl kustomize client/config/crd | kubectl create -f -
```

### WEKAFS CSI

1. On the workstation you manage Kubernetes from, add the `csi-wekafs` repository:

```
helm repo add csi-wekafs https://weka.github.io/csi-wekafs

```

2. Install the WEKA CSI Plugin. Run the following command:

```
helm install csi-wekafs csi-wekafs/csi-wekafsplugin --namespace csi-wekafs --create-namespace

```

**Note**: If you need SELinux support, see the [SELinux](../selinux/README.md) section.

<details>

<summary>Installation output example</summary>

Once the installation completes successfully, the following output is displayed:

```
NAME: csi-wekafs
LAST DEPLOYED: Mon May 29 08:36:19 2023
NAMESPACE: csi-wekafs
STATUS: deployed
REVISION: 1
TEST SUITE: None
NOTES:
Thank you for installing csi-wekafs.

Your release is named csi-wekafs.
The release is installed in namespace csi-wekafs

To learn more about the release, try:

  $ helm status -n csi-wekafs csi-wekafs
  $ helm get all -n csi-wekafs csi-wekafs

To configure a storage class and start using the driver, see the [Examples](../examples/) directory.

-------------------------------------------------- NOTICE --------------------------------------------------
| THIS VERSION INTRODUCES SUPPORT FOR ADDITIONAL VOLUME TYPES, AS WELL AS SNAPSHOT AND VOLUME CLONING CAPS |
| TO BETTER UNDERSTAND DIFFERENT TYPES OF VOLUMES AND THEIR IMPLICATIONS, REFER TO THE DOCUMENTATION ABOVE |
| ALSO, IT IS RECOMMENDED TO CAREFULLY GO OVER NEW CONFIGURATION PARAMETERS AND ITS MEANINGS, AS BEHAVIOR  |
| OF THE PLUGIN AND ITS REPORTED CAPABILITIES LARGELY DEPEND ON THE CONFIGURATION AND WEKA CLUSTER VERSION |
------------------------------------------------------------------------------------------------------------

-------------------------------------------------- WARNING -------------------------------------------------
|  SUPPORT OF LEGACY VOLUMES WITHOUT API BINDING WILL BE REMOVED IN NEXT MAJOR RELEASE OF WEKA CSI PLUGIN. |
|  NEW FEATURES RELY ON API CONNECTIVITY TO WEKA CLUSTER AND WILL NOT BE SUPPORTED ON API-UNBOUND VOLUMES. |
|  PLEASE MAKE SURE TO MIGRATE ALL EXISTING VOLUMES TO API-BASED SCHEME PRIOR TO NEXT VERSION UPGRADE.     |
------------------------------------------------------------------------------------------------------------

```

</details>

## Upgrade from any previous version to WEKA CSI Plugin v2.0

In WEKA CSI Plugin v2.0, the CSIDriver object has undergone changes. Specifically, CSIDriver objects are now immutable. Consequently, the upgrade process involves uninstalling the previous CSI version using Helm and subsequently installing the new version. It is important to note that the uninstall operation does not delete any existing secrets, StorageClasses, or PVC configurations.

**Warning**: If you plan to upgrade the existing WEKA CSI Plugin and enable directory quota enforcement for already existing volumes, bind the legacy volumes to a single secret. See the [Bind legacy volumes to API](../migration/upgrade-legacy-pv.md) section.

#### 1. Prepare for the upgrade

1. Uninstall the existing CSI Plugin. Run the following command line:

```
helm uninstall csi-wekafs --namespace csi-wekafs
```

2. Update the `csi-wekafs` helm repository. Run the following command line:

```
helm repo update csi-wekafs

```

3. Download the `csi-wekafs` git repository.

```
git clone https://github.com/weka/csi-wekafs.git --branch main --single-branch
```

#### 2. Install the upgraded helm release

Run the following command line:

```
helm install csi-wekafs --namespace csi-wekafs csi-wekafs/csi-wekafsplugin

```

## Upgrade from WEKA CSI Plugin WEKA 2.0 forward

If the WEKA CSI plugin source is v2.0 or higher, follow this workflow.

#### 1. Update helm repo

Run the following command line:

```
helm repo update csi-wekafs
```

#### 2. Update the helm deployment

Run the following command line:

```
helm upgrade --install csi-wekafs --namespace csi-wekafs csi-wekafs/csi-wekafsplugin
```

<details>

<summary>Output example</summary>

Once the upgrade completes successfully, the following output is displayed:

```
Release "csi-wekafs" has been upgraded. Happy Helming!
NAME: csi-wekafs
LAST DEPLOYED: Tue June  2 15:39:01 2023
NAMESPACE: csi-wekafs
STATUS: deployed
REVISION: 10
TEST SUITE: None
NOTES:
Thank you for installing csi-wekafsplugin.

Your release is named csi-wekafs.

To learn more about the release, try:

  $ helm status csi-wekafs
  $ helm get all csi-wekafs

```

</details>

#### 3. Elevate the OpenShift privileges

If the Kubernetes worker nodes run on RHEL and use OpenShift, elevate the OpenShift privileges for the WEKA CSI Plugin. (RHCoreOS on Kubernetes worker nodes supports NFS connections.)

To elevate the OpenShift privileges, run the following command lines:

```
oc create namespace csi-wekafs
oc adm policy add-scc-to-user privileged system:serviceaccount:csi-wekafs:csi-wekafs-node
oc adm policy add-scc-to-user privileged system:serviceaccount:csi-wekafs:csi-wekafs-controller

```

#### 4. Delete the CSI Plugin pods

The CSI Plugin fetches the WEKA filesystem cluster capabilities during the first login to the API endpoint and caches it throughout the login refresh token validity period to improve the efficiency and performance of the plugin.

However, the WEKA filesystem cluster upgrade might be unnoticed if performed during this time window, continuing to provision new volumes in the legacy mode.

To expedite the update of the Weka cluster capabilities, it is recommended to delete all the CSI Plugin pods to invalidate the cache. The pods are then restarted.

Run the following command lines:

```
kubectl delete pod -n csi-wekafs -lapp=csi-wekafs-controller
kubectl delete pod -n csi-wekafs -lapp=csi-wekafs-node
```

[^1]: The **CSI external-snapshotter** is a sidecar container in Kubernetes. It monitors for `VolumeSnapshot` and `VolumeSnapshotContent` objects and triggers snapshot operations against a CSI endpoint. It’s part of the Kubernetes’ CSI implementation.
