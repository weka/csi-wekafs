#!/usr/bin/env bash
# Update the CSI sidecar image tags in the Helm chart to their latest upstream releases.
#
# The sidecars are published to registry.k8s.io/sig-storage but released from GitHub, so the
# release tag is what this reads. Renovate covers only some of them, which is how csi-attacher and
# external-health-monitor drifted several releases behind without anything noticing - this target
# checks all of them, whatever Renovate is configured to watch.
#
#   make update-sidecars          report what is out of date, change nothing
#   make update-sidecars APPLY=1  rewrite charts/csi-wekafsplugin/values.yaml in place
#
# Requires: gh (authenticated), and for APPLY=1 nothing beyond sed.
set -euo pipefail

VALUES="charts/csi-wekafsplugin/values.yaml"
APPLY="${APPLY:-}"

# image name in registry.k8s.io/sig-storage : GitHub repo under kubernetes-csi
SIDECARS=(
  "livenessprobe:livenessprobe"
  "csi-attacher:external-attacher"
  "csi-provisioner:external-provisioner"
  "csi-node-driver-registrar:node-driver-registrar"
  "csi-resizer:external-resizer"
  "csi-snapshotter:external-snapshotter"
  "csi-external-health-monitor-controller:external-health-monitor"
)

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is required to query upstream releases" >&2
  exit 1
fi
if [[ ! -f "$VALUES" ]]; then
  echo "run this from the repository root: $VALUES not found" >&2
  exit 1
fi

# latest_release prints the highest release tag for a kubernetes-csi repo.
#
# Two traps here, both of which produced wrong answers before this was written as it is:
#
#  - external-snapshotter and external-health-monitor tag several components out of one repo
#    ("client/v8.6.0" alongside "v8.6.0"), so the tag has to be filtered to a bare vX.Y.Z.
#  - the newest release is not the first one GitHub returns. Releases come back newest-published
#    first, and a patch on an older line is often published after a newer minor - at the time of
#    writing external-provisioner's most recent release is v6.2.1, published after v6.3.0. Taking
#    the first entry therefore proposes a downgrade. Sort by version and take the maximum.
latest_release() {
  local repo="$1"
  gh api "repos/kubernetes-csi/${repo}/releases" --paginate \
    --jq '.[] | select(.prerelease == false and .draft == false) | .tag_name | select(test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))' \
    2>/dev/null | sort -V | tail -1
}

outdated=0
checked=0
for entry in "${SIDECARS[@]}"; do
  image="${entry%%:*}"
  repo="${entry##*:}"

  current=$(grep -oE "sig-storage/${image}:v[0-9]+\.[0-9]+\.[0-9]+" "$VALUES" | head -1 | sed 's/.*://')
  if [[ -z "$current" ]]; then
    echo "  ?  ${image}: not found in ${VALUES}, skipping"
    continue
  fi
  checked=$((checked + 1))

  latest=$(latest_release "$repo")
  if [[ -z "$latest" ]]; then
    echo "  !  ${image}: could not determine the latest release of kubernetes-csi/${repo}"
    continue
  fi

  if [[ "$current" == "$latest" ]]; then
    printf "  ok %-42s %s\n" "$image" "$current"
    continue
  fi

  outdated=$((outdated + 1))
  printf "  UP %-42s %s -> %s\n" "$image" "$current" "$latest"
  if [[ -n "$APPLY" ]]; then
    sed -i.bak "s|sig-storage/${image}:${current}|sig-storage/${image}:${latest}|" "$VALUES"
    rm -f "${VALUES}.bak"
  fi
done

echo
if [[ "$outdated" -eq 0 ]]; then
  echo "all ${checked} sidecars are current"
  exit 0
fi

if [[ -n "$APPLY" ]]; then
  echo "${outdated} of ${checked} updated in ${VALUES}"
  echo
  echo "A sidecar bump is not only a version string. Before committing, check that:"
  echo "  - every flag the chart passes still exists in the new release"
  echo "  - no new RBAC is required"
  echo "  - the minimum Kubernetes version is still satisfied"
  echo "and read the release notes for anything that changes behaviour by default."
else
  echo "${outdated} of ${checked} sidecars are behind; re-run with APPLY=1 to update ${VALUES}"
  exit 1
fi
