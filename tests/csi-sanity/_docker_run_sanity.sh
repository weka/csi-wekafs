#!/usr/bin/env bash

rm -rf /tmp/weka-csi-test/sanity-workspace/
rm -rf /tmp/weka-csi-test/filesystems
csi-sanity -csi.stagingdir /tmp/csi-test-staging --csi.endpoint /tmp/weka-csi-test/csi.sock -csi.mountdir=/tmp/weka-csi-test/sanity-workspace/ -ginkgo.failFast -csi.testvolumeparameters /test/wekafs-vol.yaml  -ginkgo.trace -ginkgo.v
