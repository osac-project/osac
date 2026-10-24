"""Golden tests for the assembled report, Markdown, and comparison."""

import contextlib
import io
import json
import re
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.cli import main
from coverage_report.collectors import clear, register
from coverage_report.collectors.builtin import BUILTIN_COLLECTORS

from coverage_report.render_md import _cell, render_markdown
from coverage_report.report import build_summary

import schema_check
import support

SCHEMA_DIR = Path(__file__).resolve().parent.parent / "schemas"

FEATURE = "OSAC-1111"
STALE_SHA = "b" * 40

FEATURES_YAML = """\
version: 1
features:
  - key: OSAC-1111
    name: disk-image
    aliases:
      - diskimage
    testplan: docs/testplan.md
    components:
      - fulfillment-service
    unit_packages:
      - package: example.com/mod/pkg/alpha
        rationale: alpha behaviour
      - package: example.com/mod/pkg/beta
        rationale: beta behaviour
    suites:
      integration:
        - fulfillment-service/it
      e2e:
        - tests/e2e
"""

CONFIG_YAML = """\
version: 1
features_file: config/features.yaml
enhancement_roots: []
artifacts:
  coverprofiles:
    - unit.out
  junit:
    - fulfillment-service/it/reports/*.xml
    - tests/e2e/reports/*.xml
metric_versions:
  unit: statement-weighted-v2
  integration: behavior-inventory-v1
  e2e: behavior-inventory-v1
policy:
  require_execution: false
  unit_low_coverage_percent: 50
output_dir: .artifacts/coverage-report
"""

TESTPLAN = """\
# Testplan — OSAC-1111

## Overview

- **Feature:** OSAC-1111 — disk image metadata

## Test Cases

### FR-1: Integration behaviours

#### TC-FR1-01: Creates an image

| Tier | Priority | Automation |
|---|---|---|
| integration | critical | automated |

- **Boundary:** real (deployed API and database)

#### TC-FR1-02: Lists images

| Tier | Priority | Automation |
|---|---|---|
| integration | high | automated |

### FR-2: End-to-end journeys

#### TC-FR2-01: Journeys through provisioning

| Tier | Priority | Automation |
|---|---|---|
| e2e | high | automated |

### FR-3: Wrong tier requirement

#### TC-FR3-01: Requires integration but mapped as e2e

| Tier | Priority | Automation |
|---|---|---|
| integration | high | automated |

### FR-4: Boundary requirement

#### TC-FR4-01: Needs a real provider

| Tier | Priority | Automation |
|---|---|---|
| integration | critical | automated |

- **Boundary:** real (real provider)

### FR-5: Unknown tier

#### TC-FR5-01: Inferred only

| Priority | Automation |
|---|---|
| high | automated |

### FR-6: Manual

#### TC-FR6-01: Manual check

| Tier | Priority | Automation |
|---|---|---|
| integration | medium | manual |

### FR-7: Unit behavior

#### TC-FR7-01: Registry allowlist

| Tier | Priority | Automation |
|---|---|---|
| unit | high | automated |
"""

MAPPING_YAML = """\
version: 1
feature: OSAC-1111
reviewed_by: "@qe-team"
mappings:
  - behavior: TC-FR1-01
    tests:
      - {path: fulfillment-service/it/behaviors_test.go, name: creates an image, tier: integration}
    boundary: {real: [fulfillment-api], simulated: []}
    rationale: reviewed
  - behavior: TC-FR1-02
    tests:
      - {path: fulfillment-service/it/behaviors_test.go, name: lists images, tier: integration}
    boundary: {real: [fulfillment-api], simulated: []}
    rationale: reviewed
  - behavior: TC-FR2-01
    tests:
      - {path: tests/e2e/test_journey.py, name: journeys through provisioning, tier: e2e}
    boundary: {real: [osac-operator], simulated: []}
    rationale: reviewed
  - behavior: TC-FR3-01
    tests:
      - {path: tests/e2e/test_journey.py, name: journeys through provisioning, tier: e2e}
    boundary: {real: [osac-operator], simulated: []}
    rationale: reviewed at the wrong tier
  - behavior: TC-FR4-01
    tests:
      - {path: fulfillment-service/it/behaviors_test.go, name: needs a real provider, tier: integration}
    boundary: {real: [], simulated: [fake-storage]}
    rationale: reviewed but simulated only
"""

