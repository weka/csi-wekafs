#!/usr/bin/env bash

set -e
set -o pipefail

BASE_DIR=$(dirname "$0")
WEKAFS_NAMESPACE="csi-wekafsplugin"

# deploy wekafs plugin yaml

if [[ $FORCE_REMOVE_DATA ]]; then
  echo "removing all persistent volume claims. Please note that if attached to any pods the operation will be stuck"
fi


echo "removing wekafs plugin daemonset"
kubectl -n csi-wekafsplugin delete daemonsets.apps csi-wekafsplugin

echo "removing wekafs plugin roles and permissions"
# TODO: remove the old names
kubectl -n csi-wekafsplugin delete clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role-binding csi-attacher-role csi-provisioner-role csi-wekafs-attacher-role csi-wekafs-provisioner-role

# TODO: remove the old names
kubectl -n csi-wekafsplugin delete clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role csi-wekafs-attacher-runner csi-wekafs-provisioner-runner

# TODO: remove the old names
kubectl -n csi-wekafsplugin delete rolebindings.rbac.authorization.k8s.io csi-wekafsplugin-role-binding csi-wekafs-attacher-role-cfg csi-wekafs-provisioner-role-cfg

# TODO: remove the old names
kubectl -n csi-wekafsplugin delete roles.rbac.authorization.k8s.io csi-wekafsplugin-role csi-wekafs-attacher-cfg csi-wekafs-provisioner-cfg

# TODO: remove the old names
kubectl -n csi-wekafsplugin delete serviceaccounts csi-wekafsplugin csi-wekafs-attacher csi-wekafs-provisioner


# namespace
echo "removing  wekafsplugin namespace"
kubectl get namespace "$WEKAFS_NAMESPACE" &>/dev/null && kubectl delete namespace "$WEKAFS_NAMESPACE"


echo $(date +%H:%M:%S) "cleanup completed successfully"

echo "Please note that user-created entities of the following types are not removed:"
echo - persistentvolumeclaim
echo - persisentvolume
echo - storageclasses.storage.k8s.io
