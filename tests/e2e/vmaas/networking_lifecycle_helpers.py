from __future__ import annotations

from collections.abc import Callable
from uuid import uuid4

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_security_group_deletion,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until


def create_and_wait_for_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, name: str, ipv4_cidr: str
) -> tuple[str, str]:
    virtual_network_id = grpc.create_virtual_network(name=name, ipv4_cidr=ipv4_cidr)
    virtual_network_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=virtual_network_id)
    wait_for_virtual_network_ready(k8s=k8s_hub_client, name=virtual_network_cr_name)
    return virtual_network_id, virtual_network_cr_name


def create_and_wait_for_subnet(
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    virtual_network_id: str,
    ipv4_cidr: str,
    name_prefix: str = "test-subnet",
) -> tuple[str, str]:
    subnet_name = f"{name_prefix}-{uuid4().hex[:8]}"
    subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=virtual_network_id, ipv4_cidr=ipv4_cidr)
    subnet_cr_name: str | None = None
    try:
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        assert subnet_id in grpc.list_subnet_ids()
        subnet: dict = grpc.get_subnet(subnet_id=subnet_id)
        assert subnet["object"]["metadata"]["name"] == subnet_name
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)
        return subnet_id, subnet_cr_name
    except Exception:
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        raise


def delete_and_wait_for_subnet(
    grpc: GRPCClient, k8s_hub_client: K8sClient, subnet_id: str, subnet_cr_name: str | None
) -> None:
    wait_for_cr_deletion: Callable[[], None] | None = None
    if subnet_cr_name is not None:

        def wait_for_subnet_cr_deletion() -> None:
            wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr_name)

        wait_for_cr_deletion = wait_for_subnet_cr_deletion
    _delete_and_wait_for_network_resource(
        resource_id=subnet_id,
        resource_kind="Subnet",
        delete=lambda: grpc.delete_subnet(subnet_id=subnet_id),
        wait_for_cr_deletion=wait_for_cr_deletion,
        list_ids=grpc.list_subnet_ids,
    )


def delete_and_wait_for_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, virtual_network_id: str, virtual_network_cr_name: str | None
) -> None:
    wait_for_cr_deletion: Callable[[], None] | None = None
    if virtual_network_cr_name is not None:

        def wait_for_virtual_network_cr_deletion() -> None:
            wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=virtual_network_cr_name)

        wait_for_cr_deletion = wait_for_virtual_network_cr_deletion
    _delete_and_wait_for_network_resource(
        resource_id=virtual_network_id,
        resource_kind="VirtualNetwork",
        delete=lambda: grpc.delete_virtual_network(vn_id=virtual_network_id),
        wait_for_cr_deletion=wait_for_cr_deletion,
        list_ids=grpc.list_virtual_network_ids,
    )


def delete_and_wait_for_security_group(
    grpc: GRPCClient, k8s_hub_client: K8sClient, security_group_id: str, security_group_cr_name: str | None
) -> None:
    wait_for_cr_deletion: Callable[[], None] | None = None
    if security_group_cr_name is not None:

        def wait_for_security_group_cr_deletion() -> None:
            wait_for_security_group_deletion(k8s=k8s_hub_client, name=security_group_cr_name)

        wait_for_cr_deletion = wait_for_security_group_cr_deletion
    _delete_and_wait_for_network_resource(
        resource_id=security_group_id,
        resource_kind="SecurityGroup",
        delete=lambda: grpc.delete_security_group(sg_id=security_group_id),
        wait_for_cr_deletion=wait_for_cr_deletion,
        list_ids=grpc.list_security_group_ids,
    )


def _delete_and_wait_for_network_resource(
    resource_id: str,
    resource_kind: str,
    delete: Callable[[], None],
    wait_for_cr_deletion: Callable[[], None] | None,
    list_ids: Callable[[], list[str]],
) -> None:
    delete()
    if wait_for_cr_deletion is not None:
        wait_for_cr_deletion()
    poll_until(
        fn=lambda: resource_id not in list_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"{resource_kind} {resource_id} removal from API",
    )