BEHAVIORS_TEST = """\
package it

func TestCreatesImage(t *testing.T) {}
func TestListsImages(t *testing.T) {}
"""

INFERRED_TEST = """\
package it

// There is no reviewed mapping for this case.
func TestInferredOnly(t *testing.T) {
\t// TC-FR5-01
}
"""

JOURNEY_TEST = """\
def test_journeys_through_provisioning():
    assert True
"""

COVERPROFILE = """\
mode: set
example.com/mod/pkg/alpha/a.go:1.1,2.2 2 1
example.com/mod/pkg/alpha/a.go:3.1,4.2 2 0
example.com/mod/pkg/beta/b.go:1.1,2.2 2 0
"""

PASS_FAIL_XML = """\
<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="integration-suite">
    <properties><property name="revision" value="{sha}"/></properties>
    <testcase classname="integration-suite" name="creates an image" status="passed"/>
    <testcase classname="integration-suite" name="lists images" status="failed">
      <failure message="expected 409"/>
    </testcase>
  </testsuite>
</testsuites>
"""

STALE_XML = """\
<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="e2e-suite">
    <properties><property name="revision" value="{sha}"/></properties>
    <testcase classname="tests.e2e" name="journeys through provisioning" status="passed"/>
  </testsuite>
</testsuites>
"""


def write(root, relative, text):
    path = Path(root, relative)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def run_main(argv):
    stdout = io.StringIO()
    stderr = io.StringIO()
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        exit_code = main(argv)
    return exit_code, stdout.getvalue(), stderr.getvalue()


def build_repo(root):
    """Create the golden repository, commit it, then add revisioned artifacts."""
    write(root, "config/mappings/OSAC-1111.yaml", MAPPING_YAML)
    write(root, "docs/testplan.md", TESTPLAN)
    write(root, "fulfillment-service/it/behaviors_test.go", BEHAVIORS_TEST)
    write(root, "fulfillment-service/it/inferred_test.go", INFERRED_TEST)
    write(root, "tests/e2e/test_journey.py", JOURNEY_TEST)
    write(root, "unit.out", COVERPROFILE)
    support.make_repo(Path(root), features=FEATURES_YAML, config=CONFIG_YAML)
    sha = support.head(root)

    # Artifacts written after the commit carry the commit they describe.
    write(
        root,
        "fulfillment-service/it/reports/pass-fail.xml",
        PASS_FAIL_XML.format(sha=sha),
    )
    write(
        root,
        "tests/e2e/reports/stale.xml",
        STALE_XML.format(sha=STALE_SHA),
    )
    write(root, "unit.out.meta.json", json.dumps({"revision": sha}))
    return sha


class GoldenCase(unittest.TestCase):
    def setUp(self):
        clear()
        from coverage_report.collectors.builtin import register_builtins

        register_builtins()
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = Path(self._tmp.name)
        self.sha = build_repo(self.repo)
        self.output_dir = Path(self.repo, ".artifacts", "coverage-report")
        self.run_golden()

    def tearDown(self):
        clear()

    def run_golden(self, extra=None, feature=FEATURE):
        argv = [] if feature is None else [feature]
        argv.extend(
            [
                "--repo",
                str(self.repo),
                "--now",
                "2026-10-22T00:00:00Z",
            ]
        )
        argv.extend(extra or [])
        return run_main(argv)

    def report(self, name=FEATURE):
        return support.read_json(self.output_dir / name / "report.json")

    def markdown(self, name=FEATURE):
        return (self.output_dir / name / "report.md").read_text(encoding="utf-8")

    def feature(self, report=None):
        report = report or self.report()
        return report["features"][0]

    def metric(self, tier, report=None):
        return self.feature(report)["tiers"][tier]

    def behavior(self, behavior_id, report=None):
        for entry in self.feature(report)["behaviors"]:
            if entry["id"] == behavior_id:
                return entry
        raise AssertionError("no behavior {0}".format(behavior_id))


