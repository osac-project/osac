"""Contract and property tests for the behavior-inventory metric engine."""

import itertools
import unittest

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.metrics import (
    EXECUTED_FAIL,
    EXECUTED_PASS_CURRENT,
    EXECUTED_STALE,
    EXECUTION_UNKNOWN,
    NOT_EXECUTED,
    REASON_BOUNDARY_SIMULATED,
    REASON_INFERRED_ONLY,
    REASON_NO_MAPPING,
    REASON_NOT_EXECUTED,
    REASON_PLANNED_NOT_IMPLEMENTED,
    REASON_TEST_MISSING,
    REASON_WRONG_TIER,
    STATUS_EXCLUDED,
    STATUS_GAP,
    STATUS_TIER_UNASSIGNED,
    STATUS_UNASSESSABLE,
    compute_behavior_metric,
)
from coverage_report.model import (
    Behavior,
    Boundary,
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    InventoryInfo,
    MetricState,
    Tier,
    make_evidence_id,
)

FEATURE = "OSAC-1111"
REVISION = "a" * 64
RUN = "run-test"


def behavior(
    tc_id,
    *,
    tier=Tier.INTEGRATION,
    boundary=(),
    priority="high",
    assessable=True,
    exclusion_reason=None,
):
    return Behavior(
        id="{0}/{1}".format(FEATURE, tc_id),
        title="title {0}".format(tc_id),
        priority=priority,
        required_tier=tier,
        required_boundary=tuple(boundary),
        assessable=assessable,
        exclusion_reason=exclusion_reason,
    )


def mapping(
    subject,
    tier,
    *,
    evidence_class=EvidenceClass.DECLARED_MAPPING,
    boundary=None,
    source=None,
    name="a test",
    path="tests/it/a_test.go",
    confidence=None,
    heuristic=None,
    kind="config-mapping",
    details_extra=None,
):
    details = {"name": name, "path": path}
    details.update(details_extra or {})
    source = source or "{0}::{1}".format(path, name)
    return EvidenceRecord(
        id=make_evidence_id("mappings", kind, subject, source),
        evidence_class=evidence_class,
        tier=tier,
        feature=FEATURE,
        subject=subject,
        source=source,
        collector="mappings",
        kind=kind,
        boundary=boundary or Boundary(),
        details=details,
        confidence=confidence,
        heuristic=heuristic,
    )


def execution(
    identity,
    tier,
    *,
    outcome="passed",
    freshness=Freshness.CURRENT,
    path="tests/it/a_test.go",
):
    return EvidenceRecord(
        id=make_evidence_id("junit", "junit-testcase", identity, path),
        evidence_class=EvidenceClass.OBSERVED_EXECUTION,
        tier=tier,
        feature=FEATURE,
        subject=identity,
        source=path,
        collector="junit",
        kind="junit-testcase",
        freshness=freshness,
        details={"outcome": outcome, "file": path},
    )


def inventory(*, path="docs/testplan.md", duplicate_ids=()):
    return InventoryInfo(
        feature=FEATURE,
        path=path,
        revision=REVISION,
        behavior_count=0,
        duplicate_ids=duplicate_ids,
    )


def compute(behaviors, records=(), tier=Tier.INTEGRATION, inventory_info=None, **kwargs):
    return compute_behavior_metric(
        feature=FEATURE,
        tier=tier,
        behaviors=behaviors,
        records=records,
        inventory=inventory_info if inventory_info is not None else inventory(),
        run_id=RUN,
        **kwargs
    )


def by_id(result):
    return {item.behavior.id: item for item in result.assessments}


