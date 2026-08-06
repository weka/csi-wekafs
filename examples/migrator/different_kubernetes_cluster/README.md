# Overview

Scenario (c): **a different Kubernetes cluster attached to the same Weka cluster.**

The data is not moving. What changes is everything around the volumes — how the new cluster
reaches the Weka API, where the driver is installed, and how volumes are mounted and
scheduled. This is the first example that uses a transform file.

## Example Intentions

1. Restore the export from [../backup_restore](../backup_restore) onto a cluster whose
   Weka CSI Plugin is installed differently
2. Change the API endpoints, because the new cluster reaches the same Weka cluster from
   another network segment
3. Retarget the CSI driver name, the secret namespace, the mount options and the node
   affinity topology key
4. Show that volume handles are **untouched**, which is what makes this scenario safe for
   snapshot-backed volumes as well

## Configuration Requirements

The target cluster must reach the **same** Weka cluster. If the data was replicated to a
different Weka cluster, filesystem names change and volume handles must be rewritten — use
[../different_weka_cluster](../different_weka_cluster) instead.

## What is different about the target

| | Source | Target |
| --- | --- | --- |
| Weka cluster | same | **same** |
| Weka API endpoints | `172.31.41.54:14000,172.31.47.152:14000` | `10.100.0.11:14000,10.100.0.12:14000` |
| Driver namespace | `csi-wekafs` | `weka-csi` |
| Driver name | `csi.weka.io` | `weka-infra.weka.io` |
| Topology key | `topology.weka.io/global` | `topology.weka-infra.weka.io/accessible` |
| Mount options | `noatime` | `ro,noatime` |

> **NOTE:** The credentials do not change, because it is the same Weka cluster. That is why
  the transform below sets `endpoints` but not `password`.

# Workflow

## 1. Before

The source cluster from [../README.md](../README.md), unchanged.

## 2. Export

