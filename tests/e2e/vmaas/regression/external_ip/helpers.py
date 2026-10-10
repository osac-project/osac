from __future__ import annotations

import logging
from typing import Any
from uuid import uuid4

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_external_ip_allocated, wait_for_external_ip_cr
from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


def pool_status(private_grpc: GRPCClient, pool_id: str) -> dict[str, Any]:
    pool = private_grpc.get_external_ip_pool(pool_id=pool_id)
    raw = pool["object"]["status"]
    return {
        "total": int(raw.get("total", 0)),
        "allocated": int(raw.get("allocated", 0)),
        "available": int(raw.get("available", 0)),
    }


def create_ip(grpc: GRPCClient, k8s: K8sClient, pool_id: str) -> tuple[str, str]:
    ip_name: str = f"test-ip-{uuid4().hex[:8]}"
    ip_id: str = grpc.create_external_ip(name=ip_name, pool=pool_id)
    ip_cr_name: str = wait_for_external_ip_cr(k8s=k8s, uuid=ip_id)
    wait_for_external_ip_allocated(k8s=k8s, name=ip_cr_name)
    return ip_id, ip_cr_name


def delete_ip(grpc: GRPCClient, ip_id: str) -> None:
    grpc.delete_external_ip(external_ip_id=ip_id)
