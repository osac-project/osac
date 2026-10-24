"""Behavior-inventory coverage metrics.

The integration and e2e metrics answer one question per behavior: *is there a
reviewed, correctly tiered, boundary-adequate mapping for it?* They are computed
by the engine from the merged evidence, never by a collector, so the numerator
and denominator always have the same provenance and can be re-derived from the
report.

Rules (``behavior-inventory-v1``)
---------------------------------
* Denominator: assessable behaviors whose ``required_tier`` is the tier under
  report. A behavior that requires the other tier is excluded, not counted as
  uncovered. A behavior with no tier is never in either denominator: it is
  counted in ``tier_unassigned``, and its presence blocks both tier metrics
  because the denominator is incomplete.
* Numerator: a denominator behavior is covered when at least one
  ``declared_mapping`` record exists for it *at that tier*. A higher tier does
  not satisfy a lower one and vice versa; an inferred mapping never counts.
* Boundary: when the behavior requires a boundary kind, the mapping's boundary
  must provide it. A simulated boundary cannot satisfy a required real one.
* Execution: ``observed_execution`` evidence is joined to the covering mappings.
  In v1 (``policy.require_execution: false``) it is reported but does not gate
  the numerator. With gating on, only a current passing execution counts.

Nothing here reads a file, the clock, or the network.
"""

from dataclasses import dataclass
from typing import Dict, List, Optional, Sequence, Tuple

from .collectors.junit import execution_match_keys, match_key, normalize_test_path
from .collectors.mappings import KIND_MISSING_TEST, KIND_PLANNED_MISSING
from .model import (
    Behavior,
    Boundary,
    Diagnostic,
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    InventoryInfo,
    Metric,
    MetricState,
    Tier,
)
from .unit_metrics import round_percent_value

BEHAVIOR_METRIC_VERSION = "behavior-inventory-v1"

#: Assessment status of one behavior at one tier.
STATUS_COVERED = "covered"
STATUS_GAP = "gap"
STATUS_EXCLUDED = "excluded"
STATUS_UNASSESSABLE = "unassessable"
STATUS_TIER_UNASSIGNED = "tier-unassigned"

#: Execution status attached to a behavior assessment.
EXECUTED_PASS_CURRENT = "executed-pass-current"
EXECUTED_FAIL = "executed-fail"
EXECUTED_STALE = "stale"
NOT_EXECUTED = "not-executed"
EXECUTION_UNKNOWN = "unknown"

#: Gap reasons, strongest cause first. The order is the precedence used when a
#: behavior has several imperfect records.
REASON_NO_MAPPING = "no-mapping"
REASON_INFERRED_ONLY = "inferred-only"
REASON_WRONG_TIER = "wrong-tier"
REASON_BOUNDARY_SIMULATED = "boundary-simulated"
REASON_TEST_MISSING = "test-missing"
REASON_NOT_EXECUTED = "not-executed"
REASON_PLANNED_NOT_IMPLEMENTED = "planned-not-implemented"
REASON_TIER_UNASSIGNED = "tier-unassigned"

OUTCOME_PASSED = "passed"
OUTCOME_FAILED = "failed"
OUTCOME_SKIPPED = "skipped"

_OPERATION = "compute_metrics"


@dataclass(frozen=True)
class BehaviorAssessment:
    """One behavior's status at one tier, with the evidence behind it."""

    behavior: Behavior
    tier: Tier
    status: str
    reason: str
    evidence_ids: Tuple[str, ...]
    candidate_ids: Tuple[str, ...]
    execution_status: str
    boundary: Boundary

    def evidence_class(self) -> str:
        """Return the strongest evidence class backing this assessment."""
        if self.status == STATUS_COVERED:
            return EvidenceClass.DECLARED_MAPPING.value
        if self.status == STATUS_TIER_UNASSIGNED:
            return (
                EvidenceClass.INFERRED_MAPPING.value
                if self.candidate_ids
                else "none"
            )
        if self.status in (STATUS_EXCLUDED, STATUS_UNASSESSABLE):
            return "none"
        if self.reason == REASON_INFERRED_ONLY:
            return EvidenceClass.INFERRED_MAPPING.value
        if self.reason in (REASON_TEST_MISSING, REASON_PLANNED_NOT_IMPLEMENTED):
            return EvidenceClass.GAP_UNKNOWN.value
        if self.reason == REASON_NO_MAPPING:
            return "none"
        return EvidenceClass.DECLARED_MAPPING.value

    def to_dict(self) -> Dict[str, object]:
        return {
            "status": self.status,
            "reason": self.reason,
            "evidence_class": self.evidence_class(),
            "evidence_ids": list(self.evidence_ids),
            "candidate_ids": list(self.candidate_ids),
            "execution_status": self.execution_status,
            "boundary": self.boundary.to_dict(),
        }


