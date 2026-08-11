#!/usr/bin/env bash
# Stamp the Helm charts in the working tree with a version, so a chart built from a dirty tree
# installs the images that tree produces.
#
# CI does the same thing in its own workspace before packaging, and deliberately does not commit
# the result: the version is a property of the artifact, not of the source. The committed charts
# therefore carry the last released version, which is what keeps dev and main from conflicting on
# these files at every merge.
#
#   make update-charts                 stamp with the version derived from git
#   make update-charts VERSION=v9.9.9  stamp with an explicit version
#   make update-charts RESTORE=1       put the committed versions back
#
# Requires: yq.
set -euo pipefail

PLUGIN_CHART="charts/csi-wekafsplugin"
MS_CHART="charts/csi-metricsserver"
VERSION="${VERSION:-}"
RESTORE="${RESTORE:-}"

if [[ ! -d "$PLUGIN_CHART" ]]; then
  echo "run this from the repository root: $PLUGIN_CHART not found" >&2
  exit 1
fi

if [[ -n "$RESTORE" ]]; then
  # Everything this script touches is tracked, so git is the record of what it should be.
  paths="$PLUGIN_CHART/Chart.yaml $PLUGIN_CHART/values.yaml"
  [[ -d "$MS_CHART" ]] && paths="$paths $MS_CHART/Chart.yaml $MS_CHART/values.yaml"
  # shellcheck disable=SC2086
  git checkout -- $paths
  echo "restored the committed chart versions"
  exit 0
fi

if ! command -v yq >/dev/null 2>&1; then
  echo "yq is required" >&2
  exit 1
fi

if [[ -z "$VERSION" ]]; then
  # Same shape as the version CI computes: the last release tag plus the distance from it, so two
  # different commits never produce the same tag.
  VERSION="$(git describe --tags --always 2>/dev/null || true)"
  if [[ -z "$VERSION" ]]; then
    echo "could not derive a version from git; pass VERSION=... explicitly" >&2
    exit 1
  fi
fi

# The chart version must be valid SemVer, and git describe's "v2.9.0-6-gd343de7" already is.
# appVersion is a free-form string and keeps the leading v, matching what the release does.
APP_VERSION="$VERSION"
CHART_VERSION="${VERSION#v}"
DRIVER_VERSION="${VERSION#v}"

stamp_plugin() {
  yq -i ".version = \"$CHART_VERSION\"" "$PLUGIN_CHART/Chart.yaml"
  yq -i ".appVersion = \"$APP_VERSION\"" "$PLUGIN_CHART/Chart.yaml"
  yq -i ".sources[0] = \"https://github.com/weka/csi-wekafs/tree/$APP_VERSION/charts/csi-wekafsplugin\"" "$PLUGIN_CHART/Chart.yaml"
  yq -i ".csiDriverVersion = \"$DRIVER_VERSION\"" "$PLUGIN_CHART/values.yaml"
  echo "  $PLUGIN_CHART -> $CHART_VERSION (images tagged $DRIVER_VERSION)"
}

stamp_metricsserver() {
  [[ -d "$MS_CHART" ]] || return 0
  yq -i ".version = \"$CHART_VERSION\"" "$MS_CHART/Chart.yaml"
  yq -i ".appVersion = \"$APP_VERSION\"" "$MS_CHART/Chart.yaml"
  yq -i ".sources[0] = \"https://github.com/weka/csi-wekafs/tree/$APP_VERSION/charts/csi-metricsserver\"" "$MS_CHART/Chart.yaml"
  yq -i ".image.tag = \"$APP_VERSION\"" "$MS_CHART/values.yaml"
  echo "  $MS_CHART -> $CHART_VERSION (image tagged $APP_VERSION)"
}

stamp_plugin
stamp_metricsserver

cat <<EOF

Stamped for local use only - do not commit these files. The committed charts carry the last
released version on purpose, so that dev and main never disagree about them.

The diff will show more than the version lines: yq rewrites the file and drops blank lines. That
is what CI does too, so the chart you install locally matches the one it publishes.

  helm upgrade --install csi-wekafs -n csi-wekafs --create-namespace $PLUGIN_CHART
  make update-charts RESTORE=1     # put the committed versions back
EOF
