# Enabling Snapshot Functionality on Kubernetes
To enable snapshot functionality, Kubernetes Snapshot Controller must be installed along with related CRDs.

1. Check out external-snapshotter project
   from https://github.com/kubernetes-csi/external-snapshotter/tree/master/client/config/crd
2. Install snapshot controller:
    ```
    kubectl -n kube-system kustomize deploy/kubernetes/snapshot-controller | kubectl create -f -
    ```
3. Install Snapshot CRDs:
   ```
   kubectl kustomize client/config/crd | kubectl create -f -
   ```

For additional information, refer to [Kubernetes documentation](https://github.com/kubernetes-csi/external-snapshotter/tree/master#usage)
