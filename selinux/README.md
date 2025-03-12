# Add SELinux support

## Overview
Security-Enhanced Linux (SELinux) is a Linux kernel security module designed to enforce access control security policies, including Mandatory Access Controls (MAC).

This section applies exclusively to SELinux-enabled deployments that are not based on the Red Hat OpenShift Container Platform (OCP). In OCP environments, all required configurations are automatically handled.

The purpose of this section is to address permission issues that prevent containers from accessing objects stored on the WEKA cluster.

To add SELinux support, perform the following procedures:

1. Install a custom SELinux policy.
2. Install and configure the WEKA CSI Plugin.
3. Test the WEKA CSI Plugin operation.

### Install a custom SELinux policy
The policy comes both as a Type Enforcement file (`csi-wekafs.te`), and as a Common Intermediate Language (`csi-wekafs.cil`) file, which can be installed on the system using `semodule` command.

1. Distribute the SELinux policy package to all Kubernetes nodes, by using either one of those options:
    *   Clone WEKA CSI Plugin Github repository:

        ```shell
        git clone https://github.com/weka/csi-wekafs.git
        ```
    * Copy the content of the `selinux` directory directly to Kubernetes nodes
2. Apply the policy package directly:
    ```
    $ cd csi-wekafs/selinux
    $ semodule -i csi-wekafs.pp
    ```

   Verify that the policy is applied correctly:

   ```shell
   $ semodule -l | grep wekafs
   csi-wekafs
   ```
   If the output matches mentioned above, skip to step 4. Otherwise, proceed to step 3 to build the policy from the sources.
3.  In certain circumstances, the pre-compiled policy installation could fail. For example, in a different Kernel version or Linux distribution. In this case, build the policy and install it from the source using the following steps:

   ```shell
   checkmodule -M -m -o csi-wekafs.mod csi-wekafs.te
   semodule_package -o csi-wekafs.pp -m csi-wekafs.mod
   semodule -i csi-wekafs.pp
   ```
   For this purpose, the `policycoreutils-devel` package (or its alternative in case of Linux distribution different from the Red Hat family) is required.

   Verify that the policy is applied correctly:

    ```shell
    $ semodule -l | grep wekafs
    csi-wekafs
    ```

### Install and configure the WEKA CSI Plugin

1. To label volumes correctly, install the WEKA CSI Plugin in an SELinux-compatible mode. To do that, set the `selinuxSupport` value to `"enforced"` or `"mixed”` by editing the file `values.yaml` or passing the parameter directly in the `helm` installation command.

Example:

```shell
$ helm install --upgrade csi-wekafsplugin csi-wekafs/csi-wekafsplugin --namespace csi-wekafsplugin --create-namespace --set selinuxSupport=enforced
```
Follow these considerations:

* WEKA CSI Plugin supports both the `enforced` and `mixed` modes of `selinuxSupport`. The installation depends on the following mode settings:
    * When `selinuxSupport` is `enforced`, only SELinux-enabled CSI plugin node components are installed.
    * When `selinuxSupport` is `mixed`, both non-SELinux and SELinux-enabled components are installed.
    * When `selinuxSupport` is `off`, only non-SELinux CSI plugin node components are installed.
*   The SELinux status cannot be known from within the CSI plugin pod. Therefore, a way of distinguishing between SELinux-enabled and non-SELinux nodes is required. WEKA CSI Plugin relies on the node affinity mechanism by matching the value of a certain node label in a mutually exclusive way. Only when the label exists and is set to true, an SELinux-enabled node component starts on that node. Otherwise, the non-SELinux node component starts.

To ensure that the plugin starts in compatibility mode, set the following label on each SELinux-enabled Kubernetes node:
* If a node label is modified after installing the WEKA CSI Plugin node component on that node, terminate the csi-wekafs-node-XXXX component on the affected node. As a result, a replacement pod is automatically scheduled on the node but with the correct SELinux configuration.

   > **NOTE:** Since SELinux status cannot be known from within CSI plugin pod, 
   > a certain way of distinguishing between SELinux-enabled and non-SELinux nodes needs to be established.
   > Binding of relevant CSI node component to node is mutually exclusive and relies on node affinity mechanism by matching host labels.  
   Hence, the following label must be set on each SELinux-enabled Kubernetes node to ensure the plugin start in compatibility mode:
   ```shell
   csi.weka.io/selinux_enabled="true"
   ```
*   If another label stating SELinux support is already maintained on nodes, you can modify the expected label name in the `selinuxNodeLabel` parameter by editing the file `values.yaml` or by setting it directly during the WEKA CSI Plugin installation.

    > **NOTE:** If node label was modified after Weka CSI Plugin node component has already 
    > deployed on that node, terminate the csi-wekafs-node-XXXX component on the affected node,
    > a replacement pod will be scheduled on the node automatically, but with correct SELinux configuration.

