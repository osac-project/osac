#!/usr/bin/env python3
"""Read selected AgentlessNet API-ID rows from a fixture SQLite database."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path


if len(sys.argv) != 4:
    raise SystemExit("usage: inspect_state.py STATE_FILE KIND API_ID")

state_path = Path(sys.argv[1])
kind = sys.argv[2]
api_id = sys.argv[3]
tables = {
    "network_attachment": ("network_attachments", "NetworkAttachment"),
    "external_ip_attachment": ("attachments", "ExternalIPAttachment"),
    "external_ip_pool": ("external_ip_pools", "ExternalIPPool"),
    "external_ip": ("external_ips", "ExternalIP"),
}
if kind not in tables:
    raise SystemExit(f"unknown state kind: {kind}")

table, tombstone_kind = tables[kind]
with sqlite3.connect(f"file:{state_path}?mode=ro", uri=True) as connection:
    row = connection.execute(
        f"SELECT phase, payload FROM {table} WHERE uid = ?", (api_id,)
    ).fetchone()
    tombstone = connection.execute(
        "SELECT tenant_id FROM deleted_resources WHERE kind = ? AND uid = ?",
        (tombstone_kind, api_id),
    ).fetchone()
    claims = []
    if kind == "network_attachment":
        claims = connection.execute(
            "SELECT switch_name, port_name FROM network_attachment_ports "
            "WHERE attachment_uid = ? ORDER BY switch_name, port_name",
            (api_id,),
        ).fetchall()

result = {
    "present": row is not None,
    "phase": row[0] if row else None,
    "entry": json.loads(row[1]) if row else None,
    "tombstoned": tombstone is not None,
    "claims": claims,
}
print(json.dumps(result, sort_keys=True))
