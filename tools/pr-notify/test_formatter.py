"""Unit tests for Slack mrkdwn and compact summary formatters."""

import unittest
from datetime import date
from unittest.mock import patch

from formatter import format_message, format_summary_from_data
from models import ClassifiedPR, PRData, PRStatus


def _make_pr_data(**overrides) -> PRData:
    """Create a PRData with sensible defaults."""
    defaults = {
        "title": "Fix widget rendering",
        "url": "https://github.com/osac-project/osac/pull/42",
        "author": "alice",
        "repo": "osac-project/osac",
        "created_at": "2026-04-20T10:00:00Z",
        "is_draft": False,
        "labels": [],
        "reviews": [],
        "review_requests": [],
        "last_commit_date": "2026-04-20T10:00:00Z",
        "ci_status": None,
    }
    defaults.update(overrides)
    return PRData(**defaults)


def _make_classified(
    status: PRStatus = PRStatus.NEEDS_REVIEW,
    age_days: int = 2,
    reviewer_name: str | None = None,
    **pr_overrides,
) -> ClassifiedPR:
    """Create a ClassifiedPR with defaults."""
    return ClassifiedPR(
        pr=_make_pr_data(**pr_overrides),
        status=status,
        age_days=age_days,
        reviewer_name=reviewer_name,
    )


