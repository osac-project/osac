"""End-to-end CLI tests against throwaway git repositories."""

import contextlib
import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.cli import main
from coverage_report.collectors import clear, register
from coverage_report.model import (
    CollectorBatch,
    EvidenceClass,
    EvidenceRecord,
    Metric,
    MetricState,
    Tier,
    make_evidence_id,
)

import support


def run_main(argv):
    stdout = io.StringIO()
    stderr = io.StringIO()
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        exit_code = main(argv)
    return exit_code, stdout.getvalue(), stderr.getvalue()


class CliTestCase(unittest.TestCase):
    def setUp(self):
        clear()
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = support.make_repo(Path(self._tmp.name))
        self.output_dir = Path(self.repo, ".artifacts", "coverage-report")

    def tearDown(self):
        clear()

    def run_feature(self, feature="OSAC-2540", extra=None):
        argv = [] if feature is None else [feature]
        argv.extend(["--repo", str(self.repo), "--now", "2026-10-22T00:00:00Z"])
        argv.extend(extra or [])
        return run_main(argv)

    def read_report(self, name="OSAC-2540"):
        return support.read_json(Path(self.output_dir, name, "report.json"))

    def read_manifest(self, name="OSAC-2540"):
        return support.read_json(Path(self.output_dir, name, "run-manifest.json"))


class TestNoCollectorRun(CliTestCase):
    def test_missing_evidence_is_never_zero(self):
        exit_code, stdout, stderr = self.run_feature()

        self.assertEqual(exit_code, 0, stderr)
        report = self.read_report()
        self.assertEqual(len(report["metrics"]), 3)
        by_tier = {metric["tier"]: metric for metric in report["metrics"]}
        # No unit collector produced evidence: unavailable, not zero.
        self.assertEqual(by_tier["unit"]["state"], "unavailable")
        # No behavior inventory was loaded: not applicable, not zero.
        for tier in ("integration", "e2e"):
            self.assertEqual(by_tier[tier]["state"], "na")
            self.assertIsNone(by_tier[tier]["value_percent"])
        for metric in report["metrics"]:
            self.assertIsNone(metric["numerator"])
            self.assertIsNone(metric["denominator"])

        codes = [diagnostic["code"] for diagnostic in report["diagnostics"]]
        self.assertEqual(codes.count("NO_COLLECTOR_EVIDENCE"), 1)
        self.assertEqual(codes.count("BEHAVIOR_INVENTORY_UNAVAILABLE"), 2)

    def test_success_prints_output_paths(self):
        exit_code, stdout, _ = self.run_feature()

        self.assertEqual(exit_code, 0)
        for label in ("report:", "markdown:", "manifest:", "diagnostics:"):
            self.assertIn(label, stdout)

    def test_output_layout_is_complete(self):
        self.run_feature()
        target = Path(self.output_dir, "OSAC-2540")

        for relative in (
            "report.json",
            "report.md",
            "run-manifest.json",
            "diagnostics/diagnostics.json",
            "inputs/digests.json",
        ):
            self.assertTrue(Path(target, relative).is_file(), relative)

    def test_report_markdown_shows_states(self):
        self.run_feature()
        markdown = Path(self.output_dir, "OSAC-2540", "report.md").read_text(
            encoding="utf-8"
        )

        self.assertIn("| Feature | Unit | Integration | E2E |", markdown)
        self.assertIn("OSAC-2540 (disk-image)", markdown)
        self.assertIn("unavailable", markdown)

    def test_full_plan_writes_ALL_directory(self):
        exit_code, _, stderr = self.run_feature(feature=None)

        self.assertEqual(exit_code, 0, stderr)
        report = self.read_report("ALL")
        self.assertEqual(
            [feature["key"] for feature in report["features"]],
            ["OSAC-2540", "OSAC-3000"],
        )
        self.assertEqual(report["scope"]["matched_by"], "full_plan")


