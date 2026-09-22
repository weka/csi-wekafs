#!/usr/bin/env bash
#
# Deletes end-to-end clusters older than MAX_AGE_HOURS. See the comment at the top of
# .github/workflows/e2e-bliss-reap.yaml for why this exists rather than a bliss TTL.
#
# Env:
#   MAX_AGE_HOURS  age past which a cluster is deleted (default 12)
#   DRY_RUN        "true" to report without deleting (default "true", so a mistake costs nothing)
#   NAME_PREFIX    only clusters whose name starts with this are ever considered
#
set -euo pipefail

MAX_AGE_HOURS="${MAX_AGE_HOURS:-12}"
DRY_RUN="${DRY_RUN:-true}"
NAME_PREFIX="${NAME_PREFIX:-csi-e2e-}"

# The prefix is the only thing standing between this script and somebody's hand-built cluster.
# An empty prefix would match every deployment in the account, so refuse it outright rather than
# treat it as "no filter".
if [[ -z "${NAME_PREFIX}" ]]; then
  echo "NAME_PREFIX is empty; refusing to consider every deployment in the account" >&2
  exit 2
fi

echo "Reaping deployments named ${NAME_PREFIX}* older than ${MAX_AGE_HOURS}h (dry_run=${DRY_RUN})"

# `bliss info --all --json` is the only listing that carries created_at, which is what age is
# computed from. If it fails, stop: an empty list from a broken call is indistinguishable from
# an empty list from a clean account, and the second is not worth guessing at.
if ! ALL="$(bliss info --all --json)"; then
  echo "bliss info --all --json failed; not reaping anything" >&2
  exit 1
fi

EXPIRED="$(printf '%s' "${ALL}" | "$(dirname "${BASH_SOURCE[0]}")/expired_clusters.py" "${MAX_AGE_HOURS}" "${NAME_PREFIX}")"

if [[ -z "${EXPIRED}" ]]; then
  echo "Nothing to reap."
  exit 0
fi

FAILED=0
while IFS=$'\t' read -r name age; do
  [[ -n "${name}" ]] || continue
  if [[ "${DRY_RUN}" == "true" ]]; then
    echo "WOULD DELETE ${name} (${age}h old)"
    continue
  fi
  echo "Deleting ${name} (${age}h old)"
  # One failure must not strand the rest: a cluster that resists deletion is exactly the kind
  # that also needs the next one cleaned up.
  if ! bliss delete "${name}"; then
    echo "FAILED to delete ${name}" >&2
    FAILED=1
  fi
done <<<"${EXPIRED}"

exit "${FAILED}"
