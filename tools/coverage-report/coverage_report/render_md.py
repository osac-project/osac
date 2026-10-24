"""Markdown rendering.

The renderer formats values that are already in the report dictionary. It does
not compute a percentage, join evidence, or decide a verdict: every number and
state shown here was decided by the engine and is present in ``report.json``.
"""

import re
from typing import Dict, List, Mapping, Sequence

from .model import TIER_ORDER

_STATE_WORDS = frozenset({"na", "unavailable", "stale", "blocked"})

_EVIDENCE_CLASS_LEGEND = (
    ("measured", "a collector measured the artifact directly (unit profile)."),
    ("observed_execution", "a test ran; JUnit records what happened."),
    ("declared_mapping", "a human-reviewed mapping says this test covers it."),
    ("inferred_mapping", "a heuristic suggests coverage; it never counts."),
    ("gap_unknown", "the mapping names evidence that cannot be found."),
)


#: Characters that could change Markdown structure or inject HTML/images/links.
#: The backslash is included so an existing escape cannot smuggle a separator
#: through a single pass.
_MARKDOWN_SPECIAL_RE = re.compile(r"([\\`*_\[\]<>!|])")


def _cell(value: object) -> str:
    """Escape untrusted text for a Markdown table cell.

    Table cells carry evidence-controlled text (test titles, paths, diagnostic
    messages). Newlines become spaces so a value cannot start a new block, and
    backslashes, inline HTML, images, links, emphasis, code spans, and cell
    separators are escaped in one pass.
    """
    text = str(value if value is not None else "")
    text = text.replace("\r", " ").replace("\n", " ")
    return _MARKDOWN_SPECIAL_RE.sub(r"\\\1", text)


def _percent(value: object) -> str:
    try:
        return "{0:.1f}".format(float(value))
    except (TypeError, ValueError):
        return "0.0"


def _metric_cell(metric: Mapping[str, object]) -> str:
    state = str(metric.get("state", ""))
    tier_unassigned = int(metric.get("tier_unassigned") or 0)
    if state == "blocked" and tier_unassigned:
        provisional = metric.get("provisional_percent")
        percent_text = (
            "-" if provisional is None else _percent(provisional) + "%"
        )
        return "blocked (provisional {0} {1}/{2}, {3} tier-unassigned)".format(
            percent_text,
            _cell(metric.get("numerator")),
            _cell(metric.get("denominator")),
            tier_unassigned,
        )
    if state in _STATE_WORDS:
        return state
    return "{0}% ({1}/{2}) [{3}]".format(
        _percent(metric.get("value_percent")),
        _cell(metric.get("numerator")),
        _cell(metric.get("denominator")),
        _cell(metric.get("metric_version")),
    )


def _unit_cell(
    metric: Mapping[str, object],
    alternatives: Sequence[Mapping[str, object]],
) -> str:
    cell = _metric_cell(metric)
    for alternative in alternatives:
        if alternative.get("tier") != "unit":
            continue
        state = str(alternative.get("state", ""))
        if state in _STATE_WORDS:
            continue
        cell = "{0} ({1}: {2}%)".format(
            cell,
            _cell(alternative.get("metric_version")),
            _percent(alternative.get("value_percent")),
        )
    return cell


def _coverage_table(report: Mapping[str, object]) -> List[str]:
    lines = [
        "| Feature | Unit | Integration | E2E |",
        "|---|---|---|---|",
    ]
    for feature in report.get("features") or []:
        tiers = feature.get("tiers") or {}
        alternatives = feature.get("alternative_metrics") or []
        cells = [
            tiers.get("unit", {}),
            tiers.get("integration", {}),
            tiers.get("e2e", {}),
        ]
        lines.append(
            "| {0} ({1}) | {2} | {3} | {4} |".format(
                _cell(feature.get("key")),
                _cell(feature.get("name")),
                _unit_cell(cells[0], alternatives),
                _metric_cell(cells[1]),
                _metric_cell(cells[2]),
            )
        )
    return lines


