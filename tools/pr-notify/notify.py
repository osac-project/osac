#!/usr/bin/env python3
"""Post PR dashboard summary to Slack -- reads pre-generated data.json from GitHub Pages."""

import argparse
import json
import logging
import sys
import urllib.request
from datetime import datetime, timezone

from config import load_config
from formatter import format_summary_from_data
from slack import already_posted_today, post_message


def setup_logging() -> None:
    log_file = f"/tmp/pr-notify-{datetime.now(timezone.utc).strftime('%Y-%m-%d')}.log"
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        handlers=[
            logging.StreamHandler(sys.stderr),
            logging.FileHandler(log_file, mode="a"),
        ],
    )
    logging.getLogger(__name__).info("Log file: %s", log_file)


def _fetch_dashboard_data(url: str) -> dict:
    """Fetch and parse data.json from a URL."""
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode())


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Read dashboard data.json and post compact summary to Slack"
    )
    parser.add_argument("--config", required=True, help="Path to TOML config file")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print formatted message to stdout instead of posting to Slack",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Post even if already posted today",
    )
    args = parser.parse_args()

    setup_logging()
    logger = logging.getLogger(__name__)

    try:
        config = load_config(args.config)

        if not config.dashboard:
            raise SystemExit("[dashboard] section required in config")
        if not config.slack_channel or not config.slack_creds_dir:
            raise SystemExit("slack_channel and slack_creds_dir required in config")

        data_url = config.dashboard.base_url.rstrip("/") + "/data.json"
        dashboard_url = config.dashboard.base_url.rstrip("/") + "/"

        logger.info("Fetching dashboard data from %s", data_url)
        data = _fetch_dashboard_data(data_url)
        logger.info(
            "Loaded data (generated_at=%s)", data.get("generated_at", "unknown")
        )

        message = format_summary_from_data(data, dashboard_url)
        logger.info("Compact summary (%d chars)", len(message))

        if args.dry_run:
            print(message)
        else:
            if not args.force and already_posted_today(config.slack_channel, config.slack_creds_dir):
                logger.info("Already posted today - skipping (use --force to override)")
                return 0
            post_message(config.slack_channel, message, config.slack_creds_dir)
            logger.info("Posted to Slack")

        return 0

    except SystemExit as e:
        logger.error("Fatal: %s", e)
        return 1
    except Exception:
        logger.exception("Unexpected error")
        return 1


if __name__ == "__main__":
    sys.exit(main())
