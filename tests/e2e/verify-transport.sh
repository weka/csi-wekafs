#!/usr/bin/env bash
#
# Proves the volume is actually mounted over the transport the run believes it is testing.
#
# Nothing in the external-storage suite checks this. It asks for a volume, gets one, and is
# satisfied - so if the plugin quietly served a wekafs leg over NFS, the whole matrix would pass
# while testing one transport twice. That is not hypothetical: NFS failback exists precisely to
# do this silently, and although values-wekafs.yaml turns it off, an assertion is proof where a
# setting is only an intention.
#
# Env:
#   TRANSPORT      wekafs | nfs
#   CSI_NAMESPACE  namespace the plugin is installed in (default csi-wekafs)
#
set -euo pipefail

TRANSPORT="${TRANSPORT:?TRANSPORT must be 'wekafs' or 'nfs'}"
CSI_NAMESPACE="${CSI_NAMESPACE:-csi-wekafs}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="csi-e2e-transport-check"

case "${TRANSPORT}" in
  # The kernel reports an NFSv4 mount as "nfs4" and v3 as "nfs"; the chart defaults to 4.1 but
  # the version is configurable, so accept either rather than fail the run over a version choice.
  wekafs) WANT='wekafs' ;;
  nfs)    WANT='nfs4|nfs' ;;
  *) echo "unknown transport '${TRANSPORT}'" >&2; exit 2 ;;
esac

cleanup() {
  kubectl delete namespace "${NS}" --wait=false >/dev/null 2>&1 || true
  kubectl delete storageclass "${NS}" --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

# NFS needs somewhere to mount from. The operator fills nfsTargetIps in from the cluster's NFS
# containers, but the setup notes in k8s-contrib record having to set it by hand, so check it
# rather than let an empty value surface later as a mount timeout that names nothing.
if [[ "${TRANSPORT}" == "nfs" ]]; then
  targets="$(kubectl -n "${CSI_NAMESPACE}" get secret csi-wekafs-api-secret \
    -o jsonpath='{.data.nfsTargetIps}' 2>/dev/null | base64 -d 2>/dev/null || true)"
  if [[ -z "${targets}" ]]; then
    echo "NOTE: the API secret carries no nfsTargetIps. The plugin will fall back to the" >&2
    echo "      cluster's NFS interface group; if mounts hang, this is the first thing to check." >&2
  else
    echo "NFS targets: ${targets}"
  fi
fi

kubectl create namespace "${NS}" --dry-run=client -o yaml | kubectl apply -f -
# A StorageClass of its own, so this never collides with the one the suite creates and deletes.
sed "s|^  name: .*|  name: ${NS}|" "${HERE}/manifests/storageclass-${TRANSPORT}.yaml" | kubectl apply -f -

kubectl apply -n "${NS}" -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: probe
spec:
  accessModes: [ReadWriteMany]
  storageClassName: ${NS}
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: probe
spec:
  restartPolicy: Never
  containers:
    - name: probe
      image: busybox:1.36
      command: ["sh", "-c", "sleep 600"]
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: probe
EOF

if ! kubectl -n "${NS}" wait --for=condition=Ready pod/probe --timeout=5m; then
  echo "the probe pod never started; the volume could not be mounted over ${TRANSPORT}" >&2
  kubectl -n "${NS}" describe pod probe || true
  kubectl -n "${NS}" get pvc,pv -o wide || true
  exit 1
fi

# /proc/mounts names the filesystem type the kernel actually used, which is the only thing that
# settles which transport carried the mount. stat -f is not usable here: it reports UNKNOWN for
# filesystems it has no magic number for.
MOUNTS="$(kubectl -n "${NS}" exec probe -- cat /proc/mounts)"
FSTYPE="$(printf '%s\n' "${MOUNTS}" | awk '$2 == "/data" {print $3; exit}')"

echo "Mounted /data as: ${FSTYPE:-<not mounted>}"
if [[ -z "${FSTYPE}" ]]; then
  echo "no mount at /data in the probe pod" >&2
  printf '%s\n' "${MOUNTS}" >&2
  exit 1
fi
if ! printf '%s' "${FSTYPE}" | grep -qE "^(${WANT})$"; then
  echo "transport mismatch: asked for ${TRANSPORT}, got a ${FSTYPE} mount" >&2
  echo "a wekafs leg served over NFS means the matrix tested one transport twice" >&2
  exit 1
fi

# Writing proves the mount is usable, not merely present. A read-only or broken mount can still
# appear in /proc/mounts with the right type.
kubectl -n "${NS}" exec probe -- sh -c 'echo ok > /data/probe && cat /data/probe' | grep -qx ok

echo "Transport verified: ${TRANSPORT} mounted as ${FSTYPE}, and it is writable"