class TestDenominator(unittest.TestCase):
    def test_tier_unassigned_is_excluded_from_the_denominator_and_blocks(self):
        behaviors = [
            behavior("TC-1", tier=Tier.INTEGRATION),
            behavior("TC-2", tier=None),
            behavior("TC-3", tier=Tier.E2E),
            behavior("TC-4", assessable=False, exclusion_reason="manual"),
        ]
        result = compute(behaviors)

        self.assertEqual(result.metric.denominator, 1)
        self.assertEqual(result.metric.tier_unassigned, 1)
        self.assertEqual(result.metric.excluded, 1)
        self.assertEqual(result.metric.unassessable, 1)
        self.assertEqual(result.metric.state, MetricState.BLOCKED)
        self.assertIsNone(result.metric.value_percent)
        self.assertEqual(result.metric.provisional_percent, 0.0)
        self.assertEqual(result.metric.reason, "BEHAVIOR_TIER_UNASSIGNED")
        self.assertEqual(
            result.breakdown,
            {
                "tier": "integration",
                "denominator": 1,
                "required_tier": 1,
                "tier_unassigned": 1,
                "excluded": 1,
                "unassessable": 1,
                "provisional_percent": 0.0,
            },
        )

    def test_assessable_tier_unassigned_behavior_status(self):
        result = compute([behavior("TC-1", tier=None)])
        assessment = by_id(result)[FEATURE + "/TC-1"]

        self.assertEqual(assessment.status, STATUS_TIER_UNASSIGNED)
        self.assertEqual(assessment.reason, "no tier declared")

    def test_all_behaviors_unassigned_block_with_zero_denominator(self):
        result = compute([behavior("TC-1", tier=None), behavior("TC-2", tier=None)])

        self.assertEqual(result.metric.state, MetricState.BLOCKED)
        self.assertEqual(result.metric.numerator, 0)
        self.assertEqual(result.metric.denominator, 0)
        self.assertEqual(result.metric.tier_unassigned, 2)
        self.assertIsNone(result.metric.provisional_percent)
        self.assertIsNone(result.metric.value_percent)

    def test_mixed_feature_reports_a_provisional_percentage(self):
        subject = "{0}/TC-1".format(FEATURE)
        result = compute(
            [behavior("TC-1"), behavior("TC-2"), behavior("TC-3", tier=None)],
            [mapping(subject, Tier.INTEGRATION)],
        )

        self.assertEqual(result.metric.state, MetricState.BLOCKED)
        self.assertEqual(result.metric.numerator, 1)
        self.assertEqual(result.metric.denominator, 2)
        self.assertEqual(result.metric.provisional_percent, 50.0)
        self.assertIsNone(result.metric.value_percent)

    def test_excluded_and_unassessable_are_not_gaps(self):
        result = compute(
            [
                behavior("TC-1", tier=Tier.E2E),
                behavior("TC-2", assessable=False, exclusion_reason="manual"),
            ]
        )
        statuses = {item.status for item in result.assessments}
        self.assertEqual(
            statuses,
            {STATUS_EXCLUDED, STATUS_UNASSESSABLE},
        )
        self.assertEqual(result.breakdown["denominator"], 0)
        self.assertIsNone(result.metric.denominator)

    def test_denominator_zero_is_na_not_zero(self):
        result = compute([behavior("TC-1", tier=Tier.E2E)])
        self.assertEqual(result.metric.state, MetricState.NA)
        self.assertIsNone(result.metric.numerator)
        self.assertIsNone(result.metric.denominator)
        self.assertEqual(
            [diagnostic.code for diagnostic in result.diagnostics],
            ["BEHAVIOR_METRIC_NO_DENOMINATOR"],
        )

    def test_no_inventory_is_na_with_a_diagnostic(self):
        result = compute_behavior_metric(
            feature=FEATURE,
            tier=Tier.INTEGRATION,
            behaviors=[behavior("TC-1")],
            records=(),
            inventory=None,
            run_id=RUN,
        )
        self.assertEqual(result.metric.state, MetricState.NA)
        self.assertIsNone(result.metric.numerator)
        self.assertEqual(result.metric.inventory_revision, None)
        self.assertEqual(
            [diagnostic.code for diagnostic in result.diagnostics],
            ["BEHAVIOR_INVENTORY_UNAVAILABLE"],
        )

    def test_duplicate_inventory_blocks(self):
        result = compute(
            [behavior("TC-1")],
            inventory_info=inventory(
                duplicate_ids=("{0}/TC-1".format(FEATURE),)
            ),
        )
        self.assertEqual(result.metric.state, MetricState.BLOCKED)
        self.assertEqual(
            [diagnostic.code for diagnostic in result.diagnostics],
            ["BEHAVIOR_INVENTORY_DUPLICATE"],
        )


