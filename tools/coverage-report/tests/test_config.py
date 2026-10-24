"""Contract tests for configuration precedence, origins, and validation."""

import os
import tempfile
import unittest
from pathlib import Path

import path_setup  # noqa: F401  (adds the package root to sys.path)

from coverage_report.config import (
    CONFIG_VERSION,
    CliOverrides,
    bundled_features_file,
    load_config,
)
from coverage_report.errors import ConfigError


class TestConfigDefaults(unittest.TestCase):
    def test_defaults_apply_when_repo_has_no_config_file(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            config = load_config(tmpdir)

        self.assertEqual(config.version, CONFIG_VERSION)
        self.assertEqual(config.features_file, bundled_features_file())
        self.assertEqual(config.origins["features_file"], "default")
        self.assertEqual(config.origins["output_dir"], "default")
        self.assertEqual(
            config.output_dir,
            os.path.join(os.path.abspath(tmpdir), ".artifacts", "coverage-report"),
        )
        self.assertEqual(config.metric_versions["unit"], "statement-weighted-v2")
        self.assertEqual(config.metric_versions["integration"], "behavior-inventory-v1")


class TestConfigPrecedence(unittest.TestCase):
    def test_repository_file_overrides_defaults(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            Path(tmpdir, ".coverage-skill.yaml").write_text(
                "version: 1\n"
                "features_file: config/features.yaml\n"
                "output_dir: out/reports\n"
                "metric_versions:\n"
                "  unit: legacy-package-average-v1\n",
                encoding="utf-8",
            )

            config = load_config(tmpdir)

        self.assertEqual(config.origins["features_file"], "repo")
        self.assertEqual(config.origins["output_dir"], "repo")
        self.assertEqual(
            config.features_file, os.path.join(os.path.abspath(tmpdir), "config", "features.yaml")
        )
        self.assertEqual(
            config.output_dir, os.path.join(os.path.abspath(tmpdir), "out", "reports")
        )
        self.assertEqual(config.metric_versions["unit"], "legacy-package-average-v1")
        self.assertEqual(config.metric_versions["e2e"], "behavior-inventory-v1")

    def test_cli_flags_override_repository_file(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            Path(tmpdir, ".coverage-skill.yaml").write_text(
                "version: 1\noutput_dir: from-repo\n", encoding="utf-8"
            )

            config = load_config(
                tmpdir,
                CliOverrides(
                    output_dir="from-cli",
                    coverprofiles=("explicit.out",),
                ),
            )

        self.assertEqual(config.origins["output_dir"], "cli")
        self.assertEqual(
            config.output_dir, os.path.join(os.path.abspath(tmpdir), "from-cli")
        )
        self.assertEqual(config.origins["artifacts"], "cli")
        self.assertEqual(
            config.coverprofiles,
            (os.path.join(os.path.abspath(tmpdir), "explicit.out"),),
        )

    def test_explicit_config_path_wins_over_repository_default_location(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            Path(tmpdir, ".coverage-skill.yaml").write_text(
                "version: 1\noutput_dir: ignored\n", encoding="utf-8"
            )
            custom = Path(tmpdir, "custom.yaml")
            custom.write_text("version: 1\noutput_dir: custom\n", encoding="utf-8")

            config = load_config(tmpdir, CliOverrides(config_path=str(custom)))

        self.assertEqual(config.config_path, str(custom))
        self.assertEqual(
            config.output_dir, os.path.join(os.path.abspath(tmpdir), "custom")
        )


class TestConfigValidation(unittest.TestCase):
    def _load(self, body: str) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            Path(tmpdir, ".coverage-skill.yaml").write_text(body, encoding="utf-8")
            load_config(tmpdir)

    def test_rejects_wrong_type_with_field_path(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 1\nenhancement_roots: not-a-list\n")
        self.assertEqual(raised.exception.code, "CONFIG_INVALID")
        self.assertEqual(raised.exception.field, "enhancement_roots")
        self.assertEqual(raised.exception.exit_code, 3)

    def test_rejects_unknown_top_level_key(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 1\nunknown_key: true\n")
        self.assertIn("unknown configuration key", raised.exception.message)
        self.assertIn("metric_versions", raised.exception.candidates)

    def test_rejects_unsupported_metric_version_with_candidates(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 1\nmetric_versions:\n  unit: made-up-v9\n")
        self.assertEqual(raised.exception.code, "CONFIG_METRIC_VERSION_UNSUPPORTED")
        self.assertEqual(raised.exception.field, "metric_versions.unit")
        self.assertEqual(
            raised.exception.candidates,
            ["legacy-package-average-v1", "statement-weighted-v2"],
        )

    def test_rejects_unknown_metric_tier(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 1\nmetric_versions:\n  sanity: behavior-inventory-v1\n")
        self.assertEqual(raised.exception.field, "metric_versions")

    def test_rejects_unknown_artifacts_key(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 1\nartifacts:\n  screenshots: []\n")
        self.assertEqual(raised.exception.field, "artifacts.screenshots")

    def test_rejects_unsupported_version(self):
        with self.assertRaises(ConfigError) as raised:
            self._load("version: 99\n")
        self.assertEqual(raised.exception.code, "CONFIG_VERSION_UNSUPPORTED")

    def test_missing_explicit_config_file_is_a_config_error(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            with self.assertRaises(ConfigError) as raised:
                load_config(
                    tmpdir,
                    CliOverrides(config_path=str(Path(tmpdir, "missing.yaml"))),
                )
        self.assertEqual(raised.exception.code, "CONFIG_NOT_FOUND")
        self.assertEqual(raised.exception.exit_code, 3)

    def test_rejects_non_mapping_config(self):
        with self.assertRaises(ConfigError):
            self._load("- just\n- a\n- list\n")


if __name__ == "__main__":
    unittest.main()
