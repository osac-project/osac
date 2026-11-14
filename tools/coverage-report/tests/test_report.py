"""Metric, gap, verdict, and rendering tests."""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from coverage_report.inputs import INTEGRATION, Feature, InputError, PackageCoverage  # noqa: E402
from coverage_report.report import (  # noqa: E402
    _verdict,
    build_feature_report,
    render_markdown,
    unit_metric,
)


def case(tc_id, tier=None, priority="high", automation="automated", execution=None, title="case"):
    lines = [
        "#### {0}: {1}".format(tc_id, title),
        "",
        "| Interface Change | Priority | Automation |",
        "|---|---|---|",
        "| IC-1 | {0} | {1} |".format(priority, automation),
        "",
    ]
    if tier is not None:
        lines.append("- **Tier / owner:** {0}.".format(tier))
    if execution is not None:
        lines.append("- **Execution:** {0}".format(execution))
    lines.append("")
    return "\n".join(lines) + "\n"


def feature(**overrides):
    values = {
        "key": "OSAC-1",
        "name": "demo",
        "aliases": (),
        "testplans": ("plan.md",),
        "unit_packages": (("pkg/a", "why"),),
        "mappings_reviewed_by": "",
        "mappings": (),
    }
    values.update(overrides)
    return Feature(**values)


class Repository:
    def __init__(self, plan_text, files=()):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        with open(os.path.join(self.root, "plan.md"), "w", encoding="utf-8") as handle:
            handle.write(plan_text)
        for relative in files:
            target = os.path.join(self.root, relative)
            os.makedirs(os.path.dirname(target), exist_ok=True)
            with open(target, "w", encoding="utf-8") as handle:
                handle.write("package test\n")

    def __enter__(self):
        return self.root

    def __exit__(self, *exc_info):
        self.tmp.cleanup()


def measured():
    return {"pkg/a": PackageCoverage("pkg/a", 10, 10)}


class UnitMetricTests(unittest.TestCase):
    def test_unavailable_when_no_profile(self):
        metric = unit_metric(feature(), None)
        self.assertEqual(metric["state"], "unavailable")
        self.assertIsNone(metric["legacy_percent"])

    def test_blocked_when_a_package_is_absent(self):
        two = feature(unit_packages=(("pkg/a", "a"), ("pkg/b", "b")))
        metric = unit_metric(two, measured())
        self.assertEqual(metric["state"], "blocked")
        self.assertIn("pkg/b", metric["detail"])

    def test_zero_and_value_percentages(self):
        zero = unit_metric(feature(), {"pkg/a": PackageCoverage("pkg/a", 10, 0)})
        self.assertEqual(zero["state"], "zero")
        self.assertEqual(zero["display"], "0.0% (0/10)")

        value = unit_metric(feature(), {"pkg/a": PackageCoverage("pkg/a", 580, 405)})
        self.assertEqual(value["state"], "value")
        self.assertEqual(value["display"], "69.8% (405/580)")

    def test_zero_statement_package_keeps_none_and_skips_legacy(self):
        two = feature(unit_packages=(("pkg/a", "a"), ("pkg/b", "b")))
        metric = unit_metric(two, {"pkg/a": PackageCoverage("pkg/a", 100, 50),
                                   "pkg/b": PackageCoverage("pkg/b", 0, 0)})
        self.assertEqual((metric["rows"][1]["percent"], metric["rows"][1]["display"]), (None, "n/a"))
        self.assertEqual(metric["legacy_percent"], 50.0)

    def test_zero_statements_is_not_measured_zero(self):
        metric = unit_metric(feature(), {"pkg/a": PackageCoverage("pkg/a", 0, 0)})
        self.assertEqual(metric["state"], "na")
        self.assertEqual(metric["detail"], "no statements in mapped packages")

    def test_statement_weighted_and_legacy_averages(self):
        three = feature(
            unit_packages=(("pkg/servers", "s"), ("pkg/create", "c"), ("pkg/describe", "d"))
        )
        packages = {
            "pkg/servers": PackageCoverage("pkg/servers", 14308, 11049),
            "pkg/create": PackageCoverage("pkg/create", 43, 9),
            "pkg/describe": PackageCoverage("pkg/describe", 46, 26),
        }
        metric = unit_metric(three, packages)
        self.assertEqual(metric["display"], "77.0% (11084/14397)")
        self.assertEqual(metric["legacy_percent"], 51.5)


