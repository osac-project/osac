from __future__ import annotations

import logging
import subprocess
from collections.abc import Callable, Generator
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
)
from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class")
def make_pool(grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> Generator[..., None, None]:
    created: list[tuple[str, str]] = []

    def _make(*, prefix: int = 24, name_prefix: str = "test-pool") -> tuple[str, str]:
        pool_name = f"{name_prefix}-{uuid4().hex[:8]}"
        subnet = allocate_worker_subnet(prefix=prefix)
        pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(subnet)])
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        created.append((pool_id, pool_cr_name))
        return pool_id, pool_cr_name

    yield _make

    for pool_id, pool_cr_name in reversed(created):
        if not k8s_hub_client.is_present(resource="externalippool", name=pool_cr_name):
            continue
        try:
            _cleanup_pool_children(grpc, k8s_hub_client, pool_id)
            private_grpc.delete_external_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError as exc:
            stderr = exc.stderr or ""
            if "Unavailable" in stderr or "no route to host" in stderr or "connection" in stderr.lower():
                logger.warning("ExternalIPPool %s teardown skipped — gRPC unreachable: %s", pool_id, stderr.strip())
                continue
            if "NotFound" not in stderr:
                logger.warning("ExternalIPPool %s teardown delete failed: %s", pool_id, stderr.strip())
                continue
            logger.warning("ExternalIPPool %s already deleted", pool_id)
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


def _cleanup_pool_children(grpc: GRPCClient, k8s: K8sClient, pool_id: str) -> None:
    """Delete all attachments and IPs belonging to a pool before deleting it."""
    pool_ip_ids: set[str] = set()
    for ip_id in grpc.list_external_ip_ids():
        try:
            ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
            if ip_obj["object"]["spec"].get("pool", {}).get("id") == pool_id:
                pool_ip_ids.add(ip_id)
        except subprocess.CalledProcessError:
            continue

    for att_id in grpc.list_external_ip_attachment_ids():
        try:
            att = grpc.get_external_ip_attachment(attachment_id=att_id)
            if att["object"]["spec"].get("external_ip", {}).get("id") in pool_ip_ids:
                grpc.delete_external_ip_attachment(attachment_id=att_id)
                att_name = k8s.get_external_ip_attachment_name(uuid=att_id, checked=False)
                if att_name:
                    wait_for_external_ip_attachment_deletion(k8s=k8s, name=att_name)
        except subprocess.CalledProcessError:
            continue

    for ip_id in pool_ip_ids:
        try:
            grpc.delete_external_ip(external_ip_id=ip_id)
            ip_name = k8s.get_external_ip_name(uuid=ip_id, checked=False)
            if ip_name:
                wait_for_external_ip_deletion(k8s=k8s, name=ip_name)
        except subprocess.CalledProcessError:
            continue


@pytest.fixture(scope="class")
def small_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:
    """A /30 pool with 2 usable IPs."""
    return make_pool(prefix=30, name_prefix="test-small-capacity-pool")
