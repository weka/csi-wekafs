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
