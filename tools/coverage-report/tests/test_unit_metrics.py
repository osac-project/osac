"""Contract tests for the pure unit-coverage metric arithmetic."""

import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.model import (
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    MetricState,
    Tier,
    make_evidence_id,
)
from coverage_report.serialize import canonical_digest
from coverage_report.unit_metrics import (
    COVERAGE_KIND,
    LEGACY_PACKAGE_AVERAGE,
    MISSING_KIND,
    STATEMENT_WEIGHTED,
    compute_unit_metric,
    percent_of,
    unit_scope_revision,
)

FEATURE = "OSAC-1000"


def measured(
    subject,
    statements,
    covered,
    *,
    freshness=Freshness.CURRENT,
    feature=FEATURE,
    percent=None,
):
    if percent is None:
        percent = percent_of(covered, statements)
    return EvidenceRecord(
        id=make_evidence_id("ginkgo-unit", COVERAGE_KIND, subject, "cover.out"),
        evidence_class=EvidenceClass.MEASURED,
        tier=Tier.UNIT,
        feature=feature,
        subject=subject,
        source="cover.out",
        collector="ginkgo-unit",
        kind=COVERAGE_KIND,
        freshness=freshness,
        details={
            "statements": statements,
            "covered": covered,
            "blocks": 1,
            "percent": percent,
        },
    )


def gap(subject, *, feature=FEATURE):
    return EvidenceRecord(
        id=make_evidence_id("ginkgo-unit", MISSING_KIND, subject, "cover.out"),
        evidence_class=EvidenceClass.GAP_UNKNOWN,
        tier=Tier.UNIT,
        feature=feature,
        subject=subject,
        source="cover.out",
        collector="ginkgo-unit",
        kind=MISSING_KIND,
        freshness=Freshness.UNKNOWN,
        details={"pattern": subject},
    )


def compute(records, *, version=STATEMENT_WEIGHTED, expected=2):
    return compute_unit_metric(
        FEATURE,
        records,
        metric_version=version,
        expected_packages=expected,
        inventory_revision="a" * 40,
    )


class TestLegacyPackageAverage(unittest.TestCase):
    def test_mean_of_package_percentages(self):
        records = [
            measured("pkg/a", 5, 2, percent=40.0),
            measured("pkg/b", 4, 2, percent=50.0),
        ]
        outcome = compute(records, version=LEGACY_PACKAGE_AVERAGE)

        self.assertEqual(outcome.metric.state, MetricState.VALUE)
        self.assertEqual(outcome.metric.value_percent, 45.0)
        # 400 + 500 tenths over 2 packages * 10.
        self.assertEqual(outcome.metric.numerator, 900)
        self.assertEqual(outcome.metric.denominator, 20)
        self.assertIn("pkg/a=40.0", outcome.metric.formula)
        self.assertIn("pkg/b=50.0", outcome.metric.formula)

    def test_round_half_up_at_the_second_decimal(self):
        records = [
            measured("pkg/a", 100, 66, percent=66.6),
            measured("pkg/b", 100, 67, percent=66.7),
        ]
        outcome = compute(records, version=LEGACY_PACKAGE_AVERAGE)

        # 66.65 rounds half-up to 66.7 (banker's rounding would give 66.6).
        self.assertEqual(outcome.metric.value_percent, 66.7)

    def test_package_average_does_not_equal_statement_weighting(self):
        records = [
            measured("pkg/small", 1, 1, percent=100.0),
            measured("pkg/large", 99, 0, percent=0.0),
        ]
        legacy = compute(records, version=LEGACY_PACKAGE_AVERAGE)
        weighted = compute(records, version=STATEMENT_WEIGHTED)

        self.assertEqual(legacy.metric.value_percent, 50.0)
        self.assertEqual(weighted.metric.value_percent, 1.0)
        self.assertEqual(weighted.metric.numerator, 1)
        self.assertEqual(weighted.metric.denominator, 100)


class TestStatementWeighted(unittest.TestCase):
    def test_weighted_value(self):
        records = [
            measured("pkg/a", 5, 2, percent=40.0),
            measured("pkg/b", 4, 2, percent=50.0),
        ]
        outcome = compute(records)

        self.assertEqual(outcome.metric.state, MetricState.VALUE)
        self.assertEqual(outcome.metric.value_percent, 44.4)
        self.assertEqual(outcome.metric.numerator, 4)
        self.assertEqual(outcome.metric.denominator, 9)
        self.assertEqual(outcome.metric.tier, Tier.UNIT)
        self.assertEqual(outcome.metric.inventory_revision, "a" * 40)

    def test_inputs_are_the_sorted_evidence_ids(self):
        records = [
            measured("pkg/b", 1, 1),
            measured("pkg/a", 1, 0),
        ]
        outcome = compute(records)

        self.assertEqual(
            list(outcome.metric.inputs),
            sorted(record.id for record in records),
        )

    def test_unsupported_version_is_a_programming_error(self):
        with self.assertRaises(ValueError):
            compute([measured("pkg/a", 1, 1)], version="nope-v9")


