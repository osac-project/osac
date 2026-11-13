from __future__ import annotations

import re
import subprocess
from collections.abc import Generator
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    fulfillment_users_for_username,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
)
from tests.e2e.core.k8s_client import K8sClient


def _tenant_for_username(private_grpc: GRPCClient, username: str) -> str:
    users = fulfillment_users_for_username(private_grpc, username=username, limit=2)
    assert len(users) == 1, f"Expected one Fulfillment user for JWT username {username!r}; found {len(users)}"
    tenant = users[0].get("metadata", {}).get("tenant", "")
    assert tenant, f"Fulfillment user {username!r} has no tenant"
    return tenant


def _delete_external_ip_pool(private_grpc: GRPCClient, k8s: K8sClient, pool_id: str, pool_cr_name: str | None) -> None:
    pool_deleted = False
    try:
        private_grpc.delete_external_ip_pool(pool_id=pool_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if re.search(r"Code:\s*NotFound", combined):
            pool_deleted = True
        else:
            raise
    else:
        pool_deleted = True

    if pool_deleted and not pool_cr_name:
        pool_cr_name = k8s.get_external_ip_pool_name(uuid=pool_id, checked=False)
    if pool_deleted and pool_cr_name and k8s.is_present(resource="externalippool", name=pool_cr_name):
        wait_for_external_ip_pool_deletion(k8s=k8s, name=pool_cr_name)


@pytest.fixture
def external_ip_ownership_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient, jwt_username: str
) -> Generator[dict[str, str], None, None]:
    """Create a private test pool and capture the JWT user's resolved tenant."""
    pool_name = f"bmaas-eip-ownership-{uuid4().hex[:8]}"
    pool_cidr = str(allocate_worker_subnet(prefix=30))
    tenant = _tenant_for_username(private_grpc, jwt_username)
    pool_id: str | None = None
    pool_cr_name: str | None = None
    try:
        pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[pool_cidr])
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        yield {
            "id": pool_id,
            "name": pool_name,
            "cr_name": pool_cr_name,
            "cidr": pool_cidr,
            "expected_external_ip_tenant": tenant,
        }
    finally:
        if pool_id:
            _delete_external_ip_pool(private_grpc, k8s_hub_client, pool_id, pool_cr_name)
