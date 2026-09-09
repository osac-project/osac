"""Formatters for classified PRs: Slack mrkdwn messages."""

from datetime import date

from models import ClassifiedPR, PRStatus

# Maximum PRs shown per repo before collapsing.
_MAX_PRS_PER_REPO = 6

_MAX_MESSAGE_LENGTH = 3900

# Emoji mapping for each PR status.
_STATUS_EMOJI: dict[PRStatus, str] = {
    PRStatus.NEEDS_REVIEW: ":eyes:",
    PRStatus.NEEDS_RE_REVIEW: ":warning:",
    PRStatus.CHANGES_REQUESTED: ":red_circle:",
    PRStatus.CI_FAILING: ":x:",
}

# Human-readable labels for each status.
_STATUS_LABEL: dict[PRStatus, str] = {
    PRStatus.NEEDS_REVIEW: "needs review",
    PRStatus.NEEDS_RE_REVIEW: "needs re-review",
    PRStatus.CHANGES_REQUESTED: "changes requested",
    PRStatus.CI_FAILING: "CI failing",
}


def _staleness_indicator(age_days: int) -> str:
    """Return a staleness emoji suffix based on PR age."""
    if age_days >= 14:
        return " :rotating_light:"
    if age_days >= 7:
        return " :hourglass:"
    return ""


def _escape_mrkdwn_text(value: str) -> str:
    """Escape untrusted text that is interpolated into Slack mrkdwn."""
    return value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def _format_pr_line(cpr: ClassifiedPR) -> str:
    """Format a single classified PR as a Slack mrkdwn line."""
    emoji = _STATUS_EMOJI[cpr.status]
    label = _STATUS_LABEL[cpr.status]

    # Append reviewer name for CHANGES_REQUESTED.
    if cpr.status == PRStatus.CHANGES_REQUESTED and cpr.reviewer_name:
        reviewer_link = f"<https://github.com/{cpr.reviewer_name}|{cpr.reviewer_name}>"
        label = f"changes requested by {reviewer_link}"

    stale = _staleness_indicator(cpr.age_days)
    author_link = f"<https://github.com/{cpr.pr.author}|{cpr.pr.author}>"

    title = _escape_mrkdwn_text(cpr.pr.title)
    return f"  {emoji} <{cpr.pr.url}|{title}> — {author_link} · {cpr.age_days}d · {label}{stale}"


_REVIEWABLE_STATUSES = frozenset(
    {PRStatus.NEEDS_REVIEW, PRStatus.NEEDS_RE_REVIEW, PRStatus.CHANGES_REQUESTED}
)


def format_message(classified_prs: list[ClassifiedPR], repos: list[str]) -> str:
    """Format classified PRs into a Slack mrkdwn message.

    Only reviewable PRs (needs review, needs re-review, changes requested) are
    listed individually.  CI-failing, draft, and approved PRs are summarized in
    a single footer line so the message stays focused on what the team can act on.

    Args:
        classified_prs: List of classified PRs to format.
        repos: Full list of monitored repos (used for empty-state count).

    Returns:
        Slack mrkdwn string ready for posting.
    """
    actionable = [
        cpr for cpr in classified_prs
        if cpr.status not in (PRStatus.DRAFT, PRStatus.APPROVED)
    ]
    reviewable = [cpr for cpr in actionable if cpr.status in _REVIEWABLE_STATUSES]
    ci_failing = [cpr for cpr in actionable if cpr.status == PRStatus.CI_FAILING]

    if not reviewable and not ci_failing:
        return f"All clear — no open PRs across {len(repos)} repos :tada:"

    # Group reviewable PRs by repo.
    prs_by_repo: dict[str, list[ClassifiedPR]] = {}
    for cpr in reviewable:
        prs_by_repo.setdefault(cpr.pr.repo, []).append(cpr)

    # Compute summary stats.
    needs_review = sum(
        1 for cpr in reviewable if cpr.status == PRStatus.NEEDS_REVIEW
    )
    stale = sum(1 for cpr in reviewable if cpr.age_days >= 7)

    today = date.today().strftime("%Y-%m-%d")
    header = f"*PR Status Summary* — {today}\n"
    header += f"{len(reviewable)} ready for review across {len(prs_by_repo)} repos"
    if needs_review:
        header += f" | {needs_review} need review"
    if stale:
        header += f" | {stale} stale (7+ days)"

    if not reviewable:
        n = len(ci_failing)
        footer = f"\n:x: {n} PR{'s' if n != 1 else ''} with CI failures (author action needed)"
        return header + footer

    # Build per-repo sections with per-repo cap.
    sections: list[str] = []
    for repo, prs in prs_by_repo.items():
        pulls_url = f"https://github.com/{repo}/pulls"
        repo_header = f"\n*<{pulls_url}|{repo}>* ({len(prs)})"
        shown = prs[:_MAX_PRS_PER_REPO]
        lines = [_format_pr_line(cpr) for cpr in shown]
        remaining = len(prs) - len(shown)
        if remaining > 0:
            lines.append(f"  _<{pulls_url}|... and {remaining} more>_")
        sections.append(repo_header + "\n" + "\n".join(lines))

    if ci_failing:
        n = len(ci_failing)
        sections.append(
            f"\n:x: {n} PR{'s' if n != 1 else ''} with CI failures (author action needed)"
        )

    message = header + "\n" + "\n".join(sections)

    if len(message) > _MAX_MESSAGE_LENGTH:
        while sections and len(message) > _MAX_MESSAGE_LENGTH:
            sections.pop()
            message = header + "\n" + "\n".join(sections)
            message += "\n_... additional repos truncated_"

    return message


# ---------------------------------------------------------------------------
# Compact Slack summary from pre-computed dashboard data.json
# ---------------------------------------------------------------------------

def format_summary_from_data(data: dict, dashboard_url: str) -> str:
    """Format a compact Slack summary from a dashboard data dict.

    Takes the pre-computed JSON structure (as served by GitHub Pages)
    rather than ClassifiedPR objects.
    """
    summary = data.get("summary", {})
    needs_review = summary.get("needs_review", 0)
    ci_failing = summary.get("ci_failing", 0)
    conflicts = summary.get("conflicts", 0)
    stale = summary.get("stale", 0)
    repo_count = len(data.get("repos", []))

    if not needs_review and not ci_failing and not conflicts:
        return (
            f"All clear - no actionable PRs across {repo_count} repos :tada:\n"
            f":chart_with_upwards_trend: <{dashboard_url}|Full PR Dashboard>"
        )

    parts = []
    if needs_review:
        parts.append(f"{needs_review} need review")
    if ci_failing:
        parts.append(f"{ci_failing} CI failing")
    if conflicts:
        parts.append(f"{conflicts} with conflicts")
    if stale:
        parts.append(f"{stale} stale (7+ days)")

    today = date.today().strftime("%Y-%m-%d")
    text = f"*PR Status* - {today} | " + " · ".join(parts)
    text += f"\n:chart_with_upwards_trend: <{dashboard_url}|Full PR Dashboard>"
    return text
