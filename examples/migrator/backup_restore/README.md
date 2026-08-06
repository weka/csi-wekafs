# Overview

Scenario (b): **the Kubernetes cluster was lost, the Weka cluster survived.**

A new Kubernetes cluster is built, the Weka CSI Plugin is reinstalled, and the exported
volume definitions are restored onto it. Nothing about the storage changed, so no transform
file is involved — this is the simplest case and the one to understand first.

## Example Intentions

1. Take a complete export of the source cluster described in [../README.md](../README.md)
2. Read the archive without any cluster at all, to see what it holds
3. Restore it onto an empty cluster, rebinding every claim to the data it had before
4. Show what happens when an export's credentials were redacted, because that is the default
   and it is the most common first surprise

## Configuration Requirements

The target cluster must already have the Weka CSI Plugin installed, in the same namespace
and under the same driver name as the source. If either differs, this is not your scenario —
use [../different_kubernetes_cluster](../different_kubernetes_cluster) instead.

# Workflow

## 1. Before: what exists on the source cluster

Three bound claims across two namespaces, two StorageClasses, one API secret. See
[../README.md](../README.md#the-source-cluster) for the full listing.

```console
$ kubectl get pvc -A
NAMESPACE        NAME                  STATUS  VOLUME                                    CAPACITY
default          pvc-wekafs-fs-api     Bound   pvc-22222222-2222-2222-2222-222222222222  100Gi
default          pvc-wekafs-snap-api   Bound   pvc-33333333-3333-3333-3333-333333333333  100Gi
team-analytics   pvc-wekafs-dir-api    Bound   pvc-11111111-1111-1111-1111-111111111111  100Gi
```

## 2. Export

```console
$ weka-csi-migrator export -o cluster.wcsi
INF Collecting Weka CSI volume definitions context=prod driver=csi.weka.io include_secret_data=false namespace=<all>
INF Found PersistentVolumes provisioned by the driver count=3
WRN PersistentVolume "pvc-33333333-3333-3333-3333-333333333333" is snapshot-backed: Weka cannot replicate snapshots, so this volume can only be restored to a Kubernetes cluster attached to the same Weka cluster
WRN credentials were redacted from 1 secret(s); re-export with --include-secret-data to make the archive directly importable
INF Export complete encrypted=false objects=9 output=cluster.wcsi volumes=3
```

Two warnings, both expected:

- The **snapshot-backed** volume is fine here — this restore targets the same Weka cluster.
  It would not be fine in [../different_weka_cluster](../different_weka_cluster).
- **Credentials were redacted.** This is the default. The archive is safe to hand around,
  but it cannot be imported as-is; see step 7.

To produce a directly-restorable archive, ask for the credentials and encrypt it. The
password is prompted for, twice, without echo:

```console
$ weka-csi-migrator export -o cluster.wcsi --include-secret-data
Enter a password to encrypt the archive:
Confirm password:
INF Export complete encrypted=true objects=9 output=cluster.wcsi volumes=3
```

> **NOTE:** `--include-secret-data` always requires a password. Use `--encrypt` to encrypt a
  redacted archive too. Passwords come from the prompt, `WEKA_CSI_MIGRATOR_PASSWORD`, or
  `--password-stdin` — never from a command-line argument.

> **WARNING**: Export refuses to overwrite an existing file. Pass `--force` to replace one.
  The archive is written through a staging file and renamed into place, so a run that fails
  partway leaves the previous archive intact rather than truncating it.

## 3. Inspect the archive

`list` needs no cluster and no kubeconfig:

```console
$ weka-csi-migrator list cluster.wcsi
Created:    2026-08-06 11:25:39 UTC by weka-csi-migrator/dev
Driver:     csi.weka.io
Encrypted:  false
Source:     prod v1.31.0
Cluster ID: 9f1c0b6e-prod
Secrets:    redacted

Volumes (3):
  PV                                        CLAIM                              FILESYSTEM                        BACKING     SIZE   PORTABLE
  pvc-11111111-1111-1111-1111-111111111111  team-analytics/pvc-wekafs-dir-api  default                           directory   100Gi  yes
  pvc-22222222-2222-2222-2222-222222222222  default/pvc-wekafs-fs-api          csivol-pvc-wekafs-f-3f786850e387  filesystem  100Gi  yes
  pvc-33333333-3333-3333-3333-333333333333  default/pvc-wekafs-snap-api        default                           snapshot    100Gi  same weka only

Objects (9):
  Secret                   1
  StorageClass             2
  PersistentVolume         3
  PersistentVolumeClaim    3

Redacted secret keys:
  csi-wekafs/csi-wekafs-api-secret.yaml: password

Warnings (2):
  - PersistentVolume "pvc-33333333-3333-3333-3333-333333333333" is snapshot-backed: ...
  - credentials were redacted from 1 secret(s); ...
```

The `PORTABLE` column is derived from the volume handle alone, with no Weka API call.

## 4. See the actual objects

`list` summarises; `show` prints what an import would apply, in apply order:

```console
$ weka-csi-migrator show cluster.wcsi --kind PersistentVolume --name pvc-11111111-1111-1111-1111-111111111111
apiVersion: v1
kind: PersistentVolume
metadata:
  annotations:
    old.example.com/owner: platform
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
    namespace: team-analytics
  csi:
    controllerExpandSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    driver: csi.weka.io
    nodePublishSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    volumeAttributes:
      filesystemName: default
    volumeHandle: weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
  mountOptions:
  - noatime
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: topology.weka.io/global
          operator: In
          values:
          - "true"
  persistentVolumeReclaimPolicy: Delete
  storageClassName: storageclass-wekafs-dir-api
```

Compare this with the live object on the source cluster and note what the export **removed**:
`uid`, `resourceVersion`, `managedFields`, finalizers, `status`, the
`pv.kubernetes.io/provisioned-by` annotation, the provisioner-deletion-secret annotations,
and the `storage.kubernetes.io/csiProvisionerIdentity` volume attribute. The user's own
`old.example.com/owner` annotation is kept.

Two details worth understanding:

- **`claimRef` kept its name and namespace, but lost its `uid`.** The pairing survives so
  the claim rebinds to its original volume. A `uid` from the old cluster would refer to a
  claim that does not exist on the new one, and the volume would sit in `Available` forever.
- **`persistentVolumeReclaimPolicy` is still `Delete`, and that is safe.**
  `external-provisioner` only reclaims volumes carrying a `pv.kubernetes.io/provisioned-by`
  annotation naming it — and the export removed that annotation. Removing it is a
  data-safety invariant, not tidying.

To validate the whole archive against a real API server without writing anything:

```console
$ weka-csi-migrator show cluster.wcsi | kubectl apply --dry-run=server -f -
```

## 5. Transform file

**Not applicable.** Nothing about the storage or the cluster configuration changed, so
objects are restored exactly as exported. The other two examples cover transforms.

## 6. Transform output

**Not applicable.**

## 7. Import

Always dry-run first:

```console
$ weka-csi-migrator import cluster.wcsi --dry-run
INF Archive verified archive=cluster.wcsi encrypted=false exported_at=2026-08-06T11:25:39Z exported_by=weka-csi-migrator/dev objects=9
WRN PersistentVolume "pvc-33333333-3333-3333-3333-333333333333" is snapshot-backed: ... origin=export
WRN credentials were redacted from 1 secret(s); ... origin=export
INF Applying objects context=dr dry_run=true

error: archive was exported without --include-secret-data, so these secrets carry no usable credentials: csi-wekafs/csi-wekafs-api-secret (password)
supply them with a transform file (secrets.<ns>/<name>.data), re-export with --include-secret-data, or pass --allow-redacted-secrets and create the secrets yourself
```

This is the redaction guard, and it is deliberate: applying a scrubbed credential would leave
the driver unable to authenticate, surfacing much later as a mount failure that points
nowhere near the cause. Three ways forward, in order of preference:

1. Re-export with `--include-secret-data` (step 2) — best when restoring the same cluster
2. Supply the credentials in a transform file — see
   [../different_weka_cluster](../different_weka_cluster)
3. `--allow-redacted-secrets`, then fix the Secret by hand afterwards

With a complete archive the import proceeds:

```console
$ weka-csi-migrator import cluster.wcsi
INF Archive verified archive=cluster.wcsi encrypted=true objects=9
INF Applying objects context=dr dry_run=false
created      Secret                 csi-wekafs/csi-wekafs-api-secret
created      StorageClass           storageclass-wekafs-dir-api
created      StorageClass           storageclass-wekafs-fs-api
created      PersistentVolume       pvc-11111111-1111-1111-1111-111111111111
created      PersistentVolume       pvc-22222222-2222-2222-2222-222222222222
created      PersistentVolume       pvc-33333333-3333-3333-3333-333333333333
created      PersistentVolumeClaim  default/pvc-wekafs-fs-api
created      PersistentVolumeClaim  default/pvc-wekafs-snap-api
created      PersistentVolumeClaim  team-analytics/pvc-wekafs-dir-api
INF Import complete objects=9
```

The order is not cosmetic. Secrets and StorageClasses exist before the volumes referencing
them, and every PersistentVolume exists before its claim — a claim applied first would have
no volume to bind to, and the control plane might dynamically provision fresh empty storage
against the StorageClass instead of adopting the restored volume.

> **NOTE:** Nothing is ever overwritten. An object that already exists aborts the import
  unless `--skip-existing` is given. Importing into a namespace that does not exist yet fails
  on the claim; create the namespaces first.

## 8. Verify

```console
$ kubectl get pvc -A
NAMESPACE        NAME                  STATUS  VOLUME                                    CAPACITY
default          pvc-wekafs-fs-api     Bound   pvc-22222222-2222-2222-2222-222222222222  100Gi
default          pvc-wekafs-snap-api   Bound   pvc-33333333-3333-3333-3333-333333333333  100Gi
team-analytics   pvc-wekafs-dir-api    Bound   pvc-11111111-1111-1111-1111-111111111111  100Gi
```

All three claims `Bound`, pointing at the same handles as before. Start a pod against one and
confirm the original data is there.

> **WARNING**: If a claim stays `Pending`, check the PV's `claimRef` — a stale `uid` is the
  usual cause, and it means the objects were applied without going through this tool.