class TestGoldenMetrics(GoldenCase):
    def test_unit_metric_uses_statement_weighting(self):
        unit = self.metric("unit")

        self.assertEqual(unit["state"], "value")
        self.assertEqual(unit["numerator"], 2)
        self.assertEqual(unit["denominator"], 6)
        self.assertEqual(unit["value_percent"], 33.3)
        self.assertEqual(unit["metric_version"], "statement-weighted-v2")

    def test_alternative_unit_metric_is_rendered_from_the_same_evidence(self):
        alternatives = self.feature()["alternative_metrics"]

        self.assertEqual(len(alternatives), 1)
        legacy = alternatives[0]
        self.assertEqual(legacy["metric_version"], "legacy-package-average-v1")
        self.assertEqual(legacy["numerator"], 500)
        self.assertEqual(legacy["denominator"], 20)
        self.assertEqual(legacy["value_percent"], 25.0)

    def test_integration_metric_is_blocked_and_provisional(self):
        metric = self.metric("integration")

        self.assertEqual(metric["state"], "blocked")
        self.assertEqual(metric["numerator"], 2)
        self.assertEqual(metric["denominator"], 4)
        self.assertIsNone(metric["value_percent"])
        self.assertEqual(metric["provisional_percent"], 50.0)
        self.assertEqual(metric["tier_unassigned"], 1)
        self.assertEqual(metric["reason"], "BEHAVIOR_TIER_UNASSIGNED")
        self.assertEqual(metric["excluded"], 2)  # e2e and unit
        self.assertEqual(metric["unassessable"], 1)
        self.assertEqual(metric["inventory_revision"], self._inventory_revision())

    def test_e2e_metric_is_blocked_and_provisional(self):
        metric = self.metric("e2e")

        self.assertEqual(metric["state"], "blocked")
        self.assertEqual(metric["numerator"], 1)
        self.assertEqual(metric["denominator"], 1)
        self.assertIsNone(metric["value_percent"])
        self.assertEqual(metric["provisional_percent"], 100.0)
        self.assertEqual(metric["tier_unassigned"], 1)
        self.assertEqual(metric["excluded"], 5)  # five integration-tier cases
        self.assertEqual(metric["unassessable"], 1)

    def test_behavior_statuses_carry_evidence_and_execution(self):
        covered = self.behavior(FEATURE + "/TC-FR1-01")["tiers"]["integration"]
        self.assertEqual(covered["status"], "covered")
        self.assertEqual(covered["evidence_class"], "declared_mapping")
        self.assertEqual(covered["execution_status"], "executed-pass-current")
        self.assertTrue(covered["evidence_ids"])

        failing = self.behavior(FEATURE + "/TC-FR1-02")["tiers"]["integration"]
        self.assertEqual(failing["status"], "covered")
        self.assertEqual(failing["execution_status"], "executed-fail")

        stale = self.behavior(FEATURE + "/TC-FR2-01")["tiers"]["e2e"]
        self.assertEqual(stale["status"], "covered")
        self.assertEqual(stale["execution_status"], "stale")

        manual = self.behavior(FEATURE + "/TC-FR6-01")["tiers"]["integration"]
        self.assertEqual(manual["status"], "unassessable")
        self.assertEqual(manual["reason"], "test case is manual")

        wrong = self.behavior(FEATURE + "/TC-FR3-01")["tiers"]["integration"]
        self.assertEqual(wrong["status"], "gap")
        self.assertEqual(wrong["reason"], "wrong-tier")

        boundary = self.behavior(FEATURE + "/TC-FR4-01")["tiers"]["integration"]
        self.assertEqual(boundary["status"], "gap")
        self.assertEqual(boundary["reason"], "boundary-simulated")

        unassigned = self.behavior(FEATURE + "/TC-FR5-01")["tiers"]["e2e"]
        self.assertEqual(unassigned["status"], "tier-unassigned")
        self.assertEqual(unassigned["reason"], "no tier declared")
        self.assertEqual(unassigned["evidence_class"], "inferred_mapping")
        self.assertTrue(unassigned["candidate_ids"])

    def test_inventory_metadata_reaches_report_and_manifest(self):
        inventory = self.feature()["inventories"][0]
        self.assertEqual(inventory["feature"], FEATURE)
        self.assertEqual(inventory["path"], "docs/testplan.md")
        self.assertEqual(inventory["behavior_count"], 8)

        manifest = support.read_json(
            self.output_dir / FEATURE / "run-manifest.json"
        )
        self.assertEqual(manifest["inventories"], [inventory])

    def test_gaps_have_reasons_priorities_and_actions(self):
        gaps = self.feature()["gaps"]
        by_key = {(gap["behavior"], gap["tier"]): gap for gap in gaps}

        self.assertEqual(by_key[(FEATURE + "/TC-FR3-01", "integration")]["reason"], "wrong-tier")
        self.assertEqual(
            by_key[(FEATURE + "/TC-FR4-01", "integration")]["reason"],
            "boundary-simulated",
        )
        tier_gap = by_key[(FEATURE + "/TC-FR5-01", None)]
        self.assertEqual(tier_gap["reason"], "tier-unassigned")
        self.assertIn("Add a tier for TC-FR5-01", tier_gap["next_action"])
        self.assertIn("behavior_tiers", tier_gap["next_action"])
        self.assertTrue(tier_gap["candidates"])
        unit_gap = by_key[("example.com/mod/pkg/beta", "unit")]
        self.assertEqual(unit_gap["reason"], "low-statement-coverage")
        self.assertEqual(unit_gap["priority"], "high")

        for gap in gaps:
            self.assertTrue(gap["next_action"])
            self.assertEqual(gap["feature"], FEATURE)

        # Gaps are ordered by priority, then tier, then subject.
        priorities = [gap["priority"] for gap in gaps]
        self.assertEqual(priorities[0], "critical")
        rank = {"critical": 0, "high": 1, "medium": 2, "low": 3, "unknown": 4}
        self.assertEqual(priorities, sorted(priorities, key=rank.__getitem__))
        # Exactly one gap for the unassigned behavior, not one per tier.
        self.assertEqual(
            len([gap for gap in gaps if gap["reason"] == "tier-unassigned"]), 1
        )

    def test_readiness_summary(self):
        summary = self.report()["summary"]
        entry = summary["features"][0]

        self.assertEqual(entry["verdict"], "insufficient evidence")
        self.assertEqual(entry["tier_unassigned"], 1)
        self.assertEqual(entry["critical_gaps"], 1)
        self.assertEqual(entry["high_gaps"], 3)
        self.assertEqual(summary["totals"]["critical_gaps"], 1)
        self.assertEqual(summary["totals"]["high_gaps"], 3)
        self.assertEqual(len(summary["top_actions"]), 4)
        self.assertEqual(
            summary["methodology"]["metric_versions"]["integration"],
            "behavior-inventory-v1",
        )
        self.assertFalse(summary["methodology"]["require_execution"])
        self.assertEqual(summary["methodology"]["tier_unassigned"], 1)
        self.assertIn(
            "1 behavior has no tier; Integration/E2E percentages are "
            "provisional.",
            summary["methodology"]["notes"],
        )

    def test_populated_report_and_manifest_match_their_schemas(self):
        report = self.report()
        manifest = support.read_json(
            self.output_dir / FEATURE / "run-manifest.json"
        )
        for name, document in (("report", report), ("manifest", manifest)):
            schema = json.loads(
                (SCHEMA_DIR / "{0}.schema.json".format(name)).read_text(
                    encoding="utf-8"
                )
            )
            self.assertEqual(schema_check.validate(document, schema), [])

    def test_diagnostics_are_reported_once(self):
        diagnostics = self.report()["diagnostics"]
        rendered = [json.dumps(item, sort_keys=True) for item in diagnostics]
        self.assertEqual(len(rendered), len(set(rendered)))

        diagnostics_file = support.read_json(
            self.output_dir / FEATURE / "diagnostics" / "diagnostics.json"
        )
        self.assertEqual(diagnostics_file["diagnostics"], diagnostics)

    def test_denominator_breakdown_is_explicit(self):
        breakdown = self.feature()["denominator_breakdown"]["integration"]
        self.assertEqual(breakdown["denominator"], 4)
        self.assertEqual(breakdown["required_tier"], 4)
        self.assertEqual(breakdown["tier_unassigned"], 1)
        self.assertEqual(breakdown["excluded"], 2)
        self.assertEqual(breakdown["unassessable"], 1)
        self.assertEqual(breakdown["provisional_percent"], 50.0)
        self.assertEqual(self.feature()["tier_unassigned"], 1)
        self.assertEqual(
            self.feature()["behavior_counts"],
            {"unit": 1, "integration": 5, "e2e": 1, "tier_unassigned": 1},
        )
        self.assertEqual(
            self.feature()["unassessable_by_tier"],
            {"unit": 0, "integration": 1, "e2e": 0, "tier_unassigned": 0},
        )
        self.assertEqual(self.feature()["unit_behaviors"], 1)

    def _inventory_revision(self):
        from coverage_report.collectors.behaviors import testplan_revision

        return testplan_revision(TESTPLAN)


