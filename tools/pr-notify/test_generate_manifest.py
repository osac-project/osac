import json
import os
import tempfile
import unittest

from generate_manifest import slug_from_data_path
from models import DEFAULT_TITLE


class TestSlugFromDataPath(unittest.TestCase):
    def test_standard_nested_path(self):
        self.assertEqual(slug_from_data_path("docs/pr-dashboard/data.json"), "pr-dashboard")

    def test_storage_dashboard_path(self):
        self.assertEqual(slug_from_data_path("docs/storage-pr-dashboard/data.json"), "storage-pr-dashboard")

    def test_flat_path_returns_empty(self):
        self.assertEqual(slug_from_data_path("data.json"), "")

    def test_deeply_nested_path(self):
        self.assertEqual(slug_from_data_path("a/b/c/data.json"), "c")

    def test_empty_string_returns_empty(self):
        self.assertEqual(slug_from_data_path(""), "")


class TestManifestGeneration(unittest.TestCase):
    def test_generates_manifest_from_config_files(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            config = os.path.join(tmpdir, "config.example.toml")
            with open(config, "w") as f:
                f.write(
                    'title = "Test Dashboard"\n'
                    'description = "A test"\n'
                    "repos = []\n"
                    "[dashboard]\n"
                    'repo = "test/repo"\n'
                    'branch = "main"\n'
                    'base_url = "https://example.com"\n'
                    'data_path = "docs/test-dashboard/data.json"\n'
                )

            output = os.path.join(tmpdir, "dashboards.json")
            orig_dir = os.getcwd()
            try:
                os.chdir(tmpdir)
                from generate_manifest import main
                import sys
                sys.argv = ["generate_manifest.py", "--output", output]
                result = main()
            finally:
                os.chdir(orig_dir)

            self.assertEqual(result, 0)
            with open(output) as f:
                data = json.load(f)
            self.assertEqual(len(data), 1)
            self.assertEqual(data[0]["title"], "Test Dashboard")
            self.assertEqual(data[0]["slug"], "test-dashboard")

    def test_missing_title_uses_fallback(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            config = os.path.join(tmpdir, "config.example.toml")
            with open(config, "w") as f:
                f.write(
                    "repos = []\n"
                    "[dashboard]\n"
                    'repo = "test/repo"\n'
                    'branch = "main"\n'
                    'base_url = "https://example.com"\n'
                )

            output = os.path.join(tmpdir, "dashboards.json")
            orig_dir = os.getcwd()
            try:
                os.chdir(tmpdir)
                from generate_manifest import main
                import sys
                sys.argv = ["generate_manifest.py", "--output", output]
                main()
            finally:
                os.chdir(orig_dir)

            with open(output) as f:
                data = json.load(f)
            self.assertEqual(data[0]["title"], DEFAULT_TITLE)


if __name__ == "__main__":
    unittest.main()