class MappingTests(unittest.TestCase):
    def test_covered_behavior(self):
        plan = case("TC-FR1-01", tier="Component integration / [DEV]", execution="`it/test.go`")
        with Repository(plan, ["it/test.go"]) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(report["behaviors"][0]["status"], "covered")
        self.assertEqual(report["metrics"]["integration"]["display"], "100.0% (1/1)")
        self.assertEqual(report["gaps"], [])

    def test_tier_without_cases_is_neutral(self):
        plan = case("TC-FR1-01", tier="Component integration / [DEV]", execution="`it/test.go`")
        with Repository(plan, ["it/test.go"]) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(report["metrics"]["e2e"]["state"], "none")
        self.assertEqual(report["metrics"]["e2e"]["display"], "no cases at this tier")
        self.assertEqual(report["verdict"], "ready")
        self.assertEqual(report["verdict_reason"], "All tiers measured, no critical or high gaps")

    def test_proposed_path_is_a_gap(self):
        plan = case("TC-FR1-01", tier="Component integration / [DEV]", execution="proposed `it/new_test.go`")
        with Repository(plan) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(report["behaviors"][0]["status"], "planned")
        self.assertEqual(report["gaps"][0]["kind"], "planned-not-implemented")
        self.assertEqual(report["gaps"][0]["next_action"], "Implement it/new_test.go")

    def test_no_path_is_a_gap(self):
        plan = case("TC-FR1-01", tier="Component integration / [DEV]", execution="`ginkgo run -r internal`")
        with Repository(plan) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(report["behaviors"][0]["status"], "no-mapping")
        self.assertEqual(report["gaps"][0]["kind"], "no-mapping")

    def test_unassigned_tier_blocks_both_tier_metrics(self):
        plan = case("TC-FR1-01", execution="`ginkgo run -r internal`")
        with Repository(plan) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        for tier in ("integration", "e2e"):
            self.assertEqual(report["metrics"][tier]["state"], "blocked")
            self.assertIn("1 tier-unassigned", report["metrics"][tier]["detail"])
        self.assertEqual(report["gaps"][0]["kind"], "tier-unassigned")
        self.assertEqual(report["gaps"][0]["next_action"], "Add a tier to TC-FR1-01 in the test plan")
        self.assertEqual(report["verdict_reason"], "1 high gap")

    def test_manual_behavior_is_excluded(self):
        plan = case("TC-FR1-01", tier="E2E / [QE]", automation="manual")
        with Repository(plan) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(report["behaviors"][0]["status"], "excluded")
        self.assertEqual(report["metrics"]["e2e"]["excluded"], 1)

    def test_reviewed_mapping_counts_and_unreviewed_does_not(self):
        mapping = {"behavior": "TC-FR1-01", "test": "it/test.go", "tier": INTEGRATION}
        plan = case("TC-FR1-01", tier="Component integration / [DEV]")
        with Repository(plan, ["it/test.go"]) as root:
            reviewed = build_feature_report(
                feature(mappings_reviewed_by="@reviewer", mappings=(mapping,)), root, "abc", measured()
            )
            unreviewed = build_feature_report(
                feature(mappings_reviewed_by="PENDING-REVIEW", mappings=(mapping,)), root, "abc", measured()
            )
        self.assertEqual(reviewed["behaviors"][0]["status"], "covered")
        self.assertEqual(reviewed["inferred"], [])
        self.assertEqual(unreviewed["behaviors"][0]["status"], "no-mapping")
        self.assertEqual(unreviewed["inferred"], [{"behavior": "TC-FR1-01", "test": "it/test.go"}])
        self.assertEqual(unreviewed["metrics"]["integration"]["state"], "zero")

    def test_inline_behavior_joins_inventory(self):
        inline = ({"id": "flow-1", "title": "Flow", "tier": "e2e"},)
        mapping = {"behavior": "flow-1", "test": "tests/e2e/x.py", "tier": "e2e"}
        with Repository("") as root:
            report = build_feature_report(
                feature(testplans=(), behaviors=inline, mappings=(mapping,)), root, "abc", measured()
            )
        self.assertEqual(report["behaviors"][0]["id"], "flow-1")
        self.assertEqual(report["metrics"]["e2e"]["state"], "zero")
        self.assertEqual(report["verdict"], "insufficient evidence")
        self.assertEqual(report["verdict_reason"],
                         "E2E at 0.0% (0/1): 1 drafted test link pending review; Integration: no test plan")
        self.assertEqual(report["inferred"], [{"behavior": "flow-1", "test": "tests/e2e/x.py"}])

    def test_no_plan_tier_without_cases_is_na(self):
        inline = ({"id": "flow-1", "title": "Flow", "tier": "e2e"},)
        with Repository("") as root:
            report = build_feature_report(feature(testplans=(), behaviors=inline), root, "abc", measured())
        self.assertEqual(report["metrics"]["integration"]["state"], "na")
        self.assertEqual(report["metrics"]["integration"]["detail"], "no test plan")
        self.assertEqual(report["metrics"]["e2e"]["state"], "zero")
        self.assertEqual(report["verdict_reason"],
                         "E2E at 0.0% (0/1): no reviewed test links yet; Integration: no test plan")

    def test_plan_without_cases_is_na_not_ready(self):
        with Repository("# Plan\n\nNo test cases here.\n") as root:
            report = build_feature_report(feature(testplans=("plan.md",)), root, "abc", measured())
        self.assertEqual(report["metrics"]["integration"]["state"], "na")
        self.assertEqual(report["metrics"]["e2e"]["state"], "na")
        self.assertEqual(report["verdict"], "insufficient evidence")

    def test_undecodable_test_plan_is_an_input_error(self):
        with tempfile.TemporaryDirectory() as root:
            with open(os.path.join(root, "plan.md"), "wb") as handle:
                handle.write(b"#### TC-1: bad \xff\xfe\n")
            with self.assertRaises(InputError):
                build_feature_report(feature(testplans=("plan.md",)), root, "abc", measured())

    def test_missing_test_plan(self):
        with Repository("") as root:
            report = build_feature_report(feature(testplans=("absent.md",)), root, "abc", measured())
        self.assertEqual(report["metrics"]["integration"]["state"], "na")
        self.assertEqual(report["metrics"]["e2e"]["state"], "na")
        self.assertEqual(report["verdict"], "insufficient evidence")
        self.assertEqual(report["verdict_reason"], "Integration: no test plan; E2E: no test plan")

    def test_multiple_testplans_prefix_ids(self):
        plans = ("OSAC-1-a/testplan.md", "OSAC-2-b/testplan.md")
        with tempfile.TemporaryDirectory() as root:
            for plan in plans:
                target = os.path.join(root, plan)
                os.makedirs(os.path.dirname(target), exist_ok=True)
                with open(target, "w", encoding="utf-8") as handle:
                    handle.write(case("TC-FR1-01", tier="E2E / [QE]"))
            report = build_feature_report(feature(testplans=plans), root, "abc", measured())
        ids = [behavior["id"] for behavior in report["behaviors"]]
        self.assertEqual(ids, ["OSAC-1/TC-FR1-01", "OSAC-2/TC-FR1-01"])


