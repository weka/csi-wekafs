#!/usr/bin/env bash

set -e
set -o pipefail

BASE_DIR=$(dirname "$0")
WEKAFS_NAMESPACE="csi-wekafsplugin"

# deploy wekafs plugin yaml

if [[ $FORCE_REMOVE_DATA ]]; then
  echo "removing all persistent volume claims. Please note that if attached to any pods the operation will be stuck"
fi


echo "Removing wekafs plugin daemonset"
kubectl -n csi-wekafsplugin get daemonsets.apps csi-wekafsplugin 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete daemonsets.apps csi-wekafsplugin

echo "Removing wekafs plugin roles and permissions"
kubectl -n csi-wekafsplugin get clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role-binding 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role-binding

kubectl -n csi-wekafsplugin get clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role

kubectl -n csi-wekafsplugin get rolebindings.rbac.authorization.k8s.io csi-wekafsplugin-role-binding 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete rolebindings.rbac.authorization.k8s.io csi-wekafsplugin-role-binding

kubectl -n csi-wekafsplugin get roles.rbac.authorization.k8s.io csi-wekafsplugin-role 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete roles.rbac.authorization.k8s.io csi-wekafsplugin-role

kubectl -n csi-wekafsplugin get serviceaccounts csi-wekafsplugin 2>/dev/null && \
  kubectl -n csi-wekafsplugin delete serviceaccounts csi-wekafsplugin

echo "Removing wekafsplugin namespace"
kubectl get namespace "$WEKAFS_NAMESPACE" 2>/dev/null && kubectl delete namespace "$WEKAFS_NAMESPACE"

echo "Please note that user-created entities of the following types are not removed:"
echo - persistentvolumeclaim
echo - persisentvolume
echo - storageclasses.storage.k8s.io

echo "Cleanup completed successfully"

