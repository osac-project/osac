"""Contract tests for batched PR dashboard generation."""

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from generate import build_dashboard_data
from generate_all import main
from models import ClassifiedPR, Config, PRData, PRStatus


def _pr(author: str, repo: str = "osac-project/osac") -> PRData:
    return PRData(
        title="Example PR",
        url="https://github.com/osac-project/osac/pull/1",
        author=author,
        repo=repo,
        created_at="2026-01-01T00:00:00Z",
        is_draft=False,
        labels=[],
        reviews=[],
        review_requests=[],
        last_commit_date="2026-01-01T00:00:00Z",
        ci_status="SUCCESS",
    )


class TestGenerateAll(unittest.TestCase):
    """Multiple dashboards reuse a single GitHub snapshot."""

    @patch("generate_all.fetch_open_prs")
    def test_fetches_shared_repositories_once_and_writes_each_dashboard(self, mock_fetch):
        mock_fetch.return_value = [
            _pr("alice"),
            _pr("bob", repo="osac-project/osac-ui"),
        ]

        with tempfile.TemporaryDirectory() as tmpdir:
            config_dir = Path(tmpdir) / "configs"
            output_root = Path(tmpdir) / "output"
            config_dir.mkdir()
            (config_dir / "config.general.toml").write_text(
                "title = 'General'\n"
                "repos = ['osac-project/osac']\n"
                "[dashboard]\n"
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/general'\n"
                "data_path = 'docs/general/data.json'\n"
            )
            (config_dir / "config.filtered.toml").write_text(
                "title = 'Filtered'\n"
                "repos = ['osac-project/osac']\n"
                "filter_authors = ['alice']\n"
                "[dashboard]\n"
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/filtered'\n"
                "data_path = 'docs/filtered/data.json'\n"
            )
            (config_dir / "config.other.toml").write_text(
                "title = 'Other'\n"
                "repos = ['osac-project/osac-ui']\n"
                "[dashboard]\n"
                "repo = 'osac-project/osac'\n"
                "branch = 'main'\n"
                "base_url = 'https://example.test/other'\n"
                "data_path = 'docs/other/data.json'\n"
            )

            args = [
                "generate_all.py",
                "--config-glob",
                str(config_dir / "config.*.toml"),
                "--output-root",
                str(output_root),
            ]
            with patch.object(sys, "argv", args):
                self.assertEqual(main(), 0)

            mock_fetch.assert_called_once_with(
                ["osac-project/osac", "osac-project/osac-ui"]
            )
            with (output_root / "docs/general/data.json").open() as data_file:
                general = json.load(data_file)
            with (output_root / "docs/filtered/data.json").open() as data_file:
                filtered = json.load(data_file)
            with (output_root / "docs/other/data.json").open() as data_file:
                other = json.load(data_file)

            self.assertEqual(general["title"], "General")
            self.assertEqual([repo["name"] for repo in general["repos"]], ["osac-project/osac"])
            self.assertEqual(filtered["title"], "Filtered")
            self.assertEqual(len(filtered["repos"][0]["prs"]), 1)
            self.assertEqual([repo["name"] for repo in other["repos"]], ["osac-project/osac-ui"])


class TestBotAttributionFiltering(unittest.TestCase):
    """Bot-authored PRs with attribution are included under the human author's filter."""

    def _bot_pr(self) -> PRData:
        return PRData(
            title="OSAC-5309: automated update",
            url="https://github.com/osac-project/osac/pull/99",
            author="redhat-chai-bot",
            repo="osac-project/osac",
            created_at="2026-01-01T00:00:00Z",
            is_draft=False,
            labels=[],
            reviews=[],
            review_requests=[],
            last_commit_date="2026-01-01T00:00:00Z",
            ci_status="SUCCESS",
            body="@janboll requested in [Slack thread](https://slack.com/t/1)",
        )

    @patch("generate.classify_prs")
    def test_bot_pr_included_when_attributed_author_in_filter(self, mock_classify):
        pr = self._bot_pr()
        mock_classify.return_value = [
            ClassifiedPR(pr=pr, status=PRStatus.NEEDS_REVIEW, age_days=1),
        ]
        config = Config(
            repos=["osac-project/osac"],
            filter_authors=["janboll"],
        )

        data = build_dashboard_data([pr], config)

        self.assertEqual(len(data["repos"]), 1)
        self.assertEqual(len(data["repos"][0]["prs"]), 1)
        self.assertEqual(data["repos"][0]["prs"][0]["author"], "redhat-chai-bot")

    @patch("generate.classify_prs")
    def test_bot_pr_excluded_when_attributed_author_not_in_filter(self, mock_classify):
        pr = self._bot_pr()
        mock_classify.return_value = [
            ClassifiedPR(pr=pr, status=PRStatus.NEEDS_REVIEW, age_days=1),
        ]
        config = Config(
            repos=["osac-project/osac"],
            filter_authors=["someoneelse"],
        )

        data = build_dashboard_data([pr], config)

        self.assertEqual(data["repos"], [])


if __name__ == "__main__":
    unittest.main()
