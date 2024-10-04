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

