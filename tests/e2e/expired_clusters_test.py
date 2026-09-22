#!/usr/bin/env python3
"""Tests for the age filter that decides which clusters get deleted.

This is the one piece of the end-to-end tooling whose failure mode is destroying someone's
work, so it is tested against captured bliss output rather than only in anger. Run with:

    python3 -m unittest discover -s tests/e2e -p '*_test.py'
"""
import datetime
import unittest

from expired_clusters import expired

NOW = datetime.datetime(2026, 9, 22, 12, 0, 0, tzinfo=datetime.timezone.utc)
PREFIX = "csi-e2e-"


def deployment(name, hours_ago, **extra):
    born = NOW - datetime.timedelta(hours=hours_ago)
    d = {"cluster_name": name, "created_at": born.isoformat().replace("+00:00", "Z")}
    d.update(extra)
    return d


class ExpiredClusters(unittest.TestCase):
    def names(self, deployments, max_age_hours=12):
        return [name for name, _ in expired(deployments, PREFIX, max_age_hours, NOW)]

    def test_old_e2e_clusters_are_selected(self):
        self.assertEqual(
            self.names([deployment("csi-e2e-123", 13), deployment("csi-e2e-456", 40)]),
            ["csi-e2e-123", "csi-e2e-456"],
        )

    def test_young_clusters_are_left_alone(self):
        self.assertEqual(self.names([deployment("csi-e2e-123", 11.9)]), [])

    # The prefix is the only thing protecting hand-built clusters. A long-lived cluster someone
    # is working on must never be selected, however old it is.
    def test_foreign_clusters_are_never_selected(self):
        old = [
            deployment("sergey-dev", 1000),
            deployment("scale-test-10k", 5000),
            deployment("", 5000),
        ]
        self.assertEqual(self.names(old), [])

    # Near-misses on the prefix are still foreign clusters.
    def test_prefix_must_match_at_the_start(self):
        self.assertEqual(self.names([deployment("my-csi-e2e-123", 100)]), [])

    # Deleting because a timestamp could not be read would turn a parsing bug into data loss.
    def test_unreadable_timestamps_are_kept(self):
        cases = [
            {"cluster_name": "csi-e2e-1", "created_at": "not a date"},
            {"cluster_name": "csi-e2e-2", "created_at": ""},
            {"cluster_name": "csi-e2e-3"},
        ]
        self.assertEqual(self.names(cases), [])

    # bliss returns an object for a single cluster and a list for --all.
    def test_a_single_object_is_accepted(self):
        self.assertEqual(self.names(deployment("csi-e2e-solo", 99)), ["csi-e2e-solo"])

    def test_empty_input(self):
        self.assertEqual(self.names([]), [])

    # A timestamp without a zone is read as UTC rather than as local time, which would shift the
    # age by the runner's offset and reap clusters early.
    def test_naive_timestamps_are_read_as_utc(self):
        naive = {
            "cluster_name": "csi-e2e-naive",
            "created_at": (NOW - datetime.timedelta(hours=13)).replace(tzinfo=None).isoformat(),
        }
        self.assertEqual(self.names([naive]), ["csi-e2e-naive"])


if __name__ == "__main__":
    unittest.main()
