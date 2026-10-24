"""Contract tests for the evidence model and its invariants."""

import itertools
import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.errors import EvidencePromotionError, ModelError
from coverage_report.model import (
    ALLOWED_EVIDENCE_TRANSITIONS,
    Boundary,
    Behavior,
    CollectorBatch,
    Diagnostic,
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    Metric,
    MetricState,
    Tier,
    InventoryInfo,
    assert_no_promotion,
    make_evidence_id,
    merge_batches,
    revision_freshness,
)


def record(
    *,
    subject="subject-a",
    source="source-a",
    collector="collector-a",
    kind="statement",
    evidence_class=EvidenceClass.MEASURED,
    tier=Tier.UNIT,
    feature="OSAC-2540",
    confidence=None,
    heuristic=None,
):
    return EvidenceRecord(
        id=make_evidence_id(collector, kind, subject, source),
        evidence_class=evidence_class,
        tier=tier,
        feature=feature,
        subject=subject,
        source=source,
        collector=collector,
        kind=kind,
        confidence=confidence,
        heuristic=heuristic,
    )


class TestEvidenceIds(unittest.TestCase):
    def test_id_is_deterministic(self):
        first = make_evidence_id("c", "k", "s", "x")
        second = make_evidence_id("c", "k", "s", "x")
        self.assertEqual(first, second)
        self.assertRegex(first, r"^ev-[0-9a-f]{16}$")

    def test_every_identity_field_changes_the_id(self):
        base = make_evidence_id("c", "k", "s", "x")
        variants = {
            make_evidence_id("c2", "k", "s", "x"),
            make_evidence_id("c", "k2", "s", "x"),
            make_evidence_id("c", "k", "s2", "x"),
            make_evidence_id("c", "k", "s", "x2"),
        }
        self.assertNotIn(base, variants)
        self.assertEqual(len(variants), 4)

    def test_id_ignores_revision_and_timestamp(self):
        first = record()
        second = EvidenceRecord(
            id=first.id,
            evidence_class=first.evidence_class,
            tier=first.tier,
            feature=first.feature,
            subject=first.subject,
            source=first.source,
            collector=first.collector,
            kind=first.kind,
            revision="new-sha",
            timestamp="2026-10-22T00:00:00Z",
        )
        self.assertEqual(first.id, second.id)


class TestEvidenceRecord(unittest.TestCase):
    def test_confidence_is_only_allowed_for_inferred_mapping(self):
        with self.assertRaises(ModelError):
            record(evidence_class=EvidenceClass.MEASURED, confidence=0.9)
        with self.assertRaises(ModelError):
            record(evidence_class=EvidenceClass.DECLARED_MAPPING, heuristic="name-match")

        inferred = record(
            evidence_class=EvidenceClass.INFERRED_MAPPING,
            confidence=0.42,
            heuristic="path-similarity",
        )
        self.assertEqual(inferred.confidence, 0.42)
        self.assertEqual(inferred.heuristic, "path-similarity")

    def test_confidence_is_bounded(self):
        with self.assertRaises(ModelError):
            record(evidence_class=EvidenceClass.INFERRED_MAPPING, confidence=1.5)

    def test_boundary_is_normalized_and_sorted(self):
        item = EvidenceRecord(
            id="ev-0000000000000000",
            evidence_class=EvidenceClass.OBSERVED_EXECUTION,
            tier=Tier.E2E,
            feature="OSAC-2540",
            subject="subject",
            source="source",
            collector="collector",
            kind="junit",
            boundary=Boundary(real=("b", "a"), simulated=("z",)),
        )
        self.assertEqual(item.boundary.real, ("a", "b"))
        self.assertEqual(item.to_dict()["boundary"], {"real": ["a", "b"], "simulated": ["z"]})

    def test_blank_subject_is_rejected(self):
        with self.assertRaises(ModelError):
            EvidenceRecord(
                id="ev-0000000000000000",
                evidence_class=EvidenceClass.MEASURED,
                tier=Tier.UNIT,
                feature="OSAC-2540",
                subject="",
                source="source",
                collector="collector",
                kind="statement",
            )


class TestBehaviors(unittest.TestCase):
    def test_unassessable_behavior_requires_a_reason(self):
        with self.assertRaises(ModelError):
            Behavior(id="OSAC-2540/TC-1", title="t", assessable=False)

        behavior = Behavior(
            id="OSAC-2540/TC-1",
            title="t",
            assessable=False,
            exclusion_reason="requires hardware not in CI",
        )
        self.assertEqual(behavior.exclusion_reason, "requires hardware not in CI")

    def test_unknown_required_boundary_is_rejected(self):
        with self.assertRaises(ModelError):
            Behavior(
                id="OSAC-2540/TC-1", title="t", required_boundary=("cloud",)
            )


