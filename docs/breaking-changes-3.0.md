# Breaking changes in WEKA CSI Plugin 3.0

Version 3.0 removes the legacy, API-less communication model. Every volume operation now goes
through the WEKA REST API, and every volume must resolve to an API secret.

Read this before upgrading. If your cluster still has volumes provisioned in the legacy model, the
upgrade will leave them unusable — see [What to do about existing legacy
volumes](#what-to-do-about-existing-legacy-volumes).

## The legacy communication model is gone

Up to 2.x, the plugin could operate a volume with no API credentials at all. In that mode it never
called the WEKA REST API: capacity was written into an extended attribute on the volume directory,
mounts were unauthenticated, and quota enforcement was unavailable.

That mode has been removed. In 3.0:

* A `CreateVolume`, `DeleteVolume`, `ExpandVolume` or mount request that arrives without API
  credentials **fails with a clear error** instead of silently falling back to degraded behaviour.
* The StorageClass must reference an API secret through the standard CSI secret parameters
  (`csi.storage.k8s.io/provisioner-secret-name` and its namespace counterpart, and the matching
  controller-expand and node-publish parameters).
* Mounting a filesystem always requests a mount token from the API, so authenticated mounts,
  SCMC and non-root organizations work uniformly.

### `legacyVolumeSecretName` has been removed

The `legacyVolumeSecretName` Helm value, and the cluster-wide secret it mounted into the plugin
pods at `/legacy-volume-access`, are gone. It existed so that volumes from a legacy StorageClass —
which cannot be edited to add a secret reference, because StorageClass parameters are immutable —
could still reach the API through one global fallback.

With the legacy model removed there is nothing for it to serve.

> **Check your `values.yaml` before upgrading.** The chart's schema does not reject unknown keys, so
> a `values.yaml` that still sets `legacyVolumeSecretName` installs without any warning — the
> setting is simply ignored, and the secret is no longer mounted. Nothing tells you at upgrade time
> that a volume relying on it has stopped working.

## Directory-backed volumes are *not* affected

This is the most common misreading of this change, so to be explicit:

**`dir/v1` volumes continue to work and are fully supported.** They are not deprecated and they are
not removed. What changed is only their name in the documentation and in log messages: they are now
called **directory-backed** volumes rather than "legacy" volumes, because "legacy" now refers to
exactly one thing — the API-less communication model.

A `dir/v1` volume that references an API secret works in 3.0 exactly as it did in 2.x. Volume
handles are unchanged, so nothing needs to be rewritten.

## Volumes with no quota, and why capacity is still read from extended attributes

Capacity for a WEKA CSI volume belongs in a **directory quota** on the WEKA cluster. Extended
attributes on the volume directory (`user.weka_capacity`) are a much older mechanism, kept only for
clusters too old to support directory quotas as volumes.

3.0 still reads and writes those attributes. That is deliberate, and it is a stay of execution
rather than a design decision:

> **Due to a bug in earlier CSI Plugin versions, some volumes were created without a quota ever
> being set on the WEKA cluster.** For those volumes the extended attribute is the *only* record of
> their capacity. Removing extended-attribute support today would break them — the plugin would
> have nothing left to read a capacity from.

The attributes will be removed in a later release. Before that can happen, affected volumes have to
be repaired, and the repair is not something the plugin can do on its own during an upgrade: it has
to reconcile Kubernetes objects against WEKA cluster state.

### The intended fix

A migration readiness script will:

* list every PersistentVolume in the cluster and keep those provisioned by the `csi.weka.io` driver;
* look up each one's quota entry on the WEKA cluster through the REST API;
* where no quota exists, create one matching the capacity declared on the PersistentVolume.

The extended attribute is **disregarded entirely** in that flow. The PersistentVolume is the source
of truth for what the volume's capacity is supposed to be; the attribute is at best a copy of it and
at worst stale.

This script does not ship yet. Until it does, and until you have run it, treat any volume that has
no quota on the WEKA cluster as depending on extended attributes, and do not assume a future release
will keep reading them.

> The older `migration/migrate-legacy-csi-volumes.sh` is **not** this script. It walks a filesystem
> rather than the PersistentVolume list, takes its capacity from the extended attribute rather than
> from the PersistentVolume, and drives the `weka` CLI rather than the REST API.

## `debugPath` and dev mode have been removed

The `--debugpath` command line flag is gone, along with the "dev mode" it enabled. In that mode the
plugin bind-mounted a local directory instead of mounting WekaFS, and fabricated a directory tree
under `.snapshots` to imitate snapshot behaviour so the CSI sanity suite could run with no WEKA
cluster attached.

It has no production use, and the imitation diverged from real cluster behaviour often enough to
hide bugs rather than surface them. The CSI sanity suite now runs against a real WEKA cluster over
NFS instead.

If you pass `--debugpath` to the plugin, **it will fail to start with an unknown-flag error.**

## What to do about existing legacy volumes

**Migration of legacy volumes into 3.0 is not supported.** There is no in-place upgrade path, and
the `migration/migrate-legacy-csi-volumes.sh` procedure documented for earlier releases does not
apply to 3.0.

The reason is that a legacy volume's identity is bound to a StorageClass with no secret reference.
StorageClass parameters are immutable, so such a volume can never be made to resolve an API secret
in place, and 3.0 has no fallback left to catch it.

Your data is not affected: it is on the WEKA cluster, and none of these procedures touch it. What
has to be rebuilt is the Kubernetes object describing it. Two options:

1. **Before upgrading**, while still on 2.x, complete the legacy-to-API migration described in
   [Upgrade legacy persistent volumes for capacity enforcement](../migration/upgrade-legacy-pv.md).
   Volumes that end up with a real API secret reference carry over to 3.0 unchanged.

2. **If you have already upgraded**, or prefer not to run the 2.x migration, recreate each legacy
   volume as a static one. The data stays in place throughout. See
   [Recreate a legacy PersistentVolume as a static volume](../migration/recreate-legacy-pv-as-static.md).

## Summary of removed settings

| Removed | Kind | Replacement |
| --- | --- | --- |
| `legacyVolumeSecretName` | Helm value | Reference an API secret from each StorageClass |
| `/legacy-volume-access` | Secret mount path | none |
| `--debugpath` | Plugin command line flag | none |

Still present in 3.0, but scheduled for removal — see [Volumes with no quota, and why capacity is
still read from extended attributes](#volumes-with-no-quota-and-why-capacity-is-still-read-from-extended-attributes):

| Retained for now | Why |
| --- | --- |
| `user.weka_capacity` extended attribute | The only capacity record for volumes created without a quota by an earlier CSI Plugin bug |
