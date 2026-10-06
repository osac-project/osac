from __future__ import annotations

from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.sanity


def test_virtual_network_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    vn_name: str = f"test-vnet-{uuid4().hex[:8]}"
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None

    try:
        vn_id, vn_cr_name = create_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_name, "10.100.0.0/16")

        assert vn_id in grpc.list_virtual_network_ids()

        vn: dict = grpc.get_virtual_network(vn_id=vn_id)
        assert vn["object"]["metadata"]["name"] == vn_name

        subnet_id, subnet_cr_name = create_and_wait_for_subnet(grpc, k8s_hub_client, vn_id, "10.100.1.0/24")
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        subnet_id = None
        subnet_cr_name = None

        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        vn_id = None
        vn_cr_name = None
    finally:
        if subnet_id is not None and subnet_cr_name is not None:
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        if vn_id is not None and vn_cr_name is not None:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
