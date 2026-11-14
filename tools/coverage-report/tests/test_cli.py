"""CLI tests: unknown features, output paths, and deterministic reruns."""

import io
import json
import os
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from coverage_report.cli import main  # noqa: E402

FEATURES = """\
features:
  - key: OSAC-1
    name: demo
    aliases:
      - dm
    testplans: [plan.md]
    unit_packages: []
  - key: OSAC-2
    name: second
    testplans: [plan.md]
    unit_packages: []
"""

PLAN = "#### TC-FR1-01: A case\n\n| Interface Change | Priority | Automation |\n|---|---|---|\n| IC-1 | high | automated |\n"


class CliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        config = os.path.join(self.root, "tools", "coverage-report")
        os.makedirs(config)
        with open(os.path.join(config, "features.yaml"), "w", encoding="utf-8") as handle:
            handle.write(FEATURES)
        with open(os.path.join(self.root, "plan.md"), "w", encoding="utf-8") as handle:
            handle.write(PLAN)
        self.out = os.path.join(self.root, "out")

    def tearDown(self):
        self.tmp.cleanup()

    def run_cli(self, argv):
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = main(argv + ["--repo", self.root, "--out", self.out])
        return code, stdout.getvalue(), stderr.getvalue()

    def write_configs(self):
        directory = os.path.join(self.root, "config")
        os.makedirs(directory)
        for name, group, key in (("core.yaml", "core", "OSAC-1"), ("vmaas.yaml", "vmaas", "OSAC-3")):
            with open(os.path.join(directory, name), "w", encoding="utf-8") as handle:
                handle.write("group: {0}\nfeatures:\n  - key: {1}\n    name: {1}\n"
                             "    testplans: []\n    unit_packages: []\n".format(group, key))
        return directory

    def test_features_directory_merges_and_lists_groups(self):
        code, _stdout, _stderr = self.run_cli(["--features", self.write_configs()])
        self.assertEqual(code, 0)
        with open(os.path.join(self.out, "ALL", "report.json"), encoding="utf-8") as handle:
            report = json.load(handle)
        self.assertEqual([row["key"] for row in report["groups"]], ["core", "vmaas"])
        self.assertEqual([item["feature"]["key"] for item in report["features"]], ["OSAC-1", "OSAC-3"])

    def test_duplicate_area_key_across_files_exits_two(self):
        directory = os.path.join(self.root, "dupes")
        os.makedirs(directory)
        for name in ("a.yaml", "b.yaml"):
            with open(os.path.join(directory, name), "w", encoding="utf-8") as handle:
                handle.write("features:\n  - key: OSAC-1\n    testplans: []\n    unit_packages: []\n")
        code, _stdout, stderr = self.run_cli(["--features", directory])
        self.assertEqual(code, 2)
        self.assertIn("duplicate", stderr)

    def test_group_filter(self):
        code, _stdout, _stderr = self.run_cli(["--features", self.write_configs(), "--group", "vmaas"])
        self.assertEqual(code, 0)
        with open(os.path.join(self.out, "vmaas", "report.json"), encoding="utf-8") as handle:
            report = json.load(handle)
        self.assertEqual([item["feature"]["key"] for item in report["features"]], ["OSAC-3"])
        self.assertEqual(report["groups"], [{"key": "vmaas"}])

    def test_unknown_feature_exits_two_with_suggestions(self):
        code, _stdout, stderr = self.run_cli(["nope"])
        self.assertEqual(code, 2)
        self.assertIn("OSAC-1", stderr)

    def test_alias_writes_both_reports(self):
        code, stdout, _stderr = self.run_cli(["dm"])
        self.assertEqual(code, 0)
        json_path = os.path.join(self.out, "OSAC-1", "report.json")
        markdown_path = os.path.join(self.out, "OSAC-1", "report.md")
        self.assertTrue(os.path.isfile(json_path))
        self.assertTrue(os.path.isfile(markdown_path))
        self.assertEqual(stdout.split(), [json_path, markdown_path])

    def test_rerun_is_byte_identical(self):
        self.run_cli(["dm"])
        json_path = os.path.join(self.out, "OSAC-1", "report.json")
        with open(json_path, "rb") as handle:
            first = handle.read()
        self.run_cli(["dm"])
        with open(json_path, "rb") as handle:
            self.assertEqual(first, handle.read())

    def test_failed_markdown_publish_restores_report_json(self):
        self.run_cli(["dm"])
        directory = os.path.join(self.out, "OSAC-1")
        json_path = os.path.join(directory, "report.json")
        previous = b'{"previous": true}\n'
        with open(os.path.join(directory, "report.json"), "wb") as handle:
            handle.write(previous)
        real_replace = os.replace
        calls = []

        def failing_replace(source, target):
            calls.append(target)
            if len(calls) == 2:
                raise OSError("disk full")
            return real_replace(source, target)

        with mock.patch("coverage_report.cli.os.replace", side_effect=failing_replace):
            code, _stdout, stderr = self.run_cli(["dm"])
        self.assertEqual(code, 2)
        self.assertIn("cannot write report", stderr)
        with open(json_path, "rb") as handle:
            self.assertEqual(previous, handle.read())
        self.assertFalse(os.path.exists(os.path.join(directory, "report.md.tmp")))

    def test_all_features_writes_all_directory(self):
        code, _stdout, _stderr = self.run_cli([])
        self.assertEqual(code, 0)
        with open(os.path.join(self.out, "ALL", "report.json"), encoding="utf-8") as handle:
            report = json.load(handle)
        self.assertEqual([item["feature"]["key"] for item in report["features"]], ["OSAC-1", "OSAC-2"])
        self.assertEqual([row["area"] for row in report["overview"]], ["demo", "second"])
        first = report["overview"][0]
        self.assertEqual((first["key"], first["group"]), ("OSAC-1", ""))
        self.assertEqual(first["unit"], {"state": "unavailable", "display": "unavailable"})
        self.assertEqual(first["gaps"], {"critical": 0, "high": 1, "total": 1})
        self.assertEqual((first["verdict"], first["verdict_reason"]), ("not ready", "1 high gap"))
        with open(os.path.join(self.out, "ALL", "report.md"), encoding="utf-8") as handle:
            self.assertTrue(handle.read().startswith("# Coverage overview"))


if __name__ == "__main__":
    unittest.main()
