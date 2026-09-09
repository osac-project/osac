"""Convert classified PRs into a JSON-serializable dict for the static dashboard."""

from datetime import datetime, timezone

from models import ClassifiedPR, DEFAULT_TITLE, PRStatus


_REVIEWABLE_STATUSES = frozenset(
    {PRStatus.NEEDS_REVIEW, PRStatus.NEEDS_RE_REVIEW, PRStatus.CHANGES_REQUESTED}
)


def format_dashboard_data(
    classified_prs: list[ClassifiedPR],
    repos: list[str],
    title: str = DEFAULT_TITLE,
) -> dict:
    """Build a dict suitable for JSON serialization and dashboard rendering.

    Args:
        classified_prs: List of classified PRs from all repos.
        repos: Full list of monitored repo slugs (owner/name).
        title: Dashboard title displayed in the HTML header.

    Returns:
        Dict with keys: title, generated_at, summary, repos, ci_health.
    """
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    reviewable = [
        cpr for cpr in classified_prs if cpr.status in _REVIEWABLE_STATUSES
    ]

    summary = {
        "needs_review": sum(
            1 for cpr in classified_prs
            if cpr.status in (PRStatus.NEEDS_REVIEW, PRStatus.NEEDS_RE_REVIEW)
        ),
        "ci_failing": sum(
            1 for cpr in classified_prs if cpr.status == PRStatus.CI_FAILING
        ),
        "conflicts": sum(
            1 for cpr in classified_prs if cpr.status == PRStatus.CONFLICTS
        ),
        "stale": sum(1 for cpr in reviewable if cpr.age_days >= 7),
        "approved": sum(
            1 for cpr in classified_prs if cpr.status == PRStatus.APPROVED
        ),
        "draft": sum(
            1 for cpr in classified_prs if cpr.status == PRStatus.DRAFT
        ),
    }

    prs_by_repo: dict[str, list[ClassifiedPR]] = {}
    for cpr in classified_prs:
        prs_by_repo.setdefault(cpr.pr.repo, []).append(cpr)

    repo_data = []
    for repo_name in sorted(prs_by_repo):
        prs = prs_by_repo[repo_name]
        repo_data.append({
            "name": repo_name,
            "pulls_url": f"https://github.com/{repo_name}/pulls",
            "prs": [_serialize_pr(cpr) for cpr in prs],
        })

    ci_health = []
    for repo_name in sorted(set(cpr.pr.repo for cpr in classified_prs)):
        repo_prs = [cpr for cpr in classified_prs if cpr.pr.repo == repo_name]
        all_checks = [cr for cpr in repo_prs for cr in cpr.pr.check_runs]
        if not all_checks:
            continue
        passed = sum(1 for cr in all_checks if cr.conclusion == "SUCCESS")
        total = len(all_checks)
        ci_health.append({
            "repo": repo_name,
            "total_checks": total,
            "passed": passed,
            "pass_rate": int(passed / total * 100) if total else 0,
        })

    return {
        "title": title,
        "generated_at": now,
        "summary": summary,
        "repos": repo_data,
        "ci_health": ci_health,
    }


def _serialize_pr(cpr: ClassifiedPR) -> dict:
    return {
        "title": cpr.pr.title,
        "url": cpr.pr.url,
        "author": cpr.pr.author,
        "status": cpr.status.value,
        "age_days": cpr.age_days,
        "reviewer_name": cpr.reviewer_name,
        "check_runs": [
            {
                "name": cr.name,
                "conclusion": cr.conclusion,
                "details_url": cr.details_url,
            }
            for cr in cpr.pr.check_runs
        ],
    }