class TestMarkdown(GoldenCase):
    def test_readiness_and_coverage_sections_are_present(self):
        markdown = self.markdown()

        self.assertIn("## Readiness summary", markdown)
        self.assertIn("| OSAC-1111 | insufficient evidence |", markdown)
        self.assertIn("## Coverage table", markdown)
        self.assertIn("| Feature | Unit | Integration | E2E |", markdown)
        self.assertIn("## Detail", markdown)
        self.assertIn("## What changed since last run", markdown)
        self.assertIn("execution gating is off", markdown)
        self.assertIn("PENDING-REVIEW", markdown)
        self.assertIn("evidence classes", markdown.lower())
        self.assertIn(
            "1 unit-tier behavior (unit tier uses package coverage",
            markdown,
        )
        self.assertIn(
            "Behaviors: 5 at this tier (4 assessable, 1 unassessable)",
            markdown,
        )

    def test_coverage_table_shows_both_unit_versions(self):
        markdown = self.markdown()

        self.assertIn(
            "33.3% (2/6) [statement-weighted-v2] "
            "(legacy-package-average-v1: 25.0%)",
            markdown,
        )
        self.assertIn(
            "blocked (provisional 50.0% 2/4, 1 tier-unassigned)", markdown
        )
        self.assertIn(
            "blocked (provisional 100.0% 1/1, 1 tier-unassigned)", markdown
        )

    def test_every_coverage_table_number_exists_in_report_json(self):
        report = self.report()
        known = []
        for feature in report["features"]:
            known.extend(feature["tiers"].values())
            known.extend(feature["alternative_metrics"])

        table_lines = [
            line
            for line in self.markdown().splitlines()
            if line.startswith("| OSAC-")
            and (
                "[behavior-inventory-v1]" in line
                or "[statement-weighted-v2]" in line
                or "[legacy-package-average-v1]" in line
            )
        ]
        self.assertTrue(table_lines)
        # Two shapes: "62.5% (5/8)" and "provisional 50.0% 2/4".
        patterns = (
            re.compile(r"(\d+\.\d+)% \((\d+)/(\d+)\)"),
            re.compile(r"provisional (\d+\.\d+)% (\d+)/(\d+)"),
        )

        for line in table_lines:
            matches = [match for pattern in patterns for match in pattern.findall(line)]
            self.assertTrue(matches, line)
            for percent, numerator, denominator in matches:
                self.assertTrue(
                    any(
                        (
                            metric.get("value_percent") == float(percent)
                            or metric.get("provisional_percent") == float(percent)
                        )
                        and metric.get("numerator") == int(numerator)
                        and metric.get("denominator") == int(denominator)
                        for metric in known
                    ),
                    "table value {0}% ({1}/{2}) is not in report.json".format(
                        percent, numerator, denominator
                    ),
                )