class TestMetrics(unittest.TestCase):
    def test_value_state_needs_a_positive_denominator(self):
        with self.assertRaises(ModelError):
            Metric(
                feature="OSAC-2540",
                tier=Tier.UNIT,
                metric_version="statement-weighted-v2",
                state=MetricState.VALUE,
                numerator=1,
                denominator=0,
                value_percent=100.0,
            )

    def test_non_value_states_reject_a_percentage(self):
        with self.assertRaises(ModelError):
            Metric(
                feature="OSAC-2540",
                tier=Tier.UNIT,
                metric_version="statement-weighted-v2",
                state=MetricState.UNAVAILABLE,
                value_percent=0.0,
            )

    def test_zero_is_distinct_from_unavailable(self):
        zero = Metric(
            feature="OSAC-2540",
            tier=Tier.UNIT,
            metric_version="statement-weighted-v2",
            state=MetricState.ZERO,
            numerator=0,
            denominator=10,
            value_percent=0.0,
        )
        unavailable = Metric(
            feature="OSAC-2540",
            tier=Tier.UNIT,
            metric_version="statement-weighted-v2",
            state=MetricState.UNAVAILABLE,
        )
        self.assertEqual(zero.to_dict()["state"], "zero")
        self.assertIsNone(unavailable.to_dict()["value_percent"])


class TestMerge(unittest.TestCase):
    def test_dedupes_by_id(self):
        first = record()
        batch = CollectorBatch(collector="collector-a", records=(first, first))
        merged = merge_batches([batch])
        self.assertEqual(len(merged.records), 1)

    def test_conflicting_duplicate_ids_fail_regardless_of_order(self):
        first = record()
        conflicting = EvidenceRecord(
            id=first.id,
            evidence_class=first.evidence_class,
            tier=first.tier,
            feature=first.feature,
            subject=first.subject,
            source=first.source,
            collector=first.collector,
            kind=first.kind,
            details={"different": True},
        )
        with self.assertRaises(ModelError) as forward:
            merge_batches(
                [
                    CollectorBatch(collector="a", records=(first,)),
                    CollectorBatch(collector="a", records=(conflicting,)),
                ]
            )
        with self.assertRaises(ModelError) as backward:
            merge_batches(
                [
                    CollectorBatch(collector="a", records=(conflicting,)),
                    CollectorBatch(collector="a", records=(first,)),
                ]
            )
        self.assertEqual(forward.exception.code, backward.exception.code)

    def test_output_is_identical_for_every_batch_permutation(self):
        batches = [
            CollectorBatch(
                collector="a",
                records=(record(subject="s2", source="z"), record(subject="s1")),
                behaviors=(Behavior(id="OSAC-2540/TC-2", title="two"),),
                metrics=(
                    Metric(
                        feature="OSAC-2540",
                        tier=Tier.E2E,
                        metric_version="behavior-inventory-v1",
                        state=MetricState.NA,
                    ),
                ),
                diagnostics=(
                    Diagnostic(
                        run_id="run-x",
                        collector="a",
                        operation="collect",
                        code="SOMETHING",
                        message="m",
                        severity="info",
                    ),
                ),
            ),
            CollectorBatch(
                collector="b",
                records=(record(collector="b", subject="s3"),),
                behaviors=(Behavior(id="OSAC-2540/TC-1", title="one"),),
                metrics=(
                    Metric(
                        feature="OSAC-2540",
                        tier=Tier.UNIT,
                        metric_version="statement-weighted-v2",
                        state=MetricState.NA,
                    ),
                ),
            ),
        ]
        baseline = merge_batches(batches).to_dict()
        for permutation in itertools.permutations(batches):
            self.assertEqual(merge_batches(permutation).to_dict(), baseline)

    def test_metrics_are_ordered_by_feature_then_tier(self):
        batches = [
            CollectorBatch(
                collector="a",
                metrics=(
                    Metric(
                        feature="OSAC-3000",
                        tier=Tier.UNIT,
                        metric_version="statement-weighted-v2",
                        state=MetricState.NA,
                    ),
                    Metric(
                        feature="OSAC-2540",
                        tier=Tier.E2E,
                        metric_version="behavior-inventory-v1",
                        state=MetricState.NA,
                    ),
                ),
            )
        ]
        merged = merge_batches(batches)
        self.assertEqual(
            [metric.sort_key() for metric in merged.metrics],
            [("OSAC-2540", "e2e"), ("OSAC-3000", "unit")],
        )


