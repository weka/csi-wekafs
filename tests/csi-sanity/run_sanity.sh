#!/usr/bin/env sh

mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path

cleanup() {
  echo "CLEANING UP"
  rm -rf /tmp/weka-csi-test/sanity-workspace
  rm -rf /tmp/weka-csi-test/filesystems
  rm -rf /tmp/csi-test-staging
}

# ---------------------- DIR VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
directory() {
  echo "DIRECTORY VOLUME AND SNAPSHOTS STARTED"
  cleanup
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace \
    -ginkgo.seed 0 \
    -ginkgo.skip="NodeExpandVolume" \
    -ginkgo.skip="NodeStageVolume" \
    -ginkgo.skip="NodeUnstageVolume" \
    -ginkgo.vv \
    -csi.testvolumeparameters /test/wekafs-dirv1.yaml
}

directory_nfs() {
  echo "RUNNING IN NFS MODE"
  directory "$@"
}

# ---------------------- SNAPSHOT VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
snapshot() {
  echo "SNAPSHOT VOLUMES WITH 2nd LEVEL SNAPSHOTS STARTED"
  cleanup
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace \
    -ginkgo.seed 0 \
    -ginkgo.skip="NodeExpandVolume" \
    -ginkgo.skip="NodeStageVolume" \
    -ginkgo.skip="NodeUnstageVolume" \
    -ginkgo.vv \
    -csi.testvolumeparameters /test/wekafs-snapvol.yaml
}
# ---------------------- SNAPSHOT VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
snapshot_nfs() {
  echo "RUNNING IN NFS MODE"
  snapshot "$@"
}

# ---------------------- FILESYSTEM VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
filesystem() {
  echo "FILESYSTEM VOLUMES STARTED"
  cleanup
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace \
    -ginkgo.seed 0 \
    -ginkgo.skip="NodeExpandVolume" \
    -ginkgo.skip="NodeStageVolume" \
    -ginkgo.skip="NodeUnstageVolume" \
    -ginkgo.vv \
    -csi.testvolumeparameters /test/wekafs-fs.yaml
}

filesystem_nfs() {
  echo "RUNNING IN NFS MODE"
  filesystem "$@"
}

"$@"