class TestDeterminism(GoldenCase):
    def test_rerun_with_pinned_now_is_byte_identical(self):
        first_report = (self.output_dir / FEATURE / "report.json").read_bytes()
        first_markdown = (self.output_dir / FEATURE / "report.md").read_bytes()

        exit_code, _, stderr = self.run_golden()

        self.assertEqual(exit_code, 0, stderr)
        self.assertEqual(
            (self.output_dir / FEATURE / "report.json").read_bytes(), first_report
        )
        self.assertEqual(
            (self.output_dir / FEATURE / "report.md").read_bytes(), first_markdown
        )

    def test_reversed_collector_registration_is_byte_identical(self):
        first = (self.output_dir / FEATURE / "report.json").read_bytes()

        clear()
        for collector in reversed(BUILTIN_COLLECTORS):
            register(collector)
        exit_code, _, stderr = self.run_golden()

        self.assertEqual(exit_code, 0, stderr)
        self.assertEqual(
            (self.output_dir / FEATURE / "report.json").read_bytes(), first
        )

    def test_reversed_junit_argument_order_is_byte_identical(self):
        junit = sorted(
            str(path)
            for path in (
                Path(self.repo, "fulfillment-service/it/reports/pass-fail.xml"),
                Path(self.repo, "tests/e2e/reports/stale.xml"),
            )
        )
        coverprofile = str(Path(self.repo, "unit.out"))
        # A fresh output directory so neither run compares against the golden
        # report produced in setUp.
        explicit_out = str(Path(self.repo, "explicit-out"))
        first_extra = [
            "--junit",
            junit[0],
            "--junit",
            junit[1],
            "--coverprofile",
            coverprofile,
            "--out",
            explicit_out,
        ]
        second_extra = [
            "--junit",
            junit[1],
            "--junit",
            junit[0],
            "--coverprofile",
            coverprofile,
            "--out",
            explicit_out,
        ]

        first_exit, _, first_error = self.run_golden(extra=first_extra)
        first = (Path(explicit_out, FEATURE, "report.json")).read_bytes()
        second_exit, _, second_error = self.run_golden(extra=second_extra)

        self.assertEqual(first_exit, 0, first_error)
        self.assertEqual(second_exit, 0, second_error)
        self.assertEqual(
            (Path(explicit_out, FEATURE, "report.json")).read_bytes(), first
        )


