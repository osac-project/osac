"""Schema conformance tests for generated reports, manifests, and records."""

import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path

import yaml

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.cli import main
from coverage_report.collectors import clear

import schema_check
import support

SCHEMA_DIR = Path(__file__).resolve().parent.parent / "schemas"
REPO_ROOT_CONFIG = Path(__file__).resolve().parents[3] / ".coverage-skill.yaml"


def load_schema(name):
    with open(str(SCHEMA_DIR / name), "r", encoding="utf-8") as handle:
        return json.load(handle)


class TestSchemaFiles(unittest.TestCase):
    def test_every_schema_is_draft_2020_12_with_a_versioned_id(self):
        names = sorted(path.name for path in SCHEMA_DIR.glob("*.schema.json"))
        self.assertEqual(
            names,
            [
                "config.schema.json",
                "diagnostic.schema.json",
                "evidence.schema.json",
                "manifest.schema.json",
                "metric.schema.json",
                "report.schema.json",
            ],
        )
        for name in names:
            schema = load_schema(name)
            self.assertEqual(
                schema["$schema"], "https://json-schema.org/draft/2020-12/schema", name
            )
            self.assertIn("/v1/", schema["$id"], name)

    def test_shipped_repository_config_matches_the_config_schema(self):
        with open(str(REPO_ROOT_CONFIG), "r", encoding="utf-8") as handle:
            config = yaml.safe_load(handle)

        errors = schema_check.validate(config, load_schema("config.schema.json"))
        self.assertEqual(errors, [])


class TestGeneratedDocumentsValidate(unittest.TestCase):
    def setUp(self):
        clear()
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = support.make_repo(Path(self._tmp.name))
        with contextlib.redirect_stdout(io.StringIO()):
            exit_code = main(
                [
                    "--repo",
                    str(self.repo),
                    "--now",
                    "2026-10-22T00:00:00Z",
                ]
            )
        self.assertEqual(exit_code, 0)
        self.target = Path(
            self.repo, ".artifacts", "coverage-report", "ALL"
        )

    def tearDown(self):
        clear()

    def test_report_matches_report_schema(self):
        report = support.read_json(self.target / "report.json")

        errors = schema_check.validate(report, load_schema("report.schema.json"))
        self.assertEqual(errors, [])

    def test_manifest_matches_manifest_schema(self):
        manifest = support.read_json(self.target / "run-manifest.json")

        errors = schema_check.validate(manifest, load_schema("manifest.schema.json"))
        self.assertEqual(errors, [])

    def test_each_metric_matches_metric_schema(self):
        report = support.read_json(self.target / "report.json")
        schema = load_schema("metric.schema.json")

        self.assertTrue(report["metrics"])
        for metric in report["metrics"]:
            self.assertEqual(schema_check.validate(metric, schema), [], metric)

    def test_each_diagnostic_matches_diagnostic_schema(self):
        report = support.read_json(self.target / "report.json")
        schema = load_schema("diagnostic.schema.json")

        self.assertTrue(report["diagnostics"])
        for diagnostic in report["diagnostics"]:
            self.assertEqual(
                schema_check.validate(diagnostic, schema), [], diagnostic
            )

    def test_each_evidence_record_matches_evidence_schema(self):
        report = support.read_json(self.target / "report.json")
        schema = load_schema("evidence.schema.json")

        for record in report["evidence"]:
            self.assertEqual(schema_check.validate(record, schema), [], record)


class TestCheckerRejectsInvalidDocuments(unittest.TestCase):
    def test_missing_required_property_is_reported(self):
        schema = load_schema("metric.schema.json")
        instance = {
            "feature": "OSAC-2540",
            "tier": "unit",
            "metric_version": "statement-weighted-v2",
            "state": "unavailable",
        }

        errors = schema_check.validate(instance, schema)

        self.assertTrue(any("missing required property" in error for error in errors))

    def test_bad_enum_is_reported(self):
        schema = load_schema("diagnostic.schema.json")
        instance = {
            "run_id": "run-0000000000000000",
            "collector": "engine",
            "operation": "compute_metrics",
            "code": "OK",
            "message": "m",
            "severity": "fatal",
            "recovery": "",
            "duration_ms": 0,
        }

        errors = schema_check.validate(instance, schema)

        self.assertTrue(any("not one of" in error for error in errors))

    def test_unexpected_property_is_reported(self):
        schema = load_schema("config.schema.json")
        instance = {"version": 1, "surprise": True}

        errors = schema_check.validate(instance, schema)

        self.assertTrue(any("unexpected property" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
