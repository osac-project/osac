import os
import re
import tomllib

from models import Config, DashboardConfig

_DASHBOARD_DATA_PATH_PATTERN = re.compile(
    r"docs/(?!\.)[A-Za-z0-9._-]+/data\.json"
)


def load_config(path: str) -> Config:
    """Load and validate a TOML configuration file.

    Args:
        path: Path to the TOML config file.

    Returns:
        A Config dataclass with validated fields.

    Raises:
        SystemExit: On missing file, missing required fields, or parse errors.
    """
    if not os.path.isfile(path):
        raise SystemExit(f"Config file not found: {path}")

    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except tomllib.TOMLDecodeError as e:
        raise SystemExit(f"Failed to parse TOML config '{path}': {e}")

    raw_repos = data.get("repos")
    if not isinstance(raw_repos, list) or not raw_repos:
        raise SystemExit(
            f"Field 'repos' must be a non-empty array in config '{path}'"
        )
    for repo in raw_repos:
        if not isinstance(repo, str) or not re.fullmatch(
            r"[A-Za-z0-9._-]+/[A-Za-z0-9._-]+", repo
        ):
            raise SystemExit(
                f"Invalid repository '{repo}' in config '{path}'; expected 'owner/name'"
            )

    dashboard = None
    if "dashboard" in data:
        d = data["dashboard"]
        if not isinstance(d, dict):
            raise SystemExit(
                f"Field 'dashboard' must be a table in config '{path}'"
            )
        for f in ("repo", "branch", "base_url"):
            if f not in d:
                raise SystemExit(
                    f"Missing required field 'dashboard.{f}' in config '{path}'"
                )
        data_path = d.get("data_path", "docs/pr-dashboard/data.json")
        if not isinstance(data_path, str) or not _DASHBOARD_DATA_PATH_PATTERN.fullmatch(
            data_path
        ):
            raise SystemExit(
                f"Field 'dashboard.data_path' must use 'docs/<dashboard>/data.json' in config '{path}'"
            )
        dashboard = DashboardConfig(
            repo=d["repo"],
            branch=d["branch"],
            base_url=d["base_url"],
            data_path=data_path,
        )

    raw_authors = data.get("filter_authors")
    if raw_authors is None:
        filter_authors = None
    elif not isinstance(raw_authors, list) or not all(
        isinstance(a, str) for a in raw_authors
    ):
        raise SystemExit(
            f"Field 'filter_authors' must be an array of strings in config '{path}'"
        )
    else:
        filter_authors = raw_authors

    return Config(
        repos=raw_repos,
        dashboard=dashboard,
        filter_authors=filter_authors,
        title=data.get("title"),
        description=data.get("description"),
    )
