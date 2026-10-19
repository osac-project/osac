#!/usr/bin/env python3
"""Read the isolated SQLite state used by the pool integration target."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path


def snapshot(path: Path) -> None:
    uri = f"{path.resolve().as_uri()}?mode=ro"
    with sqlite3.connect(uri, uri=True) as connection:
        connection.row_factory = sqlite3.Row
        version = connection.execute("PRAGMA user_version").fetchone()[0]
        tables = sorted(
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        )
        pools = [
            dict(row)
            for row in connection.execute(
                """SELECT uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr
                   FROM external_ip_pools ORDER BY uid"""
            )
        ]
    print(
        json.dumps(
            {
                "user_version": version,
                "tables": tables,
                "pools": pools,
            },
            sort_keys=True,
        )
    )


def main() -> None:
    if len(sys.argv) < 3:
        raise SystemExit("usage: pool_state.py snapshot STATE_FILE")
    action = sys.argv[1]
    path = Path(sys.argv[2])
    if action == "snapshot" and len(sys.argv) == 3:
        snapshot(path)
    else:
        raise SystemExit("invalid pool_state.py arguments")


if __name__ == "__main__":
    main()
