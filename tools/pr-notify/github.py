import json
import logging
import subprocess
import time

from models import CheckRun, PRData

logger = logging.getLogger(__name__)

INITIAL_BACKOFF_SECONDS = 2
MAX_RETRIES = 3
DELAY_BETWEEN_QUERIES_SECONDS = 1
PR_PAGE_SIZE = 50
DETAIL_PAGE_SIZE = 100
DETAIL_BATCH_SIZE = 5
PAGE_BATCH_SIZE = 5
CONNECTION_CONFIG = {
    "labels": {"direction": "forward"},
    "reviews": {"direction": "backward"},
    "reviewRequests": {"direction": "forward"},
    "contexts": {"direction": "forward"},
}
PAGINATED_CONNECTIONS = tuple(CONNECTION_CONFIG)


class GitHubFetchError(RuntimeError):
    """Raised when a complete PR dashboard snapshot cannot be collected."""


def _build_graphql_query(repos: list[str], after: str | None = None) -> str:
    """Build a single GraphQL query fetching open PRs from multiple repos.

    Each repo gets an aliased sub-query (repo_0, repo_1, etc.) so all data
    comes back in one API call.
    """
    repo_fragments = []
    after_clause = f", after: {json.dumps(after)}" if after is not None else ""
    for idx, repo in enumerate(repos):
        owner, name = repo.split("/", 1)
        alias = f"repo_{idx}"
        repo_fragments.append(f"""
    {alias}: repository(owner: "{owner}", name: "{name}") {{
      nameWithOwner
      pullRequests(states: OPEN, first: {PR_PAGE_SIZE}{after_clause}) {{
        totalCount
        pageInfo {{ hasNextPage endCursor }}
        nodes {{
          number
          title
          body
          url
          author {{ login }}
          createdAt
          isDraft
          mergeable
          {_connection_fields("labels", "first: 20")}
          {_connection_fields("reviews", "last: 20")}
          {_connection_fields("reviewRequests", "first: 10")}
          commits(last: 1) {{
            nodes {{
              commit {{
                committedDate
                statusCheckRollup {{
                  state
                  {_connection_fields("contexts", "first: 20")}
                }}
              }}
            }}
          }}
        }}
      }}
    }}""")

    return "{\n" + "\n".join(repo_fragments) + "\n}"


def _connection_fields(connection: str, page_args: str) -> str:
    """Return the GraphQL fields for one paginated PR connection."""
    direction = CONNECTION_CONFIG[connection]["direction"]
    page_info = (
        "hasPreviousPage startCursor"
        if direction == "backward"
        else "hasNextPage endCursor"
    )
    if connection == "labels":
        return f"""
    labels({page_args}) {{
      pageInfo {{ {page_info} }}
      nodes {{ name }}
    }}"""
    if connection == "reviews":
        return f"""
    reviews({page_args}) {{
      pageInfo {{ {page_info} }}
      nodes {{
        author {{ login }}
        state
        submittedAt
      }}
    }}"""
    if connection == "reviewRequests":
        return f"""
    reviewRequests({page_args}) {{
      pageInfo {{ {page_info} }}
      nodes {{
        requestedReviewer {{
          ... on User {{ login }}
        }}
      }}
    }}"""
    if connection == "contexts":
        return f"""
    contexts({page_args}) {{
      pageInfo {{ {page_info} }}
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
    }}"""
    raise ValueError(f"Unsupported connection: {connection}")


def _context_connection_fields(page_args: str) -> str:
    """Return the commits wrapper containing the check-context connection."""
    return f"""
      commits(last: 1) {{
        nodes {{
          commit {{
            statusCheckRollup {{
              {_connection_fields("contexts", page_args)}
            }}
          }}
        }}
      }}"""


def _connection_page_args(connection: str, cursor: str) -> str:
    """Return GraphQL pagination arguments for a connection cursor."""
    if CONNECTION_CONFIG[connection]["direction"] == "backward":
        return f"last: {DETAIL_PAGE_SIZE}, before: {json.dumps(cursor)}"
    return f"first: {DETAIL_PAGE_SIZE}, after: {json.dumps(cursor)}"


