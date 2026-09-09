#!/usr/bin/env python3
"""Generate PR dashboard data -- fetch open PRs, classify, and write data.json."""

import argparse
import json
import logging
import sys
from datetime import datetime, timezone

from classifier import classify_prs
from config import load_config
from data_formatter import format_dashboard_data
from github import fetch_open_prs
from models import DEFAULT_TITLE


def setup_logging() -> None:
    log_file = f"/tmp/pr-generate-{datetime.now(timezone.utc).strftime('%Y-%m-%d')}.log"
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        handlers=[
            logging.StreamHandler(sys.stderr),
            logging.FileHandler(log_file, mode="a"),
        ],
    )
    logging.getLogger(__name__).info("Log file: %s", log_file)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Fetch open PRs, classify, and write dashboard data.json"
    )
    parser.add_argument("--config", required=True, help="Path to TOML config file")
    parser.add_argument("--output", required=True, help="Path to write data.json")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print JSON to stdout instead of writing to file",
    )
    parser.add_argument(
        "--print-data-path",
        action="store_true",
        help="Print the dashboard.data_path from the config and exit",
    )
    args = parser.parse_args()

    setup_logging()
    logger = logging.getLogger(__name__)

    try:
        config = load_config(args.config)

        if args.print_data_path:
            data_path = config.dashboard.data_path if config.dashboard else "docs/pr-dashboard/data.json"
            print(data_path)
            return 0

        logger.info("Loaded config: %d repos", len(config.repos))

        prs = fetch_open_prs(config.repos)
        logger.info("Fetched %d open PRs across %d repos", len(prs), len(config.repos))

        classified = classify_prs(prs)
        logger.info("Classified %d PRs", len(classified))

        if config.filter_authors:
            allowed = {a.lower() for a in config.filter_authors}
            classified = [cpr for cpr in classified if cpr.pr.author.lower() in allowed]
            logger.info(
                "Filtered to %d PRs by %d team members",
                len(classified),
                len(allowed),
            )

        data = format_dashboard_data(
            classified, config.repos, title=config.title or DEFAULT_TITLE
        )
        data_json = json.dumps(data, indent=2)
        logger.info("Generated dashboard data (%d chars)", len(data_json))

        if args.dry_run:
            print(data_json)
        else:
            with open(args.output, "w") as f:
                f.write(data_json + "\n")
            logger.info("Wrote data.json to %s", args.output)

        return 0

    except SystemExit as e:
        logger.error("Fatal: %s", e)
        return 1
    except Exception:
        logger.exception("Unexpected error")
        return 1


if __name__ == "__main__":
    sys.exit(main())
