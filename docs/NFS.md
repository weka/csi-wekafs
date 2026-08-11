# WEKA CSI Plugin with NFS transport

## Overview
While using the native WekaFS driver as the storage connectivity layer is the recommended approach for integrating WekaFS with Kubernetes, version 2.5.0 introduces the option to utilize the WEKA CSI Plugin over NFS transport. This allows you to employ WekaFS as a storage backend for your Kubernetes cluster without requiring the installation of the WEKA client on every Kubernetes node.

### Benefits of using WEKA CSI Plugin with NFS transport

* **Simplified deployment**: Eliminate the need to install the WEKA client on each Kubernetes node.
* **Interoperability**: Use the WEKA CSI Plugin on nodes where the WEKA client is not yet installed or unsupported.
* **Flexibility**: For specific use cases, leverage the WEKA CSI Plugin with NFS transport while continuing to use the native WekaFS driver for others.
* **Performance**: Mount pods across multiple IPs within the same NFS interface group, maximizing performance and simplifying management.
* **Ease of migration**: Use the WEKA CSI Plugin with NFS transport as an interim solution while transitioning to the native WekaFS driver. Once the WEKA client is deployed on all nodes, you can switch to the native WekaFS driver without altering the storage configuration—reboot the node.

### Limitations and constraints

>**Warning**:
>As of version 2.5.0 and until further notice, publishing snapshot-backed volumes via NFS transport is not recommended. This is an open issue currently under investigation.

* **Feature parity**: Certain features and capabilities available with the native WekaFS driver may be absent when using the WEKA CSI Plugin with NFS transport.
* **Complexity**: NFS transport necessitates additional configuration on the WEKA cluster and may require further networking setup on the Kubernetes cluster.
* **Interoperability**: A single Kubernetes node cannot simultaneously use NFS and WekaFS transport.
* **Migration**: Transitioning from NFS transport to WekaFS transport requires rebooting the Kubernetes nodes after deploying the WEKA client.
* **Network configuration**: NFS interface group IP addresses must be accessible from the Kubernetes cluster nodes.
* **Security**: NFS transport is generally less secure than the native WekaFS driver and may necessitate additional security measures.
* **Quality of Service (QoS)**: QoS is not supported with NFS transport.

### Host Network mode

The WEKA CSI Plugin installs automatically in `hostNetwork` mode when using NFS transport. `hostNetwork` mode is required for NFS transport, so the `hostNetwork` parameter in the `values.yaml` file is ignored.

### Security considerations

The WEKA CSI Plugin with NFS transport uses NFSv4.1 (default) or NFSv3 protocols to connect to the WEKA cluster. However, support for Kerberos authentication is not available in this version of the WEKA CSI Plugin. Therefore, using NFS transport only in secure and trusted networks is recommended.

### Interoperability with WekaFS driver

The WEKA CSI Plugin with NFS transport is fully interoperable with the native WekaFS driver. This allows you to use WekaFS transport and NFS within the same Kubernetes cluster, including publishing the same volume to different pods using different transport layers from different nodes. However, only one transport layer can be used on a single node at any time.

#### Mount options

The WEKA CSI Plugin automatically sets mount options for NFS transport. When custom mount options are specified in the storage class, the WEKA CSI Plugin translates them into NFS alternatives.
>**Note**:
>To customize mount behavior, always specify options in the storage class. The WEKA CSI Plugin ensures compatibility by translating these options to NFS-supported alternatives.
>Directly setting NFS mount options is not supported. Any unknown or unsupported mount options are ignored.

#### QoS and performance

NFS transport does not support quality of Service (QoS). The NFS protocol and the network configuration constrain performance.

#### Scheduling workloads by transport protocol

The WEKA CSI Plugin node server populates the transport protocol in the node topology. This feature enables node affinity rules to direct workloads to use WekaFS transport on nodes where the WEKA client is installed. For example, in hybrid deployments where some nodes have the WEKA client installed while others do not, administrators may prefer to run storage-intensive workloads on nodes with the WEKA client, allowing more generic workloads to operate on other nodes.

