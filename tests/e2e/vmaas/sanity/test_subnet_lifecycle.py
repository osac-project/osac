from __future__ import annotations

from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_grpc_subnet_ready
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.sanity


def test_subnet_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    vn_name: str = f"test-vnet-{uuid4().hex[:8]}"
    vn_id, vn_cr_name = create_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_name, "10.200.0.0/16")

    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    try:
        subnet_id, subnet_cr_name = create_and_wait_for_subnet(grpc, k8s_hub_client, vn_id, "10.200.1.0/24")
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)
    finally:
        try:
            if subnet_id is not None:
                delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        finally:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