class VerdictTests(unittest.TestCase):
    def metrics(self, **states):
        base = {tier: {"state": "value", "display": "100.0% (1/1)"}
                for tier in ("unit", "integration", "e2e")}
        for tier, state in states.items():
            base[tier]["state"] = state
        return base

    def verdict(self, metrics, gaps):
        return _verdict(metrics, gaps, {})[0]

    def test_rules(self):
        self.assertEqual(self.verdict(self.metrics(), []), "ready")
        self.assertEqual(self.verdict(self.metrics(), [{"priority": "critical"}]), "not ready")
        self.assertEqual(self.verdict(self.metrics(), [{"priority": "low"}]), "ready")
        self.assertEqual(self.verdict(self.metrics(e2e="zero"), []), "insufficient evidence")
        self.assertEqual(self.verdict(self.metrics(e2e="none"), []), "ready")
        self.assertEqual(self.verdict(self.metrics(unit="unavailable"), []), "insufficient evidence")
        self.assertEqual(self.verdict(self.metrics(integration="blocked"), []), "insufficient evidence")
        self.assertEqual(self.verdict(self.metrics(integration="na"), []), "insufficient evidence")
        self.assertEqual(self.verdict(self.metrics(integration="blocked", e2e="zero"), []), "insufficient evidence")
        self.assertEqual(self.verdict(self.metrics(integration="blocked"), [{"priority": "high"}]), "not ready")


class RenderingTests(unittest.TestCase):
    def test_escapes_pipes_and_newlines(self):
        plan = case("TC-FR1-01", tier="Component integration / [DEV]", execution="proposed `it/x_test.go`",
                    title="Pipes | here")
        with Repository(plan) as root:
            report = build_feature_report(feature(), root, "abc", measured())
        report["gaps"][0]["next_action"] = "first\nsecond"
        markdown = render_markdown(report)
        self.assertIn("Pipes \\| here", markdown)
        self.assertIn("first second", markdown)

    def test_serialization_is_stable(self):
        plan = case("TC-FR1-01", tier="E2E / [QE]", execution="proposed `tests/e2e/x_test.py`")
        with Repository(plan) as root:
            first = build_feature_report(feature(), root, "abc", measured())
            second = build_feature_report(feature(), root, "abc", measured())
        self.assertEqual(json.dumps(first, sort_keys=True), json.dumps(second, sort_keys=True))
        self.assertNotIn("timestamp", json.dumps(first).casefold())


if __name__ == "__main__":
    unittest.main()
