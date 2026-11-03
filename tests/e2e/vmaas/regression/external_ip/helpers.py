from __future__ import annotations

from typing import Any

from tests.e2e.core.grpc_client import GRPCClient


def pool_status(private_grpc: GRPCClient, pool_id: str) -> dict[str, Any]:
    pool = private_grpc.get_external_ip_pool(pool_id=pool_id)
    raw = pool["object"]["status"]
    return {
        "total": int(raw.get("total", 0)),
        "allocated": int(raw.get("allocated", 0)),
        "available": int(raw.get("available", 0)),
    }
