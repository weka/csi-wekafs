#!/usr/bin/env python3
"""Reads `bliss info --all --json` on stdin and prints the clusters that are past their age.

Split out of reap-clusters.sh so it can be tested on a captured bliss output without an OCI
account or a live deployment, which is the only practical way to be confident in a script whose
failure mode is deleting somebody's cluster.

Prints one "<name>\t<age-in-hours>" line per expired cluster to stdout; everything else goes to
stderr so the caller can consume stdout directly.
"""
import datetime
import json
import sys


def expired(deployments, prefix, max_age_hours, now):
    # A single-cluster query returns an object; --all returns a list. Accept both, so a change
    # in bliss's output shape degrades into "nothing matched" rather than a crash.
    if isinstance(deployments, dict):
        deployments = [deployments]

    for d in deployments:
        name = d.get("cluster_name", "")
        created = d.get("created_at")
        if not name.startswith(prefix):
            continue
        if not created:
            # No timestamp means no way to judge age. Deleting on a guess is the one outcome
            # worse than leaving a cluster running.
            print(f"SKIP {name}: no created_at", file=sys.stderr)
            continue
        try:
            born = datetime.datetime.fromisoformat(created.replace("Z", "+00:00"))
        except ValueError:
            print(f"SKIP {name}: unparseable created_at {created!r}", file=sys.stderr)
            continue
        if born.tzinfo is None:
            born = born.replace(tzinfo=datetime.timezone.utc)
        age = (now - born).total_seconds() / 3600
        if age > max_age_hours:
            yield name, age
        else:
            print(f"KEEP {name}: {age:.1f}h old", file=sys.stderr)


def main():
    if len(sys.argv) != 3:
        print("usage: expired_clusters.py <max_age_hours> <name_prefix>", file=sys.stderr)
        return 2
    max_age_hours = float(sys.argv[1])
    prefix = sys.argv[2]
    if not prefix:
        print("refusing to run with an empty name prefix", file=sys.stderr)
        return 2

    raw = sys.stdin.read().strip() or "[]"
    try:
        deployments = json.loads(raw)
    except json.JSONDecodeError as err:
        print(f"could not parse bliss output: {err}", file=sys.stderr)
        return 1

    now = datetime.datetime.now(datetime.timezone.utc)
    for name, age in expired(deployments, prefix, max_age_hours, now):
        print(f"{name}\t{age:.1f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