def _readiness(report: Mapping[str, object]) -> List[str]:
    summary = report.get("summary") or {}
    lines = [
        "## Readiness summary",
        "",
        "| Feature | Verdict | Critical gaps | High gaps | "
        "Unavailable / stale / blocked | Zero metrics | Tier unassigned |",
        "|---|---|---|---|---|---|---|",
    ]
    for entry in summary.get("features") or []:
        sources: List[str] = []
        for label, key in (
            ("unavailable", "unavailable_metrics"),
            ("stale", "stale_metrics"),
            ("blocked", "blocked_metrics"),
        ):
            for tier in entry.get(key) or []:
                sources.append("{0}: {1}".format(tier, label))
        for tier in entry.get("missing_inventory_metrics") or []:
            sources.append("{0}: no test plan".format(tier))
        zero = entry.get("zero_metrics") or []
        lines.append(
            "| {0} | {1} | {2} | {3} | {4} | {5} | {6} |".format(
                _cell(entry.get("key")),
                _cell(entry.get("verdict")),
                _cell(entry.get("critical_gaps")),
                _cell(entry.get("high_gaps")),
                _cell("; ".join(sources) or "none"),
                _cell(", ".join(zero) or "none"),
                _cell(entry.get("tier_unassigned")),
            )
        )

    methodology = summary.get("methodology") or {}
    lines.extend(["", "Methodology:", ""])
    for note in methodology.get("notes") or []:
        lines.append("- {0}".format(note))

    actions = summary.get("top_actions") or []
    if actions:
        lines.extend(["", "Top actions:", ""])
        for index, action in enumerate(actions, start=1):
            scope = _cell(action.get("feature"))
            if action.get("tier"):
                scope = "{0} {1}".format(scope, _cell(action.get("tier")))
            lines.append(
                "{0}. [{1}] {2} {3} — {4}".format(
                    index,
                    _cell(action.get("priority")),
                    scope,
                    _cell(action.get("subject")),
                    _cell(action.get("action")),
                )
            )
    return lines


def _boundary_text(boundary: Mapping[str, object]) -> str:
    real = boundary.get("real") or []
    simulated = boundary.get("simulated") or []
    parts = []
    if real:
        parts.append("real: {0}".format(", ".join(str(item) for item in real)))
    if simulated:
        parts.append(
            "simulated: {0}".format(", ".join(str(item) for item in simulated))
        )
    return "; ".join(parts) or "-"


def _metric_detail(
    metric: Mapping[str, object],
    breakdown: Mapping[str, object],
) -> List[str]:
    state = str(metric.get("state", ""))
    lines = ["- Formula: {0}".format(metric.get("formula") or "-")]
    if metric.get("numerator") is not None:
        if metric.get("value_percent") is not None:
            suffix = "{0}%".format(_percent(metric.get("value_percent")))
        elif metric.get("provisional_percent") is not None:
            suffix = "provisional {0}%".format(
                _percent(metric.get("provisional_percent"))
            )
        else:
            suffix = "no percentage"
        lines.append(
            "- Numerator / denominator: {0}/{1} ({2})".format(
                _cell(metric.get("numerator")),
                _cell(metric.get("denominator")),
                suffix,
            )
        )
    if state not in ("value", "zero"):
        reason = metric.get("reason")
        lines.append(
            "- State: {0}{1}".format(
                state, " ({0})".format(reason) if reason else ""
            )
        )
    if breakdown.get("denominator") is not None:
        lines.append(
            "- Denominator: {0} (declared tier {1}); tier-unassigned {2}; "
            "excluded {3}; unassessable {4}".format(
                _cell(breakdown.get("denominator")),
                _cell(breakdown.get("required_tier")),
                _cell(breakdown.get("tier_unassigned")),
                _cell(breakdown.get("excluded")),
                _cell(breakdown.get("unassessable")),
            )
        )
    # When the unit metric has no value there is no scope to describe.
    if metric.get("tier") == "unit" and state not in ("value", "zero"):
        return lines
    if metric.get("inventory_revision"):
        lines.append(
            "- Scope revision: `{0}`".format(metric["inventory_revision"])
        )
    return lines


