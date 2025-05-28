# Release v2.7.3
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* fix(WEKAPP-490309): use resolution of inode via API for CSI role starting from Weka 4.4.7 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/517
* feat(CSI-359): allow retention of SElinux policy machine configuration on OCP clusters by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/515
### Bug Fixes
* fix(CSI-358): rotation to another API andpoint does not happen on error 503 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/514
### Miscellaneous
* ci: make sure to use latest docker buildx to support GH cache v2 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/516


# Release v2.7.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This version incorporates minor performance improvements and switches tracing from Jaeger to OTLP
### Improvements
* feat(CSI-342): get filesystem free space via API without requiring fs mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/476
### Miscellaneous
* feat(CSI-317): switch from jaeger to otlptracegrpc by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/492


# Release v2.7.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release includes bug fixes and stability improvements

### Improvements
* feat(CSI-356): avoid failback to xattr upon quota set error by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/494
### Bug Fixes
* feat(CSI-355): if user of role CSI cannot resolvePath via API, switch to mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/493
* fix(CSI-357): server default mount options take precedence over custom ones by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/495

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in stale file handle error when trying to access the volume contents from within the pod.This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

# Release v2.7.0
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

# Release v2.7.0-beta
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release provides ability to provision encrypted volumes using a cluster-wide KMS configuration.
The encryption functionality is not complete and will provide additional abilities in next major version.

### New features
* feat(CSI-315): partial support of encrypted volumes for FS backing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/442
### Bug Fixes
* fix(CSI-326): invalid socket dir path causing 2 instances of CSI plugin to interfere with each other by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/453


# Release v2.6.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This version resolves issues that could occur during accessing CSI-published volumes on SELinux-enabled nodes.
Since the issue is related to switching to RedHat Universal Base Image (UBI9), the interim solution is to revert switching to UBI.
In the following versions, a better solution will be incorporated and the plugin will be again based on UBI9 image.

### Improvements
* refactor(CSI-272): move NFS client registration to APIClient startup rather than on each mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/436
* refactor(CSI-318): add configurable wait for filesystem / snapshot deletion by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/437
### Bug Fixes
* fix(CSI-322): revert CSI-309 migrate from Alpine to RedHat UBI9 base image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/448
* fix(CSI-320): print raw entry in log when endpoint address fails to be parsed" by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/443
* fix(CSI-323): when snapshot of directory backed volumes is prohibited, incorrect error message is shown stating volume is legacy by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/449
### Miscellaneous
* chore(deps): optimize CSI sanity speed during CI by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/438


# Release v2.6.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-321): provide ability to add custom labels to CSI components by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/445
### Miscellaneous
* chore(deps): update helm/chart-testing-action action to v2.7.0 by @renovate in https://github.com/weka/csi-wekafs/pull/433


# Release v2.6.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-300): add arm64 support by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/379* 
* feat(CSI-312): add topology awareness by providing accessibleTopology in PV creation by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/426
* feat(CSI-313): add configuration for skipping out-of-band volume garbage collection by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/427
### Improvements
* feat(CSI-310): drop container_name mount option from volume context by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/408
* feat(CSI-311): add CSI driver version used for provisioning a PV into volumeContext by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/409
* feat(CSI-308): add support for ReadWriteOncePod, ReadOnlyOncePod by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/399
* feat(CSI-309): migrate from Alpine to RedHat UBI9 base image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/400
### Bug fixes
* refactor(CSI-305): change mount Map logic for WEKAFS to align with NFS and support same fs name on SCMC by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/383
* chore(deps): improve the way of locar to delete multi-depth directories by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/422
* fix(CSI-306): compatibility for sync_on_close not logged by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/395
### Miscellaneous
* chore(deps): add LICENSE to UBI /licenses by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/418
* chore(deps): update golang dependencies as of 2024-12-09 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/410
* chore(deps): update helm/kind-action action to v1.12.0 by @renovate in https://github.com/weka/csi-wekafs/pull/414
* chore(deps): update registry.access.redhat.com/ubi9/ubi to v9.5-1736404036 by @renovate in https://github.com/weka/csi-wekafs/pull/421
* fix(deps): update golang.org/x/exp digest to 7588d65 by @renovate in https://github.com/weka/csi-wekafs/pull/407
* fix(deps): update module google.golang.org/grpc to v1.69.4 by @renovate in https://github.com/weka/csi-wekafs/pull/406
* fix(deps): update module google.golang.org/protobuf to v1.36.2 by @renovate in https://github.com/weka/csi-wekafs/pull/415
* chore(deps): add labels to CSI Docker image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/425
* chore(deps): update go dependencies as of 2025-01-19 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/429


