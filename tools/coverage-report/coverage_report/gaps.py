"""Gap extraction and ordering.

A gap is a concrete next action, not a restatement of the metric. Behavior gaps
come from the per-tier assessments; unit gaps come from measured unit evidence
(a mapped package missing from the profile, or a package below the configured
statement-coverage threshold).

Ordering is fixed: priority (critical, high, medium, low, unknown), then tier
(unit, integration, e2e), then subject id. The readiness summary and the top
actions both read the same sorted list, so the report cannot disagree with
itself about what matters most.
"""

from typing import Dict, List, Mapping, Optional, Sequence, Tuple

from .model import (
    TIER_ORDER,
    EvidenceClass,
    EvidenceRecord,
    Tier,
)
from .metrics import (
    REASON_BOUNDARY_SIMULATED,
    REASON_INFERRED_ONLY,
    REASON_NO_MAPPING,
    REASON_NOT_EXECUTED,
    REASON_PLANNED_NOT_IMPLEMENTED,
    REASON_TEST_MISSING,
    REASON_TIER_UNASSIGNED,
    REASON_WRONG_TIER,
    BehaviorAssessment,
    STATUS_GAP,
    STATUS_TIER_UNASSIGNED,
)
from .unit_metrics import COVERAGE_KIND, MISSING_KIND, round_percent_value

SUBJECT_BEHAVIOR = "behavior"
SUBJECT_PACKAGE = "package"

REASON_PACKAGE_MISSING = "package-missing"
REASON_LOW_STATEMENT_COVERAGE = "low-statement-coverage"

_PRIORITY_RANK = {
    "critical": 0,
    "high": 1,
    "medium": 2,
    "low": 3,
    "unknown": 4,
}

_TIER_RANK = {tier: index for index, tier in enumerate(TIER_ORDER)}


def priority_key(priority: Optional[str]) -> str:
    """Return the normalized priority name, defaulting to unknown."""
    value = (priority or "").strip().casefold()
    return value if value in _PRIORITY_RANK else "unknown"


def sort_key(gap: Mapping[str, object]) -> Tuple[int, int, str, str]:
    """Return the report-wide gap ordering key.

    A tier-unassigned gap has no tier; it sorts after the concrete tiers within
    its priority so the tier-specific actions stay grouped.
    """
    raw_tier = gap.get("tier")
    tier_rank = (
        _TIER_RANK[Tier(str(raw_tier))]
        if raw_tier
        else len(TIER_ORDER)
    )
    return (
        _PRIORITY_RANK[priority_key(str(gap.get("priority") or ""))],
        tier_rank,
        str(gap.get("behavior") or ""),
        str(gap.get("reason") or ""),
    )


def sort_gaps(gaps: Sequence[Mapping[str, object]]) -> List[Dict[str, object]]:
    """Return gaps in the canonical order."""
    return sorted((dict(gap) for gap in gaps), key=sort_key)


def _candidates(
    index: Mapping[str, EvidenceRecord], evidence_ids: Sequence[str]
) -> List[Dict[str, object]]:
    candidates: List[Dict[str, object]] = []
    for evidence_id in evidence_ids:
        record = index.get(evidence_id)
        if record is None:
            continue
        candidates.append(
            {
                "evidence_id": record.id,
                "source": record.source,
                "tier": record.tier.value,
                "confidence": record.confidence,
                "heuristic": record.heuristic,
            }
        )
    return sorted(
        candidates,
        key=lambda item: (
            -(float(item["confidence"] or 0.0)),
            str(item["source"]),
        ),
    )


def _assessment_records(
    evidence_ids: Sequence[str],
    index: Mapping[str, EvidenceRecord],
) -> List[EvidenceRecord]:
    """Return the assessment's records from a prebuilt index, in id order."""
    return [index[evidence_id] for evidence_id in evidence_ids if evidence_id in index]


def _next_action(
    *,
    feature: str,
    behavior_id: str,
    tier: Tier,
    reason: str,
    assessment: BehaviorAssessment,
    evidence: Sequence[EvidenceRecord],
    candidates: Sequence[EvidenceRecord],
) -> str:
    config = "config/mappings/{0}.yaml".format(feature)
    if reason == REASON_NO_MAPPING:
        return (
            "Add a reviewed mapping for {0} at {1} in {2}, pointing at a test "
            "that exists.".format(behavior_id, tier.value, config)
        )
    if reason == REASON_INFERRED_ONLY:
        sources = ", ".join(sorted({record.source for record in candidates}))
        return (
            "Review the inferred mapping(s) for {0} ({1}) and record the "
            "reviewer in {2}.".format(behavior_id, sources or "no source", config)
        )
    if reason == REASON_WRONG_TIER:
        tiers = ", ".join(
            sorted({record.tier.value for record in evidence})
        )
        return (
            "Map {0} at {1}; its reviewed mapping is at {2} instead, and a "
            "test at another tier does not satisfy the {1} requirement.".format(
                behavior_id, tier.value, tiers or "another tier"
            )
        )
    if reason == REASON_BOUNDARY_SIMULATED:
        return (
            "Replace the simulated mapping for {0} with a {1} test that "
            "exercises the required {2} boundary.".format(
                behavior_id,
                tier.value,
                "/".join(assessment.behavior.required_boundary) or "real",
            )
        )
    if reason == REASON_PLANNED_NOT_IMPLEMENTED:
        paths = sorted(
            {
                str((record.details or {}).get("path", ""))
                for record in evidence
            }
            - {""}
        )
        return "Implement {0} (planned in testplan)".format(
            ", ".join(paths) or "<path>"
        )
    if reason == REASON_TEST_MISSING:
        paths = ", ".join(
            sorted(
                {
                    str((record.details or {}).get("path", ""))
                    for record in evidence
                }
            )
        )
        return (
            "Fix the mapping for {0}: the named test {1} does not exist.".format(
                behavior_id, paths or "<unknown>"
            )
        )
    if reason == REASON_NOT_EXECUTED:
        return (
            "Run the mapped {0} test for {1} at the reported commit and make it "
            "pass; execution gating is enabled.".format(tier.value, behavior_id)
        )
    return "Review the evidence for {0} at {1}.".format(behavior_id, tier.value)


