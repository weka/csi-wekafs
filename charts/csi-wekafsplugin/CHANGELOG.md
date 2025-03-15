<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release provides ability to provision encrypted volumes using a cluster-wide KMS configuration.
The encryption functionality is not complete and will provide additional abilities in next major version.

### New features
* feat(CSI-315): partial support of encrypted volumes for FS backing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/442
### Improvements
* refactor(CSI-330): use native kubernetes client for handling labels and remove reliance on kubectl init container by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/460
* fix(CSI-331): add terminationGracePeriodSeconds to controller and pod workloads by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/462
* feat(CSI-337): no readiness check and labels removal when weka client not running by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/469
* feat(CSI-340): use API for inode resolution on path for setting quota by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/473
* feat(CSI-341): cache filesystem and snapshot objects to avoid multiple similar API calls by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/474
### Bug Fixes
* fix(CSI-326): invalid socket dir path causing 2 instances of CSI plugin to interfere with each other by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/453
* fix(CSI-329): report volume accessible topology with label corresponding to driver name for multiple instances in large clusters by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/461
* fix(CSI-332): collision on OCP machineConfigs when installing multiple instances due to same name by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/463
* fix(CSI-330): label cleanup should be done only on node server by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/472
* fix(CSI-333): acl mount option mishandled by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/468
* fix(CSI-343): panic on apiclient when endpoint list is empty by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/475
* fix(CSI-350): avoid wekafs mounter from mounting if weka is not running by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/484
* fix(CSI-351): incorrect error message when creating PVC with CSI secret missing endpoints by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/485
* fix(CSI-352): topology.csi.weka.io/transport=wekafs & topology.wekafs.csi/node labels missing on weka client restart by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/486
* fix: do not wait 2 times when deleting nfs permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/488
### Miscellaneous
* ci(chore): update workflows to use larger server group by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/459
* chore(deps): update go dependencies as of 2025-02-16 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/464
* chore(deps): update go dependencies as of 2025-03-15 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/487
### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in stale file handle error when trying to access the volume contents from within the pod.This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

