#!/usr/bin/env bash
set -e

rm -rf /tmp/weka-csi-test/sanity-workspace/
rm -rf /tmp/weka-csi-test/filesystems
rm -rf /tmp/csi-test-staging


# ---------------------- LEGACY DIR VOLUME NO API BINDING (NO SNAPSHOT SUPPORT) ----------------------
echo "LEGACY SANITY STARTED"
mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
    --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -ginkgo.reportPassed \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.seed 0 \
    -ginkgo.v \
    -csi.testvolumeparameters /test/wekafs-dirv1.yaml
then
  echo "LEGACY SANITY PASSED"
else
  RETCODE=$?
  echo "LEGACY SANITY FAILED"
  exit $RETCODE
fi


# ---------------------- DIR VOLUME WITH API BINDING EXCLUDING SNAPSHOTS ----------------------
echo "DIRECTORY VOLUME NO SNAPSHOTS STARTED"
mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
    --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -ginkgo.seed 0 \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.v \
    -csi.testvolumeparameters /test/wekafs-dirv1.yaml
then
  echo "DIRECTORY VOLUME NO SNAPSHOTS PASSED"
else
  RETCODE=$?
  echo "DIRECTORY VOLUME NO SNAPSHOTS FAILED"
  exit $RETCODE
fi


# ---------------------- FS VOLUME WITH API BINDING EXCLUDING SNAPSHOTS ----------------------
echo "FS VOLUME NO SNAPSHOTS STARTED"
 mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller-no-snaps.sock \
    --csi.endpoint /tmp/weka-csi-test/node-no-snaps.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -csi.testvolumeparameters /test/wekafs-fs.yaml \
    -ginkgo.seed 0 \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.v
then
  echo "FS VOLUME NO SNAPSHOTS PASSED"
else
  RETCODE=$?
  echo "FS VOLUME NO SNAPSHOTS FAILED"
  exit $RETCODE
fi

# ---------------------- DIR VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
echo "DIRECTORY VOLUME AND SNAPSHOTS STARTED"
mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -ginkgo.seed 0 \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.v \
    -csi.testvolumeparameters /test/wekafs-dirv1.yaml
then
  echo "DIRECTORY VOLUME AND SNAPSHOTS PASSED"
else
  RETCODE=$?
  echo "DIRECTORY VOLUME AND SNAPSHOTS FAILED"
  exit $RETCODE
fi


# ---------------------- SNAPSHOT VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
echo "SNAPSHOT VOLUMES WITH 2nd LEVEL SNAPSHOTS STARTED"
mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -ginkgo.seed 0 \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.v \
    -csi.testvolumeparameters /test/wekafs-snapvol.yaml
then
  echo "SNAPSHOT VOLUMES WITH 2nd LEVEL SNAPSHOTS PASSED"
else
  RETCODE=$?
  echo "SNAPSHOT VOLUMES WITH 2nd LEVEL SNAPSHOTS FAILED"
  exit $RETCODE
fi

# ---------------------- FILESYSTEM VOLUME WITH API BINDING AND SNAPSHOTS ----------------------
echo "FILESYSTEM VOLUMES STARTED"
mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
if \
  csi-sanity -csi.stagingdir /tmp/csi-test-staging \
    --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
    --csi.endpoint /tmp/weka-csi-test/node.sock \
    -csi.secrets /test/wekafs-api-secret.yaml \
    -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
    -ginkgo.seed 0 \
    -ginkgo.failFast \
    -ginkgo.progress \
    -ginkgo.v \
    -csi.testvolumeparameters /test/wekafs-fs.yaml
then
  echo "FILESYSTEM VOLUMES PASSED"
else
  RETCODE=$?
  echo "FILESYSTEM VOLUMES FAILED"
  exit $RETCODE
fi

