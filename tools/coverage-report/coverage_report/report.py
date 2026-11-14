"""Turn parsed inputs into one report dictionary, then render it.

The dictionary is the single source of truth: ``report.json`` serialises it and
``report.md`` formats the values already inside it. Markdown rendering never
computes a percentage.
"""

from __future__ import annotations

import os
from decimal import ROUND_HALF_UP, Decimal
from typing import Dict, List, Optional, Sequence, Tuple

from .inputs import (
    E2E,
    INTEGRATION,
    NONE,
    UNIT,
    Behavior,
    Feature,
    InputError,
    PackageCoverage,
    candidate_test_paths,
    contained_path,
    execution_matrix,
    normalize_tier,
    parse_testplan,
    plan_key,
    round_percent,
    testplan_path,
)

TIERS = (UNIT, INTEGRATION, E2E)
PRIORITY_ORDER = {"critical": 0, "high": 1, "medium": 2, "low": 3}
INSUFFICIENT_STATES = {"unavailable", "blocked", "na"}
VERDICT_INSUFFICIENT = "insufficient evidence"
VERDICT_NOT_READY = "not ready"
VERDICT_READY = "ready"
TIER_LABELS = {UNIT: "Unit", INTEGRATION: "Integration", E2E: "E2E"}


def _display_percent(covered: int, denominator: int) -> str:
    percent = round_percent(covered, denominator)
    if percent is None:
        return "n/a"
    return "{0:.1f}% ({1}/{2})".format(percent, covered, denominator)


def _legacy_percent(rows: Sequence[dict]) -> Optional[float]:
    values = [Decimal(str(row["percent"])) for row in rows if row["percent"] is not None]
    if not values:
        return None
    mean = sum(values) / Decimal(len(values))
    return float(mean.quantize(Decimal("0.1"), rounding=ROUND_HALF_UP))


def _is_reviewed(value: str) -> bool:
    """Accept only an ``@user`` or ``org/team`` reviewer handle."""
    text = (value or "").strip()
    if "/" not in text and not text.startswith("@"):
        return False
    parts = text.lstrip("@").split("/")
    return len(parts) <= 2 and all(
        part and part.replace("-", "").replace("_", "").replace(".", "").isalnum()
        for part in parts
    )


def unit_metric(feature: Feature, packages: Optional[Dict[str, PackageCoverage]]) -> dict:
    """Measure statement coverage for the feature's mapped Go packages."""
    rows: List[dict] = []
    missing = [name for name, _rationale in feature.unit_packages if packages is not None and name not in packages]
    for name, rationale in feature.unit_packages:
        package = packages.get(name) if packages is not None else None
        row = {"package": name, "rationale": rationale,
               "statements": None, "covered": None, "percent": None}
        if packages is None:
            row["display"] = "unavailable"
        elif package is None:
            row["display"] = "not in profile"
        else:
            row.update(statements=package.statements, covered=package.covered,
                       percent=round_percent(package.covered, package.statements),
                       display=_display_percent(package.covered, package.statements))
        rows.append(row)

    if packages is None:
        return {"state": "unavailable", "display": "unavailable",
                "detail": "no coverprofile supplied", "rows": rows, "legacy_percent": None}
    if missing:
        detail = "package not in coverprofile: {0}".format(", ".join(missing))
        return {"state": "blocked", "display": "blocked",
                "detail": detail, "rows": rows, "legacy_percent": None}

    statements = sum(row["statements"] for row in rows)
    covered = sum(row["covered"] for row in rows)
    legacy = _legacy_percent(rows)
    if statements == 0:
        return {"state": "na", "display": "na",
                "detail": "no statements in mapped packages",
                "rows": rows, "legacy_percent": None}
    if covered == 0:
        return {"state": "zero", "display": _display_percent(0, statements),
                "detail": "measured Go statements; nothing covered",
                "covered": covered, "statements": statements, "rows": rows, "legacy_percent": legacy}
    return {"state": "value", "display": _display_percent(covered, statements),
            "detail": "measured Go statements; legacy package average {0:.1f}%".format(legacy),
            "covered": covered, "statements": statements, "rows": rows, "legacy_percent": legacy}


