# Weka CSI Plugin with NFS transport

## Overview
Although using native WekaFS driver as the underlying storage connectivity layer is recommended way to use WekaFS with Kubernetes, 
it is also possible to use the Weka CSI Plugin over NFS transport. 
This allows you to use WekaFS as a storage backend for your Kubernetes cluster without the need to install the Weka client on each Kubernetes node.

### Benefits of using Weka CSI Plugin with NFS transport
- **Simplified deployment**: No need to install the Weka client on each Kubernetes node
- **Interoperability**: Use Weka CSI Plugin on nodes where the Weka client is not yet installed, or is not currently supported
- **Flexibility**: Use Weka CSI Plugin with NFS transport for specific use-cases, while using the native WekaFS driver for other use-cases
- **Performance**: Pods are mounted across multiple IPs on the same NFS interface group, maximizing performance and simplifying management
- **Ease of migration**: Use Weka CSI Plugin with NFS transport as a stepping stone to migrate to the native WekaFS driver.  
    After deployment of the Weka client on all nodes, you can switch to the native WekaFS driver without changing the storage configuration 
    by simply rebooting the node.

### Limitations and Constraints
- **Performance**: NFS transport is not as performant as the native WekaFS driver and it is not recommended for high-performance workloads
- **Feature Parity**: Some features and capabilities of the native WekaFS driver are not available when using the Weka CSI Plugin with NFS transport
- **Complexity**: NFS transport requires additional configuration on the Weka cluster, and may require additional networking configuration on the Kubernetes cluster
- **Interoperability**: Same Kubernetes node cannot use both NFS and WekaFS transport at the same time
- **Migration**: Migrating from NFS transport to WekaFS transport requires rebooting the Kubernetes nodes (after Weka client deployment)
- **Network Configuration**: NFS interface group IP addresses must be accessible from the Kubernetes cluster nodes
- **Security**: NFS transport is less secure than the native WekaFS driver, and may require additional security considerations
- **QoS**: QoS is not supported for NFS transport

### Host Network Mode
Weka CSI Plugin will automatically install in `hostNetwork` mode when using NFS transport. 
Since hostNetwork mode is required for NFS transport, the `hostNetwork` parameter in the `values.yaml` file is ignored in such case.

### Security Considerations
- The Weka CSI Plugin with NFS transport uses NFSv4.1 protocol to connect to the Weka cluster.
- Support for Kerberos authentication is not available in this version of Weka CSI Plugin.
- It is recommended to use NFS transport only in secure and trusted networks.

## Interoperability with WekaFS driver
The Weka CSI Plugin with NFS transport is fully interoperable with the native WekaFS driver.

This means that you can use both WekaFS transport and NFS in the same Kubernetes cluster, 
and even for publishing the same volume to different pods using different transport layers (from different nodes).  
However, only one transport layer can be used on a single node at a time.

### Mount options
Mount options for the NFS transport are set automatically by the Weka CSI Plugin. When custom mount options are used in storage class,
the Weka CSI Plugin will translate them to NFS alternatives. Unknown or unsupported mount options will be ignored.

### QoS and Performance
QoS is not supported for NFS transport. Performance is limited by the NFS protocol and the network configuration.

### Scheduling Workloads by Transport Protocol
Weka CSI Plugin node server populates the transport protocol in the node topology.
This allows you to use node affinity rules to force workloads to use WekaFS transport on nodes where the Weka client is installed.
For example, in hybrid deployments where some nodes have the Weka client installed and some do not, 
the administrator would prefer storage-intensive workloads to run on nodes with the Weka client installed, while more generic workloads can run on other nodes.

In order to force a workload to a certain transport type, a nodeSelector can be applied to the workload manifest.
#### An example of a nodeSelector for WekaFS transport:
```yaml
nodeSelector:
  topology.weka.com/transport: wekafs
```
#### An example of a nodeSelector for NFS transport:
```yaml
nodeSelector:
  topology.weka.com/transport: nfs
```

### Switching from NFS to WekaFS transport
To switch between NFS and WekaFS transport, you need to:
1. Install the Weka client on Kubernetes node
2. Reboot the Kubernetes node

After the node is rebooted, the Weka CSI Plugin will automatically switch to using the WekaFS transport.
Existing volumes can be reattached to the pods without any changes.

## Prerequisites
Those are the minimum prerequisites for using Weka CSI Plugin with NFS transport:

- Weka cluster must be installed and configured
- NFS protocol must be configured on the Weka cluster
- NFS interface group must be created on the Weka cluster
- NFS interface group IP addresses must be accessible from the Kubernetes cluster nodes

> **WARNING:** When multiple NFS interface groups are defined on Weka clusters, 
> the `pluginConfig.mountProtocol.interfaceGroupName` parameter must be set to the desired NFS interface group name in the `values.yaml` file.  
> If the parameter is not set, an arbitrary NFS interface group will be used, that could potentially cause performance or networking issues.

> **NOTE**: NFS Client group called `WekaCSIPluginClients` is created automatically by the Weka CSI Plugin. 
> Then, upon each volume creation or publishing, the Kubernetes node IP address is added to the NFS Client group automatically.
> 
> Although, adding the node IP addresses one by one is the most secure way to configure the NFS Client group, this could become cumbersome in large deployments.
> In such case, using a network range (CIDR) is recommended.
> You may predefine the NFS Client group with a network range (CIDR) in the Weka cluster, and then use the `pluginConfig.mountProtocol.nfsClientGroupName` 
> parameter in the `values.yaml` file to specify the NFS Client group name.