@dataclass(frozen=True)
class BehaviorMetricResult:
    """A metric plus the per-behavior detail and denominator breakdown."""

    metric: Metric
    assessments: Tuple[BehaviorAssessment, ...]
    breakdown: Dict[str, int]
    diagnostics: Tuple[Diagnostic, ...]


def _diagnostic(
    *,
    run_id: str,
    feature: str,
    code: str,
    message: str,
    severity: str,
    recovery: str = "",
) -> Diagnostic:
    return Diagnostic(
        run_id=run_id,
        collector="engine",
        operation=_OPERATION,
        code=code,
        message=message,
        severity=severity,
        recovery=recovery,
    )


def _boundary_satisfies(behavior: Behavior, boundary: Boundary) -> bool:
    """Return whether ``boundary`` provides every kind the behavior requires."""
    for kind in behavior.required_boundary:
        if kind == "real" and not boundary.real:
            return False
        if kind == "simulated" and not boundary.simulated:
            return False
    return True


def _mapping_name(record: EvidenceRecord) -> str:
    value = (record.details or {}).get("name", "")
    return value if isinstance(value, str) else ""


def _mapping_path(record: EvidenceRecord) -> str:
    value = (record.details or {}).get("path", "")
    return value if isinstance(value, str) else ""


@dataclass(frozen=True)
class _ExecutionIndex:
    """Execution records indexed by the keys a mapping can match."""

    by_identity: Dict[str, Tuple[EvidenceRecord, ...]]
    by_name: Dict[str, Tuple[EvidenceRecord, ...]]
    by_prefix: Dict[str, Tuple[EvidenceRecord, ...]]
    by_path: Dict[str, Tuple[EvidenceRecord, ...]]


def _add_index(
    table: Dict[str, List[EvidenceRecord]],
    key: str,
    record: EvidenceRecord,
) -> None:
    if key:
        table.setdefault(key, []).append(record)


def _freeze_index(
    table: Dict[str, List[EvidenceRecord]]
) -> Dict[str, Tuple[EvidenceRecord, ...]]:
    return {
        key: tuple({record.id: record for record in records}.values())
        for key, records in table.items()
    }


def _build_execution_index(
    executions: Sequence[EvidenceRecord],
) -> _ExecutionIndex:
    """Normalize each execution record once, then index it for lookup."""
    by_identity: Dict[str, List[EvidenceRecord]] = {}
    by_name: Dict[str, List[EvidenceRecord]] = {}
    by_prefix: Dict[str, List[EvidenceRecord]] = {}
    by_path: Dict[str, List[EvidenceRecord]] = {}

    for record in executions:
        details = record.details or {}
        keys = execution_match_keys(
            record.subject,
            str(details.get("file", "")),
            record.source,
        )
        _add_index(by_identity, keys.identity, record)
        _add_index(by_name, keys.name, record)
        for prefix in keys.prefixes:
            _add_index(by_prefix, prefix, record)
        for suffix in keys.path_suffixes:
            _add_index(by_path, suffix, record)

    return _ExecutionIndex(
        by_identity=_freeze_index(by_identity),
        by_name=_freeze_index(by_name),
        by_prefix=_freeze_index(by_prefix),
        by_path=_freeze_index(by_path),
    )


def _matching_executions(
    mapping: EvidenceRecord, index: _ExecutionIndex
) -> Tuple[EvidenceRecord, ...]:
    """Return the executions a mapping joins, using index lookups only."""
    mapped_name = match_key(_mapping_name(mapping))
    if mapped_name:
        candidates = (
            index.by_identity.get(mapped_name, ())
            + index.by_name.get(mapped_name, ())
            + index.by_prefix.get(mapped_name, ())
        )
    else:
        mapped_path = normalize_test_path(_mapping_path(mapping))
        candidates = index.by_path.get(mapped_path, ()) if mapped_path else ()
    return tuple({record.id: record for record in candidates}.values())


