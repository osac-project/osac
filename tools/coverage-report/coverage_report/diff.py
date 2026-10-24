"""Comparison against a previously published report.

A trend is only meaningful when the metric means the same thing in both runs.
For every metric the comparison checks that ``metric_version`` and
``inventory_revision`` match; when they do not, the entry says plainly that it
is not trend-comparable instead of printing a misleading delta.

The comparison is a pure function of two report dictionaries. The CLI decides
which previous report to read (``--previous`` or the one already published in
the output directory).
"""

from typing import Dict, List, Mapping, Optional, Tuple

NOT_COMPARABLE = "not trend-comparable (version/inventory changed)"

TREND_CHANGED = "changed"
TREND_NOT_COMPARABLE = "not-comparable"
TREND_ADDED = "added"
TREND_REMOVED = "removed"

_AVAILABILITY_STATES = frozenset({"unavailable", "stale", "blocked"})

_METRIC_FIELDS = (
    "state",
    "numerator",
    "denominator",
    "value_percent",
    "metric_version",
    "inventory_revision",
)


def _metrics_by_key(
    report: Mapping[str, object]
) -> Dict[Tuple[str, str], Dict[str, object]]:
    result: Dict[Tuple[str, str], Dict[str, object]] = {}
    for entry in report.get("metrics") or []:
        if not isinstance(entry, dict):
            continue
        feature = entry.get("feature")
        tier = entry.get("tier")
        if isinstance(feature, str) and isinstance(tier, str):
            result[(feature, tier)] = entry
    return result


def _metric_view(entry: Mapping[str, object]) -> Dict[str, object]:
    return {field: entry.get(field) for field in _METRIC_FIELDS}


def _behavior_ids(report: Mapping[str, object]) -> set:
    ids = set()
    for entry in report.get("behaviors") or []:
        if isinstance(entry, dict) and isinstance(entry.get("id"), str):
            ids.add(entry["id"])
    return ids


def _evidence_classes(report: Mapping[str, object]) -> Dict[str, str]:
    result: Dict[str, str] = {}
    for entry in report.get("evidence") or []:
        if not isinstance(entry, dict):
            continue
        evidence_id = entry.get("id")
        evidence_class = entry.get("evidence_class")
        if isinstance(evidence_id, str) and isinstance(evidence_class, str):
            result[evidence_id] = evidence_class
    return result


def _execution_statuses(
    report: Mapping[str, object]
) -> Dict[Tuple[str, str, str], str]:
    result: Dict[Tuple[str, str, str], str] = {}
    for feature in report.get("features") or []:
        if not isinstance(feature, dict):
            continue
        feature_key = feature.get("key")
        for behavior in feature.get("behaviors") or []:
            if not isinstance(behavior, dict):
                continue
            behavior_id = behavior.get("id")
            tiers = behavior.get("tiers")
            if not isinstance(tiers, dict):
                continue
            for tier, detail in tiers.items():
                if isinstance(detail, dict) and isinstance(behavior_id, str):
                    result[(str(feature_key), behavior_id, str(tier))] = str(
                        detail.get("execution_status", "unknown")
                    )
    return result


def _states(
    metrics: Mapping[Tuple[str, str], Mapping[str, object]]
) -> Dict[Tuple[str, str], str]:
    return {
        key: str(entry.get("state")) for key, entry in metrics.items()
    }


def build_diff(
    previous: Optional[Mapping[str, object]],
    current: Mapping[str, object],
) -> Dict[str, object]:
    """Return the comparison block stored in the report and rendered in Markdown."""
    if not isinstance(previous, Mapping):
        return {
            "previous_run_id": None,
            "previous_generated_at": None,
            "has_previous": False,
            "metrics": [],
            "metrics_added": [],
            "metrics_removed": [],
            "behaviors_added": [],
            "behaviors_removed": [],
            "mapping_class_changes": [],
            "execution_status_changes": [],
            "availability_changes": [],
        }

    before_metrics = _metrics_by_key(previous)
    after_metrics = _metrics_by_key(current)

    metric_changes: List[Dict[str, object]] = []
    for key in sorted(set(before_metrics) & set(after_metrics)):
        before = before_metrics[key]
        after = after_metrics[key]
        comparable = (
            before.get("metric_version") == after.get("metric_version")
            and before.get("inventory_revision") == after.get("inventory_revision")
        )
        if comparable and _metric_view(before) == _metric_view(after):
            continue
        metric_changes.append(
            {
                "feature": key[0],
                "tier": key[1],
                "trend": TREND_CHANGED if comparable else TREND_NOT_COMPARABLE,
                "reason": "" if comparable else NOT_COMPARABLE,
                "previous": _metric_view(before),
                "current": _metric_view(after),
            }
        )

    metrics_added = [
        {"feature": key[0], "tier": key[1], "trend": TREND_ADDED}
        for key in sorted(set(after_metrics) - set(before_metrics))
    ]
    metrics_removed = [
        {"feature": key[0], "tier": key[1], "trend": TREND_REMOVED}
        for key in sorted(set(before_metrics) - set(after_metrics))
    ]

    before_behaviors = _behavior_ids(previous)
    after_behaviors = _behavior_ids(current)
    behaviors_added = sorted(after_behaviors - before_behaviors)
    behaviors_removed = sorted(before_behaviors - after_behaviors)

    before_classes = _evidence_classes(previous)
    after_classes = _evidence_classes(current)
    mapping_class_changes = [
        {
            "evidence_id": evidence_id,
            "previous": before_classes[evidence_id],
            "current": after_classes[evidence_id],
        }
        for evidence_id in sorted(set(before_classes) & set(after_classes))
        if before_classes[evidence_id] != after_classes[evidence_id]
    ]

    before_execution = _execution_statuses(previous)
    after_execution = _execution_statuses(current)
    execution_changes = [
        {
            "feature": key[0],
            "behavior": key[1],
            "tier": key[2],
            "previous": before_execution[key],
            "current": after_execution[key],
        }
        for key in sorted(set(before_execution) & set(after_execution))
        if before_execution[key] != after_execution[key]
    ]

    before_states = _states(before_metrics)
    after_states = _states(after_metrics)
    availability_changes = [
        {
            "feature": key[0],
            "tier": key[1],
            "previous": before_states.get(key),
            "current": after_states.get(key),
        }
        for key in sorted(set(before_states) | set(after_states))
        if before_states.get(key) != after_states.get(key)
        and (
            before_states.get(key) in _AVAILABILITY_STATES
            or after_states.get(key) in _AVAILABILITY_STATES
        )
    ]

    previous_run_id = previous.get("run_id")
    return {
        "previous_run_id": (
            previous_run_id if isinstance(previous_run_id, str) else None
        ),
        "previous_generated_at": previous.get("generated_at"),
        "has_previous": True,
        "metrics": metric_changes,
        "metrics_added": metrics_added,
        "metrics_removed": metrics_removed,
        "behaviors_added": behaviors_added,
        "behaviors_removed": behaviors_removed,
        "mapping_class_changes": mapping_class_changes,
        "execution_status_changes": execution_changes,
        "availability_changes": availability_changes,
    }
