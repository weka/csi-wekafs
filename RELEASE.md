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
./deploy/kubernetes-latest/cleanup.sh
```

Then, a new version can be deployed, by following either one of the procedures below:
- [helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [deploy script](./README.md)
- [helm local installation](deploy/helm/csi-wekafsplugin/LOCAL.md)


# Release 0.5.0
Initial release