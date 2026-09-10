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

    def test_loads_dashboard_data_path_in_published_dashboard_directory(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "config.toml"
            path.write_text(
                "repos = ['osac-project/osac']\n"
                "[dashboard]\n"
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/team'\n"
                "data_path = 'docs/team/data.json'\n"
            )

            config = load_config(str(path))

        self.assertEqual(config.dashboard.data_path, "docs/team/data.json")

    def test_rejects_invalid_dashboard_metadata_types_and_urls(self):
        invalid_configs = (
            (
                "repo = 123\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/team'\n",
                "dashboard.repo",
            ),
            (
                "repo = 'osac-project/osac'\n"
                "branch = 1\n"
                "base_url = 'https://example.test/team'\n",
                "dashboard.branch",
            ),
            (
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                'base_url = ["https://example.test/team"]\n',
                "dashboard.base_url",
            ),
            (
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                'base_url = "http://example.test/team"\n',
                "dashboard.base_url",
            ),
        )
        for dashboard_config, error_field in invalid_configs:
            with self.subTest(dashboard_config=dashboard_config):
                with tempfile.TemporaryDirectory() as tmpdir:
                    path = Path(tmpdir) / "config.toml"
                    path.write_text(
                        "repos = ['osac-project/osac']\n"
                        "[dashboard]\n"
                        + dashboard_config
                    )

                    with self.assertRaisesRegex(SystemExit, error_field):
                        load_config(str(path))

    def test_rejects_dashboard_data_path_outside_published_dashboard_directory(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "config.toml"
            path.write_text(
                "repos = ['osac-project/osac']\n"
                "[dashboard]\n"
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/team'\n"
                "data_path = 'docs/team/alternate.json'\n"
            )

            with self.assertRaisesRegex(SystemExit, "docs/<dashboard>/data.json"):
                load_config(str(path))

    def test_rejects_dot_prefixed_dashboard_data_path(self):
        for data_path in (
            "docs/./data.json",
            "docs/../data.json",
            "docs/.hidden/data.json",
        ):
            with self.subTest(data_path=data_path):
                with tempfile.TemporaryDirectory() as tmpdir:
                    path = Path(tmpdir) / "config.toml"
                    path.write_text(
                        "repos = ['osac-project/osac']\n"
                        "[dashboard]\n"
                        "repo = 'osac-project/osac'\n"
                        "branch = 'main'\n"
                        "base_url = 'https://example.test/team'\n"
                        f"data_path = '{data_path}'\n"
                    )

                    with self.assertRaisesRegex(
                        SystemExit, "docs/<dashboard>/data.json"
                    ):
                        load_config(str(path))


if __name__ == "__main__":
    unittest.main()
