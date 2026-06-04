from __future__ import annotations

import ipaddress
import logging
import random
from typing import Any
from uuid import uuid4

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_public_ip_allocated, wait_for_public_ip_cr
from tests.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)

# This network is a portion of the RFC 1918 private range
IPV4_NETWORK: str = "172.27.0.0/16"

_used_subnets: set[ipaddress.IPv4Network] = set()


def get_random_subnet(prefix: int = 24) -> ipaddress.IPv4Network:
    network = ipaddress.IPv4Network(IPV4_NETWORK)
    count = 2 ** (prefix - network.prefixlen)
    for idx in random.sample(range(count), count):
        base = int(network.network_address) + idx * (2 ** (32 - prefix))
        subnet = ipaddress.IPv4Network(f"{ipaddress.IPv4Address(base)}/{prefix}")
        if subnet not in _used_subnets and not any(subnet.overlaps(s) for s in _used_subnets):
            _used_subnets.add(subnet)
            return subnet
    raise RuntimeError(f"No non-overlapping /{prefix} subnet available in {IPV4_NETWORK}")


def pool_status(private_grpc: GRPCClient, pool_id: str) -> dict[str, Any]:
    pool = private_grpc.get_public_ip_pool(pool_id=pool_id)
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
    ip_id: str = grpc.create_public_ip(name=ip_name, pool=pool_id)
    ip_cr_name: str = wait_for_public_ip_cr(k8s=k8s, uuid=ip_id)
    wait_for_public_ip_allocated(k8s=k8s, name=ip_cr_name)
    return ip_id, ip_cr_name


def delete_ip(grpc: GRPCClient, ip_id: str) -> None:
    grpc.delete_public_ip(public_ip_id=ip_id)
