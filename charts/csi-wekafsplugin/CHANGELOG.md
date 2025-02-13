<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release provides ability to provision encrypted volumes using a cluster-wide KMS configuration.
The encryption functionality is not complete and will provide additional abilities in next major version.

### New features
* feat(CSI-315): partial support of encrypted volumes for FS backing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/442
### Bug Fixes
* fix(CSI-326): invalid socket dir path causing 2 instances of CSI plugin to interfere with each other by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/453


