"""Unit tests for the PR classification engine."""

import unittest
from datetime import datetime, timezone, timedelta

from classifier import classify_prs, _latest_review_per_author
from models import PRData, PRStatus


def _make_pr(**overrides) -> PRData:
    """Create a PRData with sensible defaults, overriding as needed."""
    now = datetime.now(timezone.utc)
    defaults = {
        "title": "Test PR",
        "url": "https://github.com/org/repo/pull/1",
        "author": "alice",
        "repo": "org/repo",
        "created_at": (now - timedelta(days=2)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "is_draft": False,
        "labels": [],
        "reviews": [],
        "review_requests": [],
        "last_commit_date": (now - timedelta(days=2)).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "ci_status": None,
    }
    defaults.update(overrides)
    return PRData(**defaults)


def _make_review(author: str, state: str, submitted_at: str) -> dict:
    """Create a review dict matching the expected shape."""
    return {"author": author, "state": state, "submitted_at": submitted_at}


class TestClassifier(unittest.TestCase):
    """Tests for classify_prs covering all 6 status states and edge cases."""

    def test_no_reviews_needs_review(self):
        """1. PR with no reviews -> NEEDS_REVIEW."""
        pr = _make_pr(reviews=[])
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_only_commented_reviews_needs_review(self):
        """2. PR with only COMMENTED reviews -> NEEDS_REVIEW."""
        pr = _make_pr(
            reviews=[
                _make_review("bob", "COMMENTED", "2026-04-20T10:00:00Z"),
                _make_review("carol", "COMMENTED", "2026-04-20T11:00:00Z"),
            ]
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_approved_review(self):
        """3. PR with APPROVED review -> APPROVED."""
        pr = _make_pr(
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.APPROVED)

    def test_changes_requested_sets_reviewer_name(self):
        """4. PR with CHANGES_REQUESTED -> CHANGES_REQUESTED + reviewer_name."""
        pr = _make_pr(
            reviews=[
                _make_review("carol", "CHANGES_REQUESTED", "2026-04-21T10:00:00Z"),
            ]
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CHANGES_REQUESTED)
        self.assertEqual(result[0].reviewer_name, "carol")

    def test_approved_then_new_commit_needs_re_review(self):
        """5. PR with APPROVED then new commit -> NEEDS_RE_REVIEW."""
        pr = _make_pr(
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-20T10:00:00Z"),
            ],
            last_commit_date="2026-04-21T12:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_RE_REVIEW)

    def test_draft_overrides_approvals(self):
        """6. Draft PR with approvals -> DRAFT (draft overrides)."""
        pr = _make_pr(
            is_draft=True,
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.DRAFT)

    def test_conflicts_overrides_ci_and_review(self):
        """7. PR with merge conflicts -> CONFLICTS (overrides CI and reviews)."""
        pr = _make_pr(
            mergeable="CONFLICTING",
            ci_status="FAILURE",
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CONFLICTS)

    def test_conflicts_without_ci_failure(self):
        """7b. PR with merge conflicts but passing CI -> CONFLICTS."""
        pr = _make_pr(mergeable="CONFLICTING", ci_status="SUCCESS")
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CONFLICTS)

    def test_mergeable_pr_not_classified_as_conflicts(self):
        """7c. Mergeable PR with CI failure -> CI_FAILING (not conflicts)."""
        pr = _make_pr(mergeable="MERGEABLE", ci_status="FAILURE")
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CI_FAILING)

    def test_mergeable_unknown_not_classified_as_conflicts(self):
        """7d. PR with mergeable UNKNOWN -> falls through to CI/review priority."""
        pr = _make_pr(mergeable="UNKNOWN", ci_status="SUCCESS")
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_ci_failure_overrides_approval(self):
        """8. Approved PR with CI failure -> CI_FAILING."""
        pr = _make_pr(
            ci_status="FAILURE",
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CI_FAILING)

    def test_multiple_reviewers_blocking_wins(self):
        """8. One approved, one changes_requested -> CHANGES_REQUESTED."""
        pr = _make_pr(
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
                _make_review("carol", "CHANGES_REQUESTED", "2026-04-21T11:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.CHANGES_REQUESTED)
        self.assertEqual(result[0].reviewer_name, "carol")

    def test_dismissed_review_treated_as_no_review(self):
        """9. Dismissed review -> treated as no review from that author."""
        pr = _make_pr(
            reviews=[
                _make_review("bob", "APPROVED", "2026-04-20T10:00:00Z"),
                _make_review("bob", "DISMISSED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        # Bob's latest is DISMISSED, so no active review -> NEEDS_REVIEW
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_review_requests_no_reviews_needs_review(self):
        """10. PR with review_requests but no reviews -> NEEDS_REVIEW."""
        pr = _make_pr(
            reviews=[],
            review_requests=["bob", "carol"],
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_age_days_calculated(self):
        """Bonus: age_days is calculated correctly from created_at."""
        now = datetime.now(timezone.utc)
        five_days_ago = (now - timedelta(days=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        pr = _make_pr(created_at=five_days_ago)
        result = classify_prs([pr])
        self.assertEqual(result[0].age_days, 5)

    def test_bot_reviews_filtered(self):
        """Bonus: Bot reviews are filtered out and don't affect classification."""
        pr = _make_pr(
            reviews=[
                _make_review("dependabot[bot]", "APPROVED", "2026-04-21T10:00:00Z"),
                _make_review("github-actions[bot]", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        # Bot approvals don't count -> NEEDS_REVIEW
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_coderabbit_approval_alone_needs_review(self):
        """Bonus: CodeRabbit-only approval still needs human review."""
        pr = _make_pr(
            reviews=[
                _make_review("coderabbitai[bot]", "APPROVED", "2026-04-21T10:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.NEEDS_REVIEW)

    def test_coderabbit_plus_human_approval_is_approved(self):
        """Bonus: CodeRabbit + human approval -> APPROVED."""
        pr = _make_pr(
            reviews=[
                _make_review("coderabbitai[bot]", "APPROVED", "2026-04-21T10:00:00Z"),
                _make_review("bob", "APPROVED", "2026-04-21T11:00:00Z"),
            ],
            last_commit_date="2026-04-20T08:00:00Z",
        )
        result = classify_prs([pr])
        self.assertEqual(result[0].status, PRStatus.APPROVED)

    def test_latest_review_per_author_deduplication(self):
        """Bonus: _latest_review_per_author returns only the latest per author."""
        reviews = [
            _make_review("bob", "CHANGES_REQUESTED", "2026-04-20T10:00:00Z"),
            _make_review("bob", "APPROVED", "2026-04-21T10:00:00Z"),
        ]
        result = _latest_review_per_author(reviews)
        self.assertEqual(len(result), 1)
        self.assertEqual(result["bob"]["state"], "APPROVED")


if __name__ == "__main__":
    unittest.main()
