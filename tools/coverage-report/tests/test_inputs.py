"""Input parsing tests: coverprofiles and test plans."""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from coverage_report.inputs import (  # noqa: E402
    E2E,
    INTEGRATION,
    NONE,
    UNIT,
    InputError,
    execution_matrix,
    load_features,
    normalize_tier,
    parse_coverprofiles,
    parse_testplan,
    testplan_path,
    candidate_test_paths,
)


class CoverprofileTests(unittest.TestCase):
    def write(self, directory, name, text):
        path = os.path.join(directory, name)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(text)
        return path

    def test_parses_and_merges_profiles_keeping_max_count(self):
        with tempfile.TemporaryDirectory() as directory:
            first = self.write(
                directory, "a.out",
                "mode: set\n"
                "example.com/pkg/a.go:1.1,2.2 3 0\n"
                "example.com/pkg/a.go:3.1,4.2 2 1\n",
            )
            second = self.write(
                directory, "b.out",
                "mode: set\n"
                "example.com/pkg/a.go:1.1,2.2 3 1\n"
                "example.com/other/b.go:1.1,2.2 5 0\n",
            )
            packages = parse_coverprofiles([first, second])

        self.assertEqual(packages["example.com/pkg"].statements, 5)
        self.assertEqual(packages["example.com/pkg"].covered, 5)
        self.assertEqual(packages["example.com/other"].statements, 5)
        self.assertEqual(packages["example.com/other"].covered, 0)

    def test_rejects_missing_mode_line(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.write(directory, "bad.out", "example.com/pkg/a.go:1.1,2.2 1 1\n")
            with self.assertRaises(InputError):
                parse_coverprofiles([path])

    def test_rejects_malformed_line(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.write(directory, "bad.out", "mode: set\nexample.com/pkg/a.go 1\n")
            with self.assertRaises(InputError):
                parse_coverprofiles([path])


class FeatureConfigTests(unittest.TestCase):
    def rejects(self, text):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "features.yaml")
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(text)
            with self.assertRaises(InputError):
                load_features(path)

    def test_rejects_duplicate_keys(self):
        self.rejects("features:\n  - key: OSAC-1\n    key: OSAC-2\n    testplans: [plan.md]\n")

    def test_rejects_scalar_unit_packages_and_aliases(self):
        for body in ("unit_packages: pkg/x", "unit_packages: 5", "aliases: alias"):
            self.rejects("features:\n  - key: OSAC-1\n    {0}\n".format(body))

    def test_rejects_incomplete_inline_behaviors(self):
        for behavior in ("id: B1", "title: Title"):
            self.rejects(
                "features:\n  - key: OSAC-1\n    behaviors:\n      - {0}\n".format(behavior)
            )

    def test_invalid_utf8_config_is_an_input_error(self):
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "features.yaml")
            with open(path, "wb") as handle:
                handle.write(b"features:\n  - key: OSAC-1\n    name: \xff\xfe\n")
            with self.assertRaises(InputError):
                load_features(path)


class TestplanPathTests(unittest.TestCase):
    def test_containment(self):
        with tempfile.TemporaryDirectory() as directory:
            def project(name):
                os.makedirs(os.path.join(directory, name))
                with open(os.path.join(directory, name, "tp.md"), "w", encoding="utf-8") as handle:
                    handle.write("")
                return os.path.join(directory, name)

            repo, external, elsewhere = project("repo"), project("external"), project("elsewhere")
            self.assertEqual(testplan_path(repo, "tp.md")[1], "")
            os.symlink(external, os.path.join(repo, "enhancement-proposals"))
            self.assertEqual(testplan_path(repo, "enhancement-proposals/tp.md")[1],
                             os.path.realpath(external))
            os.symlink(elsewhere, os.path.join(repo, "plans"))
            self.assertRaises(InputError, testplan_path, repo, "plans/tp.md")
            self.assertEqual(testplan_path(repo, "plans/tp.md", [elsewhere])[1],
                             os.path.realpath(elsewhere))
            for bad in ("../tp.md", os.path.join(elsewhere, "tp.md")):
                self.assertRaises(InputError, testplan_path, repo, bad)
            self.assertEqual(testplan_path(repo, "missing.md")[1], "")


class TierTests(unittest.TestCase):
    def test_vocabulary(self):
        self.assertEqual(normalize_tier("Unit / [DEV]"), UNIT)
        self.assertEqual(normalize_tier("Unit / [DEV] plus catalog integration"), UNIT)
        self.assertEqual(normalize_tier("E2E / [QE]"), E2E)
        self.assertEqual(normalize_tier("Component integration / [DEV]"), INTEGRATION)
        self.assertEqual(normalize_tier("Contract / [DEV]"), INTEGRATION)
        self.assertEqual(normalize_tier("UI integration / [UI]"), INTEGRATION)
        self.assertEqual(normalize_tier("Contract and component integration"), INTEGRATION)
        self.assertEqual(normalize_tier("Story 8.05"), NONE)
        self.assertEqual(normalize_tier(""), NONE)