def _build_pr_details_query(
    repo: str, connections_by_number: dict[int, set[str]]
) -> str:
    """Build a bounded query for overflowed PR connections."""
    owner, name = repo.split("/", 1)
    aliases = []
    for number, connections in connections_by_number.items():
        alias = f"pr_{number}"
        fields = []
        for connection in ("labels", "reviews", "reviewRequests"):
            if connection in connections:
                page_args = (
                    f"last: {DETAIL_PAGE_SIZE}"
                    if CONNECTION_CONFIG[connection]["direction"] == "backward"
                    else f"first: {DETAIL_PAGE_SIZE}"
                )
                fields.append(_connection_fields(connection, page_args))
        if "contexts" in connections:
            fields.append(
                _context_connection_fields(f"first: {DETAIL_PAGE_SIZE}")
            )
        aliases.append(
            f"""
    {alias}: pullRequest(number: {number}) {{
      {''.join(fields)}
    }}"""
        )
    return (
        "{\n  repository(owner: "
        + json.dumps(owner)
        + ", name: "
        + json.dumps(name)
        + ") {\n"
        + "\n".join(aliases)
        + "\n  }\n}"
    )


def _build_connection_page_query(repo: str, requests: list[dict]) -> str:
    """Build one query for independent pages across multiple PR connections."""
    owner, name = repo.split("/", 1)
    aliases = []
    for request in requests:
        connection = request["connection"]
        page_args = _connection_page_args(connection, request["cursor"])
        fields = (
            _context_connection_fields(page_args)
            if connection == "contexts"
            else _connection_fields(connection, page_args)
        )
        aliases.append(f"""
    {request["alias"]}: pullRequest(number: {request["number"]}) {{
      {fields}
    }}""")
    return f"""
{{
  repository(owner: {json.dumps(owner)}, name: {json.dumps(name)}) {{
    {''.join(aliases)}
  }}
}}"""


def _extract_connection(pr: dict, connection: str) -> dict:
    """Extract a connection from a pull request node."""
    if connection != "contexts":
        return pr.get(connection, {})
    commits = pr.get("commits", {}).get("nodes", [])
    if not commits:
        return {}
    rollup = commits[0].get("commit", {}).get("statusCheckRollup")
    return rollup.get("contexts", {}) if rollup else {}


def _connection_has_more(connection: str, page_info: dict) -> bool:
    """Return whether a connection has another page in its direction."""
    if CONNECTION_CONFIG[connection]["direction"] == "backward":
        return page_info.get("hasPreviousPage", False)
    return page_info.get("hasNextPage", False)


def _connection_cursor(connection: str, page_info: dict) -> str | None:
    """Return the cursor needed to fetch the next connection page."""
    cursor_name = (
        "startCursor"
        if CONNECTION_CONFIG[connection]["direction"] == "backward"
        else "endCursor"
    )
    return page_info.get(cursor_name)


def _new_connection_state(
    repo: str, pr_number: int, connection: str, initial: dict
) -> dict:
    """Validate and initialize state for one paginated connection."""
    page_info = initial.get("pageInfo", {})
    direction = (
        "hasPreviousPage"
        if CONNECTION_CONFIG[connection]["direction"] == "backward"
        else "hasNextPage"
    )
    if direction not in page_info:
        raise GitHubFetchError(
            f"Pull request {repo}#{pr_number} response omitted {connection} "
            "pagination metadata"
        )
    return {
        "pages": [list(initial.get("nodes", []))],
        "page_info": page_info,
    }


def _fetch_connection_pages(repo: str, states: dict[tuple[int, str], dict]) -> None:
    """Fetch remaining pages for independent PR connections in batches."""
    while True:
        requests = []
        for (pr_number, connection), state in states.items():
            if not _connection_has_more(connection, state["page_info"]):
                continue
            cursor = _connection_cursor(connection, state["page_info"])
            if not cursor:
                raise GitHubFetchError(
                    f"Pull request {repo}#{pr_number} has another {connection} "
                    "page but no cursor"
                )
            requests.append(
                {
                    "alias": f"page_{pr_number}_{connection}",
                    "number": pr_number,
                    "connection": connection,
                    "cursor": cursor,
                }
            )

        if not requests:
            break

        for start in range(0, len(requests), PAGE_BATCH_SIZE):
            batch = requests[start:start + PAGE_BATCH_SIZE]
            response = _run_graphql_query(_build_connection_page_query(repo, batch))
            repository = response.get("data", {}).get("repository")
            if repository is None:
                raise GitHubFetchError(f"No data returned for repository '{repo}'")

            for request in batch:
                pull_request = repository.get(request["alias"])
                if pull_request is None:
                    raise GitHubFetchError(
                        f"No data returned for pull request {repo}#{request['number']}"
                    )
                page = _extract_connection(pull_request, request["connection"])
                page_info = page.get("pageInfo", {})
                direction = (
                    "hasPreviousPage"
                    if CONNECTION_CONFIG[request["connection"]]["direction"]
                    == "backward"
                    else "hasNextPage"
                )
                if direction not in page_info:
                    raise GitHubFetchError(
                        f"Pull request {repo}#{request['number']} response omitted "
                        f"{request['connection']} pagination metadata"
                    )
                state = states[(request["number"], request["connection"])]
                state["pages"].append(page.get("nodes", []))
                state["page_info"] = page_info

    for (pr_number, connection), state in states.items():
        pages = state["pages"]
        if CONNECTION_CONFIG[connection]["direction"] == "backward":
            pages = reversed(pages)
        state["result"] = {
            "pageInfo": {"hasNextPage": False, "hasPreviousPage": False},
            "nodes": [node for page in pages for node in page],
        }


