from __future__ import annotations

import logging
import subprocess
from collections.abc import Callable, Generator
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_public_ip_deletion,
    wait_for_public_ip_pool_cr,
    wait_for_public_ip_pool_deletion,
    wait_for_public_ip_pool_ready,
)
from tests.core.k8s_client import K8sClient
from tests.vmaas.public_ip.helpers import create_ip, get_random_subnet

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class")
def make_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient
) -> Generator[..., None, None]:
    created: list[tuple[str, str]] = []

    def _make(*, prefix: int = 24, name_prefix: str = "test-pool") -> tuple[str, str]:
        pool_name = f"{name_prefix}-{uuid4().hex[:8]}"
        subnet = get_random_subnet(prefix=prefix)
        pool_id = private_grpc.create_public_ip_pool(name=pool_name, cidrs=[str(subnet)])
        pool_cr_name = wait_for_public_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_public_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        created.append((pool_id, pool_cr_name))
        return pool_id, pool_cr_name

    yield _make

    for pool_id, pool_cr_name in reversed(created):
        if not k8s_hub_client.is_present(resource="publicippool", name=pool_cr_name):
            continue
        try:
            private_grpc.delete_public_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError as exc:
            stderr = exc.stderr or ""
            if "Unavailable" in stderr or "no route to host" in stderr or "connection" in stderr.lower():
                logger.warning("PublicIPPool %s teardown skipped — gRPC unreachable: %s", pool_id, stderr.strip())
                continue
            if "NotFound" not in stderr:
                logger.warning("PublicIPPool %s teardown delete failed: %s", pool_id, stderr.strip())
                continue
            logger.warning("PublicIPPool %s already deleted", pool_id)
        wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


@pytest.fixture
def public_ip_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:
    return make_pool()


@pytest.fixture(scope="class")
def small_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:
    """A /30 pool with 2 usable IPs."""
    return make_pool(prefix=30, name_prefix="test-small-capacity-pool")


@pytest.fixture(scope="class")
def created_ips(grpc: GRPCClient) -> Generator[list[tuple[str, str]], None, None]:
    """Track IPs across chained tests; clean up any survivors on teardown."""
    ips: list[tuple[str, str]] = []
    yield ips
    for ip_id, _ in reversed(ips):
        try:
            grpc.delete_public_ip(public_ip_id=ip_id)
        except subprocess.CalledProcessError:
            logger.warning("PublicIP %s teardown failed, may need manual cleanup", ip_id)


@pytest.fixture
def public_ip(
    grpc: GRPCClient, k8s_hub_client: K8sClient, public_ip_pool: tuple[str, str]
) -> Generator[tuple[str, str], None, None]:
    pool_id, _ = public_ip_pool
    ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id)
    yield ip_id, ip_cr_name
    if k8s_hub_client.is_present(resource="publicip", name=ip_cr_name):
        try:
            grpc.delete_public_ip(public_ip_id=ip_id)
        except subprocess.CalledProcessError as exc:
            logger.warning("PublicIP %s gRPC delete failed in teardown: %s", ip_id, (exc.stderr or "").strip())
        wait_for_public_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