# Release v2.5.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-295): add affinity for controller and separated nodeSelector for controller and node by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/377
* feat(CSI-302): convert controller StatefulSet to Deployment by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/381
* feat(CSI-303): add livenessProbe to attacher sidecar by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/382
### Bug Fixes
* fix(CSI-294): caCertificate, NfsTargetIps, localContainerName are not hashed in API client by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/371
* fix(CSI-292): parse NFS version 3.0 to correctly pass it to mountoption by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/372
* fix(CSI-297): nfsTargetIps override is handled incorreclty when empty by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/374
* fix(CSI-296): node registration fails after switch transport from NFS to Wekafs due to label conflict by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/375
* feat(CSI-301): bump locar to version 0.4.2 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/380
### Miscellaneous
* docs: fix the example of static provisioning of directory-backed volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/370
* chore(deps): update actions/checkout digest to 11bd719 by @renovate in https://github.com/weka/csi-wekafs/pull/352
* fix(deps): update kubernetes packages to v0.31.2 by @renovate in https://github.com/weka/csi-wekafs/pull/376
* chore(deps): update registry.k8s.io/kubernetes/kubectl to v1.31.2 by @renovate in https://github.com/weka/csi-wekafs/pull/373
* fix(deps): update golang.org/x/exp digest to f66d83c by @renovate in https://github.com/weka/csi-wekafs/pull/349
* fix(deps): update module github.com/prometheus/client_golang to v1.20.5 by @renovate in https://github.com/weka/csi-wekafs/pull/369

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in `stale file handle` error when trying to access the volume contents from within the pod. 
  This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

# Release v2.5.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
   This enhancement allows providing a base64-encoded CA certificate in X509 format for secure API connectivity
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
   This feature provides a way to provision and publish WEKA CSI volumes via NFS transport for clusters that cannot be installed with Native WEKA client software. For additional information, refer to https://github.com/weka/csi-wekafs/blob/main/docs/NFS.md
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
   This feature provides a way to monitor WEKA CSI volume usage statistics via kubelet statistics collection. 
   The following statistics are supported:
    * `kubelet_volume_stats_capacity_bytes`
    * `kubelet_volume_stats_available_bytes`
    * `kubelet_volume_stats_used_bytes`
    * `kubelet_volume_stats_inodes`
    * `kubelet_volume_stats_inodes_free`
    * `kubelet_volume_stats_inodes_used`

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in `stale file handle` error when trying to access the volume contents from within the pod. 
  This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
* fix(CSI-274): add sleep before mount if nfs was reconfigured by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/353
* chore(deps): add OTEL tracing and span logging for GRPC server by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/361
* feat(CSI-288): validate API user role prior to performing ops by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/365
* feat(CSI-289): add default nfs option for rdirplus by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/368
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
* fix(CSI-273): remove rdirplus from mountoptions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/355
* fix(CSI-275): version of NFS is only set to V4 during NFS permission creation by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/354
* fix(CSI-276): allow unpublish even if publish failed with stale file handle by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/356
* feat(CSI-286): whitespace not trimmed for localContainerName in CSI secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/364
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340
* chore(deps): update Go dependencies to latest by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/357

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.5.0-beta2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
### Bug Fixes
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
### Miscellaneous
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340


**Full Changelog**: https://github.com/weka/csi-wekafs/compare/v2.5.0-beta...main

<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.5.0-beta
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.4.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* fix(CSI-226): support IPv6 in APIclient by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/287
* feat(CSI-227): allow host networking via configuration by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/288
### Improvements
* fix(CSI-237): increase parralelism of PV deletions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/295
### Bug Fixes
* fix(CSI-224,WEKAPP-417375): race condition on multiple volume deletion in parallel by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/286
* fix(CSI-236): for OCP installations, only 1 machineConfigPolicy was created by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/294
### Miscellaneous
* chore(deps): update dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/278
* chore(deps): put installation slack link in code block by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/291
* chore(deps): allow WEKAPP tickets in lint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/290
* chore(deps): bump Go dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/297


