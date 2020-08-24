#!/usr/bin/env bash

set -e
set -o pipefail

WEKAFS_NAMESPACE="csi-wekafsplugin"

usage() {
echo -e "
Weka CSI plugin cleanup utility, Copyright 2020 Weka IO

Usage: $0 [--namespace <WEKAFS_NAMESPACE>]

--namespace WEKAFS_NAMESPACE    name of the namespace CSI plugin is configured in, deafault \"csi-wekafsplugin\"
"
}

exit_usage() {
  echo -e "$@"
  usage
  exit 1
}

parse_args() {
  for arg in "$@"; do
    case "$arg" in
      -n|--namespace)
        WEKAFS_NAMESPACE="$2"
        shift 2
        ! [[ $WEKAFS_NAMESPACE ]] && exit_usage "Invalid namespace specified"
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo -e "Invalid arguments"
        usage
        exit 1
        ;;
    esac
  done
}

cleanup() {
  echo "Weka CSI plugin cleanup utility"
  echo "Removing wekafs plugin daemonset"
  kubectl -n "$WEKAFS_NAMESPACE" get daemonsets.apps csi-wekafsplugin &>/dev/null && \
    kubectl -n "$WEKAFS_NAMESPACE" delete daemonsets.apps csi-wekafsplugin

  echo "Removing wekafs controller plugin statefulset"
  kubectl -n "$WEKAFS_NAMESPACE" get statefulset.apps csi-wekafsplugin-controller &>/dev/null && \
    kubectl -n "$WEKAFS_NAMESPACE" delete statefulset.apps csi-wekafsplugin-controller

  echo "Removing wekafs plugin roles and permissions"
  kubectl get clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role-binding &>/dev/null && \
    kubectl delete clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role-binding

  kubectl get clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-controller &>/dev/null && \
    kubectl delete clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-controller

  kubectl get clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-node &>/dev/null && \
    kubectl delete clusterrolebindings.rbac.authorization.k8s.io csi-wekafsplugin-node

  kubectl get clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role &>/dev/null && \
    kubectl delete clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-cluster-role

  kubectl get clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-controller &>/dev/null && \
    kubectl delete clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-controller

  kubectl get clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-node &>/dev/null && \
    kubectl delete clusterroles.rbac.authorization.k8s.io csi-wekafsplugin-node

  kubectl get rolebindings.rbac.authorization.k8s.io csi-wekafsplugin-role-binding &>/dev/null && \
    kubectl delete rolebindings.rbac.authorization.k8s.io csi-wekafsplugin-role-binding

  kubectl get roles.rbac.authorization.k8s.io csi-wekafsplugin-role &>/dev/null && \
    kubectl delete roles.rbac.authorization.k8s.io csi-wekafsplugin-role

  kubectl -n "$WEKAFS_NAMESPACE" get serviceaccounts csi-wekafsplugin &>/dev/null && \
    kubectl -n "$WEKAFS_NAMESPACE" delete serviceaccounts csi-wekafsplugin

  kubectl -n "$WEKAFS_NAMESPACE" get serviceaccounts csi-wekafsplugin-controller &>/dev/null && \
    kubectl -n "$WEKAFS_NAMESPACE" delete serviceaccounts csi-wekafsplugin-controller

  kubectl -n "$WEKAFS_NAMESPACE" get serviceaccounts csi-wekafsplugin-node &>/dev/null && \
    kubectl -n "$WEKAFS_NAMESPACE" delete serviceaccounts csi-wekafsplugin-node

  echo "Removing wekafsplugin namespace"
  kubectl get namespace "$WEKAFS_NAMESPACE" &>/dev/null && kubectl delete namespace "$WEKAFS_NAMESPACE"

  echo "Removing Weka CSI driver"
  kubectl get csidrivers csi.weka.io &>/dev/null && kubectl delete csidrivers csi.weka.io
  echo "Please note that user-created entities of the following types are not removed:"
  echo - persistentvolumeclaim
  echo - persisentvolume
  echo - storageclasses.storage.k8s.io

  echo "Cleanup completed successfully"
}

main() {
  parse_args "$@"
  cleanup
}

main "$@"