def _behavior_mapping(
    behavior: Behavior,
    mappings: Sequence[dict],
    reviewed: bool,
    matrix_text: str,
    repo_root: str,
    inferred: List[dict],
) -> dict:
    """Return the mapping status for one automated, tiered behavior."""
    declared, planned = candidate_test_paths(behavior.execution, matrix_text, repo_root)
    for mapping in mappings:
        path = str(mapping.get("test", "")).strip()
        if not path:
            continue
        if not reviewed:
            inferred.append({"behavior": behavior.id, "test": path})
        elif str(mapping.get("tier", "")).strip().casefold() == behavior.tier:
            resolved = contained_path(repo_root, path)
            if resolved is not None and os.path.isfile(resolved):
                declared.append(path)
            else:
                planned.append(path)

    declared = list(dict.fromkeys(declared))
    planned = list(dict.fromkeys(path for path in planned if path not in declared))
    return {"status": "covered" if declared else ("planned" if planned else "no-mapping"),
            "declared": declared, "planned": _best_path(planned)}


def _best_path(paths: Sequence[str]) -> str:
    """Pick the most specific planned path: a full path over a glob or basename."""
    if not paths:
        return ""
    return sorted(paths, key=lambda path: ("/" not in path or "*" in path, len(path), path))[0]


def _tier_metric(tier: str, behaviors: Sequence[Behavior], statuses: Dict[str, dict], plan: bool) -> dict:
    automated = [behavior for behavior in behaviors if not behavior.excluded]
    unassigned = [behavior for behavior in automated if behavior.tier == NONE]
    assessable = [behavior for behavior in automated if behavior.tier == tier]
    excluded = [behavior for behavior in behaviors if behavior.excluded and behavior.tier == tier]
    if unassigned:
        return {
            "state": "blocked",
            "display": "blocked",
            "detail": "{0} tier-unassigned behavior(s)".format(len(unassigned)),
            "excluded": len(excluded),
        }
    if not assessable:
        if plan:
            return {"state": "none", "display": "no cases at this tier",
                    "detail": "no automated {0} behaviors".format(tier),
                    "excluded": len(excluded)}
        return {"state": "na", "display": "na", "detail": "no test plan", "excluded": len(excluded)}
    covered = sum(1 for behavior in assessable if statuses[behavior.id]["status"] == "covered")
    denominator = len(assessable)
    state = "zero" if covered == 0 else "value"
    if covered:
        detail = "{0} of {1} automated behaviors have a declared existing test".format(covered, denominator)
    else:
        detail = "no automated behavior has a declared existing test"
    return {
        "state": state,
        "display": _display_percent(covered, denominator),
        "detail": detail,
        "covered": covered,
        "denominator": denominator,
        "excluded": len(excluded),
    }


