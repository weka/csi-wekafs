# CSI WekaFS SELinux Support

When installing Weka CSI plugin on SELinux-enabled Kubernetes cluster, pods might be denied access
to the persistent volumes provisioned on top of Weka filesystem.

The reason behind this is a lack of permissions for containers to access objects stored on Weka cluster.

In this directory you can find a custom policy that provides all the necessary security configuration to optionally 
enable pod access to WekaFS-based Persistent Volumes, and it should be applied 
on each Kubernetes worker node that is intended to service WekaFS-based persistent volumes.

The policy comes both as a Type Enforcement file, and as a precompiled policy package.
In order to use Weka CSI Plugin with SELinux enforcement, the following steps must be performed:

1. Distribute the SELinux policy package to all Kubernetes nodes, by using either one of those options:
   * Clone Weka CSI Plugin Github repository, by issuing
     ```shell
     git clone https://github.com/weka/csi-wekafs.git
     ```
   * Copy the content of `selinux` directory directly to Kubernetes nodes
2. Apply the policy package directly by issuing:
   ```shell
   semodule -i csi-wekafs.pp
   ```
3. In certain circumstances (e.g. different Kernel version or Linux distribution), 
   the pre-compiled policy installation could fail. In this case, the policy must be built
   and installed from source by following the procedure below.
   ```shell
   checkmodule -M -m -o csi-wekafs.mod csi-wekafs.te
   semodule_package -o csi-wekafs.pp -m csi-wekafs.mod
   make -f /usr/share/selinux/devel/Makefile csi-wekafs.pp
   semodule -i csi-wekafs.pp
   ```
   > **NOTE**: for this purpose, `policycoreutils-devel` package 
   > (or its alternative in case of Linux distribution different from RedHat family) is required 
4. The policy provides a boolean setting which allows on-demand enablement of relevant permissions.
   To enable WekaFS CSI volumes access from pods, perform the command
   ```shell
   setsebool container_use_wekafs=on
   ```
   To disable access, perform the command
   ```shell
   setsebool container_use_wekafs=off
   ```
   The configuration changes are applied immediately.
5. To enable support for SELinux on Weka CSI Plugin, install the plugin with `selinuxMount` option enabled, e.g.
   ```shell
   helm install --upgrade csi-wekafsplugin csi-wekafs/csi-wekafsplugin --namespace csi-wekafsplugin --create-namespace [--set selinuxMount="true"]
   ```

> **NOTE:** SELinux configuration is global per Helm release installation. 
> 
> Once SELinux support is enabled on Weka CSI Plugin, mounting of volumes on
> nodes with SELinux disabled, or in case the SELinux policy is not applied, will fail. 