class TestCoverageRules(unittest.TestCase):
    def test_declared_mapping_at_the_tier_covers(self):
        subject = "{0}/TC-1".format(FEATURE)
        result = compute([behavior("TC-1")], [mapping(subject, Tier.INTEGRATION)])

        self.assertEqual(result.metric.state, MetricState.VALUE)
        self.assertEqual(result.metric.numerator, 1)
        self.assertEqual(result.metric.denominator, 1)
        self.assertEqual(result.metric.value_percent, 100.0)
        self.assertEqual(result.metric.inputs, (mapping_record_id(subject),))

    def test_inferred_mapping_never_counts(self):
        subject = "{0}/TC-1".format(FEATURE)
        record = mapping(
            subject,
            Tier.INTEGRATION,
            evidence_class=EvidenceClass.INFERRED_MAPPING,
            confidence=0.7,
            heuristic="unreviewed-config",
        )
        result = compute([behavior("TC-1")], [record])

        self.assertEqual(result.metric.numerator, 0)
        self.assertEqual(result.metric.state, MetricState.ZERO)
        assessment = by_id(result)[subject]
        self.assertEqual(assessment.status, STATUS_GAP)
        self.assertEqual(assessment.reason, REASON_INFERRED_ONLY)
        self.assertEqual(assessment.candidate_ids, (record.id,))

    def test_no_mapping_is_distinguished_from_inferred_only(self):
        result = compute([behavior("TC-1")])
        self.assertEqual(by_id(result)[FEATURE + "/TC-1"].reason, REASON_NO_MAPPING)

    def test_wrong_tier_is_a_gap_at_the_required_tier(self):
        subject = "{0}/TC-1".format(FEATURE)
        result = compute(
            [behavior("TC-1")], [mapping(subject, Tier.E2E)]
        )
        assessment = by_id(result)[subject]
        self.assertEqual(assessment.status, STATUS_GAP)
        self.assertEqual(assessment.reason, REASON_WRONG_TIER)

    def test_simulated_boundary_cannot_satisfy_required_real(self):
        subject = "{0}/TC-1".format(FEATURE)
        record = mapping(
            subject,
            Tier.INTEGRATION,
            boundary=Boundary(simulated=("fake-storage",)),
        )
        result = compute(
            [behavior("TC-1", boundary=("real",))], [record]
        )
        assessment = by_id(result)[subject]
        self.assertEqual(assessment.status, STATUS_GAP)
        self.assertEqual(assessment.reason, REASON_BOUNDARY_SIMULATED)

    def test_real_boundary_satisfies_required_real(self):
        subject = "{0}/TC-1".format(FEATURE)
        record = mapping(
            subject,
            Tier.INTEGRATION,
            boundary=Boundary(real=("deployed-api",)),
        )
        result = compute([behavior("TC-1", boundary=("real",))], [record])
        self.assertEqual(result.metric.numerator, 1)

    def test_missing_test_is_reported_as_test_missing(self):
        subject = "{0}/TC-1".format(FEATURE)
        record = mapping(
            subject,
            Tier.INTEGRATION,
            evidence_class=EvidenceClass.GAP_UNKNOWN,
            kind="declared-test-missing",
        )
        result = compute([behavior("TC-1")], [record])
        self.assertEqual(by_id(result)[subject].reason, REASON_TEST_MISSING)

    def test_planned_test_is_not_implemented(self):
        subject = "{0}/TC-1".format(FEATURE)
        record = mapping(
            subject,
            Tier.INTEGRATION,
            evidence_class=EvidenceClass.GAP_UNKNOWN,
            kind="planned-test-missing",
            details_extra={"path": "tests/e2e/mcp/planned.py"},
        )
        result = compute([behavior("TC-1")], [record])
        assessment = by_id(result)[subject]

        self.assertEqual(assessment.reason, REASON_PLANNED_NOT_IMPLEMENTED)
        self.assertEqual(assessment.evidence_class(), "gap_unknown")
        self.assertEqual(result.metric.numerator, 0)
        self.assertEqual(result.metric.state, MetricState.ZERO)