def _execution_status(
    mappings: Sequence[EvidenceRecord],
    index: _ExecutionIndex,
) -> str:
    """Return the aggregate execution status for a behavior's mappings."""
    if not mappings:
        return EXECUTION_UNKNOWN
    matched: Dict[str, EvidenceRecord] = {}
    for mapping in mappings:
        for execution in _matching_executions(mapping, index):
            matched[execution.id] = execution
    if not matched:
        return NOT_EXECUTED

    def outcome(record: EvidenceRecord) -> str:
        value = (record.details or {}).get("outcome", "")
        return value if isinstance(value, str) else ""

    current = [
        record
        for record in matched.values()
        if record.freshness is Freshness.CURRENT
    ]
    if any(outcome(record) == OUTCOME_PASSED for record in current):
        return EXECUTED_PASS_CURRENT
    if any(outcome(record) == OUTCOME_FAILED for record in current):
        return EXECUTED_FAIL
    if any(record.freshness is Freshness.STALE for record in matched.values()):
        return EXECUTED_STALE
    if any(outcome(record) == OUTCOME_SKIPPED for record in current):
        return NOT_EXECUTED
    return EXECUTION_UNKNOWN


def _assess(
    behavior: Behavior,
    tier: Tier,
    records: Sequence[EvidenceRecord],
    *,
    execution_index: _ExecutionIndex,
    require_execution: bool,
) -> BehaviorAssessment:
    """Assess one behavior that belongs in the denominator."""
    declared_here = [
        record
        for record in records
        if record.evidence_class is EvidenceClass.DECLARED_MAPPING
        and record.tier is tier
    ]
    declared_other = [
        record
        for record in records
        if record.evidence_class is EvidenceClass.DECLARED_MAPPING
        and record.tier is not tier
    ]
    inferred = [
        record
        for record in records
        if record.evidence_class is EvidenceClass.INFERRED_MAPPING
    ]
    missing_here = [
        record
        for record in records
        if record.evidence_class is EvidenceClass.GAP_UNKNOWN
        and record.kind == KIND_MISSING_TEST
        and record.tier is tier
    ]
    planned_here = [
        record
        for record in records
        if record.evidence_class is EvidenceClass.GAP_UNKNOWN
        and record.kind == KIND_PLANNED_MISSING
        and record.tier is tier
    ]

    adequate = [
        record
        for record in declared_here
        if _boundary_satisfies(behavior, record.boundary)
    ]
    execution_status = _execution_status(adequate, execution_index)

    if adequate and (
        not require_execution or execution_status == EXECUTED_PASS_CURRENT
    ):
        return BehaviorAssessment(
            behavior=behavior,
            tier=tier,
            status=STATUS_COVERED,
            reason="",
            evidence_ids=tuple(sorted(record.id for record in adequate)),
            candidate_ids=tuple(sorted(record.id for record in inferred)),
            execution_status=execution_status,
            boundary=adequate[0].boundary,
        )

    if adequate:
        # A correct mapping exists but the policy requires a current passing
        # run, and the join did not find one.
        reason = REASON_NOT_EXECUTED
    elif planned_here:
        # The approved plan names the test; it is not implemented yet.
        reason = REASON_PLANNED_NOT_IMPLEMENTED
    elif missing_here:
        reason = REASON_TEST_MISSING
    elif declared_here:
        reason = REASON_BOUNDARY_SIMULATED
    elif declared_other:
        reason = REASON_WRONG_TIER
    elif inferred:
        reason = REASON_INFERRED_ONLY
    else:
        reason = REASON_NO_MAPPING

    evidence: List[EvidenceRecord] = (
        list(declared_here)
        + list(planned_here)
        + list(missing_here)
        + list(declared_other)
    )
    boundary_source: Optional[EvidenceRecord] = None
    for candidate in evidence or inferred:
        boundary_source = candidate
        break

    return BehaviorAssessment(
        behavior=behavior,
        tier=tier,
        status=STATUS_GAP,
        reason=reason,
        evidence_ids=tuple(sorted(record.id for record in evidence)),
        candidate_ids=tuple(sorted(record.id for record in inferred)),
        execution_status=execution_status,
        boundary=(
            boundary_source.boundary if boundary_source is not None else Boundary()
        ),
    )


