"""Unit tests for the JSON data formatter."""

import unittest
from unittest.mock import patch
from datetime import datetime, timezone

from data_formatter import format_dashboard_data
from models import CheckRun, ClassifiedPR, PRData, PRStatus


def _make_pr_data(**overrides) -> PRData:
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
    return ClassifiedPR(
        pr=_make_pr_data(**pr_overrides),
        status=status,
        age_days=age_days,
        reviewer_name=reviewer_name,
    )


class TestDashboardData(unittest.TestCase):

    def test_top_level_keys(self):
        data = format_dashboard_data([], ["r1"])
        self.assertIn("generated_at", data)
        self.assertIn("summary", data)
        self.assertIn("repos", data)
        self.assertIn("ci_health", data)

    def test_empty_prs(self):
        data = format_dashboard_data([], ["r1", "r2"])
        self.assertEqual(data["summary"]["needs_review"], 0)
        self.assertEqual(data["summary"]["ci_failing"], 0)
        self.assertEqual(data["repos"], [])

    def test_summary_counts(self):
        prs = [
            _make_classified(status=PRStatus.NEEDS_REVIEW),
            _make_classified(status=PRStatus.NEEDS_RE_REVIEW),
            _make_classified(status=PRStatus.CI_FAILING),
            _make_classified(status=PRStatus.APPROVED),
            _make_classified(status=PRStatus.DRAFT),
        ]
        data = format_dashboard_data(prs, ["osac-project/osac"])
        self.assertEqual(data["summary"]["needs_review"], 2)
        self.assertEqual(data["summary"]["ci_failing"], 1)
        self.assertEqual(data["summary"]["approved"], 1)
        self.assertEqual(data["summary"]["draft"], 1)

    def test_stale_count(self):
        prs = [
            _make_classified(status=PRStatus.NEEDS_REVIEW, age_days=10),
            _make_classified(status=PRStatus.NEEDS_REVIEW, age_days=2),
        ]
        data = format_dashboard_data(prs, ["osac-project/osac"])
        self.assertEqual(data["summary"]["stale"], 1)

    def test_repos_grouped(self):
        prs = [
            _make_classified(repo="osac-project/osac"),
            _make_classified(repo="osac-project/osac-ui"),
            _make_classified(repo="osac-project/osac"),
        ]
        data = format_dashboard_data(prs, ["osac-project/osac", "osac-project/osac-ui"])

        self.assertEqual(len(data["repos"]), 2)
        names = [r["name"] for r in data["repos"]]
        self.assertIn("osac-project/osac", names)
        self.assertIn("osac-project/osac-ui", names)

        fs_repo = next(r for r in data["repos"] if r["name"] == "osac-project/osac")
        self.assertEqual(len(fs_repo["prs"]), 2)
        self.assertEqual(fs_repo["pulls_url"], "https://github.com/osac-project/osac/pulls")

    def test_pr_serialization(self):
        prs = [_make_classified(
            status=PRStatus.CHANGES_REQUESTED,
            reviewer_name="bob",
            title="Fix bug",
            author="alice",
            age_days=5,
        )]
        data = format_dashboard_data(prs, ["osac-project/osac"])
        pr = data["repos"][0]["prs"][0]

        self.assertEqual(pr["title"], "Fix bug")
        self.assertEqual(pr["author"], "alice")
        self.assertEqual(pr["status"], "changes_requested")
        self.assertEqual(pr["age_days"], 5)
        self.assertEqual(pr["reviewer_name"], "bob")

    def test_check_runs_serialized(self):
        pr_data = _make_pr_data(check_runs=[
            CheckRun(name="unit-tests", conclusion="SUCCESS", details_url="https://example.com/1"),
            CheckRun(name="lint", conclusion="FAILURE", details_url="https://example.com/2"),
        ])
        cpr = ClassifiedPR(pr=pr_data, status=PRStatus.NEEDS_REVIEW, age_days=1)
        data = format_dashboard_data([cpr], ["osac-project/osac"])

        checks = data["repos"][0]["prs"][0]["check_runs"]
        self.assertEqual(len(checks), 2)
        self.assertEqual(checks[0]["name"], "unit-tests")
        self.assertEqual(checks[0]["conclusion"], "SUCCESS")
        self.assertEqual(checks[1]["name"], "lint")
        self.assertEqual(checks[1]["conclusion"], "FAILURE")

    def test_ci_health(self):
        pr_data = _make_pr_data(check_runs=[
            CheckRun(name="tests", conclusion="SUCCESS", details_url=""),
            CheckRun(name="lint", conclusion="SUCCESS", details_url=""),
            CheckRun(name="build", conclusion="FAILURE", details_url=""),
        ])
        cpr = ClassifiedPR(pr=pr_data, status=PRStatus.NEEDS_REVIEW, age_days=1)
        data = format_dashboard_data([cpr], ["osac-project/osac"])

        self.assertEqual(len(data["ci_health"]), 1)
        ci = data["ci_health"][0]
        self.assertEqual(ci["repo"], "osac-project/osac")
        self.assertEqual(ci["total_checks"], 3)
        self.assertEqual(ci["passed"], 2)
        self.assertEqual(ci["pass_rate"], 66)

    def test_ci_health_empty_when_no_checks(self):
        prs = [_make_classified()]
        data = format_dashboard_data(prs, ["osac-project/osac"])
        self.assertEqual(data["ci_health"], [])

    def test_generated_at_format(self):
        data = format_dashboard_data([], [])
        self.assertTrue(data["generated_at"].endswith("Z"))
        datetime.fromisoformat(data["generated_at"].replace("Z", "+00:00"))


if __name__ == "__main__":
    unittest.main()