class TestExecutionJoin(unittest.TestCase):
    def setUp(self):
        self.subject = "{0}/TC-1".format(FEATURE)
        self.record = mapping(self.subject, Tier.INTEGRATION, name="creates image")
        self.identity = "integration-suite::creates image"

    def status(self, executions, **kwargs):
        result = compute(
            [behavior("TC-1")], [self.record] + list(executions), **kwargs
        )
        return by_id(result)[self.subject].execution_status

    def test_passing_current_execution(self):
        self.assertEqual(
            self.status([execution(self.identity, Tier.INTEGRATION)]),
            EXECUTED_PASS_CURRENT,
        )

    def test_failing_current_execution(self):
        self.assertEqual(
            self.status(
                [execution(self.identity, Tier.INTEGRATION, outcome="failed")]
            ),
            EXECUTED_FAIL,
        )

    def test_stale_execution(self):
        self.assertEqual(
            self.status(
                [execution(self.identity, Tier.INTEGRATION, freshness=Freshness.STALE)]
            ),
            EXECUTED_STALE,
        )

    def test_no_execution_is_not_executed(self):
        self.assertEqual(self.status([]), NOT_EXECUTED)

    def test_no_mapping_is_unknown(self):
        result = compute([behavior("TC-1")], [])
        self.assertEqual(
            by_id(result)[self.subject].execution_status, EXECUTION_UNKNOWN
        )

    def test_suite_prefix_mapping_joins_ginkgo_identity(self):
        record = mapping(self.subject, Tier.INTEGRATION, name="integration-suite")
        result = compute(
            [behavior("TC-1")],
            [record, execution(self.identity, Tier.INTEGRATION)],
        )
        self.assertEqual(
            by_id(result)[self.subject].execution_status, EXECUTED_PASS_CURRENT
        )

    def test_execution_alone_never_establishes_mapping(self):
        result = compute([behavior("TC-1")], [execution(self.identity, Tier.INTEGRATION)])
        self.assertEqual(result.metric.numerator, 0)
        self.assertEqual(by_id(result)[self.subject].reason, REASON_NO_MAPPING)

    def test_execution_gating_is_off_by_default(self):
        result = compute(
            [behavior("TC-1")],
            [
                self.record,
                execution(self.identity, Tier.INTEGRATION, outcome="failed"),
            ],
        )
        self.assertEqual(result.metric.numerator, 1)
        self.assertEqual(result.metric.state, MetricState.VALUE)

    def test_execution_gating_on_requires_a_current_pass(self):
        failed = compute(
            [behavior("TC-1")],
            [
                self.record,
                execution(self.identity, Tier.INTEGRATION, outcome="failed"),
            ],
            require_execution=True,
        )
        self.assertEqual(failed.metric.numerator, 0)
        self.assertEqual(by_id(failed)[self.subject].reason, REASON_NOT_EXECUTED)

        passed = compute(
            [behavior("TC-1")],
            [self.record, execution(self.identity, Tier.INTEGRATION)],
            require_execution=True,
        )
        self.assertEqual(passed.metric.numerator, 1)


def mapping_record_id(subject):
    return mapping(subject, Tier.INTEGRATION).id