def behavior_gaps(
    *,
    feature: str,
    tier: Tier,
    assessments: Sequence[BehaviorAssessment],
    records: Sequence[EvidenceRecord],
    testplan: Optional[str],
) -> List[Dict[str, object]]:
    """Return one gap per behavior that is not covered at ``tier``."""
    index = {record.id: record for record in records}
    gaps: List[Dict[str, object]] = []
    for assessment in assessments:
        if assessment.status != STATUS_GAP:
            continue
        behavior = assessment.behavior
        evidence = _assessment_records(assessment.evidence_ids, index)
        candidates = _assessment_records(assessment.candidate_ids, index)
        gaps.append(
            {
                "feature": feature,
                "behavior": behavior.id,
                "behavior_title": behavior.title,
                "subject_kind": SUBJECT_BEHAVIOR,
                "tier": tier.value,
                "priority": priority_key(behavior.priority),
                "reason": assessment.reason,
                "candidates": _candidates(index, assessment.candidate_ids),
                "requirement_anchor": behavior.requirement_anchor,
                "testplan": testplan,
                "next_action": _next_action(
                    feature=feature,
                    behavior_id=behavior.id,
                    tier=tier,
                    reason=assessment.reason,
                    assessment=assessment,
                    evidence=evidence,
                    candidates=candidates,
                ),
            }
        )
    return gaps


def tier_unassigned_gaps(
    *,
    feature: str,
    assessments: Sequence[BehaviorAssessment],
    records: Sequence[EvidenceRecord],
    testplan: Optional[str],
) -> List[Dict[str, object]]:
    """Return exactly one gap per behavior that has no tier.

    The assessment list from either tier contains the same unassigned
    behaviors, so the ids are deduplicated: a reviewer fixes the tier once, not
    once per tier. Inferred candidates are kept because they help choose it.
    """
    index = {record.id: record for record in records}
    seen = set()
    gaps: List[Dict[str, object]] = []
    for assessment in sorted(
        assessments, key=lambda item: item.behavior.id
    ):
        if assessment.status != STATUS_TIER_UNASSIGNED:
            continue
        behavior = assessment.behavior
        if behavior.id in seen:
            continue
        seen.add(behavior.id)
        tc_id = behavior.id.split("/", 1)[-1]
        gaps.append(
            {
                "feature": feature,
                "behavior": behavior.id,
                "behavior_title": behavior.title,
                "subject_kind": SUBJECT_BEHAVIOR,
                "tier": None,
                "priority": priority_key(behavior.priority),
                "reason": REASON_TIER_UNASSIGNED,
                "candidates": _candidates(index, assessment.candidate_ids),
                "requirement_anchor": behavior.requirement_anchor,
                "testplan": testplan,
                "next_action": (
                    "Add a tier for {0} in the testplan execution matrix or in "
                    "config/mappings/{1}.yaml behavior_tiers".format(
                        tc_id, feature
                    )
                ),
            }
        )
    return gaps


def unit_gaps(
    *,
    feature: str,
    records: Sequence[EvidenceRecord],
    low_coverage_percent: float,
    testplan: Optional[str],
) -> List[Dict[str, object]]:
    """Return gaps for missing and under-covered unit packages."""
    gaps: List[Dict[str, object]] = []
    for record in records:
        if record.feature != feature:
            continue
        if record.kind == MISSING_KIND:
            gaps.append(
                {
                    "feature": feature,
                    "behavior": record.subject,
                    "behavior_title": "unit package {0}".format(record.subject),
                    "subject_kind": SUBJECT_PACKAGE,
                    "tier": Tier.UNIT.value,
                    "priority": "high",
                    "reason": REASON_PACKAGE_MISSING,
                    "candidates": [],
                    "requirement_anchor": None,
                    "testplan": testplan,
                    "next_action": (
                        "Run the tests for {0} with coverage, or fix the "
                        "unit_packages entry in the feature registry.".format(
                            record.subject
                        )
                    ),
                }
            )
            continue
        if (
            record.evidence_class is not EvidenceClass.MEASURED
            or record.kind != COVERAGE_KIND
        ):
            continue
        details = record.details or {}
        try:
            percent = round_percent_value(details.get("percent", 0))
        except Exception:  # noqa: BLE001 - defensive against bad evidence
            continue
        if percent >= low_coverage_percent:
            continue
        gaps.append(
            {
                "feature": feature,
                "behavior": record.subject,
                "behavior_title": "unit package {0}".format(record.subject),
                "subject_kind": SUBJECT_PACKAGE,
                "tier": Tier.UNIT.value,
                "priority": "high" if percent <= 0 else "medium",
                "reason": REASON_LOW_STATEMENT_COVERAGE,
                "candidates": [],
                "requirement_anchor": None,
                "testplan": testplan,
                "next_action": (
                    "Add tests for the uncovered statements in {0} ({1}% "
                    "< {2}%).".format(
                        record.subject, percent, low_coverage_percent
                    )
                ),
            }
        )
    return gaps
