from __future__ import annotations

from uuid import uuid4

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


def test_security_group_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient, network_class: str) -> None:
    vn_name: str = f"sg-test-vnet-{uuid4().hex[:8]}"
    vn_id: str = grpc.create_virtual_network(name=vn_name, network_class=network_class, ipv4_cidr="10.210.0.0/16")
    vn_cr_name: str = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
    wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)

    sg_name: str = f"sg-test-{uuid4().hex[:8]}"
    sg_id: str = grpc.create_security_group(name=sg_name, virtual_network=vn_id)

    sg_cr_name: str = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
    assert sg_id in grpc.list_security_group_ids()

    sg: dict = grpc.get_security_group(sg_id=sg_id)
    assert sg["object"]["metadata"]["name"] == sg_name

    wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)

    grpc.delete_security_group(sg_id=sg_id)
    wait_for_security_group_deletion(k8s=k8s_hub_client, name=sg_cr_name)
    poll_until(
        fn=lambda: sg_id not in grpc.list_security_group_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"SecurityGroup {sg_id} removal from API",
    )

    grpc.delete_virtual_network(vn_id=vn_id)
    wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr_name)