def build_feature_report(
    feature: Feature, repo_root: str, commit: str, packages: Optional[Dict[str, PackageCoverage]],
    allow_external: Sequence[str] = ()
) -> dict:
    plans = list(feature.testplans)
    multi = len(plans) > 1
    behaviors: List[Behavior] = []
    matrices: Dict[str, str] = {}
    external_roots = set()
    testplan_found = False
    for plan in plans:
        path, trusted = testplan_path(repo_root, plan, allow_external)
        if trusted:
            external_roots.add(trusted)
        try:
            with open(path, encoding="utf-8") as handle:
                text = handle.read()
        except UnicodeDecodeError as exc:
            raise InputError("cannot decode test plan {0}: {1}".format(plan, exc)) from exc
        except OSError:
            continue
        parsed = parse_testplan(text, prefix=plan_key(plan) + "/" if multi else "")
        if parsed:
            testplan_found = True
        matrix = execution_matrix(text)
        for behavior in parsed:
            matrices[behavior.id] = matrix.get(behavior.tc_id, "")
        behaviors.extend(parsed)
    for item in feature.behaviors:
        behavior_id = str(item["id"]).strip()
        behaviors.append(Behavior(
            tc_id=behavior_id,
            title=str(item["title"]).strip(),
            priority=str(item.get("priority", "")).strip().casefold(),
            automation="automated",
            tier=normalize_tier(str(item.get("tier", ""))),
            execution="",
            id=behavior_id,
        ))

    reviewed = _is_reviewed(feature.mappings_reviewed_by)
    mappings_by_behavior: Dict[str, List[dict]] = {}
    for mapping in feature.mappings:
        key = str(mapping.get("behavior", "")).strip()
        mappings_by_behavior.setdefault(key, []).append(mapping)

    inferred: List[dict] = []
    statuses: Dict[str, dict] = {}
    gaps: List[dict] = []
    for behavior in behaviors:
        if behavior.excluded:
            statuses[behavior.id] = {"status": "excluded"}
            continue
        if behavior.tier == UNIT:
            statuses[behavior.id] = {"status": "unit-not-scored"}
            continue
        if behavior.tier == NONE:
            statuses[behavior.id] = {"status": "tier-unassigned"}
            gaps.append(_gap(behavior, "tier-unassigned",
                             "Add a tier to {0} in the test plan".format(behavior.id)))
            continue
        mapping = _behavior_mapping(
            behavior,
            mappings_by_behavior.get(behavior.id, ()),
            reviewed,
            matrices.get(behavior.id, ""),
            repo_root,
            inferred,
        )
        statuses[behavior.id] = {"status": mapping["status"]}
        if mapping["status"] == "covered":
            continue
        if mapping["status"] == "planned":
            gaps.append(_gap(behavior, "planned-not-implemented",
                             "Implement {0}".format(mapping["planned"])))
        else:
            gaps.append(_gap(behavior, "no-mapping",
                             "Map {0} to a test file".format(behavior.id)))

    metrics = {
        "unit": unit_metric(feature, packages),
        "integration": _tier_metric(INTEGRATION, behaviors, statuses, testplan_found),
        "e2e": _tier_metric(E2E, behaviors, statuses, testplan_found),
    }
    gaps.sort(key=lambda gap: (PRIORITY_ORDER.get(gap["priority"], 9), gap["behavior"], gap["kind"], gap["next_action"]))
    tiers = {behavior.id: behavior.tier for behavior in behaviors}
    inferred_counts = {tier: sum(tiers.get(item["behavior"]) == tier for item in inferred)
                       for tier in (INTEGRATION, E2E)}
    verdict, verdict_reason = _verdict(metrics, gaps, inferred_counts)
    return {
        "feature": {"key": feature.key, "name": feature.name, "group": feature.group,
                    "testplans": list(feature.testplans)},
        "external_roots": sorted(external_roots),
        "commit": commit,
        "verdict": verdict,
        "verdict_reason": verdict_reason,
        "metrics": metrics,
        "behaviors": [
            {"id": behavior.id, "title": behavior.title, "priority": behavior.priority,
             "automation": behavior.automation, "tier": behavior.tier,
             "status": statuses[behavior.id]["status"]}
            for behavior in behaviors
        ],
        "gaps": gaps,
        "inferred": inferred,
    }


def _gap(behavior: Behavior, kind: str, next_action: str) -> dict:
    return {
        "behavior": behavior.id,
        "title": behavior.title,
        "priority": behavior.priority if behavior.priority in PRIORITY_ORDER else "unknown",
        "kind": kind,
        "next_action": next_action,
    }


def _verdict(
    metrics: Dict[str, dict], gaps: Sequence[dict], inferred_counts: Dict[str, int]
) -> Tuple[str, str]:
    """Return ``(verdict, reason)`` from the metrics and gaps."""
    serious = [gap for gap in gaps if gap["priority"] in ("critical", "high")]
    if serious:
        critical = sum(1 for gap in serious if gap["priority"] == "critical")
        high = len(serious) - critical
        if critical and high:
            return VERDICT_NOT_READY, "{0} critical and {1} high gaps".format(critical, high)
        count, kind = (critical, "critical") if critical else (high, "high")
        return VERDICT_NOT_READY, "{0} {1} gap{2}".format(count, kind, "" if count == 1 else "s")
    zero_parts, other_parts = [], []
    for tier in TIERS:
        metric = metrics[tier]
        state = metric.get("state")
        label = TIER_LABELS[tier]
        if state == "zero":
            count = inferred_counts.get(tier, 0)
            suffix = ("no reviewed test links yet" if not count else
                      "{0} drafted test link{1} pending review".format(
                          count, "" if count == 1 else "s"))
            zero_parts.append("{0} at {1}: {2}".format(label, metric["display"], suffix))
        elif state in INSUFFICIENT_STATES:
            if state == "na":
                other_parts.append("{0}: {1}".format(label, metric.get("detail", "na")))
            else:
                other_parts.append("{0} {1}: {2}".format(label, state, metric.get("detail", "")))
    parts = zero_parts + other_parts
    if parts:
        return VERDICT_INSUFFICIENT, "; ".join(parts)
    return VERDICT_READY, "All tiers measured, no critical or high gaps"


# --- Markdown ---------------------------------------------------------------


def _cell(value: object) -> str:
    return " ".join(str(value).split()).replace("|", "\\|")


