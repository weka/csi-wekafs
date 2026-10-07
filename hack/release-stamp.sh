#!/usr/bin/env bash
# Stamp the repository for a release: chart versions, generated docs, and release notes.
# Used by .github/workflows/release-v3.yaml; also runnable locally (then restore with git checkout).
#
#   hack/release-stamp.sh v3.0.0 notes.md
#
# Requires: yq, helm with the schema-gen plugin, docker (for helm-docs).
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <vX.Y.Z> <notes-file>" >&2
  exit 2
fi

VERSION="$1"
NOTES_FILE="$2"
CHART="charts/csi-wekafsplugin"

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like v1.2.3, got: $VERSION" >&2
  exit 2
fi
if [[ ! -f "$NOTES_FILE" ]]; then
  echo "notes file not found: $NOTES_FILE" >&2
  exit 2
fi
if [[ ! -d "$CHART" ]]; then
  echo "run this from the repository root: $CHART not found" >&2
  exit 1
fi

VERSION="$VERSION" hack/update-charts.sh >/dev/null
yq -i '
  .annotations."artifacthub.io/prerelease" = "false" |
  .annotations."artifacthub.io/containsSecurityUpdates" = "false" |
  del(.annotations."artifacthub.io/changes")
' "$CHART/Chart.yaml"

helm schema-gen "$CHART/values.yaml" >| "$CHART/values.schema.json"

# Same helm-docs invocation as the old release workflow: top-level README.md, then the chart README.
docker run --rm -v "$PWD:/data" -w /data jnorwood/helm-docs:latest \
  helm-docs -s file -c "$CHART" -o ../../README.md -t ../../README.md.gotmpl
docker run --rm -v "$PWD:/data" -w /data jnorwood/helm-docs:latest \
  helm-docs -s file -c "$CHART"

# Drop GitHub's generator comment and the "Full Changelog" footer, as the old workflow did.
changelog="$(mktemp)"
trap 'rm -f "$changelog" RELEASE.md.new' EXIT
grep -Ev '^(\*\*Full Changelog\*\*|<!-- Release notes generated)' "$NOTES_FILE" > "$changelog" || true

# chart-releaser takes the release body from the chart's CHANGELOG.md
cp "$changelog" "$CHART/CHANGELOG.md"

{ echo "# Release $VERSION"; cat "$changelog"; cat RELEASE.md; } > RELEASE.md.new
mv RELEASE.md.new RELEASE.md

echo "stamped $VERSION"