class TestPropertyInvariants(unittest.TestCase):
    def test_adding_an_uncovered_assessable_behavior_only_grows_the_denominator(self):
        subject = "{0}/TC-1".format(FEATURE)
        records = [mapping(subject, Tier.INTEGRATION)]
        before = compute([behavior("TC-1")], records)

        after = compute([behavior("TC-1"), behavior("TC-2")], records)

        self.assertEqual(after.metric.numerator, before.metric.numerator)
        self.assertEqual(after.metric.denominator, before.metric.denominator + 1)
        self.assertEqual(after.metric.tier_unassigned, 0)

    def test_adding_a_tier_unassigned_behavior_blocks_without_growing_the_denominator(self):
        subject = "{0}/TC-1".format(FEATURE)
        records = [mapping(subject, Tier.INTEGRATION)]
        before = compute([behavior("TC-1")], records)

        after = compute(
            [behavior("TC-1"), behavior("TC-2", tier=None)], records
        )

        self.assertEqual(after.metric.denominator, before.metric.denominator)
        self.assertEqual(after.metric.numerator, before.metric.numerator)
        self.assertEqual(after.metric.tier_unassigned, 1)
        self.assertEqual(after.metric.state, MetricState.BLOCKED)

    def test_adding_a_qualifying_declared_mapping_only_grows_the_numerator(self):
        first = "{0}/TC-1".format(FEATURE)
        second = "{0}/TC-2".format(FEATURE)
        behaviors = [behavior("TC-1"), behavior("TC-2")]
        before = compute(behaviors, [mapping(first, Tier.INTEGRATION)])

        after = compute(
            behaviors,
            [
                mapping(first, Tier.INTEGRATION),
                mapping(second, Tier.INTEGRATION),
            ],
        )

        self.assertEqual(after.metric.numerator, before.metric.numerator + 1)
        self.assertEqual(after.metric.denominator, before.metric.denominator)

    def test_inferred_evidence_never_changes_the_numerator(self):
        subject = "{0}/TC-1".format(FEATURE)
        before = compute([behavior("TC-1")], [])
        after = compute(
            [behavior("TC-1")],
            [
                mapping(
                    subject,
                    Tier.INTEGRATION,
                    evidence_class=EvidenceClass.INFERRED_MAPPING,
                    confidence=0.6,
                    heuristic="keyword-overlap",
                )
            ],
        )
        self.assertEqual(after.metric.numerator, before.metric.numerator)

    def test_passing_execution_without_mapping_never_changes_the_numerator(self):
        before = compute([behavior("TC-1")], [])
        after = compute(
            [behavior("TC-1")],
            [execution("integration-suite::creates image", Tier.INTEGRATION)],
        )
        self.assertEqual(after.metric.numerator, before.metric.numerator)

    def test_result_is_identical_for_every_record_permutation(self):
        first = "{0}/TC-1".format(FEATURE)
        second = "{0}/TC-2".format(FEATURE)
        behaviors = [behavior("TC-1"), behavior("TC-2")]
        records = [
            mapping(first, Tier.INTEGRATION),
            mapping(second, Tier.INTEGRATION),
            mapping(second, Tier.INTEGRATION, source="other::source"),
            execution("integration-suite::creates image", Tier.INTEGRATION),
        ]
        baseline = compute(behaviors, records)
        expected = (
            baseline.metric.to_dict(),
            [item.to_dict() for item in baseline.assessments],
        )
        for permutation in itertools.permutations(records):
            result = compute(behaviors, permutation)
            self.assertEqual(
                (
                    result.metric.to_dict(),
                    [item.to_dict() for item in result.assessments],
                ),
                expected,
            )

    def test_rounding_is_half_up_to_one_decimal(self):
        behaviors = [behavior("TC-{0}".format(index)) for index in range(16)]
        records = [
            mapping("{0}/TC-{1}".format(FEATURE, index), Tier.INTEGRATION)
            for index in range(1)
        ]
        result = compute(behaviors, records)
        # 1/16 = 6.25%, which must round half-up to 6.3 rather than to 6.2.
        self.assertEqual(result.metric.value_percent, 6.3)


if __name__ == "__main__":
    unittest.main()
