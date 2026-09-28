from __future__ import annotations

import subprocess
from collections.abc import Iterator
from dataclasses import dataclass, field
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected, wait_for_virtual_network_cr, wait_for_virtual_network_deletion
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression

_FABRIC_DOMAINS = f"{PUBLIC_API}.FabricDomains"
_VIRTUAL_NETWORKS = f"{PUBLIC_API}.VirtualNetworks"
_ETHERNET_EW = "FABRIC_DOMAIN_TYPE_ETHERNET_EW"


@dataclass
class FabricDomainResources:
    clients: dict[str, GRPCClient]
    virtual_networks: dict[str, str] = field(default_factory=dict)
    virtual_network_crs: dict[str, str] = field(default_factory=dict)
    fabric_domains: list[tuple[GRPCClient, str]] = field(default_factory=list)


def _list_ids(client: GRPCClient, *, service: str) -> list[str]:
    response = client.call(service=f"{service}/List")
    return [item["id"] for item in response.get("items", [])]


def _create_fabric_domain(
    client: GRPCClient, *, name: str, virtual_network_id: str, servers: list[str], fabric_type: str | int = _ETHERNET_EW
) -> str:
    response = client.call(
        service=f"{_FABRIC_DOMAINS}/Create",
        data={
            "object": {
                "metadata": {"name": name},
                "spec": {"type": fabric_type, "servers": servers, "virtual_network": virtual_network_id},
            }
        },
    )
    return response["object"]["id"]


def _assert_rejected(client: GRPCClient, *, data: dict, code: str) -> None:
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        client.call(service=f"{_FABRIC_DOMAINS}/Create", data=data)
    assert_grpc_rejected(exc_info, code)


def _delete_fabric_domain(client: GRPCClient, fabric_domain_id: str) -> None:
    output, rc = client.call_unchecked(service=f"{_FABRIC_DOMAINS}/Delete", data={"id": fabric_domain_id})
    if rc != 0 and "Code: NotFound" not in output:
        raise AssertionError(f"Failed to delete FabricDomain {fabric_domain_id}: {output}")
    poll_until(
        fn=lambda: fabric_domain_id not in _list_ids(client, service=_FABRIC_DOMAINS),
        until=lambda absent: absent is True,
        retries=30,
        delay=2,
        description=f"FabricDomain {fabric_domain_id} removal from API",
    )


def _delete_virtual_network(
    client: GRPCClient, *, virtual_network_id: str, k8s: K8sClient, cr_name: str | None
) -> None:
    output, rc = client.call_unchecked(service=f"{_VIRTUAL_NETWORKS}/Delete", data={"id": virtual_network_id})
    if rc != 0 and "Code: NotFound" not in output:
        raise AssertionError(f"Failed to delete VirtualNetwork {virtual_network_id}: {output}")
    if cr_name is not None:
        wait_for_virtual_network_deletion(k8s=k8s, name=cr_name)
    poll_until(
        fn=lambda: virtual_network_id not in _list_ids(client, service=_VIRTUAL_NETWORKS),
        until=lambda absent: absent is True,
        retries=30,
        delay=2,
        description=f"VirtualNetwork {virtual_network_id} removal from API",
    )


@pytest.fixture
def fabric_domain_resources(
    private_grpc: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    jwt_grpc_tenant2: GRPCClient,
    k8s_hub_client: K8sClient,
) -> Iterator[FabricDomainResources]:
    """Create tenant-owned VirtualNetworks on an Ethernet east-west capable class."""
    network_classes = private_grpc.list_network_classes()
    if not any(
        network_class.get("capabilities", {}).get("supportsEastWestEthernet")
        and network_class.get("spec", {}).get("eastWestConfig", {}).get("ethernetEw", {}).get("templateId")
        for network_class in network_classes
    ):
        pytest.skip("FabricDomain E2E requires an Ethernet east-west capable NetworkClass with a template ID")

    resources = FabricDomainResources(clients={"tenant1": jwt_grpc_tenant1_admin, "tenant2": jwt_grpc_tenant2})
    octet = int(uuid4().hex[:2], 16)
    try:
        for tenant, cidr_prefix in (("tenant1", "10.230"), ("tenant2", "10.231")):
            client = resources.clients[tenant]
            virtual_network_id = client.create_virtual_network(
                name=f"fd-api-{tenant}-{uuid4().hex[:8]}", ipv4_cidr=f"{cidr_prefix}.{octet}.0/24"
            )
            resources.virtual_networks[tenant] = virtual_network_id
            cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=virtual_network_id)
            resources.virtual_network_crs[tenant] = cr_name

        yield resources
    finally:
        for client, fabric_domain_id in reversed(resources.fabric_domains):
            _delete_fabric_domain(client, fabric_domain_id)
        for tenant, virtual_network_id in reversed(list(resources.virtual_networks.items())):
            _delete_virtual_network(
                resources.clients[tenant],
                virtual_network_id=virtual_network_id,
                k8s=k8s_hub_client,
                cr_name=resources.virtual_network_crs.get(tenant),
            )


