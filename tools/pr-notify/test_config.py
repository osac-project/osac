"""Contract tests for dashboard configuration loading."""

import tempfile
import unittest
from pathlib import Path

from config import load_config


class TestLoadConfig(unittest.TestCase):
    """Repository configuration must be safe for GitHub query construction."""

    def test_loads_valid_repository_list(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "config.toml"
            path.write_text("repos = ['osac-project/osac']\n")

            config = load_config(str(path))

        self.assertEqual(config.repos, ["osac-project/osac"])

    def test_rejects_empty_repository_list(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "config.toml"
            path.write_text("repos = []\n")

            with self.assertRaisesRegex(SystemExit, "non-empty array"):
                load_config(str(path))

    def test_rejects_repository_that_cannot_form_owner_name_query(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "config.toml"
            path.write_text("repos = ['osac-project/osac\" name: \"other']\n")

            with self.assertRaisesRegex(SystemExit, "expected 'owner/name'"):
                load_config(str(path))


if __name__ == "__main__":
    unittest.main()
