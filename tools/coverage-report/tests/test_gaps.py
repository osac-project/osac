"""Tests for gap extraction and next actions."""

import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.gaps import behavior_gaps, sort_gaps, tier_unassigned_gaps
from coverage_report.metrics import compute_behavior_metric
from coverage_report.model import (
    Behavior,
    EvidenceClass,
    EvidenceRecord,
    InventoryInfo,
    Tier,
    make_evidence_id,
)

FEATURE = "OSAC-1111"


def planned_record(tier=Tier.INTEGRATION, path="tests/e2e/mcp/planned.py"):
    subject = "{0}/TC-1".format(FEATURE)
    return EvidenceRecord(
        id=make_evidence_id("mappings", "planned-test-missing", subject, path),
        evidence_class=EvidenceClass.GAP_UNKNOWN,
        tier=tier,
        feature=FEATURE,
        subject=subject,
        source=path,
        collector="mappings",
        kind="planned-test-missing",
        details={"path": path, "name": "", "tier": tier.value},
    )


def compute(behaviors, records):
    return compute_behavior_metric(
        feature=FEATURE,
        tier=Tier.INTEGRATION,
        behaviors=behaviors,
        records=records,
        inventory=InventoryInfo(
            feature=FEATURE,
            path="enhancement-proposals/enhancements/OSAC-1111/testplan.md",
            revision="a" * 64,
        ),
        run_id="run-test",
    )


class TestGapActions(unittest.TestCase):
    def test_planned_gap_names_the_path_to_implement(self):
        record = planned_record()
        result = compute(
            [
                Behavior(
                    id=FEATURE + "/TC-1",
                    title="one",
                    priority="high",
                    required_tier=Tier.INTEGRATION,
                )
            ],
            [record],
        )

        gaps = behavior_gaps(
            feature=FEATURE,
            tier=Tier.INTEGRATION,
            assessments=result.assessments,
            records=[record],
            testplan="enhancement-proposals/enhancements/OSAC-1111/testplan.md",
        )

        self.assertEqual(len(gaps), 1)
        self.assertEqual(gaps[0]["reason"], "planned-not-implemented")
        self.assertEqual(
            gaps[0]["next_action"],
            "Implement tests/e2e/mcp/planned.py (planned in testplan)",
        )

    def test_tier_unassigned_gap_is_one_per_behavior_with_no_tier(self):
        result = compute(
            [
                Behavior(id=FEATURE + "/TC-1", title="one", priority="critical"),
                Behavior(id=FEATURE + "/TC-2", title="two", priority="high"),
            ],
            [],
        )

        gaps = tier_unassigned_gaps(
            feature=FEATURE,
            assessments=result.assessments,
            records=[],
            testplan="docs/testplan.md",
        )

        self.assertEqual(len(gaps), 2)
        for gap in gaps:
            self.assertIsNone(gap["tier"])
            self.assertEqual(gap["reason"], "tier-unassigned")
            self.assertIn("behavior_tiers", gap["next_action"])

    def test_sort_places_a_tierless_gap_after_tiered_ones_of_equal_priority(self):
        gaps = [
            {"priority": "high", "tier": None, "behavior": "b", "reason": "r"},
            {"priority": "high", "tier": "e2e", "behavior": "c", "reason": "r"},
            {"priority": "high", "tier": "unit", "behavior": "a", "reason": "r"},
        ]

        ordered = sort_gaps(gaps)

        self.assertEqual(
            [(gap["tier"], gap["behavior"]) for gap in ordered],
            [("unit", "a"), ("e2e", "c"), (None, "b")],
        )


if __name__ == "__main__":
    unittest.main()
