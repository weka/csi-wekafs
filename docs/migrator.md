# weka-csi-migrator

Exports and restores the Kubernetes objects that make up Weka CSI volumes.

The tool moves **Kubernetes metadata only**. It never contacts the Weka cluster and never
touches volume data. That is the whole idea: if a Kubernetes cluster is lost while the Weka
cluster survives, an export is enough to rebuild every PersistentVolume, claim,
StorageClass and Secret pointing back at the data that was there all along.

## Status

Phase 2 scope: **CLI, with transforms for cross-cluster and cross-geography restores**.

| Scenario | Supported |
| --- | --- |
| (a) Export PVs, PVCs, StorageClasses and Secrets | ✅ |
| (b) Restore onto a rebuilt cluster, same Weka cluster | ✅ |
| (c) Restore onto a different cluster with edits (endpoints, mountOptions, nodeAffinity) | ✅ |
| (d) Restore against a *different* Weka cluster (renamed filesystems, namespaces) | ✅ |
| (e) Continuous replication to a second Kubernetes cluster | planned |
| VolumeSnapshot / VolumeSnapshotContent export | planned |

Snapshot-backed volumes are **exported and warned about** rather than skipped, because they
restore correctly to any Kubernetes cluster attached to the *same* Weka cluster. See
[Volume portability](#volume-portability).

## Hands-on examples

Step-by-step walkthroughs of each scenario, with real command output, live in
[examples/migrator](../examples/migrator):

| Example | Weka cluster | Kubernetes cluster |
| --- | --- | --- |
| [backup_restore](../examples/migrator/backup_restore) | same | rebuilt after loss |
| [different_kubernetes_cluster](../examples/migrator/different_kubernetes_cluster) | same | different |
| [different_weka_cluster](../examples/migrator/different_weka_cluster) | different | different |

## Install

```bash
make migrator          # builds ./bin/weka-csi-migrator for the host platform
make migrator-release  # cross-compiles release archives + checksums into ./dist
make migrator-image    # container image, published from migrator.Dockerfile
```

Homebrew and published container images are wired to `migrator-release` output; the
checksum file it emits is what a formula consumes.

## Usage

### Export

```bash
# Whole cluster, credentials redacted, archive readable with standard tools
weka-csi-migrator export -o cluster.wcsi

# One namespace only. Secrets and StorageClasses are still followed wherever they live.
weka-csi-migrator export -o team-a.wcsi -n team-a

# Directly restorable: credentials included, so the archive must be encrypted.
# Prompts for a password; see Passwords below for the non-interactive ways in.
weka-csi-migrator export -o cluster.wcsi --include-secret-data

# Redacted but still encrypted at rest
weka-csi-migrator export -o cluster.wcsi --encrypt

# Only volumes that could be recreated against a different Weka cluster
weka-csi-migrator export -o portable.wcsi --skip-unexportable
```

```bash
# Replace an existing archive
weka-csi-migrator export -o cluster.wcsi --force
```

Export refuses to overwrite an existing file unless `--force` (`-f`) is given — an archive
may be the only copy of a cluster's volume definitions. The check runs before the cluster is
queried, so a doomed export fails immediately rather than after a full collection.

The archive is written through a staging file in the target's own directory and renamed into
place only once it is complete. A run that fails partway — full disk, interrupted, cluster
error — therefore leaves the previous archive intact rather than replacing it with a
truncated file that still looks like a backup.

### When is an archive encrypted?

Only when you ask for it. An export is encrypted if **either** `--include-secret-data` or
`--encrypt` is given; both then require a password.

| Flags | Credentials | Encrypted |
| --- | --- | --- |
| *(none)* | redacted | no |
| `--encrypt` | redacted | yes |
| `--include-secret-data` | included | yes (mandatory) |

A plain export contains no credentials — the sensitive keys are replaced with a visible
marker — so it is left readable on purpose: that is what lets you inspect it and plan a
migration. Use `--encrypt` when the archive should also be unreadable at rest.

`list` tells you which you have:

```
Encrypted:  false
Secrets:    redacted
```

### Passwords

Never from command-line arguments — argv is visible to every process on the host and lands
in shell history. There are three ways in, checked in this order:

| Source | When to use |
| --- | --- |
| `--password-stdin` | Scripts and CI. **Requires the password to be piped in.** |
| `WEKA_CSI_MIGRATOR_PASSWORD` | Scripts, CI, anywhere a pipe is awkward |
| Interactive prompt | Default at a terminal. Typing is not echoed. |

```bash
# Interactive: prompts, twice, without echo
weka-csi-migrator export -o cluster.wcsi --include-secret-data

# Piped
echo -n 'my-passphrase' | weka-csi-migrator export -o cluster.wcsi \
    --include-secret-data --password-stdin

# Environment
WEKA_CSI_MIGRATOR_PASSWORD='my-passphrase' weka-csi-migrator export \
    -o cluster.wcsi --include-secret-data
```

`--password-stdin` at an interactive terminal is an **error**, not a wait. Reading a
terminal to EOF would block indefinitely with no prompt, which is indistinguishable from a
hang; the tool refuses immediately and tells you how to supply the password instead.

Encryption prompts twice and requires the two to match. A typo would produce an archive
nobody can ever open, and the mistake would only surface when someone needed to restore
from it.

Decryption asks only when needed: whether an archive is encrypted is knowable only after
its header is read, so `import` and `list` open the file first and prompt only if it turns
out to be encrypted. If stdin is not a terminal and no password was supplied, they fail with
a message naming both non-interactive alternatives rather than hanging.

Prompts go to stderr, so `export --output -` can still stream an archive down a pipe while
asking for a password.

### Logging

The tool logs to **stderr** at `info` by default, using the same zerolog console format as
the driver. `--log-level` accepts `trace`, `debug`, `info`, `warn`, `error` and `off`.

```bash
weka-csi-migrator export -o cluster.wcsi --log-level debug   # per-volume detail
weka-csi-migrator export -o cluster.wcsi --log-level warn    # only problems
```

`info` reports what was found, what was written, and every warning. `debug` adds a line per
volume with its handle and backing type, plus the resolved StorageClass and Secret counts.

Logging never touches stdout. That is deliberate and covered by a test: `export --output -`
streams the archive to stdout and `list --json` streams JSON there, so a stray log line
would corrupt a pipeline. Command *results* — the `list` report, the `import` action table —
are data and go to stdout; progress and problems are logs and go to stderr.

```bash
# Safe: archive on stdout, logs on stderr
weka-csi-migrator export -o - | gpg --encrypt > cluster.wcsi.gpg
weka-csi-migrator list --json cluster.wcsi | jq '.volumes[] | select(.backing=="snapshot")'
```

### Inspect

`list` reads the manifest and touches no cluster, so it needs no kubeconfig.

```bash
weka-csi-migrator list cluster.wcsi
weka-csi-migrator list cluster.wcsi --json | jq '.volumes[]'
```

```
Created:    2026-08-05 16:20:36 UTC by weka-csi-migrator/v2.8.9
Driver:     csi.weka.io
Encrypted:  false
Secrets:    redacted

Volumes (3):
  PV       CLAIM             FILESYSTEM                 BACKING     SIZE  PORTABLE
  pv-dir   default/pvc-dir   testfs                     directory   1Gi   yes
  pv-fs    team-a/pvc-fs     csivol-fsvol-97ab4a2a2b6d  filesystem  1Gi   yes
  pv-snap  default/pvc-snap  testfs                     snapshot    1Gi   same weka only
```

### Inspect the actual objects

`list` summarises; `show` prints the manifests an import would apply, in apply order, as a
multi-document YAML stream on stdout.

```bash
weka-csi-migrator show cluster.wcsi                          # everything
weka-csi-migrator show cluster.wcsi --kind PersistentVolume  # one kind
weka-csi-migrator show cluster.wcsi -n team-a --name pvc-x   # one object
weka-csi-migrator show cluster.wcsi --output-dir ./review    # one file per object
```

To confirm an archive is valid before it touches a cluster, hand it to Kubernetes itself:

```bash
weka-csi-migrator show cluster.wcsi | kubectl apply --dry-run=client -f -
weka-csi-migrator show cluster.wcsi | kubectl apply --dry-run=server -f -   # also validates
```

`--dry-run=server` is the stronger check: it runs admission and full schema validation
against the real API server without persisting anything. `import --dry-run` complements it
by reporting which objects already exist on the target.

Worth checking by eye in `show` output: each PersistentVolume's `spec.csi.volumeHandle`
matches what the source cluster had, each PersistentVolumeClaim has a `spec.volumeName`
pinning it to its volume, and no `claimRef` carries a `uid`.

Extracted files are written mode `600`, since an archive exported with
`--include-secret-data` holds live credentials.

### Import

```bash
weka-csi-migrator import cluster.wcsi --dry-run     # report only
weka-csi-migrator import cluster.wcsi
weka-csi-migrator import cluster.wcsi --skip-existing
```

The archive is verified in full **before** anything is written. Objects are applied in
dependency order: Secrets → StorageClasses → PersistentVolumes → PersistentVolumeClaims.
Nothing is ever overwritten; an existing object aborts the import unless `--skip-existing`
is given.

Importing an archive exported *without* `--include-secret-data` is refused, naming the
secrets involved. Applying scrubbed credentials would leave the driver unable to
authenticate, surfacing much later as a mount error that points nowhere near the cause.
`--allow-redacted-secrets` overrides this once you have decided to create the secrets
yourself.

## What conversion actually does

An export is not a `kubectl get -o yaml` dump. Each object is reduced to what is needed to
recreate it, and dynamic volumes are rewritten as static ones.

**PersistentVolume**
- drops `uid`, `resourceVersion`, `generation`, `creationTimestamp`, `managedFields`,
  `finalizers`, `ownerReferences` and `status`
- drops `pv.kubernetes.io/provisioned-by`, `bound-by-controller` and the
  `volume.kubernetes.io/provisioner-deletion-secret-*` annotations
- drops the provisioner-injected `storage.kubernetes.io/csiProvisionerIdentity`
  volumeAttribute, keeping every driver-meaningful one
- keeps `claimRef` name and namespace but **strips its `uid`** — a claimRef carrying a uid
  from the old cluster never binds, leaving the volume `Available` forever
- keeps the volume handle **byte for byte** (see below)
- keeps the reclaim policy exactly as it was

**PersistentVolumeClaim**
- sets `spec.volumeName`, which is what makes the claim static. Without it the target
  cluster provisions fresh empty storage against the StorageClass and the restored data is
  orphaned.
- drops the `bind-completed`, `bound-by-controller` and `storage-provisioner` annotations,
  the `pvc-protection` finalizer, and `status`

**Secret** — sensitive keys are replaced with a visible marker unless `--include-secret-data`
is given. Redaction covers `password`, `kmsVaultRoleIdForFilesystemEncryption` and
`kmsVaultSecretIdForFilesystemEncryption`. Everything else — `username`, `organization`,
`endpoints`, `scheme`, `nfsTargetIps`, `caCertificate` — is left intact on purpose: those
are exactly the fields you need to read when planning a move to another Weka cluster.

### Reclaim policy and why exported volumes are safe

v1 preserves `persistentVolumeReclaimPolicy` verbatim, so a restored volume may still say
`Delete`. That is safe for a specific reason worth knowing:

> `external-provisioner` only reclaims a PersistentVolume whose `pv.kubernetes.io/provisioned-by`
> annotation names it. A volume without that annotation is ignored regardless of its reclaim
> policy.

Conversion removes that annotation, so a restored `Delete`-policy volume will not be
reclaimed. This is load-bearing, not cosmetic tidying — it is pinned by
`TestExportedPVIsNotReclaimable` in `pkg/migrator/convert`. Do not reinstate the annotation
without also forcing the policy to `Retain`.

### Volume handles are opaque

The handle is the driver's identifier for the data on Weka. Its format is parsed by
`pkg/volumeid`:

```
weka/v2/<fsName>[:<snapAccessPoint>][/<innerPath>]     current
dir/v1/<fsName>/<innerPath>                            legacy
```

The driver's handle generator does **not** normalise separators. With the chart default
`dynamicProvisionPath: "csi-volumes"` handles look like `weka/v2/fs/csi-volumes/vol-abc`,
but a cluster configured with an empty or slash-prefixed `dynamicProvisionPath` produces
`weka/v2/fs//vol-abc`, with a doubled slash. Those volumes are valid and in active use.

`pkg/volumeid` is therefore lossless: `Parse` retains the input verbatim, `String` returns
it unchanged, and the only mutating helper, `WithFilesystemName`, splices at a recorded
offset instead of reassembling from parts. A normalising parser would silently repoint a
restored volume at a different path.

### Volume portability

Backing type is derived from the handle alone, with no Weka API call:

| Backing | Handle shape | Restorable to a different Weka cluster |
| --- | --- | --- |
| Filesystem | `weka/v2/fs` | ✅ |
| Directory | `weka/v2/fs/path`, `dir/v1/fs/path` | ✅ |
| Snapshot | `weka/v2/fs:accessPoint` | ❌ same Weka cluster only |
| Directory on snapshot | `weka/v2/fs:accessPoint/path` | ❌ same Weka cluster only |

Weka does not replicate filesystem snapshots, so snapshot-backed volumes have nothing to
land on at a replication target. They restore fine to another Kubernetes cluster attached to
the same Weka cluster, which is why they are exported with a warning rather than dropped.
`--skip-unexportable` drops them.

## Archive format

`.wcsi` is a plaintext header followed by a payload:

```
WEKACSI1\n
{"formatVersion":1,"encrypted":false,"manifestSum":"…"}\n
<payload>
```

The payload is a gzipped tar holding `manifest.json` and one YAML document per object under
`objects/<kind>/[<namespace>/]<name>.yaml`. When encrypted it is a sequence of AES-256-GCM
frames instead; the header stays readable so the tool can explain why it cannot open a file.

Two integrity guarantees apply, and the difference is deliberate:

- **Encrypted archives are tamper-proof.** Every frame is authenticated by AES-GCM with a
  key derived via argon2id, the header digest is bound in as additional authenticated data,
  and each frame is bound to its position and to whether it terminates the stream. Editing,
  reordering, truncating or appending all fail authentication.
- **Unencrypted archives are only tamper-evident.** The manifest records a SHA-256 per
  entry and the header records a SHA-256 of the manifest. This reliably catches corruption
  and accidental edits, but anyone who can rewrite the file can recompute the digests. Use
  a password when that matters.

Import verifies whichever guarantee applies before applying a single object. A hidden
`--i-know-what-im-doing-ignore-integrity` flag downgrades digest mismatches to warnings for
salvaging a damaged archive; it cannot rescue an encrypted one, which simply will not
decrypt if altered.

Exports are reproducible: two exports of an unchanged cluster differ only in their timestamp
and, when encrypted, the random salt.

## Transforms: scenarios (c) and (d)

`--transform-file` rewrites objects on the way in, for restoring onto a cluster that differs
from the source — a different network segment, different namespaces, or a different Weka
cluster in another geography.

```bash
weka-csi-migrator show   cluster.wcsi --transform-file dr.yaml    # preview, no cluster needed
weka-csi-migrator import cluster.wcsi --transform-file dr.yaml --dry-run
weka-csi-migrator import cluster.wcsi --transform-file dr.yaml
```

`show --transform-file` runs the same chain over the same objects as the import, so it is an
exact preview rather than an approximation. Use it before every real run.

### Mapping file

Every mapping is keyed by the object's identity **as it appears in the archive**, never by
what it becomes. `list` shows you those identities.

```yaml
# Namespaces: a map, or targetNamespace for a single destination. Mutually exclusive.
namespaces:
  default: dr-default
  team-a:  dr-team-a

# Weka filesystem renames. Rewrites the volume handle, the volume attribute and the
# StorageClass parameter together, so they cannot disagree about where the data lives.
filesystems:
  testfs: testfs-replica

# The CSI driver name on the target. A single value, not a mapping: an archive holds
# exactly one driver, because export selects volumes by --driver-name.
driverName: weka-infra.weka.io

storageClasses:
  sc-dir: sc-dir-dr

persistentVolumes:
  pv-dir: pv-dir-dr

# Keyed "<source-namespace>/<name>"; the value is a bare name. Use namespaces to move it.
persistentVolumeClaims:
  default/pvc-dir: pvc-dir-dr

# Keyed "<source-namespace>/<name>". Values are plaintext and are base64-encoded for you.
# ${VAR} reads an environment variable, which is how a password stays out of this file.
secrets:
  csi-wekafs/csi-wekafs-api-secret:
    name: weka-dr-api
    namespace: weka-dr
    data:
      endpoints:    10.20.30.40:14000,10.20.30.41:14000
      organization: DR
      password:     ${WEKA_DR_PASSWORD}
    removeData: [nfsTargetIps]

# A scalar, a list, or a per-volume map. An explicit [] clears existing options.
mountOptions: ro,noatime
# mountOptions:
#   pv-dir: [ro, noatime]

# Replaced wholesale: a target cluster may publish a different topology key entirely.
nodeAffinity:
  key: topology.weka-dr.weka.io/accessible
  values: ["true"]
# nodeAffinity: {remove: true}

metadata:
  kinds: [PersistentVolume]        # optional; default is every object
  annotations:
    set:    {migrated-from: prod-us-east}
    remove: [internal.example.com/scratch]
    rename: {old.example.com/team: new.example.com/team}
  labels:
    set: {tier: dr}
```

### What the rules guarantee

**Referential integrity.** Renaming is never a single-object edit, so each mapping rewrites
every place the name appears:

| Mapping | Also rewrites |
| --- | --- |
| `namespaces` | PVC namespace **and** the PV's `claimRef.namespace` |
| `filesystems` | volume handle, `volumeAttributes.filesystemName`, SC `parameters.filesystemName` |
| `driverName` | PV `spec.csi.driver` **and** SC `provisioner` |
| `storageClasses` | the class, plus `storageClassName` on volumes **and** claims |
| `persistentVolumes` | the volume, plus the claim's `spec.volumeName` |
| `persistentVolumeClaims` | the claim, plus the PV's `claimRef.name` |
| `secrets` | the Secret, all five PV `*SecretRef`s, all six SC parameter pairs |

**Order independence.** Rules read the keys they match on from an immutable snapshot of the
object as it appeared in the archive. Without that, a namespace mapping running before a
claim rename would leave the rename unable to find its target. Because every rule keys on
source identity, rule order cannot change the result.

The `driverName` case is easy to overlook: the chart's `csiDriverName` is overridable, so a
target cluster may run the driver under a different name. A PersistentVolume naming a driver
the target does not have stays Pending forever with no node able to stage it, and a
StorageClass whose `provisioner` disagrees with its volumes silently stops serving new
claims — so both move together.

**Handles are spliced, never rebuilt.** A filesystem rename goes through
`volumeid.Handle.WithFilesystemName`, which replaces the name at a recorded offset. Everything
else in the handle — including a doubled separator — survives byte-for-byte.

### Safety

Nothing is inferred. A mapping is applied only where it was declared, and:

- **Strict parsing.** An unrecognised key is an error, not a silent no-op — a misspelled
  `namespacs:` would otherwise be a transform you believe is happening but is not.
- **Unused mappings are reported.** A mapping matching no object is almost always a typo,
  and left unreported you would discover it only when a pod failed to mount.
- **Collisions are refused up front.** Collapsing namespaces where claim names repeat is
  caught before a single object is created, rather than half-populating the cluster.
- **Unset `${VAR}` is an error.** Silently writing an empty password would produce a Secret
  that fails authentication at first mount, with nothing pointing at the cause.
- **Credentials never appear in logs.** `--log-level debug` prints every rewrite; secret
  values are shown as `<overridden>`.

### Redacted archives

A transform can supply credentials the export redacted, so the normal cross-cluster flow
needs no `--include-secret-data` at all — the target has different credentials anyway:

```bash
weka-csi-migrator export -o prod.wcsi                       # redacted, no password
WEKA_DR_PASSWORD=... weka-csi-migrator import prod.wcsi --transform-file dr.yaml
```

Transforms run **before** the redaction check, so this succeeds where a plain import of a
redacted archive would be refused.

### Still to come

Weka replicates data, but **directory quotas are not carried across**, so capacity on
imported PVs is nominal until reconciliation exists. A Weka API `validator` hook is stubbed
as a no-op: it will pre-flight that filesystems and paths exist and that quotas are present,
and later reconcile them.

## Testing

```bash
make migrator-test
```

The suite runs export and import against fake clusters end to end. The assertions that
matter most: handles survive byte-for-byte including the doubled separator, claims come back
pinned to their volumes, foreign CSI drivers are never claimed, credentials do not leak into
a redacted export, and imports never overwrite live objects.