# Release v2.3.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* fix(CSI-226): support IPv6 in APIclient by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/287
* feat(CSI-227): allow host networking via configuration by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/288
### Improvements
* fix(CSI-237): increase parralelism of PV deletions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/295
### Bug Fixes
* fix(CSI-224,WEKAPP-417375): race condition on multiple volume deletion in parallel by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/286
* fix(CSI-236): for OCP installations, only 1 machineConfigPolicy was created by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/294
### Miscellaneous
* chore(deps): update dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/278
* chore(deps): put installation slack link in code block by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/291
* chore(deps): allow WEKAPP tickets in lint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/290
* chore(deps): bump Go dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/297


# Release v2.4.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New Features
* feat(CSI-211): support new API paths nodes->processes as per cluster version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-215): improve lookup for frontend containers to include protocols by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-209): automatically update API endpoints on re-login by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-221): support configurable fsGroupPolicy by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-219): add securityContextConstraints for CSI on OCP by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-220): automatically determine selinux for OCP nodes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269

### Bug Fixes
* fix(CSI-217): Containers are filtered by status but not by state by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* fix(CSI-223): mount still attempted when local container name is missing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269

### Miscellaneous
* chore(deps): update azure/setup-helm action to v4 by @renovate in https://github.com/weka/csi-wekafs/pull/243
* chore(deps): update helm/kind-action action to v1.10.0 by @renovate in https://github.com/weka/csi-wekafs/pull/240
* chore(deps): update actions/checkout digest to 692973e by @renovate in https://github.com/weka/csi-wekafs/pull/256
* fix(deps): update module github.com/google/uuid to v1.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/221
* fix(deps): update golang.org/x/exp digest to 7f521ea by @renovate in https://github.com/weka/csi-wekafs/pull/257
* fix(deps): update module google.golang.org/grpc to v1.64.0 by @renovate in https://github.com/weka/csi-wekafs/pull/224
* fix(deps): update module github.com/rs/zerolog to v1.33.0 by @renovate in https://github.com/weka/csi-wekafs/pull/235
* chore(deps): update docker/build-push-action action to v6 by @renovate in https://github.com/weka/csi-wekafs/pull/264
* fix(deps): update module google.golang.org/protobuf to v1.34.2 by @renovate in https://github.com/weka/csi-wekafs/pull/263
* chore(deps): update softprops/action-gh-release action to v2 by @renovate in https://github.com/weka/csi-wekafs/pull/265
* fix(deps): update module github.com/hashicorp/go-version to v1.7.0 by @renovate in https://github.com/weka/csi-wekafs/pull/260
* chore(deps): update dependency go to v1.22.4 by @renovate in https://github.com/weka/csi-wekafs/pull/259


# Release v2.3.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->



# Release v2.3.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Bug Fixes
* fix(CSI-170): error not reported when moving directory to trash by @sergeyberezansky in in https://github.com/weka/csi-wekafs/pull/184

### Miscellaneous
* chore(deps): update helm/chart-testing-action action to v2.6.1 by @renovate in https://github.com/weka/csi-wekafs/pull/184
* chore(deps): update helm/chart-releaser-action action to v1.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/183


# Release v2.3.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Features
* feat(CSI-166): update CSI spec to 1.9.0 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/178

### Bug Fixes
* fix(CSI-163): missing ca-certificates package in wekafs container image by @sergeyberezansky  in https://github.com/weka/csi-wekafs/pull/179

### Miscellaneous
* chore(deps): update actions/checkout digest to b4ffde6 by @renovate in https://github.com/weka/csi-wekafs/pull/161
* chore(deps): update stefanzweifel/git-auto-commit-action action to v5 by @renovate in https://github.com/weka/csi-wekafs/pull/167
* chore(deps): update helm/chart-testing-action action to v2.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/181
* chore(deps): bump dependencies  by @sergeyberezansky  in https://github.com/weka/csi-wekafs/pull/177