class TestFormatter(unittest.TestCase):
    """Tests for the Slack mrkdwn formatter covering format, grouping, and limits."""

    @patch("formatter.date")
    def test_single_pr_formats_correctly(self, mock_date):
        """1. Single PR formats correctly with all fields."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(
            status=PRStatus.NEEDS_REVIEW,
            age_days=3,
            title="Fix widget rendering",
            url="https://github.com/osac-project/osac/pull/42",
            author="alice",
            repo="osac-project/osac",
        )
        result = format_message([cpr], ["osac-project/osac"])

        self.assertIn("*PR Status Summary*", result)
        self.assertIn("2026-04-23", result)
        self.assertIn(":eyes:", result)
        self.assertIn(
            "<https://github.com/osac-project/osac/pull/42|Fix widget rendering>",
            result,
        )
        self.assertIn("alice", result)
        self.assertIn("3d", result)
        self.assertIn("needs review", result)

    @patch("formatter.date")
    def test_prs_grouped_by_repo(self, mock_date):
        """2. PRs grouped by repo with correct headers."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        prs = [
            _make_classified(repo="osac-project/osac", title="PR 1"),
            _make_classified(repo="osac-project/osac-ui", title="PR 2"),
            _make_classified(repo="osac-project/osac", title="PR 3"),
        ]
        result = format_message(
            prs, ["osac-project/osac", "osac-project/osac-ui"]
        )

        self.assertIn("osac-project/osac>* (2)", result)
        self.assertIn("osac-project/osac-ui>* (1)", result)

    def test_empty_pr_list_all_clear(self):
        """3. Empty PR list returns 'All clear' message."""
        repos = ["osac-project/osac", "osac-project/osac-ui"]
        result = format_message([], repos)

        self.assertEqual(result, "All clear — no open PRs across 2 repos :tada:")

    @patch("formatter.date")
    def test_status_emoji_mapping(self, mock_date):
        """4. Status emoji mapping for reviewable states."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        mapping = {
            PRStatus.NEEDS_REVIEW: ":eyes:",
            PRStatus.NEEDS_RE_REVIEW: ":warning:",
            PRStatus.CHANGES_REQUESTED: ":red_circle:",
        }
        for status, expected_emoji in mapping.items():
            reviewer = "bob" if status == PRStatus.CHANGES_REQUESTED else None
            cpr = _make_classified(status=status, reviewer_name=reviewer)
            result = format_message([cpr], ["osac-project/osac"])
            self.assertIn(
                expected_emoji, result, f"Missing emoji {expected_emoji} for {status}"
            )

        # Draft and approved PRs are filtered out entirely.
        for filtered_status in (PRStatus.DRAFT, PRStatus.APPROVED):
            cpr = _make_classified(status=filtered_status)
            result = format_message([cpr], ["osac-project/osac"])
            self.assertIn("All clear", result)

    @patch("formatter.date")
    def test_ci_failing_shown_as_summary(self, mock_date):
        """4b. CI-failing PRs shown as a summary line, not listed individually."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        prs = [
            _make_classified(status=PRStatus.NEEDS_REVIEW, title="Good PR"),
            _make_classified(status=PRStatus.CI_FAILING, title="Broken PR 1"),
            _make_classified(status=PRStatus.CI_FAILING, title="Broken PR 2"),
        ]
        result = format_message(prs, ["osac-project/osac"])

        self.assertIn("Good PR", result)
        self.assertNotIn("Broken PR 1", result)
        self.assertNotIn("Broken PR 2", result)
        self.assertIn("2 PRs with CI failures (author action needed)", result)

    @patch("formatter.date")
    def test_only_ci_failing_prs(self, mock_date):
        """4c. When all PRs are CI-failing, show summary only."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        prs = [
            _make_classified(status=PRStatus.CI_FAILING, title="Broken 1"),
            _make_classified(status=PRStatus.CI_FAILING, title="Broken 2"),
        ]
        result = format_message(prs, ["osac-project/osac"])

        self.assertNotIn("Broken 1", result)
        self.assertNotIn("Broken 2", result)
        self.assertIn("2 PRs with CI failures (author action needed)", result)
        self.assertIn("0 ready for review", result)

    @patch("formatter.date")
    def test_staleness_7_days(self, mock_date):
        """5a. Staleness indicator at 7 days (hourglass)."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(age_days=7)
        result = format_message([cpr], ["osac-project/osac"])
        self.assertIn(":hourglass:", result)
        self.assertNotIn(":rotating_light:", result)

    @patch("formatter.date")
    def test_staleness_14_days(self, mock_date):
        """5b. Staleness indicator at 14 days (rotating_light)."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(age_days=14)
        result = format_message([cpr], ["osac-project/osac"])
        self.assertIn(":rotating_light:", result)
        self.assertNotIn(":hourglass:", result)

    @patch("formatter.date")
    def test_changes_requested_shows_reviewer(self, mock_date):
        """6. Changes requested shows reviewer name."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(
            status=PRStatus.CHANGES_REQUESTED,
            reviewer_name="carol",
        )
        result = format_message([cpr], ["osac-project/osac"])
        self.assertIn("changes requested by <https://github.com/carol|carol>", result)

    @patch("formatter.date")
    def test_pr_title_is_escaped_for_slack_mrkdwn(self, mock_date):
        """Untrusted PR titles cannot terminate a Slack link or mention a channel."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(title="Fix > <!channel> & notify")
        result = format_message([cpr], ["osac-project/osac"])

        self.assertIn("Fix &gt; &lt;!channel&gt; &amp; notify", result)
        self.assertNotIn("<!channel>", result)

    @patch("formatter.date")
    def test_summary_stats_header(self, mock_date):
        """7. Summary stats header counts only reviewable PRs."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        prs = [
            _make_classified(
                status=PRStatus.NEEDS_REVIEW,
                age_days=2,
                repo="osac-project/osac",
            ),
            _make_classified(
                status=PRStatus.CI_FAILING,
                age_days=8,
                repo="osac-project/osac",
            ),
            _make_classified(
                status=PRStatus.NEEDS_REVIEW,
                age_days=15,
                repo="osac-project/osac-ui",
            ),
        ]
        result = format_message(
            prs, ["osac-project/osac", "osac-project/osac-ui"]
        )

        self.assertIn("2 ready for review across 2 repos", result)
        self.assertIn("2 need review", result)
        self.assertIn("1 stale (7+ days)", result)
        self.assertIn("1 PR with CI failures", result)

    @patch("formatter.date")
    def test_per_repo_truncation(self, mock_date):
        """8. Repos with >6 PRs show top 6 + '... and N more' link."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        prs = [
            _make_classified(
                title=f"PR #{i}",
                url=f"https://github.com/osac-project/osac/pull/{i}",
            )
            for i in range(10)
        ]
        result = format_message(prs, ["osac-project/osac"])

        # Should show 6 PRs + "... and 4 more" line
        self.assertEqual(result.count(":eyes:"), 6)
        self.assertIn("... and 4 more", result)
        self.assertIn("osac-project/osac/pulls|", result)

    @patch("formatter.date")
    def test_repos_with_no_prs_omitted(self, mock_date):
        """9. Repos with no PRs are omitted from output."""
        mock_date.today.return_value = date(2026, 4, 23)
        mock_date.side_effect = lambda *a, **kw: date(*a, **kw)

        cpr = _make_classified(repo="osac-project/osac")
        result = format_message(
            [cpr],
            ["osac-project/osac", "osac-project/osac-ui"],
        )

        self.assertIn("osac-project/osac>*", result)
        self.assertNotIn("osac-ui", result)


class TestSummaryFromData(unittest.TestCase):
    """Tests for format_summary_from_data (notify mode)."""

    def test_includes_counts(self):
        data = {
            "summary": {"needs_review": 3, "ci_failing": 1, "stale": 2},
            "repos": [{"name": "r1"}, {"name": "r2"}],
        }
        result = format_summary_from_data(data, "https://example.com")
        self.assertIn("3 need review", result)
        self.assertIn("1 CI failing", result)
        self.assertIn("2 stale", result)

    def test_includes_dashboard_link(self):
        data = {
            "summary": {"needs_review": 1, "ci_failing": 0, "stale": 0},
            "repos": [{"name": "r1"}],
        }
        result = format_summary_from_data(data, "https://example.com")
        self.assertIn("<https://example.com|Full PR Dashboard>", result)

    def test_all_clear_when_no_actionable(self):
        data = {
            "summary": {"needs_review": 0, "ci_failing": 0, "stale": 0},
            "repos": [{"name": "r1"}, {"name": "r2"}],
        }
        result = format_summary_from_data(data, "https://example.com")
        self.assertIn("All clear", result)
        self.assertIn("2 repos", result)

    def test_empty_summary_defaults(self):
        data = {"summary": {}, "repos": []}
        result = format_summary_from_data(data, "https://example.com")
        self.assertIn("All clear", result)


if __name__ == "__main__":
    unittest.main()
