#!/usr/bin/env python3
"""Generate every configured dashboard from one GitHub PR snapshot."""

import argparse
import glob
import json
import logging
import sys
from pathlib import Path

from config import load_config
from generate import build_dashboard_data, setup_logging
from github import fetch_open_prs

DEFAULT_DATA_PATH = "docs/pr-dashboard/data.json"


def output_path(output_root: Path, data_path: str) -> Path:
    """Return a dashboard artifact path contained within the output root."""
    path = Path(data_path)
    if path.is_absolute() or ".." in path.parts:
        raise ValueError(f"Dashboard data path must be relative: {data_path}")
    return output_root / path


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Generate every configured PR dashboard from one snapshot"
    )
    parser.add_argument(
        "--config-glob",
        default="config*.example.toml",
        help="Glob matching dashboard configuration files",
    )
    parser.add_argument(
        "--output-root",
        required=True,
        help="Directory under which dashboard data paths are written",
    )
    args = parser.parse_args()

    setup_logging()
    logger = logging.getLogger(__name__)

    try:
        config_paths = sorted(glob.glob(args.config_glob))
        if not config_paths:
            raise ValueError(f"No dashboard configurations matched: {args.config_glob}")

        configs = [load_config(path) for path in config_paths]
        repositories = list(
            dict.fromkeys(repo for config in configs for repo in config.repos)
        )
        prs = fetch_open_prs(repositories)
        logger.info(
            "Fetched %d open PRs across %d repositories for %d dashboards",
            len(prs),
            len(repositories),
            len(configs),
        )

        output_root = Path(args.output_root)
        for config in configs:
            data_path = (
                config.dashboard.data_path if config.dashboard else DEFAULT_DATA_PATH
            )
            destination = output_path(output_root, data_path)
            destination.parent.mkdir(parents=True, exist_ok=True)
            with destination.open("w") as data_file:
                json.dump(build_dashboard_data(prs, config), data_file, indent=2)
                data_file.write("\n")
            logger.info("Wrote dashboard data to %s", destination)

        return 0
    except SystemExit as error:
        logger.error("Fatal: %s", error)
        return 1
    except Exception:
        logger.exception("Unexpected error")
        return 1


if __name__ == "__main__":
    sys.exit(main())