# Release v2.3.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-159): add weka driver monitoring for readiness probe by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/58
### Miscellaneous
* chore(deps): update actions/checkout action to v4 by @renovate in https://github.com/weka/csi-wekafs/pull/152
* fix(deps): update kubernetes packages to v0.28.1 by @renovate in https://github.com/weka/csi-wekafs/pull/139
* fix(deps): update module github.com/google/uuid to v1.3.1 by @renovate in https://github.com/weka/csi-wekafs/pull/148
* fix(deps): update module github.com/rs/zerolog to v1.30.0 by @renovate in https://github.com/weka/csi-wekafs/pull/146
* fix(deps): update module google.golang.org/grpc to v1.58.0 by @renovate in https://github.com/weka/csi-wekafs/pull/145
* fix(deps): update module github.com/kubernetes-csi/csi-lib-utils to v0.15.0 by @renovate in https://github.com/weka/csi-wekafs/pull/149
* fix(deps): update opentelemetry-go monorepo to v1.17.0 by @renovate in https://github.com/weka/csi-wekafs/pull/151
* fix(deps): update golang.org/x/exp digest to 9212866 by @renovate in https://github.com/weka/csi-wekafs/pull/144
* chore(deps): update docker/build-push-action action to v5 by @renovate in https://github.com/weka/csi-wekafs/pull/154
* chore(deps): update docker/login-action action to v3 by @renovate in https://github.com/weka/csi-wekafs/pull/155
* chore(deps): update docker/setup-buildx-action action to v3 by @renovate in https://github.com/weka/csi-wekafs/pull/156


# Release v2.2.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->



# Release v2.2.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-122): support multiple Weka clusters on same nodes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/134
### Miscellaneous
* fix(deps): update module google.golang.org/grpc to v1.56.2 by @renovate in https://github.com/weka/csi-wekafs/pull/135
* fix(deps): update golang.org/x/exp digest to 613f0c0 by @renovate in https://github.com/weka/csi-wekafs/pull/136
* chore(deps): update helm/kind-action action to v1.8.0 by @renovate in https://github.com/weka/csi-wekafs/pull/137


# Release v2.1.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* feat(CSI-57): acl mount option by @dontbreakit in https://github.com/weka/csi-wekafs/pull/128
* fix(CSI-118): cannot initialize API client with non-root organization by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/131
### Miscellaneous
* ci(CSI-116): prefix v for all components validation, also CSI-117 by @dontbreakit in https://github.com/weka/csi-wekafs/pull/129
* fix(deps): update golang.org/x/exp digest to 97b1e66 by @renovate in https://github.com/weka/csi-wekafs/pull/126
* fix(deps): update module google.golang.org/protobuf to v1.31.0 by @renovate in https://github.com/weka/csi-wekafs/pull/125


# Release 2.1.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Bug fixes
* fix(CSI-75): compatibilityMap has duplicate parameter for same functionality https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-76): filtering Rest API allowed only from 4.1 but should be from 4.0 https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-110): CSI does not propagate error when failing to init API client from secrets https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-112): panic when creating CSI snapshot-based volume and failing to initialize API client https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-113) plugin incorrectly handles secret with API endpoints separated by newline rather than comma https://github.com/weka/csi-wekafs/pull/120

### Miscellaneous
* fix(CSI-111): Replace deprecated ioutil.ReadFile / WriteFile https://github.com/weka/csi-wekafs/pull/120
* docs(CSI-115): document incorrectly states version of Weka for snapshot quotas https://github.com/weka/csi-wekafs/pull/123

**Full Changelog**: https://github.com/weka/csi-wekafs/compare/v2.1.0...v2.1.1

# Release v2.1.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-67): sign helm chart by @dontbreakit in https://github.com/weka/csi-wekafs/pull/116


### Security
* fix(CSI-109): update registry.k8s.io/sig-storage/csi-snapshotter to v6.2.2 by @renovate in https://github.com/weka/csi-wekafs/pull/113
* update Golang dependencies for the csi binary
  * fix(deps): update module golang.org/x/sync to v0.3.0 by @renovate in https://github.com/weka/csi-wekafs/pull/105
  * fix(deps): update module k8s.io/apimachinery to v0.27.3 by @renovate in https://github.com/weka/csi-wekafs/pull/106
  * fix(deps): update module github.com/prometheus/client_golang to v1.16.0 by @renovate in https://github.com/weka/csi-wekafs/pull/107
  * fix(deps): update module google.golang.org/grpc to v1.56.1 by @renovate in https://github.com/weka/csi-wekafs/pull/108
  * fix(deps): update module github.com/kubernetes-csi/csi-lib-utils to v0.14.0 by @renovate in https://github.com/weka/csi-wekafs/pull/117


# Release v2.0.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix(CSI-74): no error returned when fetching info from weka cluster fails by @dontbreakit & @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/102
* fix(CSI-107): revert csi-attacher by @dontbreakit in https://github.com/weka/csi-wekafs/pull/103


