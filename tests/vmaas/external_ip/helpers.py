from __future__ import annotations

import ipaddress
import logging
import os
from typing import Any
from uuid import uuid4

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_external_ip_allocated, wait_for_external_ip_cr
from tests.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)

# This network is a portion of the RFC 1918 private range
IPV4_NETWORK: str = "172.27.0.0/16"


def allocate_worker_subnet(prefix: int = 24) -> ipaddress.IPv4Network:
    """
    Allocate subnet with worker-based namespacing to prevent conflicts in parallel execution.

    Each pytest-xdist worker gets its own /16 subdivision of 172.27.0.0/16:
    - Worker 0 (gw0): 172.27.0.x
    - Worker 1 (gw1): 172.27.1.x
    - Worker 2 (gw2): 172.27.2.x
    - Worker 3 (gw3): 172.27.3.x

    Within each worker, a sequential counter ensures unique, deterministic CIDRs.
    """
    # Get pytest-xdist worker ID (e.g., "gw0", "gw1", etc.)
    worker_id = os.environ.get("PYTEST_XDIST_WORKER", "gw0")
    worker_num = int(worker_id.replace("gw", "")) if worker_id.startswith("gw") else 0

    # Use a sequential counter within this worker's address space
    if not hasattr(allocate_worker_subnet, "_counter"):
        allocate_worker_subnet._counter = 0

    counter = allocate_worker_subnet._counter
    allocate_worker_subnet._counter += 1

    if prefix == 24:
        # /24 blocks use the lower half of 172.27.0.0/16 (172.27.0.0 - 172.27.127.255)
        # Each worker gets 32 /24 blocks
        # Worker 0: 172.27.0.0/24, 172.27.1.0/24, ..., 172.27.31.0/24
        # Worker 1: 172.27.32.0/24, 172.27.33.0/24, ..., 172.27.63.0/24
        # Worker 2: 172.27.64.0/24, 172.27.65.0/24, ..., 172.27.95.0/24
        # Worker 3: 172.27.96.0/24, 172.27.97.0/24, ..., 172.27.127.0/24
        third_octet = worker_num * 32 + counter
        if third_octet > 127:
            raise RuntimeError(f"Worker {worker_id} exhausted /24 address space (counter={counter})")
        cidr = f"172.27.{third_octet}.0/24"
    elif prefix == 30:
        # /30 blocks use the upper half of 172.27.0.0/16 (172.27.128.0 - 172.27.255.255)
        # to avoid overlap with /24 blocks
        # Each worker gets 32 third octets, each with 64 /30 blocks
        # Worker 0: 172.27.128.x - 172.27.159.x
        # Worker 1: 172.27.160.x - 172.27.191.x
        # Worker 2: 172.27.192.x - 172.27.223.x
        # Worker 3: 172.27.224.x - 172.27.255.x
        third_octet = 128 + worker_num * 32 + (counter // 64)
        fourth_octet = (counter % 64) * 4
        if third_octet > 255:
            raise RuntimeError(f"Worker {worker_id} exhausted /30 address space (counter={counter})")
        cidr = f"172.27.{third_octet}.{fourth_octet}/30"
    else:
        raise NotImplementedError(f"Prefix /{prefix} not supported")

    return ipaddress.IPv4Network(cidr)


def pool_status(private_grpc: GRPCClient, pool_id: str) -> dict[str, Any]:
    pool = private_grpc.get_external_ip_pool(pool_id=pool_id)
    raw = pool["object"]["status"]
    return {
        "total": int(raw.get("total", 0)),
        "allocated": int(raw.get("allocated", 0)),
        "available": int(raw.get("available", 0)),
    }


def create_ip(
    grpc: GRPCClient, k8s: K8sClient, pool_id: str
) -> tuple[str, str]:
    ip_name: str = f"test-ip-{uuid4().hex[:8]}"
    ip_id: str = grpc.create_external_ip(name=ip_name, pool=pool_id)
    ip_cr_name: str = wait_for_external_ip_cr(k8s=k8s, uuid=ip_id)
    wait_for_external_ip_allocated(k8s=k8s, name=ip_cr_name)
    return ip_id, ip_cr_name


def delete_ip(grpc: GRPCClient, ip_id: str) -> None:
    grpc.delete_external_ip(external_ip_id=ip_id)