## Way of Operation
The Weka CSI Plugin with NFS transport operates in the following way:
Upon start of the Weka CSI Plugin, the plugin will:
1. Check if the Weka client is installed on the Kubernetes node
2. If client is not set up, the plugin will check whether NFS failback is enabled
3. If NFS failback is enabled, the plugin will use NFS transport for volume provisioning and publishing
4. If NFS failback is disabled, the plugin will not start and will log an error message.  
   Refer to the [Installation](#installation) section for enabling NFS failback.

Once NFS mode is enabled, the Weka CSI Plugin will use NFS transport for all volume operations. 
In such case, upon any volume create or publish request, the Weka CSI Plugin will:
1. Connect to Weka cluster API and fetch interface groups (and their IP addresses)
   If interface group name is specified in the `values.yaml` file, 
   the plugin will use the specified interface group, otherwise an arbitraty interface group will be used. 
2. Ensure that Client Group is created on the Weka cluster. 
   If the Client Group is not created, the plugin will create it.  
   > **NOTE:** If client group name is specified in the `values.yaml` file, the plugin will use the specified client group name, 
   > otherwise `WekaCSIPluginClients` client group will be used.
3. Determine the node IP address facing the inteface group IP addresses. This will be done by checking the network configuration of the node
   Then, the Weka CSI plugin will issue a UDP connection towards one of the IP addresses of the interface group, 
   The source IP address of the connection will be determined by the plugin and will be used as the `node IP address`.
4. Ensure that the `node IP address` is added to the Client Group. 
   If the node IP address is not added, the plugin will add it to the Client Group.
   If client group already has the node IP address (or it has a matching CIDR definition), the plugin will skip this step.
   > **EXAMPLE:** If the node IP address is `192.168.100.1` and the client group is defined with a network range `192.168.100.0/255.255.255.0`, 
   > node IP address will not be added
5. Identify the filesystem name to be mounted, either from StorageClass parameters (provisioning), 
   or from Volume Handle (for publishing an existing volume).
6. Ensure that NFS permission exists for the Client Group to access the filesystem.
   If the permission is not set, the plugin will set it. If the permission is already set, the plugin will skip this step.
7. Pick up a random IP address from the selected NFS interface group. 
   This IP address will be used for mounting the filesystem.
8. Perform NFS mount operation on the Kubernetes node using the selected IP address and the filesystem name.
9. Rest of the operations will be performed in a similar way as with the native WekaFS driver.

## NFS Permissions Required for Weka CSI Plugin
The Weka CSI Plugin requires AND will set the following NFS permissions on the Weka cluster:
1. **Client Group**: `WekaCSIPluginClients` (or custom client group name if set in the `values.yaml` file)
2. **Filesystem**: The filesystem name to be mounted
3. **Path**: `/` (root of the filesystem)
4. **Type**: `RW`
5. **Priority**: No priority set
6. **Supported Versions**: `V4`
7. **User Squash**: `None`
8. **Authentication Types**: `NONE`, `SYS`

> **WARNING:** Weka NFS servers will evaluate permissions based on the order of the permissions list.  
> If multiple permissions matching the IP address of the Kubernetes node and the filesystem are set, a conflict might occur.  
> Hence, it is **highly recommended** not creating additional permissions for the same filesystem 
> Also, if multiple client groups are used, it is highly recommended to make sure that IP addresses are not overlapping between client groups. 

## WEKA Cluster Preparation
Before using the Weka CSI Plugin with NFS transport, the Weka cluster must be prepared for NFS access.
This includes configuring the NFS protocol on the Weka cluster, creating an NFS interface group, and configuring at least 1 Group IP address

Alternatively, in cloud deployments where setting a Group IP address is not possible, the Weka server IP addresses can be used instead.
In such case, the IP addresses may be set via the API secret and will be used instead of the Group IP addresses.

This can be set up by providing `nfsTargetIps` parameter in the API secret. Refer to the [API secret example](../examples/common/csi-wekafs-api-secret.yaml) for more information.
> **WARNING:** Using an NFS load balancer that forwards NFS connection to multiple Weka servers is not supported at this moment.

## Installation
By default, Weka CSI Plugin components will not start unless Weka driver is not detected on Kubernetes node.
This is to prevent a potential misconfiguration where volumes are attempted to be provisioned or published on node while no Weka client is installed.

To enable NFS transport, Weka CSI plugin must be explicitly configured for using NFS failback.
This is done by setting the `pluginConfig.mountProtocol.allowNfsFailback` parameter to `true` in the `values.yaml` file.

The parameter `pluginConfig.mountProtocol.useNfs` enforces the use of NFS transport even if Weka client is installed on the node, 
and recommended to be set to `true` ONLY for testing.

Follow the [Helm installation instructions](./charts/csi-wekafsplugin/README.md) to install the Weka CSI Plugin. 
Most of the installation steps are the same as for the native WekaFS driver, however, additional parameters should be set in the `values.yaml` file,
or passed as command line arguments to the `helm install` command.

This is the example Helm install command for using NFS transport:
```console
helm upgrade csi-wekafs -n csi-wekafs --create-namespace --install csi-wekafs/csi-wekafsplugin csi-wekafs\
--set logLevel=6 \
--set pluginConfig.mountProtocol.alloeNfsFailback=true \
--set pluginConfig.allowInsecureHttps=true \
[ --set pluginConfig.mountProtocol.interfaceGroupName=MyIntefaceGroup \ ]  # optional, recommended if multiple interface groups are defined
[ --set pluginConfig.mountProtocol.clientGroupName=MyClientGroup \ ]       # optional, recommended if client group is predefined
```
