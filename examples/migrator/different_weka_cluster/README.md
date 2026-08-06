# Overview

Scenario (d): **a different Kubernetes cluster backed by a different Weka cluster, in
another geography.**

The data was replicated to a second Weka cluster, which knows the filesystem under a
different name and is reached with different credentials. This is the most complete case and
exercises every rule the migrator has.

## Example Intentions

1. Restore into a DR site whose Weka cluster is a replication target, not the original
2. Rewrite volume handles so they address the replicated filesystem, in lockstep with the
   StorageClass parameter that must agree with them
3. Move claims into DR namespaces, rename objects to avoid collisions, and point everything
   at a relocated Secret carrying DR credentials
4. Show why the **snapshot-backed** volume cannot come along, and what to do about it

## Configuration Requirements

- Weka replication already completed to the target cluster, and the replicated filesystem
  name known. `weka fs` on the DR cluster will tell you.
- The Weka CSI Plugin installed on the target, in its own namespace and under its own driver
  name.
- The DR API credentials available as an environment variable — never written into the
  mapping file.

> **WARNING**: Weka does **not** replicate filesystem snapshots. Snapshot-backed volumes
  have nothing to land on at the DR site. Export them out with `--skip-unexportable`, or
  accept that they will be created and then fail to mount.

## What is different about the target

| | Source | Target |
| --- | --- | --- |
| Weka cluster | prod | **replication target** |
| Filesystem | `default` | `default-replica` |
| Weka API endpoints | `172.31.41.54:14000,…` | `10.200.0.11:14000,…` |
| Organization / user | `Root` / `admin` | `DisasterRecovery` / `dr-csi` |
| Secret | `csi-wekafs/csi-wekafs-api-secret` | `weka-dr/weka-dr-api-secret` |
| Driver name | `csi.weka.io` | `weka-infra.weka.io` |
| Namespaces | `default`, `team-analytics` | `dr-default`, `dr-analytics` |
| StorageClasses | `…-dir-api`, `…-fs-api` | `…-dir-dr`, `…-fs-dr` |

# Workflow

## 1. Before

The source cluster from [../README.md](../README.md).

## 2. Export

Leave the snapshot-backed volume out, since it cannot be recreated at the DR site:

```console
$ weka-csi-migrator export -o cluster.wcsi --skip-unexportable
INF Collecting Weka CSI volume definitions context=prod driver=csi.weka.io include_secret_data=false namespace=<all>
INF Found PersistentVolumes provisioned by the driver count=3
WRN skipped PersistentVolume "pvc-33333333-3333-3333-3333-333333333333": snapshot-backed volumes cannot be recreated against a different Weka cluster
WRN credentials were redacted from 1 secret(s); ...
INF Export complete encrypted=false objects=7 output=cluster.wcsi volumes=2
```

Seven objects instead of nine: the snapshot-backed PV and its claim are gone.

> **NOTE:** There is no need for `--include-secret-data` here. The DR site has its own
  credentials, so carrying the production password across would be pointless as well as
  risky — the transform supplies the DR ones instead.

## 3. Show, before any transform

```console
$ weka-csi-migrator show cluster.wcsi --kind PersistentVolume --name pvc-11111111-1111-1111-1111-111111111111
...
  claimRef:
    name: pvc-wekafs-dir-api
    namespace: team-analytics
  csi:
    driver: csi.weka.io
    nodePublishSecretRef:
      name: csi-wekafs-api-secret
      namespace: csi-wekafs
    volumeAttributes:
      filesystemName: default
    volumeHandle: weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
  storageClassName: storageclass-wekafs-dir-api
```

## 4. Transform file

[`transform-different-weka-cluster.yaml`](transform-different-weka-cluster.yaml) — the file
is fully commented; the essentials:

```yaml
namespaces:
  default: dr-default
  team-analytics: dr-analytics

filesystems:
  default: default-replica          # replication landed it under a different name

driverName: weka-infra.weka.io

storageClasses:
  storageclass-wekafs-dir-api: storageclass-wekafs-dir-dr
  storageclass-wekafs-fs-api: storageclass-wekafs-fs-dr

persistentVolumes:
  pvc-11111111-1111-1111-1111-111111111111: pv-analytics-scratch-dr

persistentVolumeClaims:
  team-analytics/pvc-wekafs-dir-api: pvc-analytics-scratch

secrets:
  csi-wekafs/csi-wekafs-api-secret:
    name: weka-dr-api-secret
    namespace: weka-dr
    data:
      endpoints: 10.200.0.11:14000,10.200.0.12:14000
      organization: DisasterRecovery
      username: dr-csi
      password: ${WEKA_DR_PASSWORD}
      scheme: https
    removeData: [nfsTargetIps]

mountOptions: ro,noatime

nodeAffinity:
  key: topology.weka-infra.weka.io/accessible
  values: ["true"]

metadata:
  kinds: [PersistentVolume, PersistentVolumeClaim]
  annotations:
    set: {migrated-from: prod-eu-west, migrated-by: weka-csi-migrator}
    remove: [internal.example.com/scratch-only]
    rename: {old.example.com/owner: platform.example.com/owner}
  labels:
    set: {site: dr}
```