def _feature_markdown(report: dict) -> List[str]:
    feature = report["feature"]
    name = " ({0})".format(feature["name"]) if feature["name"] else ""
    tests = ", ".join("`{0}`".format(plan) for plan in feature["testplans"]) or "none"
    lines = [
        "# Coverage report: {0}{1}".format(feature["key"], name),
        "",
        "- Commit: `{0}`".format(report["commit"] or "unknown"),
    ]
    if feature["group"]:
        lines.append("- Group: {0}".format(feature["group"]))
    lines.append("- Test plans: {0}".format(tests))
    if report["external_roots"]:
        lines.append("- External roots: {0}".format(", ".join(map("`{0}`".format, report["external_roots"]))))
    lines.extend([
        "",
        "**Verdict: {0}**".format(report["verdict"]),
        "",
        "Reason: {0}".format(report["verdict_reason"]),
        "",
        "## Feature x tier",
        "",
        "| Tier | Coverage | Evidence |",
        "|---|---|---|",
    ])
    for tier in TIERS:
        metric = report["metrics"][tier]
        lines.append("| {0} | {1} | {2} |".format(TIER_LABELS[tier], _cell(metric["display"]), _cell(metric["detail"])))

    excluded = {tier: report["metrics"][tier].get("excluded", 0) for tier in (INTEGRATION, E2E)}
    excluded_text = ", ".join("{0} {1}".format(count, tier.upper()) for tier, count in excluded.items() if count)
    if excluded_text:
        lines.extend(["", "Excluded from scoring: {0} behavior(s) (manual or not automatable).".format(excluded_text)])

    lines.extend(["", "## Unit packages", "", "| Package | Statements | Covered | Percent | Rationale |", "|---|---|---|---|---|"])
    for row in report["metrics"]["unit"]["rows"]:
        lines.append(
            "| {0} | {1} | {2} | {3} | {4} |".format(
                _cell(row["package"]),
                _cell(row["statements"] if row["statements"] is not None else "-"),
                _cell(row["covered"] if row["covered"] is not None else "-"),
                _cell(row["display"]),
                _cell(row["rationale"]),
            )
        )

    if report["inferred"]:
        lines.extend(["", "## Inferred mappings (listed, not counted)", ""])
        for item in report["inferred"]:
            lines.append("- {0} -> `{1}` (no valid `mappings_reviewed_by`)".format(item["behavior"], item["test"]))

    lines.extend(["", "## Gaps ({0})".format(len(report["gaps"])), ""])
    if report["gaps"]:
        lines.extend(["| Priority | Behavior | Kind | Next action |", "|---|---|---|---|"])
        for gap in report["gaps"]:
            lines.append(
                "| {0} | {1} | {2} | {3} |".format(
                    _cell(gap["priority"]), _cell("{0}: {1}".format(gap["behavior"], gap["title"])),
                    _cell(gap["kind"]), _cell(gap["next_action"]),
                )
            )
    else:
        lines.append("No gaps.")

    lines.extend(
        [
            "",
            "## Legend",
            "",
            "- measured: parsed from a Go coverprofile given with `--coverprofile`.",
            "- declared: a test plan or reviewed config names an existing test file.",
            "- inferred: an unreviewed mapping; listed but never counted as covered.",
            "- gap: the plan names no existing test; the next action says what to add.",
            "- unavailable/blocked/na: missing evidence, never 0%; none: no cases at this tier.",
        ]
    )
    return lines


def render_markdown(report: dict) -> str:
    """Render one feature report or an all-features report."""
    sections = ["\n".join(_feature_markdown(item)) for item in report.get("features", [report])]
    prefix = ""
    if "overview" in report:
        rows = ["# Coverage overview", "", "| Area | Unit | Integration | E2E | Verdict |",
                "|---|---|---|---|---|"]
        rows.extend(
            "| {0} | {1} | {2} | {3} | {4} |".format(
                _cell(row["area"]), _cell(row["unit"]["display"]),
                _cell(row["integration"]["display"]), _cell(row["e2e"]["display"]),
                _cell(row["verdict"]))
            for row in report["overview"]
        )
        rows.extend([""] + [
            "- **{0}**: {1} — {2}".format(_cell(row["area"]), row["verdict"], row["verdict_reason"])
            for row in report["overview"]
        ])
        prefix = "\n".join(rows) + "\n\n---\n\n"
    return prefix + "\n\n---\n\n".join(sections) + "\n"
