# CSI WekaFS SELinux Support

## General Information
When installing Weka CSI plugin on SELinux-enabled Kubernetes cluster, pods might be denied access
to the persistent volumes provisioned on top of Weka filesystem.

The reason behind this is a lack of permissions for containers to access objects stored on Weka cluster.

In this directory you can find a custom policy that provides all the necessary security configuration to optionally 
enable pod access to WekaFS-based Persistent Volumes, and it should be applied 
on each Kubernetes worker node that is intended to service WekaFS-based persistent volumes.

The provided policy allows processes with `container_t` seclabel to access objects having `wekafs_t` label (which is set for all files and directories of mounted CSI volumes).

The policy comes both as a Type Enforcement file, and as a precompiled policy package.
In order to use Weka CSI Plugin with SELinux enforcement, the following steps must be performed:

## Custom SELinux Policy Installation
1. Distribute the SELinux policy package to all Kubernetes nodes, by using either one of those options:
   * Clone Weka CSI Plugin Github repository, by issuing
     ```shell
     git clone https://github.com/weka/csi-wekafs.git
     ```
   * Copy the content of `selinux` directory directly to Kubernetes nodes
2. Apply the policy package directly by issuing:
   ```shell
   $ semodule -i csi-wekafs.pp
   ```
   Check that the policy was applied correctly:
   ```shell
   $ getsebool -a | grep wekafs
   container_use_wekafs --> off
   ```
   If the output matches mentioned above, skip to step 4. Otherwise, proceed to step 3 to build the policy from sources.
3. In certain circumstances (e.g. different Kernel version or Linux distribution), 
   the pre-compiled policy installation could fail. In this case, the policy must be built
   and installed from source by following the procedure below.
   ```shell
   $ checkmodule -M -m -o csi-wekafs.mod csi-wekafs.te
   $ semodule_package -o csi-wekafs.pp -m csi-wekafs.mod
   $ make -f /usr/share/selinux/devel/Makefile csi-wekafs.pp
   $ semodule -i csi-wekafs.pp
   ```
   > **NOTE**: for this purpose, `policycoreutils-devel` package 
   > (or its alternative in case of Linux distribution different from RedHat family) is required

   Check that the policy was applied correctly:
   ```shell
   $ getsebool -a | grep wekafs
   container_use_wekafs --> off
   ```

4. The policy provides a boolean setting which allows on-demand enablement of relevant permissions.
   To enable WekaFS CSI volumes access from pods, perform the command
   ```shell
   $ setsebool container_use_wekafs=on
   ```
   To disable access, perform the command
   ```shell
   $ setsebool container_use_wekafs=off
   ```
   The configuration changes are applied immediately.

## CSI Plugin Installation and Configuration
1. Weka CSI Plugin must be installed in a SELinux-compatible mode to correctly label volumes.  
   This can be done by setting the `selinuxMount` value to `"true"`, either via editing values.yaml or by passing the parameter directly in Helm installation command, e.g.
   ```shell
   $ helm install --upgrade csi-wekafsplugin csi-wekafs/csi-wekafsplugin --namespace csi-wekafsplugin --create-namespace --set selinuxMount=true
   ```
2. In SELinux-compatible mode, 2 different `DaemonSet`s are created, while node affinity by label mechanism is used to bind a particular node to one set or another.  
   Hence, the following label must be set on each SELinux-enabled Kubernetes node to ensure the plugin start in compatibility mode:
   ```shell
   csi.weka.io/selinux_enabled="true"
   ```
   > **NOTE:** If another label stating SELinux support is already maintained on nodes, the expected label name may be changed by editing the `selinuxNodeLabel` parameter 
   > by either modifying it in `values.yaml` or by setting it directly during plugin installation, e.g.
   > ```shell
   > $ helm install --upgrade csi-wekafsplugin csi-wekafs/csi-wekafsplugin --namespace csi-wekafsplugin --create-namespace --set selinuxMount=true --set selinuxNodeLabel="selinux_enabled"
   > ```
   
## Checking Plugin Operation & Troubleshooting
 
1. Make sure you have configured a valid CSI API [`secret`](../examples/dynamic_api/csi-wekafs-api-secret.yaml),Create a valid Weka CSI Plugin [`storageClass`](../examples/dynamic_api)  
   > **NOTE**: If using an example `storageClass`, make sure to update endpoints and credentials prior to apply 
2. Provision a [`PersistentVolumeClaim`](../examples/dynamic_api/pvc-wekafs-dir-api.yaml)
3. Provision a [`DaemonSet`](../examples/dynamic_api/csi-daemonset.app-on-dir-api.yaml), in order to be able access of all pods on all nodes
4. Monitor the pod logs using a command below, nothing should be printed in log files:
   ```shell
   $ kubectl logs -f daemonset/csi-wekafs-test-api
   ```
   IF the command returns a repeating message like the one below, it seems that the node on which the relevant pod is running is misconfigured:
   ```shell
   /bin/sh: can't create /data/csi-wekafs-test-api-gldmk.txt: Permission denied
   ```
   
5. Obtain node name from the pod:
   ```shell
   $ kubectl get pod csi-wekafs-test-api-gldmk -o wide
   NAME                        READY   STATUS    RESTARTS   AGE   IP            NODE         NOMINATED NODE   READINESS GATES
   csi-wekafs-test-api-gldmk   1/1     Running   0          98m   10.244.15.2   don-kube-8   <none>           <none>
   ```

6. Connect to the relevant node and check if Weka CSI SELinux policy is installed and enabled
   ```shell
   $ getsebool -a | grep wekafs
   container_use_wekafs --> on
   ```
   * If the output matches example, proceed to next step. 
   * If no output, policy is not installed, proceed to [Custom SELinux Policy Installation](#Custom SELinux Policy Installation)
   * If the policy is off, enable it and check output of the pod again by issuing
     ```shell
     $ setsebool container_use_wekafs=on
     ```
7. Check if the node is labeled with plugin is operating in SELinux-compatible mode by issuing the following command:
   ```shell
   $ kubectl describe node don-kube-8 | grep csi.weka.io/selinux_enabled
                csi.weka.io/selinux_enabled=true
   ``` 
   * If the output is empty, proceed to [CSI Plugin Installation and Configuration](#CSI Plugin Installation and Configuration)
     > **NOTE:** If the label was missing and added by you during troubleshooting, the CSI node server component must be restarted on the node.  
     Perform the following command to terminate the relevant pod and another instance will start automatically:
     > ```shell
     > $ POD=$(kubectl get pod -n csi-wekafs -lcomponent=csi-wekafs-node -o wide | grep -w don-kube-8 | cut -d" " -f1)
     > $ kubectl delete pod -n csi-wekafs $POD
     >``` 
   * If the output matches example, proceed to next step
8. Collect CSI node server logs from the matching Kubernetes nodes and contact Weka Customer Success Team:
   ```shell
   $ POD=$(kubectl get pod -n csi-wekafs -lcomponent=csi-wekafs-node -o wide | grep -w don-kube-8 | cut -d" " -f1)
   $ kubectl logs -n csi-wekafs -c wekafs $POD > log.txt  
   ```