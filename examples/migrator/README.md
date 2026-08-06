# Overview

Hands-on walkthroughs for `weka-csi-migrator`, the CLI that exports the Kubernetes objects
making up Weka CSI volumes and recreates them on another cluster.

The tool moves **Kubernetes metadata only**. It never contacts the Weka cluster and never
touches volume data. That is the whole idea: if a Kubernetes cluster is lost while the Weka
cluster survives, an export is enough to rebuild every PersistentVolume, claim, StorageClass
and Secret pointing back at data that was there all along.

Full reference: [docs/migrator.md](../../docs/migrator.md).

## Example Intentions

1. These examples use one **source cluster**, described below, and walk it through three
   restore scenarios of increasing difficulty.
2. Every command output shown was captured from a real run of the tool, not hand-written.
   Object names, warnings and errors are exactly what you will see.
3. The scenarios are cumulative: read [backup_restore](backup_restore) first even if your
   real case is a cross-geography move, because the later examples assume its vocabulary.
4. The source cluster deliberately contains a **snapshot-backed** volume, so the warnings
   about what cannot cross to another Weka cluster appear in the output rather than being
   described in the abstract.

## The Scenarios

| Example | Weka cluster | Kubernetes cluster | Transform needed |
| --- | --- | --- | --- |
| [backup_restore](backup_restore) | same | rebuilt after loss | none |
| [different_kubernetes_cluster](different_kubernetes_cluster) | **same** | different | endpoints, driver name, mount options, scheduling |
| [different_weka_cluster](different_weka_cluster) | **different** | different | everything, including filesystem names |

> **NOTE:** Which one you need is decided by the *Weka* cluster, not the Kubernetes cluster.
  If the data is reachable at the same Weka cluster, volume handles do not change and you
  never touch `filesystems:`. If the data was replicated to a different Weka cluster, the
  handles must be rewritten and snapshot-backed volumes cannot come along at all.

## The Source Cluster

Every example starts from this cluster. It is a small but realistic Weka CSI installation:
two StorageClasses, one API secret, and three volumes covering each backing type.

```console
$ kubectl get storageclass
NAME                          PROVISIONER    RECLAIMPOLICY   ALLOWVOLUMEEXPANSION
storageclass-wekafs-dir-api   csi.weka.io    Delete          true
storageclass-wekafs-fs-api    csi.weka.io    Delete          true

$ kubectl get secret -n csi-wekafs
NAME                    TYPE     DATA
csi-wekafs-api-secret   Opaque   6

$ kubectl get pvc -A
NAMESPACE        NAME                  STATUS  VOLUME                                    CAPACITY  STORAGECLASS
default          pvc-wekafs-fs-api     Bound   pvc-22222222-2222-2222-2222-222222222222  100Gi     storageclass-wekafs-fs-api
default          pvc-wekafs-snap-api   Bound   pvc-33333333-3333-3333-3333-333333333333  100Gi     storageclass-wekafs-fs-api
team-analytics   pvc-wekafs-dir-api    Bound   pvc-11111111-1111-1111-1111-111111111111  100Gi     storageclass-wekafs-dir-api

$ kubectl get pv -o custom-columns='NAME:.metadata.name,HANDLE:.spec.csi.volumeHandle'
NAME                                       HANDLE
pvc-11111111-1111-1111-1111-111111111111   weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
pvc-22222222-2222-2222-2222-222222222222   weka/v2/csivol-pvc-wekafs-f-3f786850e387
pvc-33333333-3333-3333-3333-333333333333   weka/v2/default:pvc-wekafs-sn-GQ4TCMRQMNTD
```

### Reading a volume handle

The handle is the driver's opaque identifier for the data on Weka, and it is what decides
whether a volume can move to a different Weka cluster:

```
weka/v2/<filesystem>[:<snapshotAccessPoint>][/<innerPath>]
```

| PV | Handle shape | Backing | Portable to another Weka cluster |
| --- | --- | --- | --- |
| `pvc-1111…` | `weka/v2/default/csi-volumes/…` | directory | ✅ |
| `pvc-2222…` | `weka/v2/csivol-…` | filesystem | ✅ |
| `pvc-3333…` | `weka/v2/default:pvc-wekafs-sn-…` | snapshot | ❌ same Weka only |

The `:` is the tell. Weka does not replicate filesystem snapshots, so a snapshot-backed
volume has nothing to land on at a replication target. It restores perfectly well to another
Kubernetes cluster attached to the *same* Weka cluster.

> **WARNING**: Volume handles are **opaque** and must never be hand-edited. The driver does
  not normalise separators, so a cluster configured with an empty or slash-prefixed
  `dynamicProvisionPath` legitimately produces handles containing a double slash
  (`weka/v2/fs//path`). Those volumes work. Use `filesystems:` in a transform file to
  retarget a handle; it splices the name at a recorded offset and leaves the rest
  byte-for-byte intact.

## Configuration Requirements

- `weka-csi-migrator` built or installed — see [the build targets](../../docs/migrator.md#install)
- A kubeconfig pointing at the source cluster for `export`, and at the target for `import`
- The Weka CSI Plugin already installed on the **target** cluster before importing. The
  migrator recreates volume definitions, not the driver.

> **NOTE:** Nothing in these examples requires a Weka cluster to be reachable from wherever
  you run the tool. `list` and `show` do not even need a Kubernetes cluster.

# Workflow

1. Read this page, then work through [backup_restore](backup_restore) — export, inspect,
   restore, with no transform involved.
2. Move on to [different_kubernetes_cluster](different_kubernetes_cluster) for the first
   transform file: same data, new cluster around it.
3. Finish with [different_weka_cluster](different_weka_cluster), which exercises every rule
   the migrator has.

Each example follows the same seven steps: what exists before, the export command, the
`show` output, the transform file, what the transform reports, the transformed `show` output
with the changes called out, and the import.
