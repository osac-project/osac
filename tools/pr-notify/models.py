from dataclasses import dataclass, field
from enum import Enum

DEFAULT_TITLE = "OSAC PR Dashboard"


class PRStatus(Enum):
    NEEDS_REVIEW = "needs_review"
    NEEDS_RE_REVIEW = "needs_re_review"
    CHANGES_REQUESTED = "changes_requested"
    APPROVED = "approved"
    CI_FAILING = "ci_failing"
    CONFLICTS = "conflicts"
    DRAFT = "draft"


@dataclass
class DashboardConfig:
    repo: str
    branch: str
    base_url: str
    data_path: str = "docs/pr-dashboard/data.json"


@dataclass
class Config:
    repos: list[str]
    slack_channel: str | None = None
    slack_creds_dir: str | None = None
    dashboard: DashboardConfig | None = None
    filter_authors: list[str] | None = None
    title: str | None = None
    description: str | None = None


@dataclass
class CheckRun:
    name: str
    conclusion: str | None
    details_url: str


@dataclass
class PRData:
    title: str
    url: str
    author: str
    repo: str
    created_at: str
    is_draft: bool
    labels: list[str]
    reviews: list[dict]
    review_requests: list[str]
    last_commit_date: str
    ci_status: str | None
    mergeable: str | None = None
    check_runs: list[CheckRun] = field(default_factory=list)


@dataclass
class ClassifiedPR:
    pr: PRData
    status: PRStatus
    age_days: int
    reviewer_name: str | None = None