def _behavior_table(
    details: Sequence[Mapping[str, object]], tier: str
) -> List[str]:
    lines = [
        "| Behavior | Title | Priority | Tier label | Status | Evidence | Execution | Boundary |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for entry in details:
        detail = (entry.get("tiers") or {}).get(tier)
        if detail is None:
            continue
        status = str(detail.get("status"))
        reason = detail.get("reason")
        if reason:
            status = "{0} ({1})".format(status, reason)
        # The original label already contains any secondary-tier clause; the
        # structured note is in the JSON for tools that need it.
        tier_label = entry.get("tier_label") or ""
        lines.append(
            "| {0} | {1} | {2} | {3} | {4} | {5} | {6} | {7} |".format(
                _cell(entry.get("id")),
                _cell(entry.get("title")),
                _cell(entry.get("priority")),
                _cell(tier_label),
                _cell(status),
                _cell(detail.get("evidence_class")),
                _cell(detail.get("execution_status")),
                _cell(_boundary_text(detail.get("boundary") or {})),
            )
        )
    return lines


def _gap_table(gaps: Sequence[Mapping[str, object]]) -> List[str]:
    if not gaps:
        return ["No gaps."]
    lines = [
        "| Priority | Subject | Reason | Next action |",
        "|---|---|---|---|",
    ]
    for gap in gaps:
        lines.append(
            "| {0} | {1} | {2} | {3} |".format(
                _cell(gap.get("priority")),
                _cell(gap.get("behavior")),
                _cell(gap.get("reason")),
                _cell(gap.get("next_action")),
            )
        )
    return lines


def _package_table(measured: Sequence[Mapping[str, object]]) -> List[str]:
    if not measured:
        return []
    lines = [
        "| Package | Statements | Covered | Percent |",
        "|---|---|---|---|",
    ]
    for record in measured:
        details = record.get("details") or {}
        lines.append(
            "| {0} | {1} | {2} | {3}% |".format(
                _cell(record.get("subject")),
                _cell(details.get("statements")),
                _cell(details.get("covered")),
                _percent(details.get("percent")),
            )
        )
    return lines


def _detail(report: Mapping[str, object]) -> List[str]:
    lines = ["## Detail", ""]
    evidence = report.get("evidence") or []
    measured_by_feature: Dict[str, List[Mapping[str, object]]] = {}
    for record in evidence:
        if record.get("evidence_class") == "measured":
            measured_by_feature.setdefault(str(record.get("feature")), []).append(
                record
            )
    for feature in report.get("features") or []:
        key = feature.get("key")
        lines.extend(
            ["### {0} ({1})".format(_cell(key), _cell(feature.get("name"))), ""]
        )
        for inventory in feature.get("inventories") or []:
            lines.append(
                "- Inventory: `{0}` revision `{1}` behaviors {2}".format(
                    _cell(inventory.get("path")),
                    _cell(inventory.get("revision")),
                    _cell(inventory.get("behavior_count")),
                )
            )
        lines.append("")

        breakdown = feature.get("denominator_breakdown") or {}
        tiers = feature.get("tiers") or {}
        gaps = feature.get("gaps") or []

        lines.extend(["#### Unit", ""])
        lines.extend(
            _metric_detail(
                tiers.get("unit", {}), breakdown.get("unit", {})
            )
        )
        packages = _package_table(measured_by_feature.get(str(key), []))
        if packages:
            lines.extend([""] + packages)
        unit_behaviors = int(feature.get("unit_behaviors") or 0)
        if unit_behaviors:
            lines.extend(
                [
                    "",
                    "- {0} unit-tier {1} (unit tier uses package coverage; "
                    "behavior mapping shown for information)".format(
                        unit_behaviors,
                        "behavior" if unit_behaviors == 1 else "behaviors",
                    ),
                    "",
                ]
                + _behavior_table(feature.get("behaviors") or [], "unit")
            )
        unit_gaps = [gap for gap in gaps if gap.get("tier") == "unit"]
        lines.extend(["", "Unit gaps:", ""] + _gap_table(unit_gaps))
        lines.append("")

        for tier in (TIER_ORDER[1], TIER_ORDER[2]):
            lines.extend(["#### {0}".format(tier.value.capitalize()), ""])
            lines.extend(
                _metric_detail(
                    tiers.get(tier.value, {}),
                    breakdown.get(tier.value, {}),
                )
            )
            tier_total = (feature.get("behavior_counts") or {}).get(tier.value)
            tier_unassessable = (
                feature.get("unassessable_by_tier") or {}
            ).get(tier.value, 0)
            if tier_total is not None:
                lines.append(
                    "- Behaviors: {0} at this tier ({1} assessable, {2} "
                    "unassessable)".format(
                        tier_total,
                        _cell(tiers.get(tier.value, {}).get("denominator")),
                        tier_unassessable,
                    )
                )
            lines.extend(
                [""] + _behavior_table(feature.get("behaviors") or [], tier.value)
            )
            tier_gaps = [
                gap for gap in gaps if gap.get("tier") == tier.value
            ]
            lines.extend(["", "Gaps:", ""] + _gap_table(tier_gaps))
            lines.append("")

    lines.extend(["#### Evidence classes", ""])
    for name, description in _EVIDENCE_CLASS_LEGEND:
        lines.append("- `{0}`: {1}".format(name, description))
    return lines


def _format_metric_view(view: Mapping[str, object]) -> str:
    state = str(view.get("state"))
    if state in ("value", "zero"):
        return "{0} {1}% ({2}/{3}) [{4}]".format(
            state,
            _percent(view.get("value_percent")),
            _cell(view.get("numerator")),
            _cell(view.get("denominator")),
            _cell(view.get("metric_version")),
        )
    return "{0} [{1}]".format(state, _cell(view.get("metric_version")))


def _changes(report: Mapping[str, object]) -> List[str]:
    comparison = report.get("comparison") or {}
    lines = ["## What changed since last run", ""]
    if not comparison.get("has_previous"):
        lines.append("No previous report was compared.")
        return lines

    lines.append(
        "- Previous run: `{0}` ({1})".format(
            _cell(comparison.get("previous_run_id")),
            _cell(comparison.get("previous_generated_at")),
        )
    )
    change_count = 0
    for entry in comparison.get("metrics") or []:
        change_count += 1
        if entry.get("trend") == "not-comparable":
            lines.append(
                "- {0} {1}: {2}".format(
                    _cell(entry.get("feature")),
                    _cell(entry.get("tier")),
                    NOT_COMPARABLE_TEXT,
                )
            )
            continue
        lines.append(
            "- {0} {1}: {2} -> {3}".format(
                _cell(entry.get("feature")),
                _cell(entry.get("tier")),
                _format_metric_view(entry.get("previous") or {}),
                _format_metric_view(entry.get("current") or {}),
            )
        )
    for entry in comparison.get("metrics_added") or []:
        change_count += 1
        lines.append(
            "- {0} {1}: metric added".format(
                _cell(entry.get("feature")), _cell(entry.get("tier"))
            )
        )
    for entry in comparison.get("metrics_removed") or []:
        change_count += 1
        lines.append(
            "- {0} {1}: metric removed".format(
                _cell(entry.get("feature")), _cell(entry.get("tier"))
            )
        )
    for behavior_id in comparison.get("behaviors_added") or []:
        change_count += 1
        lines.append("- behavior added: {0}".format(_cell(behavior_id)))
    for behavior_id in comparison.get("behaviors_removed") or []:
        change_count += 1
        lines.append("- behavior removed: {0}".format(_cell(behavior_id)))
    for entry in comparison.get("mapping_class_changes") or []:
        change_count += 1
        lines.append(
            "- mapping class {0}: {1} -> {2}".format(
                _cell(entry.get("evidence_id")),
                _cell(entry.get("previous")),
                _cell(entry.get("current")),
            )
        )
    for entry in comparison.get("execution_status_changes") or []:
        change_count += 1
        lines.append(
            "- execution {0} {1} {2}: {3} -> {4}".format(
                _cell(entry.get("feature")),
                _cell(entry.get("tier")),
                _cell(entry.get("behavior")),
                _cell(entry.get("previous")),
                _cell(entry.get("current")),
            )
        )
    for entry in comparison.get("availability_changes") or []:
        change_count += 1
        lines.append(
            "- availability {0} {1}: {2} -> {3}".format(
                _cell(entry.get("feature")),
                _cell(entry.get("tier")),
                _cell(entry.get("previous")),
                _cell(entry.get("current")),
            )
        )
    if change_count == 0:
        lines.append(
            "- No metric, behavior, mapping, execution, or availability changes."
        )
    return lines


NOT_COMPARABLE_TEXT = "not trend-comparable (version/inventory changed)"


def _diagnostics(report: Mapping[str, object]) -> List[str]:
    diagnostics = report.get("diagnostics") or []
    if not diagnostics:
        return []
    lines = [
        "## Diagnostics",
        "",
        "| Severity | Code | Collector | Message |",
        "|---|---|---|---|",
    ]
    for diagnostic in diagnostics:
        lines.append(
            "| {0} | {1} | {2} | {3} |".format(
                _cell(diagnostic.get("severity")),
                _cell(diagnostic.get("code")),
                _cell(diagnostic.get("collector")),
                _cell(diagnostic.get("message")),
            )
        )
    return lines


def render_markdown(report: Mapping[str, object]) -> str:
    """Render the report dictionary as Markdown."""
    features = report.get("features") or []
    if len(features) == 1:
        heading = "{0} ({1})".format(features[0].get("key"), features[0].get("name"))
    else:
        heading = "all features"

    lines = ["# Coverage report: {0}".format(heading), ""]
    lines.append("- Run: `{0}`".format(report.get("run_id")))
    lines.append(
        "- Commit: `{0}` (source: {1})".format(
            (report.get("repo") or {}).get("sha"),
            (report.get("repo") or {}).get("sha_source"),
        )
    )
    lines.append("- Generated: {0}".format(report.get("generated_at")))
    lines.append("- Tool: {0}".format(report.get("tool_version")))
    lines.append("")

    lines.extend(_readiness(report))
    lines.append("")
    lines.extend(["## Coverage table", ""])
    lines.extend(_coverage_table(report))
    lines.extend(
        [
            "",
            "States: value, zero, na, unavailable, stale, blocked. Unavailable "
            "means no collector produced evidence; it is not zero coverage. "
            "Behaviors with no tier are excluded from both denominators and "
            "block the tier metrics; their percentages are provisional.",
            "",
        ]
    )
    lines.extend(_detail(report))
    lines.append("")
    lines.extend(_changes(report))
    lines.append("")
    diagnostics = _diagnostics(report)
    if diagnostics:
        lines.extend(diagnostics)
        lines.append("")
    return "\n".join(lines)
