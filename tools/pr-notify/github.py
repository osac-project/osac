import json
import logging
import subprocess
import time

from models import CheckRun, PRData

logger = logging.getLogger(__name__)

INITIAL_BACKOFF_SECONDS = 2
MAX_RETRIES = 3
DELAY_BETWEEN_QUERIES_SECONDS = 1


class GitHubFetchError(RuntimeError):
    """Raised when a complete PR dashboard snapshot cannot be collected."""


def _build_graphql_query(repos: list[str]) -> str:
    """Build a single GraphQL query fetching open PRs from multiple repos.

    Each repo gets an aliased sub-query (repo_0, repo_1, etc.) so all data
    comes back in one API call.
    """
    repo_fragments = []
    for idx, repo in enumerate(repos):
        owner, name = repo.split("/", 1)
        alias = f"repo_{idx}"
        repo_fragments.append(f"""
    {alias}: repository(owner: "{owner}", name: "{name}") {{
      nameWithOwner
      pullRequests(states: OPEN, first: 50) {{
        totalCount
        pageInfo {{ hasNextPage }}
        nodes {{
          title
          url
          author {{ login }}
          createdAt
          isDraft
          mergeable
          labels(first: 20) {{ nodes {{ name }} }}
          reviews(last: 20) {{
            pageInfo {{ hasPreviousPage }}
            nodes {{
              author {{ login }}
              state
              submittedAt
            }}
          }}
          reviewRequests(first: 10) {{
            nodes {{
              requestedReviewer {{
                ... on User {{ login }}
              }}
            }}
          }}
          commits(last: 1) {{
            nodes {{
              commit {{
                committedDate
                statusCheckRollup {{
                  state
                  contexts(first: 20) {{
                    pageInfo {{ hasNextPage }}
                    nodes {{
                      __typename
                      ... on CheckRun {{
                        name
                        conclusion
                        detailsUrl
                      }}
                      ... on StatusContext {{
                        context
                        state
                        targetUrl
                      }}
                    }}
                  }}
                }}
              }}
            }}
          }}
        }}
      }}
    }}""")

    return "{\n" + "\n".join(repo_fragments) + "\n}"


def _parse_pr_nodes(repo_name: str, pr_nodes: list[dict]) -> list[PRData]:
    """Convert raw GraphQL PR nodes into PRData dataclass instances."""
    results = []
    for pr in pr_nodes:
        # Extract last commit info
        commit_nodes = pr.get("commits", {}).get("nodes", [])
        last_commit = commit_nodes[0]["commit"] if commit_nodes else {}
        last_commit_date = last_commit.get("committedDate", "")

        # CI status and individual checks from statusCheckRollup
        rollup = last_commit.get("statusCheckRollup")
        ci_status = rollup.get("state") if rollup else None

        _STATUS_CONTEXT_STATE_MAP = {
            "SUCCESS": "SUCCESS",
            "FAILURE": "FAILURE",
            "ERROR": "FAILURE",
            "EXPECTED": None,
            "PENDING": None,
        }

        check_runs = []
        if rollup:
            contexts_data = rollup.get("contexts", {})
            if contexts_data.get("pageInfo", {}).get("hasNextPage"):
                raise GitHubFetchError(
                    "PR '%s' has more than 20 check contexts; refusing to publish "
                    "a truncated dashboard snapshot"
                    % pr.get("title", "")
                )
            for node in contexts_data.get("nodes", []):
                typename = node.get("__typename")
                if typename == "CheckRun":
                    check_runs.append(CheckRun(
                        name=node.get("name", ""),
                        conclusion=node.get("conclusion"),
                        details_url=node.get("detailsUrl", ""),
                    ))
                elif typename == "StatusContext":
                    check_runs.append(CheckRun(
                        name=node.get("context", ""),
                        conclusion=_STATUS_CONTEXT_STATE_MAP.get(
                            node.get("state", ""), None
                        ),
                        details_url=node.get("targetUrl") or "",
                    ))

        # Reviews
        reviews_data = pr.get("reviews", {})
        if reviews_data.get("pageInfo", {}).get("hasPreviousPage"):
            raise GitHubFetchError(
                "PR '%s' has more than 20 reviews; refusing to publish a "
                "truncated dashboard snapshot"
                % pr.get("title", "")
            )
        review_nodes = reviews_data.get("nodes", [])
        reviews = [
            {
                "author": r.get("author", {}).get("login", "unknown"),
                "state": r.get("state", ""),
                "submitted_at": r.get("submittedAt", ""),
            }
            for r in review_nodes
            if r.get("author")
        ]

        # Review requests
        rr_nodes = pr.get("reviewRequests", {}).get("nodes", [])
        review_requests = [
            rr.get("requestedReviewer", {}).get("login", "")
            for rr in rr_nodes
            if rr.get("requestedReviewer") and rr["requestedReviewer"].get("login")
        ]

        # Labels
        label_nodes = pr.get("labels", {}).get("nodes", [])
        labels = [label.get("name", "") for label in label_nodes]

        author_obj = pr.get("author") or {}
        results.append(
            PRData(
                title=pr.get("title", ""),
                url=pr.get("url", ""),
                author=author_obj.get("login", "ghost"),
                repo=repo_name,
                created_at=pr.get("createdAt", ""),
                is_draft=pr.get("isDraft", False),
                labels=labels,
                reviews=reviews,
                review_requests=review_requests,
                last_commit_date=last_commit_date,
                ci_status=ci_status,
                mergeable=pr.get("mergeable"),
                check_runs=check_runs,
            )
        )
    return results


