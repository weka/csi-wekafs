# Recreate a legacy PersistentVolume as a static volume

WEKA CSI Plugin 3.0 removed the legacy, API-less communication model — see [Breaking changes in
WEKA CSI Plugin 3.0](../docs/breaking-changes-3.0.md). A volume provisioned under that model is
bound to a StorageClass that carries no API secret reference, and because StorageClass parameters
are immutable it can never be made to resolve one in place.

This procedure rebuilds such a volume as a **static** PersistentVolume that references an API
secret. The data is never moved, copied or deleted: it stays exactly where it is on the WEKA
cluster, and only the Kubernetes objects describing it are replaced.

> **The whole procedure hinges on one thing: the reclaim policy must be `Retain` before the
> PersistentVolumeClaim is deleted.** With the default `Delete` policy, removing the claim tells the
> driver to destroy the data on the WEKA cluster. Do not skip step 2.

## Before you start

* Take note of the workloads using each claim. The application must be scaled down while its claim
  does not exist, so this is a planned outage per volume, not an online operation.
* Have an API secret in place — the same one your other StorageClasses reference is fine. See
  [Configure secret data](../docs/storage-class-configurations.md#configure-secret-data).
* This can be done either before or after upgrading to 3.0. Doing it **before** the upgrade means
  the workload is never broken by the upgrade itself.

## 1. Record what the volume points at

For each legacy claim, capture the PersistentVolume behind it and, critically, its volume handle:

```bash
kubectl get pvc <PVC_NAME> -n <NAMESPACE> -o jsonpath='{.spec.volumeName}'
kubectl get pv <PV_NAME> -o jsonpath='{.spec.csi.volumeHandle}{"\n"}'
kubectl get pv <PV_NAME> -o jsonpath='{.spec.capacity.storage}{"\n"}'
kubectl get pv <PV_NAME> -o yaml > /tmp/<PV_NAME>-backup.yaml
```

The handle looks like `dir/v1/<filesystem>/<inner path>`. It identifies the directory on the WEKA
cluster holding your data, and it is what the new static PersistentVolume must reuse **verbatim**.

> Copy the handle exactly as printed, including any doubled slashes. Handles are opaque strings and
> are not normalised — `dir/v1/fs//path` and `dir/v1/fs/path` are different volumes.

`weka-csi-migrator` can capture all of this for a whole cluster in one archive, which is easier
than looping over claims by hand when there are many:

```bash
weka-csi-migrator export -o legacy-volumes.wcsi
weka-csi-migrator show legacy-volumes.wcsi
```

See [the migrator documentation](../docs/migrator.md).

## 2. Switch the reclaim policy to Retain

Do this **before** deleting anything, and verify it took effect:

```bash
kubectl patch pv <PV_NAME> -p '{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}'
kubectl get pv <PV_NAME> -o jsonpath='{.spec.persistentVolumeReclaimPolicy}{"\n"}'
```

The second command must print `Retain`. If it does not, stop — deleting the claim now would delete
your data.

## 3. Scale down the workload and delete the old objects

```bash
kubectl scale deployment <APP> -n <NAMESPACE> --replicas=0
kubectl delete pvc <PVC_NAME> -n <NAMESPACE>
kubectl delete pv <PV_NAME>
```

Because the policy is `Retain`, deleting the PersistentVolume leaves the directory on the WEKA
cluster untouched. Deleting the PV object is necessary: a released PV holds a stale claim reference
that would block the new one from binding.

## 4. Create the static PersistentVolume

Create a StorageClass that references your API secret, if you do not already have one, and then a
PersistentVolume reusing the handle from step 1:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pv-recreated-static
spec:
  storageClassName: storageclass-wekafs-dir-static-api
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  volumeMode: Filesystem
  capacity:
    # the value recorded in step 1
    storage: 1Gi
  csi:
    driver: csi.weka.io
    # the handle recorded in step 1, copied verbatim
    volumeHandle: dir/v1/testfs/testdir
    nodePublishSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    controllerExpandSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
  # make the PV schedulable only on nodes with the WEKA CSI Plugin and a healthy WEKA client
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: topology.weka-infra.weka.io/accessible
              operator: In
              values:
                - "true"
```

A worked example of each static volume shape lives in
[examples/static_volume](../examples/static_volume).

## 5. Recreate the claim and restart the workload

Create a PersistentVolumeClaim naming the new volume explicitly, so it cannot bind to anything
else:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: <PVC_NAME>
  namespace: <NAMESPACE>
spec:
  storageClassName: storageclass-wekafs-dir-static-api
  volumeName: pv-recreated-static
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
```

Then scale the application back up and confirm the data is there:

```bash
kubectl scale deployment <APP> -n <NAMESPACE> --replicas=1
kubectl get pvc <PVC_NAME> -n <NAMESPACE>     # must report Bound
```

## 6. Set a quota, if you want capacity enforced

A statically provisioned volume is not created by the CSI controller, so no quota is set on it.
Until you create one on the directory — through the WEKA GUI or CLI — capacity is not enforced and
volume expansion is not allowed. Once a quota exists, both work normally.

## If something goes wrong

The data is untouched throughout, so recovery is always possible as long as you have the handle.
The PV YAML saved in step 1 contains it, and the directory on the WEKA cluster is still there. Any
number of static PersistentVolumes can be created and deleted against the same handle without
affecting the data, provided the reclaim policy stays `Retain`.