Identical to [../backup_restore](../backup_restore#2-export) — an archive is not scenario
specific, the transform is applied at import time.

```console
$ weka-csi-migrator export -o cluster.wcsi
INF Found PersistentVolumes provisioned by the driver count=3
WRN PersistentVolume "pvc-33333333-3333-3333-3333-333333333333" is snapshot-backed: ...
INF Export complete encrypted=false objects=9 output=cluster.wcsi volumes=3
```

The snapshot warning **does not apply to this scenario**: the target reaches the same Weka
cluster, so the snapshot is right where the handle says it is.

## 3. Show, before any transform

```console
$ weka-csi-migrator show cluster.wcsi --kind PersistentVolume --name pvc-11111111-1111-1111-1111-111111111111
...
  csi:
    controllerExpandSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    driver: csi.weka.io
    nodePublishSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    volumeHandle: weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
  mountOptions:
  - noatime
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: topology.weka.io/global
```

## 4. Transform file

[`transform-different-kubernetes-cluster.yaml`](transform-different-kubernetes-cluster.yaml):

```yaml
secrets:
  csi-wekafs/csi-wekafs-api-secret:
    namespace: weka-csi
    data:
      endpoints: 10.100.0.11:14000,10.100.0.12:14000

driverName: weka-infra.weka.io

nodeAffinity:
  key: topology.weka-infra.weka.io/accessible
  values: ["true"]

mountOptions: ro,noatime

metadata:
  annotations:
    set:
      migrated-from: prod-cluster
      migrated-by: weka-csi-migrator
```

Note what is **absent**: no `filesystems:`, no `namespaces:`, no renames. The data has not
moved and the workloads keep their namespaces.

> **NOTE:** Keys on the left are always the identity **as it appears in the archive**, never
  what it becomes. Read those identities with `list`.

## 5. What the transform reports

```console
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-kubernetes-cluster.yaml --log-level debug
INF Applied transform rules changes=43 rules=["driverName","secret","mountOptions","nodeAffinity","metadata"]
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret metadata.namespace: "csi-wekafs" -> "weka-csi"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.endpoints: "<previous>" -> "<overridden>"
DBG driverName: StorageClass/storageclass-wekafs-dir-api provisioner: "csi.weka.io" -> "weka-infra.weka.io"
DBG secret: StorageClass/storageclass-wekafs-dir-api parameters.csi.storage.k8s.io/provisioner-secret-namespace: "csi-wekafs" -> "weka-csi"
DBG secret: StorageClass/storageclass-wekafs-dir-api parameters.csi.storage.k8s.io/node-publish-secret-namespace: "csi-wekafs" -> "weka-csi"
DBG secret: StorageClass/storageclass-wekafs-dir-api parameters.csi.storage.k8s.io/controller-expand-secret-namespace: "csi-wekafs" -> "weka-csi"
DBG driverName: PersistentVolume/pvc-11111111-1111-1111-1111-111111111111 spec.csi.driver: "csi.weka.io" -> "weka-infra.weka.io"
DBG secret: PersistentVolume/pvc-11111111-1111-1111-1111-111111111111 spec.csi.nodePublishSecretRef.namespace: "csi-wekafs" -> "weka-csi"
DBG secret: PersistentVolume/pvc-11111111-1111-1111-1111-111111111111 spec.csi.controllerExpandSecretRef.namespace: "csi-wekafs" -> "weka-csi"
DBG mountOptions: PersistentVolume/pvc-11111111-1111-1111-1111-111111111111 spec.mountOptions: "noatime" -> "ro,noatime"
...
```

Two things to notice:

- **One `secrets:` entry produced changes in three kinds.** The Secret moved, and every
  reference to it followed: the PV's `nodePublishSecretRef` and `controllerExpandSecretRef`,
  and all the StorageClass `csi.storage.k8s.io/*-secret-namespace` parameters. A rename is
  never a single-object edit.
- **The credential value is never printed.** `data.endpoints` shows as `<overridden>`, so a
  debug log can be pasted into a ticket safely.

> **WARNING**: A mapping that matches nothing is reported as
  `WRN Transform mapping matched no object in the archive`. That is almost always a typo, and
  ignoring it means discovering the un-renamed object only when a pod fails to mount.

## 6. Show, after the transform

```console
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-kubernetes-cluster.yaml \
    --kind PersistentVolume --name pvc-11111111-1111-1111-1111-111111111111
apiVersion: v1
kind: PersistentVolume
metadata:
  annotations:
    migrated-by: weka-csi-migrator          # <- added by metadata.annotations.set
    migrated-from: prod-cluster             # <- added by metadata.annotations.set
    old.example.com/owner: platform         # <- untouched: the transform did not mention it
  name: pvc-11111111-1111-1111-1111-111111111111
spec:
  accessModes:
  - ReadWriteMany
  capacity:
    storage: 100Gi
  claimRef:
    apiVersion: v1
    kind: PersistentVolumeClaim
    name: pvc-wekafs-dir-api
    namespace: team-analytics               # <- unchanged: no namespaces: mapping
  csi:
    controllerExpandSecretRef:
      name: csi-wekafs-api-secret
      namespace: weka-csi                   # <- followed the Secret
    driver: weka-infra.weka.io              # <- driverName
    nodePublishSecretRef:
      name: csi-wekafs-api-secret
      namespace: weka-csi                   # <- followed the Secret
    volumeAttributes:
      filesystemName: default               # <- unchanged: same Weka cluster
    volumeHandle: weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a   # <- UNCHANGED
  mountOptions:
  - ro                                      # <- mountOptions
  - noatime
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: topology.weka-infra.weka.io/accessible   # <- nodeAffinity, key AND values
          operator: In
          values:
          - "true"
  persistentVolumeReclaimPolicy: Delete
  storageClassName: storageclass-wekafs-dir-api         # <- unchanged: no storageClasses: mapping
```

**The volume handle is byte-for-byte identical.** That is the defining property of this
scenario, and it is why snapshot-backed volumes are fine here.

Validate against the target's API server before writing anything:

```console
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-kubernetes-cluster.yaml \
    | kubectl apply --dry-run=server -f -
```

## 7. Import

```console
$ weka-csi-migrator import cluster.wcsi --transform-file transform-different-kubernetes-cluster.yaml --dry-run
INF Applying transform rules rules=["driverName","secret","mountOptions","nodeAffinity","metadata"]
would create Secret                 weka-csi/csi-wekafs-api-secret
would create StorageClass           storageclass-wekafs-dir-api
would create StorageClass           storageclass-wekafs-fs-api
would create PersistentVolume       pvc-11111111-1111-1111-1111-111111111111
would create PersistentVolume       pvc-22222222-2222-2222-2222-222222222222
would create PersistentVolume       pvc-33333333-3333-3333-3333-333333333333
would create PersistentVolumeClaim  default/pvc-wekafs-fs-api
would create PersistentVolumeClaim  default/pvc-wekafs-snap-api
would create PersistentVolumeClaim  team-analytics/pvc-wekafs-dir-api
INF Dry run complete, nothing was written objects=9
```

Then drop `--dry-run`. The archive here was exported without `--include-secret-data`, and
this transform sets only `endpoints`, so the `password` key is still redacted — the import
will refuse. Either re-export with `--include-secret-data`, or add the password to the
transform:

```yaml
secrets:
  csi-wekafs/csi-wekafs-api-secret:
    namespace: weka-csi
    data:
      endpoints: 10.100.0.11:14000,10.100.0.12:14000
      password: ${WEKA_API_PASSWORD}
```

```console
$ export WEKA_API_PASSWORD='...'
$ weka-csi-migrator import cluster.wcsi --transform-file transform-different-kubernetes-cluster.yaml
```

> **NOTE:** `${VAR}` reads an environment variable. An unset variable is a hard error, never
  an empty password — a Secret containing an empty credential would fail at first mount with
  nothing pointing at the cause.
