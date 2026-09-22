#!/usr/bin/env bash
#
# Installs csi-wekafs onto a Bliss-provisioned cluster for one transport, and leaves it ready
# for the external-storage suite.
#
# The chart comes from this checkout, not from a published release. That is the point: chart
# changes are a large part of what lands in this repo, and installing a released chart with only
# the image overridden would leave every template change untested.
#
# Env:
#   TRANSPORT        wekafs | nfs
#   CSI_IMAGE        driver image repository   (default quay.io/weka.io/csi-wekafs)
#   CSI_IMAGE_TAG    driver image tag          (required)
#   WEKA_CLUSTER     WekaCluster name, used to find the operator's credentials secret
#   WEKA_NAMESPACE   namespace holding that secret (default "default")
#   CSI_NAMESPACE    namespace to install the plugin into (default "csi-wekafs")
#
set -euo pipefail

TRANSPORT="${TRANSPORT:?TRANSPORT must be 'wekafs' or 'nfs'}"
CSI_IMAGE="${CSI_IMAGE:-quay.io/weka.io/csi-wekafs}"
CSI_IMAGE_TAG="${CSI_IMAGE_TAG:?CSI_IMAGE_TAG must name the image to test}"
WEKA_CLUSTER="${WEKA_CLUSTER:?WEKA_CLUSTER must name the WekaCluster}"
WEKA_NAMESPACE="${WEKA_NAMESPACE:-default}"
CSI_NAMESPACE="${CSI_NAMESPACE:-csi-wekafs}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${HERE}/../.." && pwd)"

case "${TRANSPORT}" in
  wekafs|nfs) ;;
  *) echo "unknown transport '${TRANSPORT}'" >&2; exit 2 ;;
esac

# ---------------------------------------------------------------------------------------------
# Snapshot support. k3s ships no VolumeSnapshot CRDs and no snapshot controller, and without
# them every snapshot and clone test fails on a missing API rather than on anything this driver
# did. The suite's whole snapshot family is one of the three things we set out to cover, so this
# is a prerequisite, not an extra.
# ---------------------------------------------------------------------------------------------
SNAPSHOTTER_VERSION="${SNAPSHOTTER_VERSION:-v8.2.0}"
echo "Installing external-snapshotter ${SNAPSHOTTER_VERSION} CRDs and controller"
base="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOTTER_VERSION}"
for crd in volumesnapshotclasses volumesnapshotcontents volumesnapshots; do
  kubectl apply -f "${base}/client/config/crd/snapshot.storage.k8s.io_${crd}.yaml"
done
kubectl apply -f "${base}/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml"
kubectl apply -f "${base}/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml"
kubectl -n kube-system rollout status deploy/snapshot-controller --timeout=5m

# ---------------------------------------------------------------------------------------------
# Credentials. The weka-operator generates a CSI-specific user when it reconciles the
# WekaCluster and writes it to weka-csi-<clusterName>. We copy it rather than point the
# StorageClasses at it, so the manifests the test suite reads stay literal files with no
# cluster-specific names substituted into them at run time.
# ---------------------------------------------------------------------------------------------
OPERATOR_SECRET="weka-csi-${WEKA_CLUSTER}"
echo "Waiting for the operator to publish ${OPERATOR_SECRET} in ${WEKA_NAMESPACE}"
# Secrets carry no conditions, so there is nothing for `kubectl wait` to wait on; poll instead.
for _ in $(seq 1 60); do
  if kubectl -n "${WEKA_NAMESPACE}" get secret "${OPERATOR_SECRET}" >/dev/null 2>&1; then
    break
  fi
  sleep 10
done
if ! kubectl -n "${WEKA_NAMESPACE}" get secret "${OPERATOR_SECRET}" >/dev/null 2>&1; then
  echo "operator never created ${OPERATOR_SECRET}; the WekaCluster is not ready" >&2
  kubectl -n "${WEKA_NAMESPACE}" get wekacluster -o wide || true
  exit 1
fi

kubectl create namespace "${CSI_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# Copy only the keys the chart reads. Taking the whole object would drag along the operator's
# ownerReferences, which would have the operator's garbage collector delete our copy the moment
# it tidies up, and its resourceVersion, which apply would reject.
kubectl -n "${WEKA_NAMESPACE}" get secret "${OPERATOR_SECRET}" -o json \
  | python3 -c '
import json, sys
src = json.load(sys.stdin)["data"]
wanted = ["username", "password", "organization", "endpoints", "scheme", "nfsTargetIps"]
print(json.dumps({
    "apiVersion": "v1",
    "kind": "Secret",
    "type": "Opaque",
    "metadata": {"name": "csi-wekafs-api-secret", "namespace": "'"${CSI_NAMESPACE}"'"},
    "data": {k: src[k] for k in wanted if k in src},
}))' \
  | kubectl apply -f -
echo "Copied credentials into ${CSI_NAMESPACE}/csi-wekafs-api-secret"

# ---------------------------------------------------------------------------------------------
# The plugin itself.
# ---------------------------------------------------------------------------------------------
echo "Installing csi-wekafs (${TRANSPORT}) from ${REPO}/charts/csi-wekafsplugin"
helm upgrade --install csi-wekafsplugin "${REPO}/charts/csi-wekafsplugin" \
  --namespace "${CSI_NAMESPACE}" \
  --values "${HERE}/values-${TRANSPORT}.yaml" \
  --set "images.csidriver=${CSI_IMAGE}" \
  --set "images.csidriverTag=${CSI_IMAGE_TAG}" \
  --wait --timeout 10m

kubectl -n "${CSI_NAMESPACE}" rollout status deploy/csi-wekafsplugin-controller --timeout=5m
kubectl -n "${CSI_NAMESPACE}" rollout status ds/csi-wekafsplugin-node --timeout=5m

# Prove the driver registered before handing over to a suite that would otherwise spend its
# timeout waiting for a provisioner that is not there.
kubectl get csidriver csi.weka.io
echo "csi-wekafs is up on the ${TRANSPORT} transport"
