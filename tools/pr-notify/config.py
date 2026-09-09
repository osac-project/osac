import os
import tomllib

from models import Config, DashboardConfig


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

    if "repos" not in data:
        raise SystemExit(f"Missing required field 'repos' in config '{path}'")

    raw_creds_dir = data.get("slack_creds_dir")
    slack_creds_dir = os.path.expanduser(raw_creds_dir) if raw_creds_dir else None

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
        dashboard = DashboardConfig(
            repo=d["repo"],
            branch=d["branch"],
            base_url=d["base_url"],
            data_path=d.get("data_path", "docs/pr-dashboard/data.json"),
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
        repos=data["repos"],
        slack_channel=data.get("slack_channel"),
        slack_creds_dir=slack_creds_dir,
        dashboard=dashboard,
        filter_authors=filter_authors,
        title=data.get("title"),
        description=data.get("description"),
    )