class TestComparison(GoldenCase):
    def test_changed_inventory_revision_is_not_trend_comparable(self):
        previous = Path(self.repo, "previous-report.json")
        previous.write_bytes(
            (self.output_dir / FEATURE / "report.json").read_bytes()
        )

        testplan = Path(self.repo, "docs/testplan.md")
        testplan.write_text(
            testplan.read_text(encoding="utf-8")
            + "\n#### TC-FR8-01: Added later\n\n| Tier | Priority | Automation |\n"
            "|---|---|---|\n| integration | low | automated |\n",
            encoding="utf-8",
        )

        exit_code, _, stderr = self.run_golden(
            extra=["--previous", str(previous)]
        )

        self.assertEqual(exit_code, 0, stderr)
        report = self.report()
        self.assertTrue(report["comparison"]["has_previous"])
        integration = [
            entry
            for entry in report["comparison"]["metrics"]
            if entry["tier"] == "integration"
        ]
        self.assertEqual(len(integration), 1)
        self.assertEqual(integration[0]["trend"], "not-comparable")
        self.assertIn("not trend-comparable", integration[0]["reason"])
        self.assertIn(
            "not trend-comparable",
            self.markdown(),
        )
        self.assertIn(
            "behavior added: OSAC-1111/TC-FR8-01",
            self.markdown(),
        )


TIERED_TESTPLAN = """\
# Testplan — OSAC-1111

## Test Cases

### FR-1: One tiered behavior

#### TC-FR1-01: Only integration

| Tier | Priority | Automation |
|---|---|---|
| integration | high | automated |
"""


MIXED_ASSESSABILITY_TESTPLAN = """\
# Testplan — OSAC-1111

## Test Cases

### FR-1: E2E with manual cases

#### TC-FR1-01: Automated journey

| Tier | Priority | Automation |
|---|---|---|
| e2e | critical | automated |

#### TC-FR1-02: Manual host check

| Tier | Priority | Automation |
|---|---|---|
| e2e | high | manual |

#### TC-FR1-03: Another manual host check

| Tier | Priority | Automation |
|---|---|---|
| e2e | high | manual |
"""


UNASSIGNED_TESTPLAN = """\
# Testplan — OSAC-1111

## Test Cases

### FR-1: No tier anywhere

#### TC-FR1-01: Needs a decision

| Priority | Automation |
|---|---|
| high | automated |
"""


