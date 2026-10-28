#!/usr/bin/env python3
"""Read the attachment row for the AgentlessNet contract integration target."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit("usage: inspect_state.py STATE_FILE ATTACHMENT_UID")

state_path = Path(sys.argv[1])
attachment_uid = sys.argv[2]
with sqlite3.connect(f"file:{state_path}?mode=ro", uri=True) as connection:
    row = connection.execute(
        "SELECT phase, payload FROM attachments WHERE uid = ?", (attachment_uid,)
    ).fetchone()
if row is None:
    print(json.dumps({"present": False}))
else:
    print(json.dumps({"present": True, "phase": row[0], "attachment": json.loads(row[1])}))
