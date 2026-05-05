from __future__ import annotations

import ipaddress
import logging
import random
import subprocess
from uuid import uuid4

import pytest

from tests.grpc_client import GRPCClient
from tests.helpers import (
    wait_for_public_ip_allocated,
    wait_for_public_ip_cr,
    wait_for_public_ip_deletion,
    wait_for_public_ip_pool_cr,
    wait_for_public_ip_pool_deletion,
    wait_for_public_ip_pool_ready,
)
from tests.k8s_client import K8sClient
from tests.runner import poll_until

logger = logging.getLogger(__name__)

# This IP network is reserved for benchmarking purposes by RFC 2544
IPV4_NETWORK: str = "198.18.0.0/15"

def get_random_subnet() -> ipaddress.IPv4Network:
    network = ipaddress.IPv4Network(IPV4_NETWORK)
    subnets = list(network.subnets(new_prefix=24))
    return random.choice(subnets)

@pytest.fixture
def public_ip_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient
) -> tuple[str, str]:
    pool_name: str = f"test-pool-{uuid4().hex[:8]}"
    subnet = get_random_subnet()
    
    pool_id: str = private_grpc.create_public_ip_pool(name=pool_name, cidrs=[str(subnet)])
    pool_cr_name: str = wait_for_public_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
    wait_for_public_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
    yield pool_id, pool_cr_name
    if k8s_hub_client.is_present(resource="publicippool", name=pool_cr_name):
        try:
            private_grpc.delete_public_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError:
            logger.warning("PublicIPPool %s already deleted via gRPC, cleaning up CR", pool_id)
        wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


@pytest.fixture
def public_ip(
    grpc: GRPCClient, k8s_hub_client: K8sClient, public_ip_pool: tuple[str, str]
) -> tuple[str, str]:
    pool_id, _ = public_ip_pool
    ip_name: str = f"test-ip-{uuid4().hex[:8]}"
    ip_id: str = grpc.create_public_ip(name=ip_name, pool=pool_id)
    ip_cr_name: str = wait_for_public_ip_cr(k8s=k8s_hub_client, uuid=ip_id)
    yield ip_id, ip_cr_name
    if k8s_hub_client.is_present(resource="publicip", name=ip_cr_name):
        try:
            grpc.delete_public_ip(public_ip_id=ip_id)
        except subprocess.CalledProcessError:
            logger.warning("PublicIP %s already deleted via gRPC, cleaning up CR", ip_id)
        wait_for_public_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)


def test_public_ip_pool_lifecycle(
    public_ip_pool: tuple[str, str],
    public_ip: tuple[str, str],
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
) -> None:
    pool_id, pool_cr_name = public_ip_pool
    ip_id, ip_cr_name = public_ip

    assert pool_id in private_grpc.list_public_ip_pool_ids()

    assert ip_id in grpc.list_public_ip_ids()
    wait_for_public_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

    # Delete the PublicIP first, then the pool
    grpc.delete_public_ip(public_ip_id=ip_id)
    wait_for_public_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
    poll_until(
        fn=lambda: ip_id not in grpc.list_public_ip_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"PublicIP {ip_id} removal from API",
    )

    private_grpc.delete_public_ip_pool(pool_id=pool_id)
    wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
    poll_until(
        fn=lambda: pool_id not in private_grpc.list_public_ip_pool_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"PublicIPPool {pool_id} removal from API",
    )
