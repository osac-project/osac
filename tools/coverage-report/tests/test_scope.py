"""Contract tests for feature scope resolution."""

import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.collectors import clear, register
from coverage_report.config import bundled_features_file
from coverage_report.errors import ConfigError, RepoError, ScopeError
from coverage_report.model import CollectorBatch
from coverage_report.scope import load_registry, resolve_sha, resolve_scope
from coverage_report.cli import main

import support


class TestRegistry(unittest.TestCase):
    def test_bundled_registry_parses(self):
        registry = load_registry(bundled_features_file())

        self.assertEqual(registry.version, 1)
        self.assertEqual(registry.by_key("OSAC-2540").name, "disk-image")
        self.assertEqual(registry.by_key("OSAC-2540").aliases, ("diskimage",))
        self.assertEqual(registry.by_key("OSAC-2540").suites["e2e"], ("tests/e2e",))

    def test_duplicate_keys_are_rejected(self):
        body = (
            "version: 1\n"
            "features:\n"
            "  - key: OSAC-1\n"
            "    name: one\n"
            "  - key: osac-1\n"
            "    name: two\n"
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir, "features.yaml")
            path.write_text(body, encoding="utf-8")
            with self.assertRaises(ConfigError) as raised:
                load_registry(str(path))

        self.assertEqual(raised.exception.code, "FEATURES_DUPLICATE_KEY")

    def test_unknown_feature_field_is_ignored(self):
        body = (
            "version: 1\n"
            "features:\n"
            "  - key: OSAC-1\n"
            "    name: one\n"
            "    future_field: keep-me\n"
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir, "features.yaml")
            path.write_text(body, encoding="utf-8")
            registry = load_registry(str(path))

        self.assertEqual(registry.features[0].key, "OSAC-1")

    def test_unit_packages_accept_strings_and_mappings(self):
        body = (
            "version: 1\n"
            "features:\n"
            "  - key: OSAC-1\n"
            "    name: one\n"
            "    unit_packages:\n"
            "      - pkg/a\n"
            "      - package: pkg/b\n"
            "        rationale: owns the reconciler\n"
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir, "features.yaml")
            path.write_text(body, encoding="utf-8")
            registry = load_registry(str(path))

        packages = registry.features[0].unit_packages
        self.assertEqual(packages[0].package, "pkg/a")
        self.assertEqual(packages[1].rationale, "owns the reconciler")

    def test_missing_registry_is_a_config_error(self):
        with self.assertRaises(ConfigError) as raised:
            load_registry("/nonexistent/features.yaml")
        self.assertEqual(raised.exception.code, "FEATURES_FILE_NOT_FOUND")


class TestScopeResolution(unittest.TestCase):
    def setUp(self):
        self.registry = load_registry(bundled_features_file())

    def test_resolves_by_key(self):
        features, matched_by = resolve_scope(self.registry, "OSAC-2540")
        self.assertEqual([feature.key for feature in features], ["OSAC-2540"])
        self.assertEqual(matched_by, "key")

    def test_resolves_by_name(self):
        features, matched_by = resolve_scope(self.registry, "disk-image")
        self.assertEqual([feature.key for feature in features], ["OSAC-2540"])
        self.assertEqual(matched_by, "name")

    def test_resolves_by_alias(self):
        features, matched_by = resolve_scope(self.registry, "diskimage")
        self.assertEqual([feature.key for feature in features], ["OSAC-2540"])
        self.assertEqual(matched_by, "alias")

    def test_resolution_is_case_insensitive(self):
        features, matched_by = resolve_scope(self.registry, "osac-2540")
        self.assertEqual([feature.key for feature in features], ["OSAC-2540"])
        self.assertEqual(matched_by, "key")

        features, _ = resolve_scope(self.registry, "DISK-IMAGE")
        self.assertEqual(features[0].key, "OSAC-2540")

    def test_no_argument_selects_every_feature_sorted(self):
        features, matched_by = resolve_scope(self.registry, None)
        self.assertEqual(matched_by, "full_plan")
        self.assertEqual(
            [feature.key for feature in features], sorted(feature.key for feature in features)
        )

    def test_unknown_selector_lists_close_candidates_and_snippet(self):
        with self.assertRaises(ScopeError) as raised:
            resolve_scope(self.registry, "disk-imag")
        error = raised.exception
        self.assertEqual(error.code, "SCOPE_UNRESOLVED")
        self.assertEqual(error.exit_code, 2)
        self.assertIn("disk-image", error.candidates)
        self.assertIn("features:", str(error.details["snippet"]))
        self.assertIn("key: disk-imag", str(error.details["snippet"]))

    def test_ambiguous_selector_stops_and_names_candidates(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir, "features.yaml")
            path.write_text(support.AMBIGUOUS_FEATURES_YAML, encoding="utf-8")
            registry = load_registry(str(path))

            with self.assertRaises(ScopeError) as raised:
                resolve_scope(registry, "shared")

        error = raised.exception
        self.assertEqual(error.code, "SCOPE_AMBIGUOUS")
        self.assertEqual(error.candidates, ["OSAC-1000", "OSAC-2000"])
        self.assertEqual(error.exit_code, 2)

    def test_empty_selector_is_rejected(self):
        with self.assertRaises(ScopeError) as raised:
            resolve_scope(self.registry, "   ")
        self.assertEqual(raised.exception.code, "SCOPE_UNRESOLVED")


class TestRevisionSnapshot(unittest.TestCase):
    def test_git_head_is_a_full_sha(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            repo = support.make_repo(Path(tmpdir))
            expected = support.head(repo)
            sha, source = resolve_sha(str(repo))
        self.assertEqual(source, "git")
        self.assertRegex(sha, r"^[0-9a-f]{40}$")
        self.assertEqual(sha, expected)

    def test_sha_override_is_recorded_as_cli(self):
        sha, source = resolve_sha(".", "abc123def")
        self.assertEqual(source, "cli")
        self.assertEqual(sha, "abc123def")

    def test_invalid_sha_override_is_rejected(self):
        with self.assertRaises(RepoError) as raised:
            resolve_sha(".", "not-a-sha!")
        self.assertEqual(raised.exception.code, "SHA_INVALID")

    def test_non_git_directory_is_a_repo_error(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            with self.assertRaises(RepoError) as raised:
                resolve_sha(tmpdir)
        self.assertIn(raised.exception.code, ("GIT_UNAVAILABLE", "GIT_INVALID_SHA"))


class TestScopeFailsBeforeCollection(unittest.TestCase):
    def setUp(self):
        clear()

    def tearDown(self):
        clear()

    def test_cli_does_not_invoke_collectors_on_scope_error(self):
        calls = []

        class Probe:
            name = "probe"

            def collect(self, ctx):
                calls.append(ctx.run_id)
                return CollectorBatch(collector="probe")

        register(Probe())
        with tempfile.TemporaryDirectory() as tmpdir:
            repo = support.make_repo(Path(tmpdir))
            with contextlib.redirect_stderr(io.StringIO()):
                exit_code = main(
                    [
                        "definitely-not-a-feature",
                        "--repo",
                        str(repo),
                        "--now",
                        "2026-10-22T00:00:00Z",
                    ]
                )
            output_exists = os.path.exists(
                os.path.join(str(repo), ".artifacts", "coverage-report", "ALL")
            )

        self.assertEqual(exit_code, 2)
        self.assertEqual(calls, [])
        self.assertFalse(output_exists)


if __name__ == "__main__":
    unittest.main()