A `nodeSelector` can be applied in the workload manifest to enforce a specific transport type for a workload.

**Example: a nodeSelector for WekaFS transport**

```yaml
nodeSelector:
  topology.weka.com/transport: wekafs
```

**Example: a nodeSelector for NFS transport**

```yaml
nodeSelector:
  topology.weka.com/transport: nfs
```

#### Switching from NFS to WekaFS transport

To switch from NFS to WekaFS transport, follow these steps:

1. Install the WEKA client on the Kubernetes node.
2. Reboot the Kubernetes node.

After rebooting, the WEKA CSI Plugin automatically switches to WekaFS transport. Existing volumes can be reattached to the pods without any changes.

## Minimum prerequisites for using WEKA CSI Plugin with NFS transport

* **WEKA cluster installation**: The WEKA cluster must be installed and configured.
* **NFS protocol configuration**: The NFS protocol must be configured on the WEKA cluster.
* **NFS interface group creation**: An NFS interface group must be created on the WEKA cluster.
* **Accessibility of IP addresses**: NFS interface group IP addresses must be accessible from the Kubernetes cluster nodes.

Adhere to the following considerations:

* **Interface group name configuration**: If multiple NFS interface groups are defined, set the `pluginConfig.mountProtocol.interfaceGroupName` parameter to the desired NFS interface group name in the `values.yaml` file. An arbitrary NFS interface group is used if this parameter is not set, which may lead to performance or networking issues.
* **NFS client group**: The plugin automatically creates an NFS client group called `WekaCSIPluginClients`. The Kubernetes node IP address is added to this group during each volume creation or publishing.
* **IP address configuration**: While adding node IP addresses one by one is the most secure method for configuring the NFS client group, it can be cumbersome for large deployments. In such cases, a network range (CIDR) is recommended. Predefine the NFS client group with a network range in the WEKA cluster, then specify the NFS client group name using the `pluginConfig.mountProtocol.nfsClientGroupName` parameter in the `values.yaml` file.

## WEKA CSI Plugin operation over NFS transport

Upon starting, the WEKA CSI Plugin performs the following steps:

1. **Check WEKA client installation**: Verifies if the WEKA client is installed on the Kubernetes node.
2. **NFS failback check**:
   * If the WEKA client is not set up, the plugin checks if NFS failback is enabled or if NFS use is forced.
   * If NFS failback is enabled, the plugin uses NFS transport for volume provisioning and publishing.
   * If NFS failback is disabled, the plugin does not start and logs an error message. To enable NFS failback, see [Install the WEKA CSI Plugin with NFS transport](#install-the-weka-csi-plugin-with-nfs-transport).

The plugin uses NFS transport for all volume operations when NFS mode is enabled. For any volume creation or publishing request, the WEKA CSI plugin performs the following:

1. **Connect to WEKA Cluster API**: Fetch interface groups and their IP addresses. If an interface group name is specified in the `values.yaml` file, the plugin uses that; otherwise, it selects an arbitrary interface group.
2. **Client group verification**: Ensure the Client Group exists on the WEKA cluster. If it does not, the plugin creates it.
3. **Determine node IP address**: Identify the IP address facing the interface group IP addresses by checking the node's network configuration. The plugin issues a UDP connection to one of the interface group IP addresses, using the determined source IP address as the node IP address.
4. **Add node IP to client group**: Confirm the node IP address is included in the Client Group. If not, the plugin adds it. This step is skipped if the Client Group already contains the node IP address or a matching CIDR definition.
   * **Example**: For two nodes with IP addresses 192.168.100.1 and 192.168.200.1, if the Client Group has a rule for 192.168.100.0/255.255.255.0, no new rule is added for the first node. However, a new rule 192.168.200.1/255.255.255.255 is created for the second node.
5. **Identify filesystem**: Determine the filesystem name to be mounted from StorageClass parameters (during provisioning) or the Volume Handle (when publishing an existing volume).
6. **NFS permissions**: Ensure that NFS permissions are granted for the Client Group to access the filesystem. If permissions are not set, the plugin establishes them. If permissions are already in place, this step is skipped.
7. **Select random IP address**: Select a random IP address from the selected NFS interface group to mount the filesystem.
8. **NFS mount operation**: Perform the NFS mount operation on the Kubernetes node using the selected IP address and filesystem name.
9. **Subsequent operations**:
   * Execute remaining operations similarly to how they would be done with the native WekaFS driver.
   * If a client group name is specified in the `values.yaml` file, the plugin uses that name; otherwise, it defaults to the `WekaCSIPluginClients` client group.

## NFS permissions required for WEKA CSI Plugin

The WEKA CSI Plugin requires specific NFS permissions, which it automatically configures on the WEKA cluster. These permissions are as follows:

1. **Client Group**: `WekaCSIPluginClients` (or a custom client group name if specified in the `values.yaml` file)
2. **Filesystem**: The filesystem name to be mounted
3. **Path**: `/` (root directory of the filesystem)
4. **Type**: `RW`
5. **Priority**: No priority is set
6. **Supported Versions**: `V3, V4`
7. **User Squash**: `None`
8. **Authentication Types**: `NONE`, `SYS`

>**Warning**:
>WEKA NFS servers evaluate permissions based on the order in the permissions list.
>If multiple permissions match the IP address of the Kubernetes node and filesystem, a conflict may occur.
>Therefore, it is strongly recommended not to create additional permissions for the same filesystem.
>Additionally, if multiple client groups are used, ensure that the IP addresses of the groups do not overlap.

## WEKA cluster preparation

Before using the WEKA CSI Plugin with NFS transport, prepare the WEKA cluster for NFS access. This preparation involves:

* Configuring the NFS protocol on the WEKA cluster.
* Creating an NFS interface group.
* Configuring at least one Group IP address.

In cloud deployments where setting a Group IP address is impossible, you can use the WEKA server IP addresses instead. In this case, set the IP addresses through the API secret to replace the Group IP addresses. This configuration can be done by providing the `nfsTargetIps` parameter in the API secret. For more details, see [API secret example](../examples/common/csi-wekafs-api-secret.yaml).

>**Note**:
>Using an NFS load balancer that redirects NFS connections to multiple WEKA servers (also known as NFSv4 directory referrals) is not supported.

## Install the WEKA CSI Plugin with NFS transport

By default, the WEKA CSI Plugin components do not start if a WEKA driver is detected on a Kubernetes node. This prevents potential misconfigurations where volumes may be provisioned or published on a node without an installed WEKA client.

**Procedure**

1. **Configure NFS failback**: Explicitly configure the WEKA CSI Plugin to use NFS failback by setting the `pluginConfig.mountProtocol.allowNfsFailback` parameter to `true` in the `values.yaml` file.
2. **Set NFS transport enforcement (optional)**: If you want to enforce the use of NFS transport even when the WEKA client is installed on the node, set the `pluginConfig.mountProtocol.useNfs` parameter to `true`. This option is recommended for testing purposes only.
3. **Follow Helm installation instructions**: Follow the [Helm installation instructions](../charts/csi-wekafsplugin/README.md) to install the WEKA CSI Plugin. Most installation steps are similar to those for the native WekaFS driver.
4. **Set additional parameters**: You can set any additional parameters in the `values.yaml` file or pass them as command-line arguments to the Helm install command.
5. **Run the Helm install command**: Run the following example Helm install command for using NFS transport:

```bash
helm upgrade csi-wekafs -n csi-wekafs --create-namespace --install csi-wekafs/csi-wekafsplugin csi-wekafs \
--set logLevel=6 \
--set pluginConfig.mountProtocol.allowNfsFailback=true \
--set pluginConfig.allowInsecureHttps=true \
[ --set pluginConfig.mountProtocol.interfaceGroupName=MyInterfaceGroup \ ] 
# optional, recommended if multiple interface groups are defined
[ --set pluginConfig.mountProtocol.clientGroupName=MyClientGroup \ ]    
# optional, recommended if the client group is predefined
```
