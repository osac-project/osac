#!/usr/bin/env python3
"""Create the real SQLite prerequisites for the AgentlessNet task contract."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
sys.path.insert(0, str(ROOT / "collections"))

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (  # noqa: E402
    StateStore,
)

TENANT = "agentless-net-contract"
VIRTUAL_NETWORK_ID = "a0000000-0000-4000-8000-000000000001"
SUBNET_ID = "a0000000-0000-4000-8000-000000000002"


def prepare(state_path: Path) -> None:
    store = StateStore(state_path)
    store._ensure_virtual_network(
        VIRTUAL_NETWORK_ID,
        "10.20.0.0/16",
        TENANT,
        reconcile=False,
    )
    subnet, _ = store.reserve_subnet(
        SUBNET_ID,
        VIRTUAL_NETWORK_ID,
        TENANT,
        "10.20.1.0/28",
        "eth1",
        {
            "gateway_ipv4": "10.20.1.1",
            "overlay_ipv4_cidrs": [],
            "physical_ipv4_cidrs": ["10.20.1.0/28"],
            "lb_vip_ipv4_cidrs": [],
        },
        observed_vlan_ids=set(range(1, 100)),
    )
    ready = {**subnet, "phase": "ready"}
    with sqlite3.connect(state_path) as connection:
        connection.execute(
            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
            (
                "ready",
                json.dumps(ready, sort_keys=True, separators=(",", ":")),
                SUBNET_ID,
            ),
        )


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: prepare_state.py STATE_FILE")
    prepare(Path(sys.argv[1]))