class TestDeterminism(CliTestCase):
    def test_two_runs_with_pinned_now_are_byte_identical(self):
        first_exit, _, first_error = self.run_feature()
        first_report = Path(self.output_dir, "OSAC-2540", "report.json").read_bytes()
        first_manifest = Path(
            self.output_dir, "OSAC-2540", "run-manifest.json"
        ).read_bytes()

        second_exit, _, second_error = self.run_feature()
        second_report = Path(self.output_dir, "OSAC-2540", "report.json").read_bytes()
        second_manifest = Path(
            self.output_dir, "OSAC-2540", "run-manifest.json"
        ).read_bytes()

        self.assertEqual(first_exit, 0, first_error)
        self.assertEqual(second_exit, 0, second_error)
        self.assertEqual(first_report, second_report)
        self.assertEqual(first_manifest, second_manifest)

    def test_manifest_records_origins_sha_and_digests(self):
        self.run_feature()
        manifest = self.read_manifest()

        self.assertEqual(manifest["repo"]["sha"], support.head(self.repo))
        self.assertEqual(manifest["repo"]["sha_source"], "git")
        self.assertEqual(manifest["config_origins"]["output_dir"], "repo")
        self.assertEqual(manifest["feature_resolution"]["matched_by"], "key")
        self.assertEqual(manifest["generated_at"], "2026-10-22T00:00:00Z")
        kinds = {entry["kind"] for entry in manifest["input_digests"]}
        self.assertEqual(kinds, {"config", "features", "mapping"})
        self.assertRegex(manifest["features_file"]["sha256"], r"^[0-9a-f]{64}$")

    def test_run_id_changes_with_sha(self):
        self.run_feature()
        first_run_id = self.read_report()["run_id"]
        second_exit, _, _ = self.run_feature(extra=["--sha", "deadbeef"])
        second_run_id = support.read_json(
            Path(self.output_dir, "OSAC-2540", "report.json")
        )["run_id"]

        self.assertEqual(second_exit, 0)
        self.assertNotEqual(first_run_id, second_run_id)
        self.assertEqual(
            support.read_json(
                Path(self.output_dir, "OSAC-2540", "run-manifest.json")
            )["repo"]["sha_source"],
            "cli",
        )

    def test_entry_script_runs_as_a_subprocess(self):
        script = Path(__file__).resolve().parent.parent / "coverage-report"
        completed = subprocess.run(
            [
                str(script),
                "OSAC-2540",
                "--repo",
                str(self.repo),
                "--now",
                "2026-10-22T00:00:00Z",
            ],
            capture_output=True,
            text=True,
        )

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("report:", completed.stdout)


class TestRunIdInputs(CliTestCase):
    def _configured_profile(self):
        Path(self.repo, ".coverage-skill.yaml").write_text(
            "version: 1\n"
            "features_file: config/features.yaml\n"
            "enhancement_roots: []\n"
            "artifacts:\n"
            "  coverprofiles:\n"
            "    - unit.out\n"
            "  junit: []\n"
            "output_dir: .artifacts/coverage-report\n",
            encoding="utf-8",
        )
        profile = Path(self.repo, "unit.out")
        profile.write_text(
            "mode: set\nexample.com/mod/a.go:1.1,2.2 1 1\n", encoding="utf-8"
        )
        return profile

    def test_editing_a_discovered_artifact_changes_run_id_and_comparison(self):
        profile = self._configured_profile()
        exit_code, _, stderr = self.run_feature()
        self.assertEqual(exit_code, 0, stderr)
        first = self.read_report()
        kinds = {entry["kind"] for entry in self.read_manifest()["input_digests"]}
        self.assertIn("coverprofile", kinds)

        profile.write_text(
            "mode: set\nexample.com/mod/a.go:1.1,2.2 4 0\n", encoding="utf-8"
        )
        second_exit, _, second_error = self.run_feature()

        self.assertEqual(second_exit, 0, second_error)
        second = self.read_report()
        self.assertNotEqual(second["run_id"], first["run_id"])
        self.assertTrue(second["comparison"]["has_previous"])

    def test_editing_the_testplan_changes_run_id_and_comparison(self):
        testplan = Path(self.repo, "docs", "testplan.md")
        testplan.parent.mkdir(parents=True, exist_ok=True)
        testplan.write_text(
            "# Testplan\n\n## Test Cases\n\n### FR-1: One\n\n"
            "#### TC-FR1-01: First\n\n"
            "| Tier | Priority | Automation |\n|---|---|---|\n"
            "| integration | high | automated |\n",
            encoding="utf-8",
        )
        exit_code, _, stderr = self.run_feature()
        self.assertEqual(exit_code, 0, stderr)
        first = self.read_report()
        kinds = {entry["kind"] for entry in self.read_manifest()["input_digests"]}
        self.assertIn("testplan", kinds)

        testplan.write_text(
            testplan.read_text(encoding="utf-8").replace("high", "critical"),
            encoding="utf-8",
        )
        second_exit, _, second_error = self.run_feature()

        self.assertEqual(second_exit, 0, second_error)
        second = self.read_report()
        self.assertNotEqual(second["run_id"], first["run_id"])
        self.assertTrue(second["comparison"]["has_previous"])

    def test_absent_testplan_adds_no_digest_and_keeps_the_run_id_stable(self):
        exit_code, _, stderr = self.run_feature()
        self.assertEqual(exit_code, 0, stderr)
        first = self.read_report()
        kinds = {entry["kind"] for entry in self.read_manifest()["input_digests"]}
        self.assertNotIn("testplan", kinds)

        second_exit, _, second_error = self.run_feature()

        self.assertEqual(second_exit, 0, second_error)
        self.assertEqual(self.read_report()["run_id"], first["run_id"])