def compute_behavior_metric(
    *,
    feature: str,
    tier: Tier,
    behaviors: Sequence[Behavior],
    records: Sequence[EvidenceRecord],
    inventory: Optional[InventoryInfo],
    metric_version: str = BEHAVIOR_METRIC_VERSION,
    require_execution: bool = False,
    run_id: str = "unavailable",
) -> BehaviorMetricResult:
    """Compute one behavior-inventory metric and its per-behavior detail.

    Args:
        feature: Feature key.
        tier: Integration or e2e.
        behaviors: The feature's merged behaviors (one per id).
        records: The feature's merged evidence records.
        inventory: Inventory metadata, or ``None`` when no test plan was found.
        metric_version: Must be :data:`BEHAVIOR_METRIC_VERSION`.
        require_execution: Whether a current passing execution gates coverage.
        run_id: Attached to diagnostics.
    """
    if metric_version != BEHAVIOR_METRIC_VERSION:
        raise ValueError(
            "unsupported behavior metric version {0!r}".format(metric_version)
        )

    diagnostics: List[Diagnostic] = []
    # Sort locally as well as relying on the merge, so the arithmetic is
    # independent of caller order even when the engine is used directly.
    ordered_records = sorted(records, key=lambda record: record.sort_key())
    by_subject: Dict[str, List[EvidenceRecord]] = {}
    for record in ordered_records:
        by_subject.setdefault(record.subject, []).append(record)
    executions = [
        record
        for record in ordered_records
        if record.evidence_class is EvidenceClass.OBSERVED_EXECUTION
        and record.tier is tier
    ]
    execution_index = _build_execution_index(executions)

    assessments: List[BehaviorAssessment] = []
    assessed = 0
    tier_unassigned = 0
    excluded = 0
    unassessable = 0

    for behavior in sorted(behaviors, key=lambda item: item.id):
        if not behavior.assessable:
            unassessable += 1
            assessments.append(
                BehaviorAssessment(
                    behavior=behavior,
                    tier=tier,
                    status=STATUS_UNASSESSABLE,
                    reason=behavior.exclusion_reason or "not assessable",
                    evidence_ids=(),
                    candidate_ids=(),
                    execution_status=EXECUTION_UNKNOWN,
                    boundary=Boundary(),
                )
            )
            continue
        if behavior.required_tier is None:
            tier_unassigned += 1
            behavior_records = by_subject.get(behavior.id, ())
            inferred = [
                record
                for record in behavior_records
                if record.evidence_class is EvidenceClass.INFERRED_MAPPING
            ]
            boundary = (
                behavior_records[0].boundary
                if behavior_records
                else Boundary()
            )
            assessments.append(
                BehaviorAssessment(
                    behavior=behavior,
                    tier=tier,
                    status=STATUS_TIER_UNASSIGNED,
                    reason="no tier declared",
                    evidence_ids=(),
                    candidate_ids=tuple(
                        sorted(record.id for record in inferred)
                    ),
                    execution_status=EXECUTION_UNKNOWN,
                    boundary=boundary,
                )
            )
            continue
        if behavior.required_tier is not tier:
            excluded += 1
            assessments.append(
                BehaviorAssessment(
                    behavior=behavior,
                    tier=tier,
                    status=STATUS_EXCLUDED,
                    reason="required tier is {0}".format(
                        behavior.required_tier.value
                    ),
                    evidence_ids=(),
                    candidate_ids=(),
                    execution_status=EXECUTION_UNKNOWN,
                    boundary=Boundary(),
                )
            )
            continue

        assessed += 1
        assessments.append(
            _assess(
                behavior,
                tier,
                by_subject.get(behavior.id, ()),
                execution_index=execution_index,
                require_execution=require_execution,
            )
        )

    covered = [
        assessment
        for assessment in assessments
        if assessment.status == STATUS_COVERED
    ]
    numerator = len(covered)
    inputs = tuple(
        sorted(
            {
                evidence_id
                for assessment in covered
                for evidence_id in assessment.evidence_ids
            }
        )
    )

    formula = (
        "covered behaviors / assessable behaviors whose required tier is {0}; "
        "behaviors with no tier are excluded from the denominator and block the "
        "metric; covered means at least one declared mapping at {0} whose "
        "boundary satisfies the behavior; inferred mappings never count; "
        "execution gating is {1}".format(
            tier.value, "on" if require_execution else "off"
        )
    )

    inventory_revision = inventory.revision if inventory is not None else None
    duplicate_ids = inventory.duplicate_ids if inventory is not None else ()

    # The percentage the denominator would produce. It is reported only as
    # provisional when the denominator is incomplete, never as a real value.
    provisional_percent = (
        round_percent_value(numerator * 100.0 / assessed)
        if assessed > 0
        else None
    )

    state = MetricState.VALUE
    reason = ""
    if inventory is None or inventory.path is None:
        state = MetricState.NA
        reason = "BEHAVIOR_INVENTORY_UNAVAILABLE"
        diagnostics.append(
            _diagnostic(
                run_id=run_id,
                feature=feature,
                code=reason,
                message=(
                    "no behavior inventory was loaded for {0}; the {1} metric "
                    "is not available, which is not zero coverage".format(
                        feature, tier.value
                    )
                ),
                severity="warning",
                recovery="Add the feature test plan under an enhancement root, "
                "or set testplan in the feature registry.",
            )
        )
    elif duplicate_ids:
        state = MetricState.BLOCKED
        reason = "BEHAVIOR_INVENTORY_DUPLICATE"
        diagnostics.append(
            _diagnostic(
                run_id=run_id,
                feature=feature,
                code=reason,
                message=(
                    "the test plan for {0} repeats test case id(s) {1}; the {2} "
                    "metric is blocked until every id is unique".format(
                        feature, ", ".join(duplicate_ids), tier.value
                    )
                ),
                severity="error",
                recovery="Give each test case a unique TC id in the test plan.",
            )
        )
    elif tier_unassigned:
        state = MetricState.BLOCKED
        reason = "BEHAVIOR_TIER_UNASSIGNED"
        diagnostics.append(
            _diagnostic(
                run_id=run_id,
                feature=feature,
                code=reason,
                message=(
                    "{0} assessable behavior(s) for {1} have no tier; the {2} "
                    "percentage is provisional over the {3} tiered "
                    "behavior(s)".format(
                        tier_unassigned, feature, tier.value, assessed
                    )
                ),
                severity="warning",
                recovery="Add a tier for each behavior in the test plan "
                "execution matrix or in config/mappings/{0}.yaml "
                "behavior_tiers.".format(feature),
            )
        )
    elif assessed == 0:
        state = MetricState.NA
        reason = "BEHAVIOR_METRIC_NO_DENOMINATOR"
        diagnostics.append(
            _diagnostic(
                run_id=run_id,
                feature=feature,
                code=reason,
                message=(
                    "no assessable behavior for {0} requires the {1} tier; the "
                    "metric is not applicable".format(feature, tier.value)
                ),
                severity="warning",
                recovery="Declare a tier for the behaviors that need this tier, "
                "or accept that the tier is not part of this feature.",
            )
        )
    elif numerator == 0:
        state = MetricState.ZERO

    value_percent = (
        provisional_percent
        if state in (MetricState.VALUE, MetricState.ZERO)
        else None
    )
    # "Not applicable" must not imply a measured zero: without an inventory
    # there is no numerator at all. A blocked metric keeps its arithmetic so
    # the shortfall stays auditable.
    if state is MetricState.NA:
        reported_numerator = None
        reported_denominator = None
        reported_inputs: Tuple[str, ...] = ()
    else:
        reported_numerator = numerator
        reported_denominator = assessed
        reported_inputs = inputs

    metric = Metric(
        feature=feature,
        tier=tier,
        metric_version=metric_version,
        state=state,
        numerator=reported_numerator,
        denominator=reported_denominator,
        value_percent=value_percent,
        excluded=excluded,
        unassessable=unassessable,
        tier_unassigned=tier_unassigned,
        provisional_percent=(
            provisional_percent if state is MetricState.BLOCKED else None
        ),
        reason=reason,
        inputs=reported_inputs,
        formula=formula,
        inventory_revision=inventory_revision,
    )

    return BehaviorMetricResult(
        metric=metric,
        assessments=tuple(assessments),
        breakdown={
            "tier": tier.value,
            "denominator": assessed,
            "required_tier": assessed,
            "tier_unassigned": tier_unassigned,
            "excluded": excluded,
            "unassessable": unassessable,
            "provisional_percent": (
                provisional_percent if state is MetricState.BLOCKED else None
            ),
        },
        diagnostics=tuple(
            sorted(diagnostics, key=lambda diagnostic: diagnostic.sort_key())
        ),
    )
