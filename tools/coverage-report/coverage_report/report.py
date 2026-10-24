"""Report assembly and the readiness summary.

The report dictionary is the single source of truth: the Markdown renderer,
the JSON schema validation, and the diff all read it. Nothing downstream
recomputes a number.
"""

from typing import Dict, List, Mapping, Optional, Sequence

from .gaps import sort_gaps
from .model import TIER_ORDER, MetricState

VERDICT_READY = "ready"
VERDICT_NOT_READY = "not ready"
VERDICT_INSUFFICIENT = "insufficient evidence"

_TOP_ACTIONS = 5

_BLOCKING_STATES = {
    MetricState.UNAVAILABLE.value,
    MetricState.STALE.value,
    MetricState.BLOCKED.value,
}


def build_feature_report(
    *,
    feature,
    metrics: Mapping[str, Dict[str, object]],
    alternative_metrics: Sequence[Dict[str, object]],
    inventories: Sequence[Dict[str, object]],
    denominator_breakdown: Mapping[str, Dict[str, object]],
    behavior_details: Sequence[Dict[str, object]],
    gaps: Sequence[Mapping[str, object]],
    tier_unassigned: int = 0,
    behavior_counts: Optional[Mapping[str, int]] = None,
    unassessable_by_tier: Optional[Mapping[str, int]] = None,
    mapping_file: Optional[str] = None,
) -> Dict[str, object]:
    """Assemble one feature's report block."""
    counts = dict(behavior_counts or {})
    return {
        "key": feature.key,
        "name": feature.name,
        "components": list(feature.components),
        "testplan": feature.testplan,
        "tier_unassigned": tier_unassigned,
        "unit_behaviors": int(counts.get("unit", 0)),
        "behavior_counts": counts,
        "unassessable_by_tier": dict(unassessable_by_tier or {}),
        "mapping_file": mapping_file,
        "tiers": {
            tier.value: metrics[tier.value] for tier in TIER_ORDER
        },
        "alternative_metrics": list(alternative_metrics),
        "inventories": list(inventories),
        "denominator_breakdown": {
            tier.value: denominator_breakdown[tier.value] for tier in TIER_ORDER
        },
        "behaviors": list(behavior_details),
        "gaps": sort_gaps(gaps),
    }


def _feature_verdict(feature_report: Mapping[str, object]) -> Dict[str, object]:
    metrics = feature_report.get("tiers") or {}
    gaps = feature_report.get("gaps") or []

    critical = sum(1 for gap in gaps if gap.get("priority") == "critical")
    high = sum(1 for gap in gaps if gap.get("priority") == "high")
    unavailable = sorted(
        tier
        for tier, metric in metrics.items()
        if metric.get("state") == MetricState.UNAVAILABLE.value
    )
    stale = sorted(
        tier
        for tier, metric in metrics.items()
        if metric.get("state") == MetricState.STALE.value
    )
    blocked = sorted(
        tier
        for tier, metric in metrics.items()
        if metric.get("state") == MetricState.BLOCKED.value
    )
    zero = sorted(
        tier
        for tier, metric in metrics.items()
        if metric.get("state") == MetricState.ZERO.value
    )
    tier_unassigned = int(feature_report.get("tier_unassigned") or 0)
    missing_inventory = sorted(
        tier
        for tier, metric in metrics.items()
        if metric.get("state") == MetricState.NA.value
        and metric.get("reason") == "BEHAVIOR_INVENTORY_UNAVAILABLE"
    )

    if tier_unassigned:
        # The denominator is incomplete, so no verdict about readiness can be
        # supported until the test plan assigns tiers.
        verdict = VERDICT_INSUFFICIENT
    elif critical or high or zero:
        verdict = VERDICT_NOT_READY
    elif unavailable or stale or blocked or missing_inventory:
        verdict = VERDICT_INSUFFICIENT
    else:
        verdict = VERDICT_READY

    return {
        "key": feature_report.get("key"),
        "verdict": verdict,
        "critical_gaps": critical,
        "high_gaps": high,
        "gaps": len(gaps),
        "tier_unassigned": tier_unassigned,
        "unavailable_metrics": unavailable,
        "missing_inventory_metrics": missing_inventory,
        "stale_metrics": stale,
        "blocked_metrics": blocked,
        "zero_metrics": zero,
    }


