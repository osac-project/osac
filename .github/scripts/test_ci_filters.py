"""Configuration contracts for automatic test selection."""

import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]


class CiFiltersTest(unittest.TestCase):
    def test_root_coderabbit_config_is_excluded_from_broad_test_filters(self):
        filters = yaml.safe_load((ROOT / ".github/filters/ci-filters.yml").read_text())
        for name in (
            "code",
            "e2e-code",
            "vmaas-outside-known-safe",
            "caas-outside-known-safe",
            "bmaas-outside-known-safe",
        ):
            with self.subTest(filter=name):
                self.assertIn("!.coderabbit.yaml", filters[name])
