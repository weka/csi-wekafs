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
