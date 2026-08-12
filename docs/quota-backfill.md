# Giving existing volumes their missing quota

A WEKA CSI volume's capacity is enforced by a **directory quota** on the WEKA cluster. A volume
without one still works — it just has no enforcement, and can grow past its declared size unnoticed.
Its size is recorded only in an extended attribute, which nothing checks.

The volume health reconciler can repair those volumes in place. It already sweeps every
PersistentVolume of the driver, so the repair adds no enumeration of its own, and there is no script
to remember to run.

## Settings

All three live under `controller.healthMonitor`, and all three are off by default.

| Setting | What it does |
| --- | --- |
| `backfillMissingQuotas` | Create a quota for a **dynamically provisioned** volume that has none, sized from its PersistentVolume |
| `setQuotaOnStaticVolumes` | Extend that to **statically provisioned** volumes. Requires `backfillMissingQuotas` |
| `reportVolumesWithoutQuotaAsAbnormal` | Report a volume with no quota as **abnormal**, raising a warning event on its PersistentVolumeClaim. Independent of the other two |

`setQuotaOnStaticVolumes` is separate because a static volume is yours: the plugin did not create it,
never set its quota, and its documented behaviour is that you set one yourself. Turning it on starts
enforcing a limit that was not being enforced before, using the size declared on the
PersistentVolume — check that size is the one you want first.

`reportVolumesWithoutQuotaAsAbnormal` is separate because reporting and repairing are different
decisions. You can report without repairing (to see the scale of the problem before acting), or
repair quietly without flooding every affected PersistentVolumeClaim with warning events.

## Where the values come from

| Value | Source |
| --- | --- |
| Capacity | `spec.capacity.storage` on the PersistentVolume |
| Hard or soft | `capacityEnforcement` in the PersistentVolume's `volumeAttributes` |
| Grace period | `quotaGracePeriod` in the same place |

The StorageClass parameters a volume was provisioned with are persisted verbatim into
`volumeAttributes`, so they are readable long after the StorageClass may have changed — and a
StorageClass cannot be edited anyway. For a statically provisioned volume you write those attributes
yourself, so the same lookup honours your choice.

The capacity is deliberately **not** the capacity the health probe reported. A volume with no quota
has no limit for the backend to report, which makes the probed figure exactly the wrong number. The
extended attribute is not read either: it is at best a copy of the same number, at worst stale, and
reading it would mean mounting the volume, which the reconciler never does.

An unusable `capacityEnforcement` or `quotaGracePeriod` is an error, not a guess — better to leave a
volume without a quota than to give it the wrong kind. Those values are read before the cluster is
contacted, so a misconfigured volume fails without a round trip.

## What happens in each case

Creating a quota on a directory that **already holds data** makes the cluster walk the whole tree,
stamping the quota ID onto every file — the `QUOTA_COLORING` task. A **data services container** runs
that walk in the background. An **empty** directory needs no colouring at all.

The plugin does not try to predict which case a volume is in. Finding out whether a directory is
empty would mean mounting it, and the reconciler runs on the controller, which never mounts a volume.
So the request is made, and the cluster reports what it could not do.

| Case | Outcome | Message | Condition |
| --- | --- | --- | --- |
| Directory is **empty**, no quota, inode resolves | **Quota created.** No colouring is needed, so this succeeds on any cluster, with or without a data services container | `Created quota for volume`, with the capacity, enforcement and grace period applied | Healthy. The volume now has enforcement |
| Directory is **not empty**, cluster supports data services but **has no container** | **No quota.** Retried on the next sweep | The cluster's own error, then: *"If the directory already holds data, this needs a data services container on the Weka cluster to colour the existing files, and the cluster has none — deploy one and the quota will be created on a later sweep"* | `volume exists on the Weka cluster, but has no quota, so its capacity is not enforced`. Abnormal only if `reportVolumesWithoutQuotaAsAbnormal` is on |
| Directory is **not empty**, cluster is **older than v4.3** and cannot run data services | **No quota.** Will not succeed until the cluster is upgraded or the quota is set by hand | The cluster's own error, then: *"…the Weka cluster is older than v4.3 so it cannot run one. Either upgrade it, or set the quota externally from a host with the Weka client and the filesystem mounted: `weka fs quota set <path> --filesystem <fs> --type directory --hard <bytes>`"* | As above |

In every failing case the cluster's own error is preserved and the remedy appended to it, so a
failure that is *not* about colouring — a permission problem, an unreachable cluster — is not
misattributed to data services.

### The condition, and why it is healthy by default

A volume with no quota reports its condition as:

```
volume exists on the Weka cluster, but has no quota, so its capacity is not enforced
```

It is **not** marked abnormal unless you ask for it. The volume works; what is missing is
enforcement. On a fleet part-way through repair, or one holding statically provisioned volumes that
have no quota by design, marking every one abnormal raises a warning event on every affected
PersistentVolumeClaim at once — burying the genuinely broken volumes the abnormal flag exists to
surface.

Set `reportVolumesWithoutQuotaAsAbnormal: true` when you want those events, for instance while
driving a repair to completion.

## Seeing the scale of it

Every sweep logs a summary, **whether or not any of these settings are on** — the count is free,
because the health probe already establishes it:

```
Volume health reconciliation completed  volumes=4820 quotas_missing=37 quotas_created=35 quotas_not_created=2
```

* `quotas_missing` — volumes with no quota at the start of the sweep
* `quotas_created` — repaired during it
* `quotas_not_created` — attempted and failed, each logged individually with its reason

So `quotas_missing` answers "how many of my volumes have no enforcement" before you enable anything.

## Deploying a data services container

Required only for volumes whose directories already hold data. Needs WEKA **v4.3** or newer; under
the WEKA Operator, **operator 1.13 with cluster 5.1.20**.

Point the data services configuration at a filesystem first:

```bash
weka dataservice global-config set --config-fs .config_fs
```

Under the WEKA Operator, add the container to the `WekaCluster`:

```yaml
spec:
  dynamicTemplate:
    dataServicesContainers: 1
    dataServicesCores: 1
    dataServicesFeCores: 0    # must be 0 when dataServicesContainers > 0
```

The container needs a node not already running a WEKA client container, roughly 5.5 GB of memory,
and `.config_fs` grown to about 122 GB.