class TestplanTests(unittest.TestCase):
    def parse(self, text):
        return parse_testplan(text)

    def test_reads_tier_table_manual_and_multiline_execution(self):
        behaviors = self.parse(
            "# Testplan\n\n"
            "### FR-1: Journeys\n\n"
            "#### TC-FR1-01: Complete journey\n\n"
            "| Interface Change | Priority | Automation |\n"
            "|---|---|---|\n"
            "| IC-3 | critical | automated |\n\n"
            "##### Preconditions\n\n"
            "- **Tier / owner:** E2E / [QE].\n"
            "- **Execution:** Kind suite rebuilds the existing\n"
            "  `fulfillment-service/it/it_test.go` client pattern.\n"
            "- **Boundary:** Real stack.\n\n"
            "#### TC-FR1-02: Manual host check\n\n"
            "| Interface Change | Priority | Automation |\n"
            "|---|---|---|\n"
            "| IC-4 | medium | manual |\n\n"
            "- **Tier / owner:** E2E / [QE].\n"
        )
        self.assertEqual(len(behaviors), 2)
        first, second = behaviors
        self.assertEqual(first.tc_id, "TC-FR1-01")
        self.assertEqual(first.priority, "critical")
        self.assertEqual(first.tier, E2E)
        self.assertIn("fulfillment-service/it/it_test.go", first.execution)
        self.assertEqual(first.excluded_reason, "")
        self.assertEqual(second.excluded_reason, "test case is manual")

    def test_reads_four_column_table_without_tier(self):
        behaviors = self.parse(
            "#### TC-FR1-01: Create DiskImage\n\n"
            "| Story | AC | Priority | Automation |\n"
            "|---|---|---|---|\n"
            "| OSAC-3718 | AC-1 | high | automated |\n"
        )
        self.assertEqual(len(behaviors), 1)
        self.assertEqual(behaviors[0].priority, "high")
        self.assertEqual(behaviors[0].tier, NONE)
        self.assertEqual(behaviors[0].excluded_reason, "")

    def test_empty_text_has_no_behaviors(self):
        self.assertEqual(parse_testplan(""), [])

    def test_envtest_tier_counts_as_integration(self):
        behaviors = self.parse("#### TC-1.1: Env\n\n- **Tier / owner:** Envtest / [DEV].\n")
        self.assertEqual(behaviors[0].tier, INTEGRATION)

    def test_tier_bullet_preferred_and_legacy_tier_owner_still_read(self):
        behaviors = self.parse(
            "#### TC-1.1: New form\n\n- **Tier:** E2E\n\n"
            "#### TC-1.2: Legacy form\n\n- **Tier / owner:** Unit / [DEV]\n"
        )
        self.assertEqual([b.tier for b in behaviors], [E2E, UNIT])

    def test_dotted_case_ids_in_headings_and_matrix(self):
        table = "| A | Priority | Automation |\n|---|---|---|\n| x | high | automated |\n"
        behaviors = self.parse(
            "#### TC-1.1: First\n\n" + table + "#### TC-1.10: Tenth\n\n" + table
        )
        self.assertEqual([b.tc_id for b in behaviors], ["TC-1.1", "TC-1.10"])
        rows = execution_matrix(
            "| Component | Test case IDs | Existing or proposed path and command |\n"
            "|---|---|---|\n| x | TC-1.1, TC-1.10 | `it/t.go` |\n"
        )
        self.assertEqual(sorted(rows), ["TC-1.1", "TC-1.10"])


class PathTests(unittest.TestCase):
    def test_declared_proposed_and_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            os.makedirs(os.path.join(directory, "it"))
            with open(os.path.join(directory, "it", "test.go"), "w", encoding="utf-8") as handle:
                handle.write("package it\n")
            declared, planned = candidate_test_paths(
                "existing `it/test.go`; proposed `it/new_test.go`", "", directory
            )
            self.assertEqual(declared, ["it/test.go"])
            self.assertEqual(planned, ["it/new_test.go"])

            declared, planned = candidate_test_paths("", "proposed `it/test.go`", directory)
            self.assertEqual(declared, [])
            self.assertEqual(planned, ["it/test.go"])

    def test_paths_outside_the_repo_are_planned_not_declared(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = os.path.join(directory, "repo")
            os.makedirs(repo)
            outside = os.path.join(directory, "outside_test.go")
            with open(outside, "w", encoding="utf-8") as handle:
                handle.write("")
            for token in ("../outside_test.go", outside):
                declared, planned = candidate_test_paths("`{0}`".format(token), "", repo)
                self.assertEqual(declared, [])
                self.assertEqual(planned, [token])

    def test_matrix_path_cell(self):
        rows = execution_matrix(
            "| Component | Test case IDs | Existing or proposed path and command |\n"
            "|---|---|---|\n"
            "| thing | TC-A-01, TC-A-02 | Rebuild `it/test.go` |\n"
        )
        self.assertEqual(rows["TC-A-01"], "Rebuild `it/test.go`")
        self.assertEqual(rows["TC-A-02"], "Rebuild `it/test.go`")

    def test_matrix_rows_for_the_same_case_are_merged(self):
        rows = execution_matrix(
            "| Component | Test case IDs | Existing or proposed path and command |\n"
            "|---|---|---|\n"
            "| api | TC-A-01 | `it/api_test.go` |\n"
            "| worker | TC-A-01 | `it/worker_test.go` |\n"
        )
        self.assertEqual(rows["TC-A-01"], "`it/api_test.go`; `it/worker_test.go`")


if __name__ == "__main__":
    unittest.main()