Three things that trip people up:

- **`namespaces:` does not move Secrets.** The Weka API secret lives in the driver's
  namespace, not a workload namespace. Sweeping it along with a workload mapping would point
  volumes at a secret that does not exist. Relocate it under `secrets:` instead — as above.
- **`persistentVolumeClaims:` keys are `<source-namespace>/<name>`, values are bare names.**
  Namespaces move via `namespaces:`, not here.
- **Left-hand sides are always source identities**, even for objects that several rules
  rename. `persistentVolumeClaims: {team-analytics/…}` uses the *source* namespace even
  though `namespaces:` will move it to `dr-analytics`.

## 5. What the transform reports

```console
$ export WEKA_DR_PASSWORD='...'
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-weka-cluster.yaml --log-level debug
INF Applied transform rules changes=67 rules=["namespace","filesystem","driverName","storageClass","persistentVolume","persistentVolumeClaim","secret","mountOptions","nodeAffinity","metadata"]
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret metadata.name: "csi-wekafs-api-secret" -> "weka-dr-api-secret"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret metadata.namespace: "csi-wekafs" -> "weka-dr"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.endpoints: "<previous>" -> "<overridden>"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.organization: "<previous>" -> "<overridden>"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.password: "<previous>" -> "<overridden>"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.username: "<previous>" -> "<overridden>"
DBG secret: Secret/csi-wekafs/csi-wekafs-api-secret data.nfsTargetIps: "<previous>" -> "<removed>"
DBG namespace: PersistentVolume/pvc-1111… spec.claimRef.namespace: "team-analytics" -> "dr-analytics"
DBG filesystem: PersistentVolume/pvc-1111… spec.csi.volumeHandle: "weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a" -> "weka/v2/default-replica/csi-volumes/pvc-wekafs-dir-api-97ab4a2a"
DBG filesystem: PersistentVolume/pvc-1111… spec.csi.volumeAttributes.filesystemName: "default" -> "default-replica"
DBG driverName: PersistentVolume/pvc-1111… spec.csi.driver: "csi.weka.io" -> "weka-infra.weka.io"
DBG storageClass: PersistentVolume/pvc-1111… spec.storageClassName: "storageclass-wekafs-dir-api" -> "storageclass-wekafs-dir-dr"
DBG persistentVolume: PersistentVolume/pvc-1111… metadata.name: "pvc-11111111-1111-1111-1111-111111111111" -> "pv-analytics-scratch-dr"
...
```

Look at the handle rewrite closely:

```
weka/v2/default/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
     -> weka/v2/default-replica/csi-volumes/pvc-wekafs-dir-api-97ab4a2a
        ^^^^^^^^^^^^^^^^^^^^^^ only the filesystem name changed
```

The name is **spliced** at a recorded offset; the inner path and every separator survive
byte-for-byte. Rebuilding the handle from parsed components would risk normalising away a
doubled slash and silently repointing the volume at different data.

Notice also that one `filesystems:` entry rewrote the handle, the volume attribute **and**
the StorageClass parameter. If those three ever disagreed, the volume and its class would
point at different filesystems.

## 6. Show, after the transform

```console
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-weka-cluster.yaml --kind PersistentVolume
apiVersion: v1
kind: PersistentVolume
metadata:
  annotations:
    migrated-by: weka-csi-migrator             # <- metadata.annotations.set
    migrated-from: prod-eu-west                # <- metadata.annotations.set
    platform.example.com/owner: platform       # <- renamed from old.example.com/owner, value kept
  labels:
    site: dr                                   # <- metadata.labels.set
  name: pv-analytics-scratch-dr                # <- persistentVolumes rename
spec:
  accessModes:
  - ReadWriteMany
  capacity:
    storage: 100Gi
  claimRef:
    apiVersion: v1
    kind: PersistentVolumeClaim
    name: pvc-analytics-scratch                # <- persistentVolumeClaims rename
    namespace: dr-analytics                    # <- namespaces mapping
  csi:
    controllerExpandSecretRef:
      name: weka-dr-api-secret                 # <- followed the Secret
      namespace: weka-dr                       # <- followed the Secret
    driver: weka-infra.weka.io                 # <- driverName
    nodePublishSecretRef:
      name: weka-dr-api-secret
      namespace: weka-dr
    volumeAttributes:
      filesystemName: default-replica          # <- filesystems, in lockstep with the handle
    volumeHandle: weka/v2/default-replica/csi-volumes/pvc-wekafs-dir-api-97ab4a2a   # <- filesystems
  mountOptions:
  - ro                                         # <- mountOptions
  - noatime
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: topology.weka-infra.weka.io/accessible    # <- nodeAffinity
          operator: In
          values:
          - "true"
  persistentVolumeReclaimPolicy: Delete
  storageClassName: storageclass-wekafs-dir-dr # <- storageClasses
```

