"""Shared AgentlessNet state setup for provider integration targets."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

COLLECTIONS = Path(__file__).resolve().parents[3] / "collections"
sys.path.insert(0, str(COLLECTIONS))

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (  # noqa: E402
    StateStore,
)

POOL_UID = "10000000-0000-4000-8000-000000000001"
POOL_ID = "10000000-0000-4000-8000-000000000002"
VNET_UID = "10000000-0000-4000-8000-000000000003"
SUBNET_UID = "10000000-0000-4000-8000-000000000004"
EIP_UID = "10000000-0000-4000-8000-000000000005"
EIP_ID = "10000000-0000-4000-8000-000000000006"


def prepare_provider_network_state(
    state_path: Path,
    tenant: str,
    fixture_name: str,
) -> None:
    """Seed the pool, VirtualNetwork, Subnet, and ExternalIP shared by tests."""
    store = StateStore(state_path)
    store.ensure_external_ip_pool(
        POOL_UID,
        POOL_ID,
        tenant,
        "IPv4",
        ["198.51.100.0/30"],
    )
    store._ensure_virtual_network(
        VNET_UID,
        "10.20.0.0/16",
        tenant,
        reconcile=False,
    )
    subnet, _ = store.reserve_subnet(
        SUBNET_UID,
        VNET_UID,
        tenant,
        "10.20.1.0/24",
        "eth1",
        observed_vlan_ids=set(),
    )
    ready_subnet = {**subnet, "phase": "ready"}
    with sqlite3.connect(state_path) as connection:
        connection.execute(
            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
            (
                "ready",
                json.dumps(ready_subnet, sort_keys=True, separators=(",", ":")),
                SUBNET_UID,
            ),
        )
    external_ip, _ = store.reserve_external_ip(
        EIP_UID,
        EIP_ID,
        tenant,
        POOL_UID,
        POOL_ID,
        tenant,
        "198.51.100.0/30",
    )
    if external_ip["address"] != "198.51.100.1":
        raise SystemExit(f"{fixture_name} fixture allocated an unexpected ExternalIP")