@pytest.mark.parametrize(
    ("fabric_type", "servers", "virtual_network", "expected_code"),
    [
        (999, ["server-a.example.test"], "unused-vn", "InvalidArgument"),
        (_ETHERNET_EW, [], "unused-vn", "InvalidArgument"),
        (_ETHERNET_EW, ["server-a.example.test"], "missing-vn", "NotFound"),
    ],
    ids=["unknown-type", "empty-servers", "missing-virtual-network"],
)
def test_fabric_domain_create_validation(
    jwt_grpc_tenant1_admin: GRPCClient,
    fabric_type: str | int,
    servers: list[str],
    virtual_network: str,
    expected_code: str,
) -> None:
    _assert_rejected(
        jwt_grpc_tenant1_admin,
        data={
            "object": {
                "metadata": {"name": f"fd-invalid-{uuid4().hex[:8]}"},
                "spec": {"type": fabric_type, "servers": servers, "virtual_network": virtual_network},
            }
        },
        code=expected_code,
    )


def test_fabric_domain_rejects_virtual_network_from_another_tenant(
    jwt_grpc_tenant1_admin: GRPCClient, jwt_grpc_tenant2: GRPCClient, k8s_hub_client: K8sClient
) -> None:
    """Foreign IDs remain indistinguishable from missing IDs in the public API."""
    suffix = uuid4().hex[:8]
    virtual_network_id = jwt_grpc_tenant2.create_virtual_network(
        name=f"fd-foreign-{suffix}", ipv4_cidr=f"10.232.{int(suffix[:2], 16)}.0/24"
    )
    cr_name: str | None = None
    try:
        cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=virtual_network_id)
        # The DAO intentionally returns NotFound for invisible tenant objects, preventing
        # callers from discovering whether another tenant owns a guessed identifier.
        _assert_rejected(
            jwt_grpc_tenant1_admin,
            data={
                "object": {
                    "metadata": {"name": f"fd-foreign-ref-{suffix}"},
                    "spec": {
                        "type": _ETHERNET_EW,
                        "servers": ["server-a.example.test"],
                        "virtual_network": virtual_network_id,
                    },
                }
            },
            code="NotFound",
        )
    finally:
        _delete_virtual_network(
            jwt_grpc_tenant2, virtual_network_id=virtual_network_id, k8s=k8s_hub_client, cr_name=cr_name
        )


def test_fabric_domain_crud_and_tenant_filtered_list(fabric_domain_resources: FabricDomainResources) -> None:
    """Create, resize, enforce immutable type, list by tenant, and delete domains."""
    tenant1_client = fabric_domain_resources.clients["tenant1"]
    tenant2_client = fabric_domain_resources.clients["tenant2"]
    tenant1_domain_id = _create_fabric_domain(
        tenant1_client,
        name=f"fd-tenant1-{uuid4().hex[:8]}",
        virtual_network_id=fabric_domain_resources.virtual_networks["tenant1"],
        servers=["server-a.example.test"],
    )
    fabric_domain_resources.fabric_domains.append((tenant1_client, tenant1_domain_id))
    tenant2_domain_id = _create_fabric_domain(
        tenant2_client,
        name=f"fd-tenant2-{uuid4().hex[:8]}",
        virtual_network_id=fabric_domain_resources.virtual_networks["tenant2"],
        servers=["server-b.example.test"],
    )
    fabric_domain_resources.fabric_domains.append((tenant2_client, tenant2_domain_id))

    update_response = tenant1_client.call(
        service=f"{_FABRIC_DOMAINS}/Update",
        data={
            "object": {
                "id": tenant1_domain_id,
                "spec": {"servers": ["server-a.example.test", "server-c.example.test"]},
            },
            "updateMask": {"paths": ["spec.servers"]},
        },
    )
    assert update_response["object"]["spec"]["servers"] == ["server-a.example.test", "server-c.example.test"]

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        tenant1_client.call(
            service=f"{_FABRIC_DOMAINS}/Update",
            data={
                "object": {"id": tenant1_domain_id, "spec": {"type": "FABRIC_DOMAIN_TYPE_INFINIBAND_EW"}},
                "updateMask": {"paths": ["spec.type"]},
            },
        )
    assert_grpc_rejected(exc_info, "InvalidArgument")

    tenant1_ids = _list_ids(tenant1_client, service=_FABRIC_DOMAINS)
    tenant2_ids = _list_ids(tenant2_client, service=_FABRIC_DOMAINS)
    assert tenant1_domain_id in tenant1_ids
    assert tenant2_domain_id not in tenant1_ids
    assert tenant2_domain_id in tenant2_ids
    assert tenant1_domain_id not in tenant2_ids

    _delete_fabric_domain(tenant1_client, tenant1_domain_id)
    assert tenant1_domain_id not in _list_ids(tenant1_client, service=_FABRIC_DOMAINS)
    assert tenant2_domain_id in _list_ids(tenant2_client, service=_FABRIC_DOMAINS)
