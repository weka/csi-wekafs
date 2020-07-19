#!/usr/bin/env bash
set -e

rm -rf /tmp/weka-csi-test/sanity-workspace/
rm -rf /tmp/weka-csi-test/filesystems
csi-sanity -csi.stagingdir /tmp/csi-test-staging --csi.endpoint /tmp/weka-csi-test/csi.sock -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ -ginkgo.failFast -csi.testvolumeparameters /test/wekafs-dirv1.yaml  -ginkgo.trace -ginkgo.v

mkdir -p  /tmp/weka-csi-test/filesystems/default/test/my/path
csi-sanity -csi.stagingdir /tmp/csi-test-staging --csi.endpoint /tmp/weka-csi-test/csi.sock -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ -ginkgo.failFast -csi.testvolumeparameters /test/wekafs-existingPathv1.yaml  -ginkgo.trace -ginkgo.v