# Release 2.0.0
<!-- Release notes generated using configuration in .github/release.yaml at master -->
## What's Changed
Weka CSI Plugin v2.0.0 has a comprehensive set of improvenents and new functionality:
* Support of different backings for CSI volumes (filesystem, writable snapshot, directory)
* CSI snapshot and volume cloning support
* `fsGroup` support
* Custom mount options per storageClass
* Redundant CSI controllers
* Restructuring of CI and release workflows

> **NOTE:** some of the functionality provided by Weka CSI Plugin 2.0.0 requires Weka software of version 4.2 or higher. Please refer to [documentation](README.md) for additional information

> **NOTE:** To better understand the different types of volume backings and their implications, refer to documentation.

### New features
* feat: Support of new volumes from content source by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/11
* feat: Support Mount options by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/18
* feat: Add fsGroup support on CSI driver by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/20
* feat: Support different backing types for CSI volumes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/69
* feat: official support for multiple controller server replicas by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/47
 
### Improvements
* feat: configurable log format (colorized human-readable logs or JSON structured logs) by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/26
* feat: OpenTelemetry tracing support by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/26
* feat: support of mutually exclusive mount options by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/54
* feat: Add concurrency limitation for multiple requests by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/56
* refactor: concurrency improvements by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/68

### Bug Fixes
* fix: Correctly calculate capacity for FS-based volume expansion (fixu… by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/15
* refactor: do not recover lost mounts and shorten default mountOptions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/21
* fix: plugin might crash when trying to create dir-based volume on non… by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/29
* fix: CSI-47 Snapshot volumes run out of space after filling FS space by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/35
* fix: WEKAPP-298226 volumes published with ReadOnlyMany were writable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/36
* fix: initial filesystem capacity conversion to bytes is invalid by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/38
* fix: loozen snapshot id validation for static provisioning by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/41
* fix: re-enable writecache by default by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/51
* fix: make sure op is written correctly for each function by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/67

### Miscellaneous
* style: add more logging to initial FS resize by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/37
* Add Helm linting and install test by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/13
* Push updated docs to main branch straight after PR merge by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/19
* docs: modify helm docs templates by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/22
* chore: add S3 chart upload GH task by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/23
* chore: auto increase version on feat git commit by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/24
* feat: Bump versions of packages by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/25
* chore: change docker build via native buildx GH action by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/27
* ci: add csi-sanity action to PRs by @dontbreakit in https://github.com/weka/csi-wekafs/pull/30
* ci: add release action by @dontbreakit in https://github.com/weka/csi-wekafs/pull/34
* docs: Improve documentation on mount options and different volume types by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/39
* chore: Bump CSI sidecar images to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/42
* docs: fix capacityEnforcement comment inside storageClass examples by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/46
* Add notifications to slack by @dontbreakit in https://github.com/weka/csi-wekafs/pull/53
* docs: Improve release.yaml to include additional PR labels by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/70

## Upgrade Implications
In order to support `fsGroup` functionality, the CSIDriver manifest had to be modified. Since this type of Kubernetes objects is defined as immutable, upgrading Helm release with the new version might fail.
Hence, when upgrading from version below 2.0.0, a complete uninstall and reinstall of Helm release is required. 
> NOTE: it is not required to remove any Secrets, storageClass definitions, PersistentVolumes or PersistentVolumeClaims.

## Deprecation Notice
Support of legacy volumes without API binding will be removed in next major release of Weka CSI Plugin. New features rely on API connectivity to Weka cluster and will not be supported on API unbound volumes. Please make sure to migrate all existing volumes to API based scheme prior to next version upgrade. 

# Release 0.8.4
## Bug Fixes
- Fixed an error which caused the CSI Node component to fail starting on Selinux-enabled hosts
- Fixed installation notes to correctly show the helm commands required for seeing the release

# Release 0.8.3
## Bug Fixes
- Fixed a race condition due to which CSI Node component running on same node with 
  CSI Controller component could fail to start

# Release 0.8.2
## Bug Fixes
- Fixed README.md to correct SELinux README.md URL

# Release 0.8.1
## Bug Fixes
- Fix invalid link to CSI SELinux documentation on ArtifactHub page
- Fix version strings are not updated inside Helm chart README.md

# Release 0.8.0
## New features
### SELinux support
Weka CSI Plugin can now work with SELinux-enabled Kubernetes clusters.  
> **NOTE:** Special configuration is required to deploy the Weka CSI plugin in SELinux-compatible mode  
> Refer to [SELinux Support Readme](selinux/README.md) for additional information
## Improvements
- Helm Charts were separated on per-object basis for better supportability
- Custom `kubelet` path may be set, e.g. for using Kubernetes installed into non-default directory 

## Bug Fixes
- Part of new settings in `values.yaml` were not documented
- Improved logging on failure to mount a filesystem due to authorization error
- Fixed a situation in which `csi-registrar` container (part of node server) could enter crash loop due to `csi.Node.v1` not found

# Release 0.7.4
## New features
### Support for authenticated FileSystems and additional organizations
This functionality is supported for Weka clusters of version 3.14 and up
- Filesystems set with auth-required=true can be used for CSI volumes
- Filesystems in non-root organization can be used for CSI volumes

# Release 0.7.3
## Improvements
- Volume ownership and permissions configuration can be set via [storageClass parameters](examples/dynamic_api/storageclass-wekafs-dir-api.yaml)
- Automated doc generation via helm-doc

# Release 0.7.2
## Improvements
- Upgrade sidecar components to latest versions on gcr.io

# Release 0.7.1
## Improvements
- Upgrade sidecar components to latest versions

# Release 0.7.0
## New Features
### Directory Quota support via Weka REST API
- When new dir/v1 volume is created, it is automatically bound to API quota object
- Can be set to either HARD (forbid writes with E_NOSPC) or SOFT (alerts only)
- Supported for dynamic volumes only in this version
- Requires a modification of storage class (or creation of new storage class)
- Requires a Secret creation that contains API connection information
- Current limitation: only new volumes will be set with quota. For setting quota on existing volumes, use migration script in `migration` directory
- Old volumes can be still expanded using a Legacy API secret (see values.yaml), but user is highly encouraged to migrate workloads to new storage class
- Requires Weka software of v3.13 and above. If cluster version is below v3.13, quotas will not be applied.

### Multiple Weka Clusters on same Kubernetes Control Plane
Multiple simultaneous Weka clusters are now supported by a single CSI controller.
This configuration implies a large Kubernetes cluster, which is connected to multiple
Weka clusters, e.g. in different availability zones. 

In such case, single CSI controller may take care of provisioning all volumes.
Please remember to utilize PVC annotations to ensure the PVC is bound to correct Kubernetes node.
>**NOTE:** Support for making a single Kubernetes node a member in multiple Weka clusters
> is not available at this time, and will be introduced in future Weka software versions.

## Improvements
- Build process simplified and Dockerized  
  This allows developers to build the software from sources locally
- Release process was streamlined
- Logging improvements were introduced with refined log levels
- New examples provided for using Weka REST API
- New topology label that allows scheduling of pods only on Kubernetes nodes having CSI node component.  
In order to schedule pods on supporting nodes, add this NodeSelector: ```topology.csi.weka.io/global: "true"```

## Bug Fixes
- `Failed to remove entry...` error messages appeared in logs for every inner directory during PV deletion

## Known Issues
- Authenticated mounts are not supported in current version of CSI plugin

# Release 0.6.6
## Bug fixes
- Changed default mount options to writecache to improve inter-pod performance over CSI volumes

# Release 0.6.5
## Bug fixes
- In rare circumstances, CSI plugin may fail to publish a volume after node server pod restart

# Release 0.6.4
## Improvements
- CSI node driver does not crash when node is not configured as Weka client

# Release 0.6.3
## New Features
- Deployment supported via Helm public repo
- Repository listed on ArtifactHub

## Improvements
- Fixed version strings SymVer2 compatibility
- Added values.schema.json
- Added post-installation notes
- Added documentation on values

# Release 0.6.2
## New Features
- Separation of controller and node plugin components for increased performance and stability 
- Support of deployment via [Helm](https://helm.sh/) in addition to the previous deployment scheme
- Support of adding node taints and tolerations via helm deployment

## Improvements
- Cleanup script now handles entities of all versions
- Plugin logs are now much more readable
- Docker tag pattern was changed from "latest" to version tag

## Known Issues
- During deployment, on slow networks, a node pod can arbitrary enter `CrashLoopBackoff` 
due to node-driver-registrar container loading before wekafs container
In such case, delete the container and it will be recreated automatically.

## Upgrade Steps
In order to upgrade an existing deployment from version below 0.6.0, 
the previous version has to be uninstalled first: 
 
```
./deploy/util/cleanup.sh
```

Then, a new version can be deployed, by following either one of the procedures below:
- [helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [deploy script](./README.md)
- [helm local installation](deploy/helm/csi-wekafsplugin/LOCAL.md)


# Release 0.5.0
Initial release
