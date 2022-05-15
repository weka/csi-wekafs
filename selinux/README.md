# CSI WekaFS SELinux Support

When installing Weka CSI plugin on SELinux-enabled Kubernetes cluster, pods might be denied access
to the persistent volumes provisioned on top of Weka filesystem.

The reason behind this is a mismatch between container service label and filesystem object labels.
The situation is especially correct for pods without `SecurityContext` configuration.

In order to resolve this situation, a custom SELinux policy must be applied on each Kubernetes node intended to host 
pods which intend to utilize Weka CSI persistent volumes.

The policy comes both as a source file and as a precompiled policy package.
The policy may be applied directly on a supporting OS (depends on Linux distribution and Kernel version)

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
3. If from some reason policy installation fails (e.g. due to different Kernel version):
   ```shell
   checkmodule -M -m -o csi-wekafs.mod csi-wekafs.te
   semodule_package -o csi-wekafs.pp -m csi-wekafs.mod
   make -f /usr/share/selinux/devel/Makefile csi-wekafs.pp
   semodule -i csi-wekafs.pp
   ```
   > Note: for this purpose, `policycoreutils-devel` package is required 