### Test the WEKA CSI plugin operation
1. Make sure you have configured a valid CSI API [`secret`](../examples/common/csi-wekafs-api-secret.yaml). Create a valid WEKA CSI Plugin [`storageClass`](../examples/dynamic/directory/storageclass-wekafs-dir-api.yaml).
2. Provision a [`PersistentVolumeClaim`](../examples/dynamic/directory/pvc-wekafs-dir-api.yaml).
3. Provision a [`DaemonSet`](../examples/dynamic/directory/csi-daemonset.app-on-dir-api.yaml) to enable access to all pods on all nodes.
4.  Monitor the pod logs using the following command (expect no printing in the log files):

    ```
    $ kubectl logs -f -lapp=csi-daemonset-app-on-dir-api
    ```

    If the command returns a repeating message like the following one, it is most likely that the node on which the relevant pod is running is misconfigured:

    ```
    /bin/sh: can't create /data/csi-wekafs-test-api-gldmk.txt: Permission denied
    ```
5.  Obtain the node name from the pod:

    ```
    $ kubectl get pod csi-wekafs-test-api-gldmk -o wide
    NAME                        READY   STATUS    RESTARTS   AGE   IP            NODE         NOMINATED NODE   READINESS GATES
    csi-wekafs-test-api-gldmk   1/1     Running   0          98m   10.244.15.2   don-kube-8   <none>           <none>
    ```
6.  Connect to the relevant node and check if the WEKA CSI SELinux policy is installed and enabled:

    ```shell
    $ semodule -l | grep wekafs
    csi-wekafs
    ```

    * If the result matches the example, proceed to the next step.
    * If there is no result, the policy is not installed. Perform the **Install a custom SELinux policy** procedure avove.
    *   If the policy is off, enable it and check the pod output again by running:

        ```
        $ setsebool container_use_wekafs=on
        ```
7.  Check if the node is labeled with the plugin is operating in SELinux-compatible mode by running the following command:

    ```
    $ kubectl describe node don-kube-8 | grep csi.weka.io/selinux_enabled
                 csi.weka.io/selinux_enabled=true
    ```

    * If the output is empty, Perform the **Install and configure the Weka CSI Plugin** procedure.

      If the label is missing and added by you during troubleshooting, the CSI node server component must be restarted on the node.\
      Perform the following command to terminate the relevant pod, and another instance will start automatically:

      ```
      $ POD=$(kubectl get pod -n csi-wekafs -lcomponent=csi-wekafs-node -o wide | grep -w don-kube-8 | cut -d" " -f1)
      $ kubectl delete pod -n csi-wekafs $POD
      ```

8. Collect CSI node server logs from the matching Kubernetes nodes and contact the [Customer Success Team](https://docs.weka.io/support/getting-support-for-your-weka-system).

   ```
   $ POD=$(kubectl get pod -n csi-wekafs -lcomponent=csi-wekafs-node -o wide | grep -w don-kube-8 | cut -d" " -f1)
   $ kubectl logs -n csi-wekafs -c wekafs $POD > log.txt  
   ```
## Checking Plugin Operation & Troubleshooting
 
1. Make sure you have configured a valid CSI API [`secret`](../examples/common/csi-wekafs-api-secret.yaml),Create a valid Weka CSI Plugin [`storageClass`](../examples/dynamic_api)  
   > **NOTE:** If using an example `storageClass`, make sure to update endpoints and credentials prior to apply 
2. Provision a [`PersistentVolumeClaim`](../examples/dynamic_directory/pvc-wekafs-dir-api.yaml)
3. Provision a [`DaemonSet`](../examples/dynamic_directory/csi-daemonset.app-on-dir-api.yaml), in order to be able to access of all pods on all nodes
4. Monitor the pod logs using a command below, nothing should be printed in log files:
   ```shell
   $ kubectl logs -f -lapp=csi-daemonset-app-on-dir-api
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
   * If no output, policy is not installed, proceed to [Custom SELinux Policy Installation](#custom-selinux-policy-installation)
   * If the policy is off, enable it and check output of the pod again by issuing
     ```shell
     $ setsebool container_use_wekafs=on
     ```
7. Check if the node is labeled with plugin is operating in SELinux-compatible mode by issuing the following command:
   ```shell
   $ kubectl describe node don-kube-8 | grep csi.weka.io/selinux_enabled
                csi.weka.io/selinux_enabled=true
   ``` 
   * If the output is empty, proceed to [CSI Plugin Installation and Configuration](#csi-plugin-installation-and-configuration)
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