class TestStates(unittest.TestCase):
    def setUp(self):
        clear()
        from coverage_report.collectors.builtin import register_builtins

        register_builtins()
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = Path(self._tmp.name)

    def tearDown(self):
        clear()

    def build(self, testplan):
        write(self.repo, "docs/testplan.md", testplan)
        support.make_repo(
            Path(self.repo), features=FEATURES_YAML, config=CONFIG_YAML
        )
        exit_code, _, stderr = run_main(
            [
                FEATURE,
                "--repo",
                str(self.repo),
                "--now",
                "2026-10-22T00:00:00Z",
            ]
        )
        self.assertEqual(exit_code, 0, stderr)
        return support.read_json(
            Path(self.repo, ".artifacts", "coverage-report", FEATURE, "report.json")
        )

    def test_zero_numerator_is_zero(self):
        # No unassigned behaviors, so the denominator is complete and a zero
        # numerator is a measured zero rather than a provisional percentage.
        report = self.build(TIERED_TESTPLAN)
        metrics = {
            metric["tier"]: metric for metric in report["metrics"]
        }
        self.assertEqual(metrics["integration"]["state"], "zero")
        self.assertEqual(metrics["integration"]["numerator"], 0)
        self.assertEqual(metrics["integration"]["denominator"], 1)
        self.assertEqual(metrics["integration"]["tier_unassigned"], 0)

    def test_missing_coverprofile_is_unavailable_not_zero(self):
        report = self.build(TESTPLAN)
        unit = next(metric for metric in report["metrics"] if metric["tier"] == "unit")
        self.assertEqual(unit["state"], "unavailable")
        self.assertIsNone(unit["value_percent"])

    def test_manual_cases_explain_a_smaller_e2e_denominator(self):
        report = self.build(MIXED_ASSESSABILITY_TESTPLAN)
        feature = report["features"][0]
        e2e = feature["tiers"]["e2e"]

        self.assertEqual(feature["behavior_counts"]["e2e"], 3)
        self.assertEqual(feature["unassessable_by_tier"]["e2e"], 2)
        self.assertEqual(e2e["denominator"], 1)
        self.assertIn(
            "Behaviors: 3 at this tier (1 assessable, 2 unassessable)",
            (
                Path(self.repo, ".artifacts", "coverage-report", FEATURE, "report.md")
            ).read_text(encoding="utf-8"),
        )

    def test_clean_feature_omits_the_tier_note_and_names_the_missing_mapping_file(self):
        report = self.build(TIERED_TESTPLAN)
        notes = report["summary"]["methodology"]["notes"]

        self.assertFalse(any("no tier" in note for note in notes))
        self.assertIn(
            "no reviewed mapping file (config/mappings/OSAC-1111.yaml).", notes
        )

    def test_all_unassigned_blocks_both_tiers_and_emits_one_gap(self):
        report = self.build(UNASSIGNED_TESTPLAN)
        metrics = {
            metric["tier"]: metric for metric in report["metrics"]
        }
        for tier in ("integration", "e2e"):
            self.assertEqual(metrics[tier]["state"], "blocked")
            self.assertEqual(metrics[tier]["numerator"], 0)
            self.assertEqual(metrics[tier]["denominator"], 0)
            self.assertEqual(metrics[tier]["tier_unassigned"], 1)
            self.assertIsNone(metrics[tier]["provisional_percent"])
        self.assertEqual(report["features"][0]["tier_unassigned"], 1)
        tier_gaps = [
            gap for gap in report["gaps"] if gap["reason"] == "tier-unassigned"
        ]
        self.assertEqual(len(tier_gaps), 1)
        self.assertIsNone(tier_gaps[0]["tier"])
        self.assertEqual(
            report["summary"]["features"][0]["verdict"], "insufficient evidence"
        )

    def test_duplicate_inventory_blocks_both_behavior_metrics(self):
        duplicate = TESTPLAN.replace(
            "#### TC-FR1-02: Lists images",
            "#### TC-FR1-01: Duplicated case",
        )
        report = self.build(duplicate)
        metrics = {
            metric["tier"]: metric for metric in report["metrics"]
        }
        for tier in ("integration", "e2e"):
            self.assertEqual(metrics[tier]["state"], "blocked")
        codes = [diagnostic["code"] for diagnostic in report["diagnostics"]]
        self.assertIn("BEHAVIOR_INVENTORY_DUPLICATE", codes)


class TestMarkdownEscaping(unittest.TestCase):
    def test_untrusted_title_cannot_inject_markdown_or_html(self):
        title = "line one\nline two <img src=x> [x](http://e)"

        self.assertEqual(
            _cell(title),
            "line one line two \\<img src=x\\> \\[x\\](http://e)",
        )

    def test_an_existing_escape_cannot_smuggle_a_separator(self):
        self.assertEqual(_cell("a\\|b"), "a\\\\\\|b")

    def test_table_and_emphasis_characters_are_escaped(self):
        self.assertEqual(
            _cell("a|b `code` *em* _under_ !image"),
            "a\\|b \\`code\\` \\*em\\* \\_under\\_ \\!image",
        )