class TestErrorExitCodes(CliTestCase):
    def test_unknown_feature_exits_2_with_candidates(self):
        exit_code, _, stderr = self.run_feature(feature="disk-imag")

        self.assertEqual(exit_code, 2)
        self.assertIn("SCOPE_UNRESOLVED", stderr)
        self.assertIn("candidates:", stderr)
        self.assertIn("disk-image", stderr)
        self.assertIn("recovery:", stderr)
        self.assertIn("run_id: run-", stderr)

    def test_invalid_config_exits_3(self):
        Path(self.repo, ".coverage-skill.yaml").write_text(
            "version: 1\nenhancement_roots: nope\n", encoding="utf-8"
        )

        exit_code, _, stderr = self.run_feature()

        self.assertEqual(exit_code, 3)
        self.assertIn("CONFIG_INVALID", stderr)
        self.assertIn("enhancement_roots", stderr)

    def test_missing_explicit_input_exits_3(self):
        exit_code, _, stderr = self.run_feature(
            extra=["--coverprofile", "does-not-exist.out"]
        )

        self.assertEqual(exit_code, 3)
        self.assertIn("INPUT_MISSING", stderr)

    def test_missing_repository_exits_1(self):
        exit_code, _, stderr = run_main(
            ["OSAC-2540", "--repo", "/nonexistent/repo", "--now", "2026-10-22T00:00:00Z"]
        )

        self.assertEqual(exit_code, 1)
        self.assertIn("REPO_NOT_FOUND", stderr)