class TestStates(unittest.TestCase):
    def test_measured_zero_is_zero_not_value(self):
        outcome = compute([measured("pkg/a", 4, 0, percent=0.0)])

        self.assertEqual(outcome.metric.state, MetricState.ZERO)
        self.assertEqual(outcome.metric.value_percent, 0.0)
        self.assertEqual(outcome.metric.numerator, 0)
        self.assertEqual(outcome.metric.denominator, 4)

    def test_no_records_is_unavailable_not_zero(self):
        outcome = compute([])

        self.assertEqual(outcome.metric.state, MetricState.UNAVAILABLE)
        self.assertIsNone(outcome.metric.value_percent)
        self.assertEqual(outcome.reason_code, "UNIT_EVIDENCE_UNAVAILABLE")

    def test_no_mapped_packages_is_blocked(self):
        outcome = compute([], expected=0)

        self.assertEqual(outcome.metric.state, MetricState.BLOCKED)
        self.assertEqual(outcome.reason_code, "UNIT_METRIC_SCOPE_INCOMPLETE")

    def test_missing_package_blocks_and_keeps_partial_arithmetic(self):
        records = [
            measured("pkg/a", 5, 2, percent=40.0),
            gap("pkg/absent"),
        ]
        outcome = compute(records)

        self.assertEqual(outcome.metric.state, MetricState.BLOCKED)
        self.assertIsNone(outcome.metric.value_percent)
        self.assertEqual(outcome.metric.numerator, 2)
        self.assertEqual(outcome.metric.denominator, 5)
        self.assertEqual(outcome.reason_code, "UNIT_PACKAGE_MISSING")
        self.assertIn("partial over 1 of 2", outcome.metric.formula)

    def test_all_stale_is_stale(self):
        records = [
            measured("pkg/a", 5, 2, freshness=Freshness.STALE),
            measured("pkg/b", 4, 2, freshness=Freshness.STALE),
        ]
        outcome = compute(records)

        self.assertEqual(outcome.metric.state, MetricState.STALE)
        self.assertIsNone(outcome.metric.value_percent)
        self.assertIsNone(outcome.metric.numerator)
        self.assertIsNone(outcome.metric.denominator)
        self.assertEqual(outcome.reason_code, "UNIT_EVIDENCE_STALE")

    def test_partial_stale_blocks(self):
        records = [
            measured("pkg/a", 5, 2, freshness=Freshness.STALE),
            measured("pkg/b", 4, 2, freshness=Freshness.CURRENT),
        ]
        outcome = compute(records)

        self.assertEqual(outcome.metric.state, MetricState.BLOCKED)
        self.assertIsNone(outcome.metric.value_percent)
        self.assertEqual(
            outcome.reason_code, "UNIT_EVIDENCE_PARTIAL_STALE"
        )

    def test_unknown_freshness_still_reports_a_value(self):
        outcome = compute([measured("pkg/a", 4, 2, freshness=Freshness.UNKNOWN)])

        self.assertEqual(outcome.metric.state, MetricState.VALUE)
        self.assertEqual(outcome.metric.value_percent, 50.0)

    def test_no_statements_is_na(self):
        outcome = compute([measured("pkg/a", 0, 0, percent=0.0)])

        self.assertEqual(outcome.metric.state, MetricState.NA)
        self.assertIsNone(outcome.metric.value_percent)
        self.assertEqual(outcome.reason_code, "UNIT_METRIC_NO_STATEMENTS")

    def test_records_for_another_feature_are_ignored(self):
        records = [measured("pkg/a", 4, 2, feature="OSAC-2000")]
        outcome = compute(records)

        self.assertEqual(outcome.metric.state, MetricState.UNAVAILABLE)


class TestPercentHelper(unittest.TestCase):
    def test_rounding_is_half_up(self):
        self.assertEqual(percent_of(1, 3), 33.3)
        self.assertEqual(percent_of(2, 3), 66.7)
        # 1/8 = 12.5 exactly; 1/16 = 6.25 -> 6.3 half-up.
        self.assertEqual(percent_of(1, 8), 12.5)
        self.assertEqual(percent_of(1, 16), 6.3)

    def test_zero_statements_has_no_division_error(self):
        self.assertEqual(percent_of(0, 0), 0.0)


class TestUnitScopeRevision(unittest.TestCase):
    def test_digest_is_order_independent(self):
        self.assertEqual(
            unit_scope_revision(["b", "a"]), unit_scope_revision(["a", "b"])
        )
        self.assertEqual(
            unit_scope_revision(["b", "a"]),
            canonical_digest(["a", "b"]),
        )

    def test_a_different_scope_changes_the_digest(self):
        self.assertNotEqual(
            unit_scope_revision(["a"]), unit_scope_revision(["a", "b"])
        )


if __name__ == "__main__":
    unittest.main()
