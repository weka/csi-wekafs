#!/usr/bin/env bash
#
# Runs the Kubernetes external-storage end-to-end suite against an already-installed
# csi-wekafs, for one transport.
#
# This is deliberately separate from the workflow that provisions the cluster: the slow,
# expensive part is getting a Weka cluster, and being able to re-run just the tests against a
# cluster that is already up is the difference between a five-minute iteration and a forty-minute
# one. Point KUBECONFIG at a live cluster and run this by hand; that is a supported use, not a
# workaround.
#
# Usage:
#   run-e2e.sh <wekafs|nfs> [extra ginkgo args...]
#
set -euo pipefail

TRANSPORT="${1:?first argument must be 'wekafs' or 'nfs'}"
shift || true

case "${TRANSPORT}" in
  wekafs|nfs) ;;
  *) echo "unknown transport '${TRANSPORT}': expected 'wekafs' or 'nfs'" >&2; exit 2 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS="${HERE}/manifests"
ARTIFACTS="${ARTIFACTS:-${HERE}/_artifacts/${TRANSPORT}}"
mkdir -p "${ARTIFACTS}"

# The suite version has to match the cluster it runs against: it asserts on API objects whose
# shape changes between releases, and a mismatch shows up as failures that look like driver bugs.
# Default to the cluster's own server version so the two cannot drift apart silently.
if [[ -z "${K8S_E2E_VERSION:-}" ]]; then
  K8S_E2E_VERSION="$(kubectl version -o json | python3 -c \
    'import json,sys; v=json.load(sys.stdin)["serverVersion"]; print("v%s.%s.0" % (v["major"], v["minor"].rstrip("+")))')"
fi
echo "Using external-storage suite ${K8S_E2E_VERSION}"

# ---------------------------------------------------------------------------------------------
# Fetch e2e.test. It is not vendored: it is ~200MB, tracks the Kubernetes release rather than
# this repo, and pinning a copy in git would make every cluster upgrade a repo change.
# ---------------------------------------------------------------------------------------------
E2E_CACHE="${E2E_CACHE:-/tmp/k8s-e2e-${K8S_E2E_VERSION}}"
E2E_BIN="${E2E_CACHE}/kubernetes/test/bin/e2e.test"
GINKGO_BIN="${E2E_CACHE}/kubernetes/test/bin/ginkgo"

if [[ ! -x "${E2E_BIN}" ]]; then
  echo "Downloading Kubernetes test binaries ${K8S_E2E_VERSION}"
  mkdir -p "${E2E_CACHE}"
  curl -fsSL --retry 3 \
    "https://dl.k8s.io/${K8S_E2E_VERSION}/kubernetes-test-linux-amd64.tar.gz" \
    | tar -xz -C "${E2E_CACHE}"
fi
[[ -x "${E2E_BIN}" ]] || { echo "e2e.test not found at ${E2E_BIN} after download" >&2; exit 1; }

# ---------------------------------------------------------------------------------------------
# Which tests run.
#
# The focus is the external-storage suite only. Running the whole e2e binary would pull in
# hundreds of tests about scheduling, networking and the control plane that say nothing about
# this driver and fail for reasons nobody here can fix.
# ---------------------------------------------------------------------------------------------
FOCUS="${E2E_FOCUS:-External.Storage}"

# Skips, each with the reason it is here. A skip without a reason becomes permanent by accident.
SKIPS=(
  # The driver advertises no block volume capability at all - these would not be testing a
  # shortcoming, they would be testing something that was never claimed.
  '\[Feature:Volumes\]'
  'block.volmode'
  'volumeMode'
  # The node service does not advertise EXPAND_VOLUME: capacity is a Weka quota, so a resize is
  # complete once the controller has changed it and there is nothing for the node to do.
  'NodeExpandVolume'
  # Disruptive tests reboot nodes and restart the kubelet. On a shared, freshly-provisioned
  # cluster that mostly proves the provisioner works, at the cost of most of the run's wall clock.
  '\[Disruptive\]'
  '\[Slow\]'
  # Ephemeral inline volumes are not a mode this driver supports.
  'ephemeral'
)
SKIP="${E2E_SKIP:-$(IFS='|'; echo "${SKIPS[*]}")}"

echo "Transport:  ${TRANSPORT}"
echo "Focus:      ${FOCUS}"
echo "Skip:       ${SKIP}"
echo "Artifacts:  ${ARTIFACTS}"

# Ginkgo resolves the StorageClass and VolumeSnapshotClass paths inside the driver definition
# relative to its own working directory, not to the file, so run from the manifests directory.
cd "${MANIFESTS}"

set +e
"${GINKGO_BIN}" \
  --nodes="${E2E_PARALLEL:-4}" \
  --focus="${FOCUS}" \
  --skip="${SKIP}" \
  --junit-report="report.xml" \
  --output-dir="${ARTIFACTS}" \
  --timeout="${E2E_TIMEOUT:-90m}" \
  "${E2E_BIN}" \
  -- \
  -storage.testdriver="testdriver-${TRANSPORT}.yaml" \
  -report-dir="${ARTIFACTS}" \
  "$@"
RESULT=$?
set -e

echo "external-storage suite (${TRANSPORT}) exited ${RESULT}; artifacts in ${ARTIFACTS}"
exit "${RESULT}"
