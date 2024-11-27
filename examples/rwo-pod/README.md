# Overview

## Example Intentions
ReadWriteOncePod (RWOP) is supported by WekaFS CSI driver. This example demonstrates how to use RWOP with WekaFS CSI driver.

1. This example concentrates on using RWOP with WekaFS CSI driver
2. The example makes a use of a directory-backed volume, but the feature is functional on any other type of volume backings
3. The example demonstrates that RWOP mode will not allow multiple pods to attach to the same volume

# Workflow
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-dir-api` (Located in [../dynamic_directory/storageclass-wekafs-dir-api.yaml](../dynamic_directory/storageclass-wekafs-dir-api.yaml))
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new volume `pvc-wekafs-fs-rwo-pod`
4. Edit the application pods to set nodeSelector matching one of the Kubernetes nodes in your cluster
5. Create a first pod that will attach to the volume: `csi-app-fs-rwo-pod-01`
6. Create a second pod that will attempt attaching to the volume: `csi-app-fs-rwo-pod-02` 

# Validation
Only one of the pods will be able to attach to the volume. The other pod will fail to attach to the volume.
In the logs of the second pod, you will see an error message similar to this:

   ```
   kubectl describe pod csi-app-fs-rwo-pod-02
   ```
The output should resemble this: 
```
[
  {
    "lastProbeTime": null,
    "lastTransitionTime": "2024-11-27T11:50:33Z",
    "message": "0/8 nodes are available: 1 node has pod using PersistentVolumeClaim with the same name and ReadWriteOncePod access mode, 7 node(s) didn't match Pod's node affinity/selector. preemption: 0/8 nodes are available: 1 No preemption victims found for incoming pod, 7 Preemption is not helpful for scheduling.",
    "reason": "Unschedulable",
    "status": "False",
    "type": "PodScheduled"
  }
]
```
The error message indicates that the pod is unschedulable because the volume is already attached to another pod.


> **Note:** If both pods were created on the same time or in close adjacency, 
> a possibility exists that the second pod will attach to volume instead. 
> 
> In such case, the first pod will fail to attach to the volume.
> The error message will be similar to the one above.
