#!/usr/bin/env bash

set -e
set -o pipefail

BASE_DIR=$(dirname "$0")
WEKAFS_NAMESPACE="csi-wekafsplugin"

# namespace
echo "creating wekafsplugin namespace"
kubectl get namespace "$WEKAFS_NAMESPACE" &>/dev/null || kubectl create namespace "$WEKAFS_NAMESPACE"

# deploy wekafs plugin yaml

echo "deploying wekafs components"
for i in $(ls ${BASE_DIR}/wekafs/*.yaml | sort); do
    echo "   $i"
    modified="$(cat "$i" | while IFS= read -r line; do
        nocomments="$(echo "$line" | sed -e 's/ *#.*$//')"
        if echo "$nocomments" | grep -q '^[[:space:]]*image:[[:space:]]*'; then
            # Split 'image: quay.io/k8scsi/csi-attacher:v1.0.1'
            # into image (quay.io/k8scsi/csi-attacher:v1.0.1),
            # registry (quay.io/k8scsi),
            # name (csi-attacher),
            # tag (v1.0.1).
            image=$(echo "$nocomments" | sed -e 's;.*image:[[:space:]]*;;')
            registry=$(echo "$image" | sed -e 's;\(.*\)/.*;\1;')
            name=$(echo "$image" | sed -e 's;.*/\([^:]*\).*;\1;')
            tag=$(echo "$image" | sed -e 's;.*:;;')

            # Variables are with underscores and upper case.
            varname=$(echo $name | tr - _ | tr a-z A-Z)

            # Now replace registry and/or tag, if set as env variables.
            # If not set, the replacement is the same as the original value.
            # Only do this for the images which are meant to be configurable.
            prefix=$(eval echo \${${varname}_REGISTRY:-${IMAGE_REGISTRY:-${registry}}}/ | sed -e 's;none/;;')
            if [ "$IMAGE_TAG" = "canary" ] &&
               [ -f ${BASE_DIR}/canary-blacklist.txt ] &&
               grep -q "^$name\$" ${BASE_DIR}/canary-blacklist.txt; then
                # Ignore IMAGE_TAG=canary for this particular image because its
                # canary image is blacklisted in the deployment blacklist.
                suffix=$(eval echo :\${${varname}_TAG:-${tag}})
            else
                suffix=$(eval echo :\${${varname}_TAG:-${IMAGE_TAG:-${tag}}})
            fi
            line="$(echo "$nocomments" | sed -e "s;$image;${prefix}${name}${suffix};")"
            echo "        using $line" >&2
        fi
        echo "$line"
    done)"
    if ! echo "$modified" | kubectl apply -f -; then
        echo "modified version of $i:"
        echo "$modified"
        exit 1
    fi
done

# Wait until all pods are running. We have to make some assumptions
# about the deployment here, otherwise we wouldn't know what to wait
# for: the expectation is that we run attacher, provisioner, and wekafs plugin in the default namespace.

# check number of plugins to be running
expected_running_pods=$(( $(kubectl describe nodes | grep Taints | grep -v "^Taints.*NoSchedule" -c) + 1 ))

cnt=0
while (( $(kubectl get pods --namespace "$WEKAFS_NAMESPACE" 2>/dev/null | grep '^csi-wekafs.* Running ' -c) < expected_running_pods )); do
    if [ $cnt -gt 30 ]; then
        echo "$(kubectl get pods --namespace "$WEKAFS_NAMESPACE" 2>/dev/null | grep '^csi-wekafs.* Running ' -c) running pods:"
        kubectl describe pods

        echo >&2 "ERROR: wekafs deployment not ready after over 5min"
        exit 1
    fi
    echo $(date +%H:%M:%S) "waiting for wekafs deployment to complete, attempt #$cnt"
    cnt=$(($cnt + 1))
    sleep 10
done
echo $(date +%H:%M:%S) "deployment completed successfully"
echo $(date +%H:%M:%S) "$expected_running_pods plugin pods are running:"
kubectl get pods --namespace "$WEKAFS_NAMESPACE" 2>/dev/null | grep '^csi-wekafs.* Running '