class TestCollectors(CliTestCase):
    def _collector(self):
        record_id = make_evidence_id(
            "fake", "statement", "fulfillment-service/pkg", "cov.out"
        )

        class FakeCollector:
            name = "fake"

            def collect(self, ctx):
                return CollectorBatch(
                    collector="fake",
                    records=(
                        EvidenceRecord(
                            id=record_id,
                            evidence_class=EvidenceClass.MEASURED,
                            tier=Tier.UNIT,
                            feature="OSAC-2540",
                            subject="fulfillment-service/pkg",
                            source="cov.out",
                            collector="fake",
                            kind="statement",
                        ),
                    ),
                    metrics=(
                        Metric(
                            feature="OSAC-2540",
                            tier=Tier.UNIT,
                            metric_version="statement-weighted-v2",
                            state=MetricState.VALUE,
                            numerator=8,
                            denominator=10,
                            value_percent=80.0,
                            inputs=(record_id,),
                            formula="covered / total statements",
                            inventory_revision=ctx.sha,
                        ),
                    ),
                )

        return FakeCollector()

    def test_evidence_and_metric_reach_the_report(self):
        register(self._collector())

        exit_code, _, stderr = self.run_feature()

        self.assertEqual(exit_code, 0, stderr)
        report = self.read_report()
        unit = next(
            metric for metric in report["metrics"] if metric["tier"] == "unit"
        )
        self.assertEqual(unit["state"], "value")
        self.assertEqual(unit["value_percent"], 80.0)
        self.assertEqual(len(report["evidence"]), 1)
        self.assertEqual(report["evidence"][0]["evidence_class"], "measured")
        self.assertEqual(self.read_manifest()["collectors"][0]["status"], "ok")

    def test_collector_failure_is_isolated(self):
        class BrokenCollector:
            name = "broken"

            def collect(self, ctx):
                raise RuntimeError("unreadable coverprofile")

        register(BrokenCollector())

        exit_code, _, stderr = self.run_feature()

        self.assertEqual(exit_code, 0, stderr)
        report = self.read_report()
        by_tier = {metric["tier"]: metric for metric in report["metrics"]}
        self.assertEqual(by_tier["unit"]["state"], "unavailable")
        self.assertEqual(by_tier["integration"]["state"], "na")
        self.assertEqual(by_tier["e2e"]["state"], "na")
        failed = [
            diagnostic
            for diagnostic in report["diagnostics"]
            if diagnostic["code"] == "COLLECTOR_FAILED"
        ]
        self.assertEqual(len(failed), 1)
        self.assertIn("unreadable coverprofile", failed[0]["message"])
        self.assertEqual(self.read_manifest()["collectors"][0]["status"], "error")

    def test_out_of_scope_evidence_fails_the_collector(self):
        class WrongScopeCollector:
            name = "wrong-scope"

            def collect(self, ctx):
                return CollectorBatch(
                    collector="wrong-scope",
                    records=(
                        EvidenceRecord(
                            id=make_evidence_id("wrong-scope", "k", "s", "src"),
                            evidence_class=EvidenceClass.MEASURED,
                            tier=Tier.UNIT,
                            feature="OSAC-9999",
                            subject="s",
                            source="src",
                            collector="wrong-scope",
                            kind="k",
                        ),
                    ),
                )

        register(WrongScopeCollector())

        exit_code, _, stderr = self.run_feature()

        self.assertEqual(exit_code, 0, stderr)
        self.assertEqual(self.read_manifest()["collectors"][0]["status"], "error")
        self.assertEqual(self.read_report()["evidence"], [])


class TestInputsAndComparison(CliTestCase):
    def test_explicit_input_is_copied_and_digested(self):
        coverprofile = Path(self.repo, "unit.out")
        coverprofile.write_text("mode: set\n", encoding="utf-8")

        exit_code, _, stderr = self.run_feature(
            extra=["--coverprofile", str(coverprofile)]
        )

        self.assertEqual(exit_code, 0, stderr)
        target = Path(self.output_dir, "OSAC-2540")
        self.assertEqual(
            Path(target, "inputs", "coverprofile", "unit.out").read_text(
                encoding="utf-8"
            ),
            "mode: set\n",
        )
        manifest = self.read_manifest()
        kinds = [entry["kind"] for entry in manifest["input_digests"]]
        self.assertIn("coverprofile", kinds)
        digests = support.read_json(Path(target, "inputs", "digests.json"))
        self.assertIn(
            "coverprofile",
            [entry["kind"] for entry in digests["inputs"]],
        )

    def test_previous_report_comparison_is_recorded(self):
        self.run_feature()
        previous = Path(self.output_dir, "OSAC-2540", "report.json")
        previous_run_id = support.read_json(previous)["run_id"]

        second_exit, _, stderr = self.run_feature(extra=["--previous", str(previous)])

        self.assertEqual(second_exit, 0, stderr)
        report = self.read_report()
        self.assertEqual(report["comparison"]["previous_run_id"], previous_run_id)
        self.assertIn("previous_report", self.read_manifest())

    def test_invalid_previous_report_exits_3(self):
        bad = Path(self.repo, "bad.json")
        bad.write_text(json.dumps({"not": "a report"}), encoding="utf-8")

        exit_code, _, stderr = self.run_feature(extra=["--previous", str(bad)])

        self.assertEqual(exit_code, 3)
        self.assertIn("PREVIOUS_REPORT_INVALID", stderr)


if __name__ == "__main__":
    unittest.main()