def _overflowing_connections(pr: dict) -> set[str]:
    """Return the nested PR connections that need an overflow query."""
    return {
        connection
        for connection in PAGINATED_CONNECTIONS
        if _connection_has_more(
            connection, _extract_connection(pr, connection).get("pageInfo", {})
        )
    }


def _hydrate_overflowing_connections(repo: str, pr_nodes: list[dict]) -> None:
    """Complete nested connections only for PRs whose first page overflowed."""
    connections_by_number = {}
    for pr in pr_nodes:
        connections = _overflowing_connections(pr)
        if connections:
            connections_by_number[pr.get("number")] = connections
    for number in connections_by_number:
        if not number:
            raise GitHubFetchError("Cannot paginate PR connections without PR number")
    numbers = list(connections_by_number)
    pr_by_number = {pr.get("number"): pr for pr in pr_nodes}
    for start in range(0, len(numbers), DETAIL_BATCH_SIZE):
        batch = numbers[start:start + DETAIL_BATCH_SIZE]
        batch_connections = {
            number: connections_by_number[number] for number in batch
        }
        response = _run_graphql_query(
            _build_pr_details_query(repo, batch_connections)
        )
        repository = response.get("data", {}).get("repository")
        if repository is None:
            raise GitHubFetchError(f"No data returned for repository '{repo}'")
        details_by_number = {}
        for number in batch:
            details = repository.get(f"pr_{number}")
            if details is None:
                raise GitHubFetchError(
                    f"No data returned for pull request {repo}#{number}"
                )
            details_by_number[number] = details

        states = {}
        for number, details in details_by_number.items():
            for connection in batch_connections[number]:
                initial = (
                    _extract_connection(details, "contexts")
                    if connection == "contexts"
                    else details.get(connection)
                )
                if initial is None:
                    raise GitHubFetchError(
                        f"Pull request #{number} response omitted {connection} data"
                    )
                states[(number, connection)] = _new_connection_state(
                    repo, number, connection, initial
                )

        _fetch_connection_pages(repo, states)
        for (number, connection), state in states.items():
            pr = pr_by_number[number]
            if connection == "contexts":
                commits = pr.get("commits", {}).get("nodes", [])
                rollup = (
                    commits[0].get("commit", {}).get("statusCheckRollup")
                    if commits
                    else None
                )
                if rollup is None:
                    raise GitHubFetchError(
                        f"Pull request {repo}#{number} omitted check status data"
                    )
                rollup["contexts"] = state["result"]
            else:
                pr[connection] = state["result"]


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
                    "PR '%s' has incomplete check contexts; refusing to publish "
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
                "PR '%s' has incomplete review history; refusing to publish a "
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
                body=pr.get("body", ""),
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
    """Fetch all open PRs for a single repo with rate-limit handling."""
    all_pr_nodes = []
    cursor = None
    repo_name = repo

    while True:
        query = _build_graphql_query([repo], after=cursor)
        logger.debug("Fetching PRs for %s%s", repo, " (next page)" if cursor else "")
        response = _run_graphql_query(query)

        data = response.get("data")
        if not data:
            raise GitHubFetchError(f"No data in GitHub GraphQL response for '{repo}'")

        repo_data = data.get("repo_0")
        if repo_data is None:
            raise GitHubFetchError(f"No data returned for repository '{repo}'")

        repo_name = repo_data.get("nameWithOwner", repo)
        pr_data = repo_data.get("pullRequests", {})
        all_pr_nodes.extend(pr_data.get("nodes", []))
        page_info = pr_data.get("pageInfo", {})
        if "hasNextPage" not in page_info:
            raise GitHubFetchError(
                f"Repository '{repo_name}' response omitted PR pagination metadata"
            )
        if not page_info.get("hasNextPage", False):
            _hydrate_overflowing_connections(repo, all_pr_nodes)
            return _parse_pr_nodes(repo_name, all_pr_nodes)

        cursor = page_info.get("endCursor")
        if not cursor:
            raise GitHubFetchError(
                f"Repository '{repo_name}' has another PR page but no cursor"
            )
        time.sleep(DELAY_BETWEEN_QUERIES_SECONDS)


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