def _run_graphql_query(query: str) -> dict:
    """Execute a GraphQL query via gh CLI with exponential backoff on rate limits.

    Returns the parsed JSON response data dict.
    Raises GitHubFetchError on auth errors or persistent failures.
    """
    backoff = INITIAL_BACKOFF_SECONDS
    for attempt in range(1, MAX_RETRIES + 1):
        try:
            result = subprocess.run(
                ["gh", "api", "graphql", "-f", f"query={query}"],
                capture_output=True,
                text=True,
                timeout=60,
            )
        except subprocess.TimeoutExpired:
            raise GitHubFetchError("GitHub GraphQL query timed out after 60 seconds")

        try:
            response = json.loads(result.stdout)
        except (json.JSONDecodeError, ValueError):
            if result.returncode != 0:
                error_msg = result.stderr.strip() or result.stdout.strip()
                raise GitHubFetchError(f"GitHub GraphQL query failed: {error_msg}")
            raise GitHubFetchError("Failed to parse GitHub API response (malformed JSON)")

        if "errors" in response:
            is_rate_limit = False
            for err in response["errors"]:
                msg = err.get("message", str(err))
                if any(s in msg.lower() for s in ("auth", "forbidden", "unauthorized")):
                    raise GitHubFetchError(f"GitHub auth error: {msg}")
                if "resource limits" in msg.lower() or "rate limit" in msg.lower():
                    is_rate_limit = True

            if is_rate_limit and attempt < MAX_RETRIES:
                logger.warning(
                    "GraphQL resource limits exceeded (attempt %d/%d), retrying in %ds",
                    attempt, MAX_RETRIES, backoff,
                )
                time.sleep(backoff)
                backoff *= 2
                continue

            errors = "; ".join(
                err.get("message", str(err)) for err in response["errors"]
            )
            raise GitHubFetchError(f"GitHub GraphQL query returned errors: {errors}")

        return response

    raise GitHubFetchError("GitHub GraphQL query exhausted its retry budget")


def _fetch_repo_prs(repo: str) -> list[PRData]:
    """Fetch open PRs for a single repo with rate-limit handling."""
    query = _build_graphql_query([repo])
    logger.debug("Fetching PRs for %s", repo)

    response = _run_graphql_query(query)

    data = response.get("data")
    if not data:
        raise GitHubFetchError(f"No data in GitHub GraphQL response for '{repo}'")

    repo_data = data.get("repo_0")
    if repo_data is None:
        raise GitHubFetchError(f"No data returned for repository '{repo}'")

    repo_name = repo_data.get("nameWithOwner", repo)
    pr_data = repo_data.get("pullRequests", {})
    total_count = pr_data.get("totalCount", 0)
    has_next = pr_data.get("pageInfo", {}).get("hasNextPage", False)
    if has_next:
        raise GitHubFetchError(
            "Repository '%s' has %d open PRs but the current query only fetches "
            "the first 50"
            % (
            repo_name, total_count,
            )
        )
    pr_nodes = pr_data.get("nodes", [])
    return _parse_pr_nodes(repo_name, pr_nodes)


def fetch_open_prs(repos: list[str]) -> list[PRData]:
    """Fetch all open PRs from the given repos, one repo at a time.

    Queries repos individually to avoid GraphQL resource limits on large
    queries. Adds a small delay between requests to stay under rate limits.

    Args:
        repos: List of "owner/name" repository identifiers.

    Returns:
        List of PRData for all open PRs across all repos.
    """
    all_prs: list[PRData] = []
    for idx, repo in enumerate(repos):
        if idx > 0:
            time.sleep(DELAY_BETWEEN_QUERIES_SECONDS)
        prs = _fetch_repo_prs(repo)
        all_prs.extend(prs)

    return all_prs