And the Secret everything now points at:

```console
$ weka-csi-migrator show cluster.wcsi --transform-file transform-different-weka-cluster.yaml --kind Secret
apiVersion: v1
data:
  endpoints: MTAuMjAwLjAuMTE6MTQwMDAsMTAuMjAwLjAuMTI6MTQwMDA=      # 10.200.0.11:14000,10.200.0.12:14000
  organization: RGlzYXN0ZXJSZWNvdmVyeQ==                            # DisasterRecovery
  password: ZHItYXBpLXBhc3N3b3Jk                                    # from ${WEKA_DR_PASSWORD}
  scheme: aHR0cHM=                                                  # https
  username: ZHItY3Np                                                # dr-csi
kind: Secret
metadata:
  name: weka-dr-api-secret
  namespace: weka-dr
type: Opaque
```

`nfsTargetIps` is gone, per `removeData`. Values are written as plaintext in the mapping file
and base64-encoded for you.

> **NOTE:** The claim is checked the same way:
  `show cluster.wcsi --transform-file … --kind PersistentVolumeClaim` should show
  `spec.volumeName: pv-analytics-scratch-dr` — the rename propagated to the binding.

## 7. Import

```console
$ export WEKA_DR_PASSWORD='...'
$ weka-csi-migrator import cluster.wcsi --transform-file transform-different-weka-cluster.yaml --dry-run
```

Then, for real:

```console
$ weka-csi-migrator import cluster.wcsi --transform-file transform-different-weka-cluster.yaml
INF Archive verified archive=cluster.wcsi encrypted=false objects=7
INF Applying objects context=dr dry_run=false
INF Applying transform rules rules=["namespace","filesystem","driverName","storageClass","persistentVolume","persistentVolumeClaim","secret","mountOptions","nodeAffinity","metadata"]
created      Secret                 weka-dr/weka-dr-api-secret
created      StorageClass           storageclass-wekafs-dir-dr
created      StorageClass           storageclass-wekafs-fs-dr
created      PersistentVolume       pv-analytics-scratch-dr
created      PersistentVolume       pvc-22222222-2222-2222-2222-222222222222
created      PersistentVolumeClaim  dr-default/pvc-wekafs-fs-api
created      PersistentVolumeClaim  dr-analytics/pvc-analytics-scratch
INF Import complete objects=7
```

The archive was exported **redacted**, yet the import succeeded: transforms run before the
redaction check, so the DR credentials supplied under `secrets:` satisfy it. This is the
normal shape of a cross-cluster move.

> **NOTE:** Create `dr-default` and `dr-analytics` before importing. The migrator does not
  create namespaces — it recreates volume definitions, not cluster scaffolding.

## 8. Verify

```console
$ kubectl get pvc -A
NAMESPACE      NAME                     STATUS  VOLUME                                    CAPACITY
dr-analytics   pvc-analytics-scratch    Bound   pv-analytics-scratch-dr                   100Gi
dr-default     pvc-wekafs-fs-api        Bound   pvc-22222222-2222-2222-2222-222222222222  100Gi
```

Then confirm the volume and its class agree about the filesystem — the one inconsistency
that binds cleanly but fails at mount time:

```console
$ kubectl get pv pv-analytics-scratch-dr -o jsonpath='{.spec.csi.volumeHandle}{"\n"}'
weka/v2/default-replica/csi-volumes/pvc-wekafs-dir-api-97ab4a2a

$ kubectl get sc storageclass-wekafs-dir-dr -o jsonpath='{.parameters.filesystemName}{"\n"}'
default-replica
```

> **WARNING**: Weka replicates data, but **directory quotas are not carried across**.
  Capacity on imported PVs is nominal until the quotas are re-applied on the DR cluster.
  Reconciliation is not yet implemented; check quotas by hand for directory-backed volumes.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `WRN Transform mapping matched no object` | A left-hand side does not match any archive identity. Check it against `list`. |
| `error: the transform would produce more than one object with the same identity` | Namespaces collapsed where claim names repeat. Rename with `persistentVolumeClaims`, or map namespaces separately. |
| `error: environment variable(s) not set: WEKA_DR_PASSWORD` | `${VAR}` was not exported. Deliberate — an empty password would fail much later. |
| `error: parsing transform file: unknown field "namespacs"` | Strict parsing. A misspelled key would otherwise be a transform you believe is happening but is not. |
| Claim stays `Pending` | The target namespace does not exist, or the StorageClass rename was applied to the volume but not the claim. |
| Pod fails to mount, volume looks fine | The filesystem name does not exist on the DR Weka cluster, or the quota is missing. |