class TestSummaryPolicy(unittest.TestCase):
    def summary(self, *, tier_unassigned=0, behaviors=(), metrics=None):
        feature = {
            "key": "OSAC-1111",
            "tier_unassigned": tier_unassigned,
            "tiers": {
                "unit": {"state": "value"},
                "integration": {"state": "value"},
                "e2e": {"state": "value"},
            },
            "behaviors": list(behaviors),
            "gaps": [],
        }
        feature["tiers"].update(metrics or {})
        return build_summary(
            feature_reports=[feature],
            metric_versions={
                "unit": "statement-weighted-v2",
                "integration": "behavior-inventory-v1",
                "e2e": "behavior-inventory-v1",
            },
            pending_review_mappings=0,
            require_execution=False,
            unit_low_coverage_percent=50,
        )

    def test_missing_test_plan_is_insufficient_evidence(self):
        summary = self.summary(
            metrics={
                "integration": {
                    "state": "na",
                    "reason": "BEHAVIOR_INVENTORY_UNAVAILABLE",
                },
                "e2e": {
                    "state": "na",
                    "reason": "BEHAVIOR_INVENTORY_UNAVAILABLE",
                },
            }
        )

        entry = summary["features"][0]
        self.assertEqual(entry["verdict"], "insufficient evidence")
        self.assertEqual(
            entry["missing_inventory_metrics"], ["e2e", "integration"]
        )

        report = {
            "run_id": "run-0000000000000000",
            "tool_version": "test",
            "generated_at": "2026-10-22T00:00:00Z",
            "repo": {"sha": "a" * 40, "sha_source": "cli"},
            "summary": summary,
            "features": [
                {
                    "key": "OSAC-1111",
                    "name": "one",
                    "tiers": {
                        "unit": {"state": "value"},
                        "integration": {
                            "state": "na",
                            "reason": "BEHAVIOR_INVENTORY_UNAVAILABLE",
                        },
                        "e2e": {
                            "state": "na",
                            "reason": "BEHAVIOR_INVENTORY_UNAVAILABLE",
                        },
                    },
                    "alternative_metrics": [],
                    "inventories": [],
                    "denominator_breakdown": {
                        "unit": {},
                        "integration": {},
                        "e2e": {},
                    },
                    "behaviors": [],
                    "gaps": [],
                    "behavior_counts": {},
                    "unassessable_by_tier": {},
                    "unit_behaviors": 0,
                }
            ],
            "evidence": [],
            "diagnostics": [],
            "inventories": [],
            "gaps": [],
            "behaviors": [],
            "comparison": {"has_previous": False},
        }
        self.assertIn("integration: no test plan", render_markdown(report))

    def test_empty_tier_denominator_stays_ready_eligible(self):
        summary = self.summary(
            metrics={
                "integration": {
                    "state": "na",
                    "reason": "BEHAVIOR_METRIC_NO_DENOMINATOR",
                },
                "e2e": {
                    "state": "na",
                    "reason": "BEHAVIOR_METRIC_NO_DENOMINATOR",
                },
            }
        )

        entry = summary["features"][0]
        self.assertEqual(entry["verdict"], "ready")
        self.assertEqual(entry["missing_inventory_metrics"], [])

    def test_unassigned_behaviors_make_the_verdict_insufficient_evidence(self):
        summary = self.summary(tier_unassigned=2)

        self.assertEqual(
            summary["features"][0]["verdict"], "insufficient evidence"
        )
        self.assertEqual(summary["methodology"]["tier_unassigned"], 2)
        self.assertIn(
            "2 behaviors have no tier; Integration/E2E percentages are "
            "provisional.",
            summary["methodology"]["notes"],
        )

    def test_unreviewed_tier_overrides_are_counted(self):
        summary = self.summary(
            behaviors=(
                {"tier_origin": "mapping-config", "tier_reviewed": False},
                {"tier_origin": "mapping-config", "tier_reviewed": True},
                {"tier_origin": "testplan", "tier_reviewed": True},
            )
        )

        self.assertEqual(
            summary["methodology"]["unreviewed_tier_overrides"], 1
        )


if __name__ == "__main__":
    unittest.main()
