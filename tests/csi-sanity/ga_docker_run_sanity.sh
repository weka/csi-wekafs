#!/usr/bin/env sh

mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path

# ---------------------- LEGACY DIR VOLUME NO API BINDING (NO SNAPSHOT SUPPORT) ----------------------
legacy_sanity() {
echo "LEGACY SANITY STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
  --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.always-emit-ginkgo-writer \
  -ginkgo.progress  $1\
  -ginkgo.seed 0 \
  -csi.testvolumeparameters /test/wekafs-dirv1.yaml
}

# ---------------------- DIR VOLUME WITH API BINDING EXCLUDING SNAPSHOTS ----------------------
directory_volume_no_snapshots() {
echo "DIRECTORY VOLUME NO SNAPSHOTS STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
  --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
  -csi.secrets /test/wekafs-api-secret.yaml \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.seed 0 \
  -ginkgo.progress  $1\
  -csi.testvolumeparameters /test/wekafs-dirv1.yaml
}

# ---------------------- FS VOLUME WITH API BINDING EXCLUDING SNAPSHOTS ----------------------
fs_volume_no_snapshots() {
echo "FS VOLUME NO SNAPSHOTS STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
  --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
  -csi.secrets /test/wekafs-api-secret.yaml \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -csi.testvolumeparameters /test/wekafs-fs.yaml \
  -ginkgo.seed 0 \
  -ginkgo.progress  $1
}

# ---------------------- DIR VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
directory_volume_and_snapshots() {
echo "DIRECTORY VOLUME AND SNAPSHOTS STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
  --csi.endpoint /tmp/weka-csi-test/node.sock \
  -csi.secrets /test/wekafs-api-secret.yaml \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.seed 0 \
  -ginkgo.progress  $1\
  -csi.testvolumeparameters /test/wekafs-dirv1.yaml
}

# ---------------------- SNAPSHOT VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
snaphot_volumes_with_2nd_level_shapshots() {
echo "SNAPSHOT VOLUMES WITH 2nd LEVEL SNAPSHOTS STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
  --csi.endpoint /tmp/weka-csi-test/node.sock \
  -csi.secrets /test/wekafs-api-secret.yaml \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.seed 0 \
  -ginkgo.progress  $1\
  -csi.testvolumeparameters /test/wekafs-snapvol.yaml
}

# ---------------------- FILESYSTEM VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
filesystem_volumes() {
echo "FILESYSTEM VOLUMES STARTED"

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
  --csi.endpoint /tmp/weka-csi-test/node.sock \
  -csi.secrets /test/wekafs-api-secret.yaml \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.seed 0 \
  -ginkgo.progress  $1\
  -csi.testvolumeparameters /test/wekafs-fs.yaml
}

"$@"
