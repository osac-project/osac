#!/usr/bin/env python3
"""Generate dashboards.json manifest from all config*.example.toml files."""

import argparse
import glob
import json
import os
import sys
import tomllib

from models import DEFAULT_TITLE


def slug_from_data_path(data_path: str) -> str:
    """Derive the dashboard URL slug from a data_path like 'docs/pr-dashboard/data.json'."""
    return os.path.basename(os.path.dirname(data_path))


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Generate dashboards.json manifest from config files"
    )
    parser.add_argument("--output", required=True, help="Path to write dashboards.json")
    args = parser.parse_args()

    configs = sorted(glob.glob("config*.example.toml"))
    if not configs:
        print("No config*.example.toml files found", file=sys.stderr)
        return 1

    dashboards = []
    for path in configs:
        with open(path, "rb") as f:
            data = tomllib.load(f)

        dashboard_section = data.get("dashboard", {})
        data_path = dashboard_section.get("data_path", "docs/pr-dashboard/data.json")
        slug = slug_from_data_path(data_path)
        if not slug:
            print(f"Skipping {path}: cannot derive slug from data_path '{data_path}'", file=sys.stderr)
            continue

        dashboards.append({
            "title": data.get("title") or DEFAULT_TITLE,
            "description": data.get("description", ""),
            "slug": slug,
        })

    manifest = json.dumps(dashboards, indent=2)

    if args.output == "/dev/stdout":
        print(manifest)
    else:
        os.makedirs(os.path.dirname(args.output) or ".", exist_ok=True)
        with open(args.output, "w") as f:
            f.write(manifest + "\n")
        print(f"Wrote {len(dashboards)} dashboards to {args.output}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())