class TestNoPromotion(unittest.TestCase):
    def test_identity_is_allowed_for_every_class(self):
        for evidence_class in EvidenceClass:
            self.assertIsNone(
                assert_no_promotion(evidence_class, evidence_class)
            )

    def test_only_inferred_to_declared_is_allowed(self):
        for previous, current in itertools.product(EvidenceClass, EvidenceClass):
            if previous is current:
                continue
            if (previous, current) in ALLOWED_EVIDENCE_TRANSITIONS:
                assert_no_promotion(previous, current, evidence_id="ev-1")
                continue
            with self.assertRaises(EvidencePromotionError) as raised:
                assert_no_promotion(previous, current, evidence_id="ev-1")
            self.assertEqual(raised.exception.code, "EVIDENCE_PROMOTION")
            self.assertEqual(
                raised.exception.details,
                {"evidence_id": "ev-1", "from": previous.value, "to": current.value},
            )

    def test_merge_keeps_declared_regardless_of_order(self):
        inferred = record(
            evidence_class=EvidenceClass.INFERRED_MAPPING,
            confidence=0.5,
            heuristic="h",
        )
        declared = EvidenceRecord(
            id=inferred.id,
            evidence_class=EvidenceClass.DECLARED_MAPPING,
            tier=inferred.tier,
            feature=inferred.feature,
            subject=inferred.subject,
            source=inferred.source,
            collector=inferred.collector,
            kind=inferred.kind,
        )
        for pair in ((inferred, declared), (declared, inferred)):
            merged = merge_batches(
                [CollectorBatch(collector="a", records=pair)]
            )
            self.assertEqual(len(merged.records), 1)
            self.assertIs(
                merged.records[0].evidence_class,
                EvidenceClass.DECLARED_MAPPING,
            )

    def test_merge_rejects_every_other_class_change(self):
        measured = record()
        declared = EvidenceRecord(
            id=measured.id,
            evidence_class=EvidenceClass.DECLARED_MAPPING,
            tier=measured.tier,
            feature=measured.feature,
            subject=measured.subject,
            source=measured.source,
            collector=measured.collector,
            kind=measured.kind,
        )
        for pair in ((measured, declared), (declared, measured)):
            with self.assertRaises(EvidencePromotionError) as raised:
                merge_batches([CollectorBatch(collector="a", records=pair)])
            self.assertEqual(raised.exception.code, "EVIDENCE_PROMOTION")

    def test_new_evidence_ids_are_not_promotions(self):
        first = record(subject="s1")
        second = record(subject="s2")
        merged = merge_batches(
            [CollectorBatch(collector="a", records=(first, second))]
        )
        self.assertEqual(len(merged.records), 2)


class TestRevisionFreshness(unittest.TestCase):
    SHA = "0123456789abcdef0123456789abcdef01234567"

    def test_exact_match_is_current(self):
        self.assertIs(
            revision_freshness(self.SHA, self.SHA), Freshness.CURRENT
        )
        self.assertIs(
            revision_freshness(self.SHA.upper(), self.SHA), Freshness.CURRENT
        )

    def test_abbreviated_prefix_of_seven_or_more_is_current(self):
        for length in (7, 12, 40):
            self.assertIs(
                revision_freshness(self.SHA[:length], self.SHA),
                Freshness.CURRENT,
            )
            self.assertIs(
                revision_freshness(self.SHA, self.SHA[:length]),
                Freshness.CURRENT,
            )

    def test_short_prefix_is_stale(self):
        self.assertIs(
            revision_freshness(self.SHA[:6], self.SHA), Freshness.STALE
        )

    def test_different_revision_is_stale(self):
        self.assertIs(
            revision_freshness("f" * 40, self.SHA), Freshness.STALE
        )
        self.assertIs(
            revision_freshness(self.SHA[:-1] + "0", self.SHA), Freshness.STALE
        )

    def test_missing_value_is_unknown(self):
        for record, sha in ((None, self.SHA), (self.SHA, None), ("", self.SHA)):
            self.assertIs(revision_freshness(record, sha), Freshness.UNKNOWN)

    def test_non_hex_prefix_is_not_treated_as_a_commit_prefix(self):
        self.assertIs(
            revision_freshness("release-1", "release-1-candidate"),
            Freshness.STALE,
        )


class TestInventoryInfo(unittest.TestCase):
    def test_duplicate_ids_are_sorted_and_exposed(self):
        inventory = InventoryInfo(
            feature="OSAC-2540",
            path="docs/testplan.md",
            revision="a" * 64,
            behavior_count=2,
            duplicate_ids=("OSAC-2540/TC-2", "OSAC-2540/TC-1"),
        )
        self.assertEqual(
            inventory.duplicate_ids, ("OSAC-2540/TC-1", "OSAC-2540/TC-2")
        )
        self.assertEqual(
            inventory.to_dict()["duplicate_ids"],
            ["OSAC-2540/TC-1", "OSAC-2540/TC-2"],
        )

    def test_merge_is_order_independent_and_rejects_conflicts(self):
        first = CollectorBatch(
            collector="a",
            inventories=(InventoryInfo(feature="OSAC-2540", path="a.md"),),
        )
        duplicate = CollectorBatch(
            collector="b",
            inventories=(InventoryInfo(feature="OSAC-2540", path="a.md"),),
        )
        conflict = CollectorBatch(
            collector="b",
            inventories=(InventoryInfo(feature="OSAC-2540", path="b.md"),),
        )
        self.assertEqual(
            merge_batches([first, duplicate]).inventories,
            (InventoryInfo(feature="OSAC-2540", path="a.md"),),
        )
        with self.assertRaises(ModelError):
            merge_batches([first, conflict])


class TestFreshnessAndTierCoverage(unittest.TestCase):
    def test_freshness_values(self):
        self.assertEqual(
            {item.value for item in Freshness}, {"current", "stale", "unknown"}
        )

    def test_tier_values(self):
        self.assertEqual({item.value for item in Tier}, {"unit", "integration", "e2e"})


if __name__ == "__main__":
    unittest.main()
