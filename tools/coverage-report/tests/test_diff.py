"""Tests for report-to-report comparison."""

import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.diff import NOT_COMPARABLE, build_diff


def metric(
    *,
    feature="OSAC-1111",
    tier="integration",
    state="value",
    numerator=2,
    denominator=5,
    value_percent=40.0,
    metric_version="behavior-inventory-v1",
    inventory_revision="a" * 64,
):
    return {
        "feature": feature,
        "tier": tier,
        "metric_version": metric_version,
        "state": state,
        "numerator": numerator,
        "denominator": denominator,
        "value_percent": value_percent,
        "inventory_revision": inventory_revision,
    }


def report(
    *,
    metrics=None,
    behaviors=(),
    evidence=(),
    execution=None,
    run_id="run-0000000000000000",
    generated_at="2026-10-22T00:00:00Z",
):
    execution = execution or {}
    return {
        "run_id": run_id,
        "generated_at": generated_at,
        "metrics": list(metrics or []),
        "behaviors": [{"id": behavior_id} for behavior_id in behaviors],
        "evidence": list(evidence),
        "features": [
            {
                "key": "OSAC-1111",
                "behaviors": [
                    {
                        "id": behavior_id,
                        "tiers": {
                            tier: {"execution_status": status}
                            for tier, status in execution.get(behavior_id, {}).items()
                        },
                    }
                    for behavior_id in behaviors
                ],
            }
        ],
    }


class TestDiff(unittest.TestCase):
    def test_no_previous_report(self):
        diff = build_diff(None, report(metrics=[metric()]))
        self.assertFalse(diff["has_previous"])
        self.assertIsNone(diff["previous_run_id"])
        self.assertEqual(diff["metrics"], [])

    def test_same_version_and_inventory_is_a_trend(self):
        previous = report(
            metrics=[metric(numerator=2, value_percent=40.0)],
            run_id="run-1111111111111111",
        )
        current = report(metrics=[metric(numerator=3, value_percent=60.0)])

        diff = build_diff(previous, current)

        self.assertTrue(diff["has_previous"])
        self.assertEqual(diff["previous_run_id"], "run-1111111111111111")
        self.assertEqual(len(diff["metrics"]), 1)
        entry = diff["metrics"][0]
        self.assertEqual(entry["trend"], "changed")
        self.assertEqual(entry["previous"]["numerator"], 2)
        self.assertEqual(entry["current"]["numerator"], 3)

    def test_changed_inventory_revision_is_not_comparable(self):
        previous = report(metrics=[metric(inventory_revision="a" * 64)])
        current = report(metrics=[metric(inventory_revision="b" * 64)])

        diff = build_diff(previous, current)

        entry = diff["metrics"][0]
        self.assertEqual(entry["trend"], "not-comparable")
        self.assertEqual(entry["reason"], NOT_COMPARABLE)

    def test_changed_metric_version_is_not_comparable(self):
        previous = report(
            metrics=[metric(tier="unit", metric_version="statement-weighted-v2")]
        )
        current = report(
            metrics=[metric(tier="unit", metric_version="legacy-package-average-v1")]
        )

        diff = build_diff(previous, current)

        self.assertEqual(diff["metrics"][0]["trend"], "not-comparable")

    def test_unchanged_metric_is_omitted(self):
        diff = build_diff(report(metrics=[metric()]), report(metrics=[metric()]))
        self.assertEqual(diff["metrics"], [])

    def test_behaviors_added_and_removed(self):
        diff = build_diff(
            report(behaviors=("OSAC-1111/TC-1", "OSAC-1111/TC-2")),
            report(behaviors=("OSAC-1111/TC-2", "OSAC-1111/TC-3")),
        )
        self.assertEqual(diff["behaviors_added"], ["OSAC-1111/TC-3"])
        self.assertEqual(diff["behaviors_removed"], ["OSAC-1111/TC-1"])

    def test_mapping_class_change(self):
        previous = report(
            evidence=[
                {"id": "ev-0000000000000001", "evidence_class": "inferred_mapping"}
            ]
        )
        current = report(
            evidence=[
                {"id": "ev-0000000000000001", "evidence_class": "declared_mapping"}
            ]
        )

        diff = build_diff(previous, current)

        self.assertEqual(
            diff["mapping_class_changes"],
            [
                {
                    "evidence_id": "ev-0000000000000001",
                    "previous": "inferred_mapping",
                    "current": "declared_mapping",
                }
            ],
        )

    def test_execution_status_change(self):
        previous = report(
            behaviors=("OSAC-1111/TC-1",),
            execution={"OSAC-1111/TC-1": {"integration": "not-executed"}},
        )
        current = report(
            behaviors=("OSAC-1111/TC-1",),
            execution={
                "OSAC-1111/TC-1": {"integration": "executed-pass-current"}
            },
        )

        diff = build_diff(previous, current)

        self.assertEqual(
            diff["execution_status_changes"],
            [
                {
                    "feature": "OSAC-1111",
                    "behavior": "OSAC-1111/TC-1",
                    "tier": "integration",
                    "previous": "not-executed",
                    "current": "executed-pass-current",
                }
            ],
        )

    def test_availability_change(self):
        previous = report(metrics=[metric(state="unavailable", numerator=None)])
        current = report(metrics=[metric(state="value")])

        diff = build_diff(previous, current)

        self.assertEqual(
            diff["availability_changes"],
            [
                {
                    "feature": "OSAC-1111",
                    "tier": "integration",
                    "previous": "unavailable",
                    "current": "value",
                }
            ],
        )

    def test_metrics_added_and_removed(self):
        diff = build_diff(
            report(metrics=[]),
            report(metrics=[metric()]),
        )
        self.assertEqual(
            diff["metrics_added"],
            [
                {
                    "feature": "OSAC-1111",
                    "tier": "integration",
                    "trend": "added",
                }
            ],
        )


if __name__ == "__main__":
    unittest.main()