def build_summary(
    *,
    feature_reports: Sequence[Mapping[str, object]],
    metric_versions: Mapping[str, str],
    pending_review_mappings: int,
    require_execution: bool,
    unit_low_coverage_percent: float,
) -> Dict[str, object]:
    """Return the engineering-manager summary.

    ``ready`` requires no critical/high gap and no unavailable, stale, or
    blocked metric. A measured zero is real evidence of no coverage, so it is
    reported as ``not ready`` rather than as missing evidence.
    """
    features = [_feature_verdict(report) for report in feature_reports]
    all_gaps = sort_gaps(
        [
            gap
            for report in feature_reports
            for gap in (report.get("gaps") or [])
        ]
    )
    tier_unassigned = sum(entry["tier_unassigned"] for entry in features)
    unreviewed_tier_overrides = sum(
        1
        for report in feature_reports
        for behavior in (report.get("behaviors") or [])
        if behavior.get("tier_origin") == "mapping-config"
        and behavior.get("tier_reviewed") is False
    )

    top_actions = [
        {
            "feature": gap.get("feature"),
            "tier": gap.get("tier"),
            "subject": gap.get("behavior"),
            "priority": gap.get("priority"),
            "reason": gap.get("reason"),
            "action": gap.get("next_action"),
        }
        for gap in all_gaps[:_TOP_ACTIONS]
    ]

    notes = [
        "integration and e2e use {0}; unit uses {1}.".format(
            metric_versions.get("integration", "behavior-inventory-v1"),
            metric_versions.get("unit", "statement-weighted-v2"),
        ),
        "execution gating is {0}: declared mappings count {1} a joined "
        "execution; per-behavior execution status is still reported.".format(
            "on" if require_execution else "off",
            "only with" if require_execution else "without",
        ),
    ]

    missing_mapping = [
        str(report.get("key"))
        for report in feature_reports
        if not report.get("mapping_file")
    ]
    if pending_review_mappings:
        notes.append(
            "{0} mapping entr{1} still PENDING-REVIEW and count as inferred, "
            "not declared.".format(
                pending_review_mappings,
                "y is" if pending_review_mappings == 1 else "ies are",
            )
        )
    elif missing_mapping:
        if len(missing_mapping) == 1:
            notes.append(
                "no reviewed mapping file (config/mappings/{0}.yaml).".format(
                    missing_mapping[0]
                )
            )
        else:
            notes.append(
                "no reviewed mapping file (config/mappings/<KEY>.yaml) for "
                "{0}.".format(", ".join(missing_mapping))
            )
    else:
        notes.append(
            "no mapping entries are PENDING-REVIEW; reviewed entries count as "
            "declared."
        )

    if tier_unassigned:
        notes.append(
            "{0} {1} no tier; Integration/E2E percentages are "
            "provisional.".format(
                tier_unassigned,
                "behavior has" if tier_unassigned == 1 else "behaviors have",
            )
        )
    notes.append(
        "unit gaps use the {0}% statement-coverage threshold.".format(
            unit_low_coverage_percent
        )
    )
    if unreviewed_tier_overrides:
        notes.append(
            "{0} tier assignment(s) come from a PENDING-REVIEW mapping file "
            "and are flagged unreviewed.".format(unreviewed_tier_overrides)
        )

    return {
        "features": features,
        "totals": {
            "critical_gaps": sum(item["critical_gaps"] for item in features),
            "high_gaps": sum(item["high_gaps"] for item in features),
            "gaps": len(all_gaps),
        },
        "methodology": {
            "metric_versions": {
                tier: metric_versions[tier]
                for tier in sorted(metric_versions)
            },
            "require_execution": require_execution,
            "pending_review_mappings": pending_review_mappings,
            "tier_unassigned": tier_unassigned,
            "unreviewed_tier_overrides": unreviewed_tier_overrides,
            "unit_low_coverage_percent": unit_low_coverage_percent,
            "notes": notes,
        },
        "top_actions": top_actions,
    }


def pending_review_mapping_count(records: Sequence[object]) -> int:
    """Count inferred mappings that came from an unreviewed config file."""
    count = 0
    for record in records:
        details = getattr(record, "details", {}) or {}
        evidence_class = getattr(record, "evidence_class", None)
        if getattr(evidence_class, "value", "") != "inferred_mapping":
            continue
        if (
            getattr(record, "heuristic", None) == "unreviewed-config"
            or details.get("review_status") == "unreviewed"
        ):
            count += 1
    return count


def behavior_details(
    behaviors: Sequence[object],
    assessments: Mapping[str, Mapping[str, object]],
    unit_assessments: Optional[Mapping[str, Mapping[str, object]]] = None,
) -> List[Dict[str, object]]:
    """Merge raw behaviors with their per-tier assessments.

    ``unit`` is informative only: the unit tier is measured by package
    coverage, so a unit-tier behavior's mapping status is shown in the detail
    but never becomes a metric.
    """
    unit_assessments = unit_assessments or {}
    details: List[Dict[str, object]] = []
    for behavior in sorted(behaviors, key=lambda item: item.id):
        entry = behavior.to_dict()
        tiers = {
            tier: assessments[tier][behavior.id]
            for tier in ("integration", "e2e")
            if behavior.id in assessments.get(tier, {})
        }
        if behavior.id in unit_assessments:
            tiers["unit"] = unit_assessments[behavior.id]
        entry["tiers"] = tiers
        details.append(entry)
    return details
