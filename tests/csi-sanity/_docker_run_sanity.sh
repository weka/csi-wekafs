#!/usr/bin/env bash
set -e

rm -rf /tmp/weka-csi-test/sanity-workspace/
rm -rf /tmp/weka-csi-test/filesystems
rm -rf /tmp/csi-test-staging

csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
  --csi.endpoint /tmp/weka-csi-test/node.sock \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.failFast \
  -ginkgo.trace -ginkgo.v \
  -csi.testvolumeparameters /test/wekafs-dirv1.yaml

mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
csi-sanity -csi.stagingdir /tmp/csi-test-staging \
  --csi.controllerendpoint /tmp/weka-csi-test/controller.sock \
  --csi.endpoint /tmp/weka-csi-test/node.sock \
  -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ \
  -ginkgo.failFast \
  -ginkgo.trace -ginkgo.v \
  -csi.testvolumeparameters /test/wekafs-existingPathv1.yaml
