from __future__ import annotations

from uuid import uuid4

from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_virtual_network_cr, wait_for_virtual_network_deletion, wait_for_virtual_network_ready
from tests.k8s_client import K8sClient
from tests.runner import poll_until


def test_virtual_network_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient, network_class: str) -> None:
    vn_name: str = f"test-vnet-{uuid4().hex[:8]}"
    vn_id: str = grpc.create_virtual_network(name=vn_name, network_class=network_class, ipv4_cidr="10.100.0.0/16")
    cr_name: str = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)

    assert vn_id in grpc.list_virtual_network_ids()
    wait_for_virtual_network_ready(k8s=k8s_hub_client, name=cr_name)

    grpc.delete_virtual_network(vn_id=vn_id)
    wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=cr_name)
    poll_until(
        fn=lambda: vn_id not in grpc.list_virtual_network_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"VirtualNetwork {vn_id} removal from API",
    )
