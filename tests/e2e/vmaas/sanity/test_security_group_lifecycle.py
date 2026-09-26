from __future__ import annotations

from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_security_group_cr, wait_for_security_group_ready
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.sanity


def test_security_group_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    sg_id: str | None = None
    sg_cr_name: str | None = None

    try:
        vn_name: str = f"sg-test-vnet-{uuid4().hex[:8]}"
        vn_id, vn_cr_name = create_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_name, "10.210.0.0/16")

        subnet_id, subnet_cr_name = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_id, "10.210.1.0/24", name_prefix="sg-test-subnet"
        )

        sg_name: str = f"sg-test-{uuid4().hex[:8]}"
        sg_id = grpc.create_security_group(name=sg_name, virtual_network=vn_id)

        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        assert sg_id in grpc.list_security_group_ids()

        sg: dict = grpc.get_security_group(sg_id=sg_id)
        assert sg["object"]["metadata"]["name"] == sg_name

        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)

        delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        sg_id = None

        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        subnet_cr_name = None
        subnet_id = None

        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        vn_id = None
    finally:
        if sg_id is not None:
            delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        if subnet_id is not None:
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        if vn_id is not None:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
