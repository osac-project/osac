from __future__ import annotations

import os
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
    admin: GRPCClient
    clients: dict[str, GRPCClient]
    virtual_networks: dict[str, str] = field(default_factory=dict)
    virtual_network_crs: dict[str, str] = field(default_factory=dict)
    fabric_domains: list[tuple[GRPCClient, str]] = field(default_factory=list)


def _list_ids(client: GRPCClient, *, service: str) -> list[str]:
    response = client.call(service=f"{service}/List")
    return [item["id"] for item in response.get("items", [])]


def _create_fabric_domain(
    client: GRPCClient,
    *,
    name: str,
    tenant: str,
    virtual_network_id: str,
    servers: list[str],
    fabric_type: str | int = _ETHERNET_EW,
) -> str:
    response = client.call(
        service=f"{_FABRIC_DOMAINS}/Create",
        data={
            "object": {
                "metadata": {"name": name, "tenant": tenant},
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


def _assert_provisioning_disabled(deployment: dict) -> None:
    """Reject an unsafe or incompletely rolled-out API-only test environment."""
    managers = [c for c in deployment["spec"]["template"]["spec"]["containers"] if c["name"] == "manager"]
    assert len(managers) == 1, "Expected exactly one operator manager container"
    flags = [e for e in managers[0].get("env", []) if e["name"] == "OSAC_ENABLE_NETWORKING_PROVISIONING"]
    assert len(flags) == 1 and flags[0].get("value") == "false", (
        "API-only FabricDomain tests require an explicit OSAC_ENABLE_NETWORKING_PROVISIONING=false; "
        "arbitrary test hostnames must never reach Netris"
    )
    replicas = deployment["spec"].get("replicas", 1)
    status = deployment.get("status", {})
    assert replicas > 0 and status.get("observedGeneration", 0) >= deployment["metadata"]["generation"], (
        "Wait for the operator deployment to observe the provisioning-disabled configuration"
    )
    assert all(status.get(key, 0) == replicas for key in ("replicas", "updatedReplicas", "availableReplicas")), (
        "Wait for all operator replicas to finish rolling out with provisioning disabled"
    )


@pytest.fixture(autouse=True)
def fabric_domain_api_only_environment(k8s_hub_client: K8sClient) -> None:
    """Opt in on a dedicated environment; never turn provisioning off in a shared deployment."""
    if os.environ.get("OSAC_FABRIC_DOMAIN_API_ONLY") != "true":
        pytest.skip(
            "API-only FabricDomain coverage requires OSAC_FABRIC_DOMAIN_API_ONLY=true and disabled provisioning; "
            "live Netris lifecycle coverage remains pending (see README.fabric-domain.md)"
        )
    deployment_name = os.environ.get("OSAC_FABRIC_DOMAIN_OPERATOR_DEPLOYMENT")
    assert deployment_name, "Set OSAC_FABRIC_DOMAIN_OPERATOR_DEPLOYMENT to the operator deployment in OSAC_NAMESPACE"
    _assert_provisioning_disabled(k8s_hub_client.get_json(resource="deployment", name=deployment_name))


@pytest.fixture
def fabric_domain_admin(private_grpc: GRPCClient, fulfillment_address: str) -> GRPCClient:
    """Use the existing platform-admin identity against the public API."""
    return GRPCClient(address=fulfillment_address, token_factory=lambda: private_grpc.token)


@pytest.fixture
def fabric_domain_resources(
    private_grpc: GRPCClient,
    fabric_domain_admin: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant2: GRPCClient,
    k8s_hub_client: K8sClient,
) -> Iterator[FabricDomainResources]:
    """Admins create tenant-owned API objects; no backend provisioning is exercised."""
    # Public VirtualNetwork creation uses the deployment's singleton NetworkClass.
    network_classes = [
        nc for nc in private_grpc.list_network_classes() if not nc.get("metadata", {}).get("deletionTimestamp")
    ]
    if len(network_classes) != 1 or not (
        network_classes[0].get("status", {}).get("state") == "NETWORK_CLASS_STATE_READY"
        and network_classes[0].get("capabilities", {}).get("supportsEastWestEthernet")
        and network_classes[0].get("fabricManager") == "netris"
    ):
        pytest.fail(
            "FabricDomain API tests require one ready NetworkClass with Ethernet east-west support "
            "and fabricManager=netris; template bindings are resolved by the operator from BMIT inventory"
        )

    resources = FabricDomainResources(
        admin=fabric_domain_admin,
        clients={"tenant1": jwt_grpc_tenant1, "tenant1_admin": jwt_grpc_tenant1_admin, "tenant2": jwt_grpc_tenant2},
    )
    octet = int(uuid4().hex[:2], 16)
    try:
        for tenant, cidr_prefix in (("tenant1", "10.230"), ("tenant2", "10.231")):
            response = resources.admin.call(
                service=f"{_VIRTUAL_NETWORKS}/Create",
                data={
                    "object": {
                        "metadata": {"name": f"fd-api-{tenant}-{uuid4().hex[:8]}", "tenant": tenant},
                        "spec": {"ipv4_cidr": f"{cidr_prefix}.{octet}.0/24"},
                    }
                },
            )
            virtual_network_id = response["object"]["id"]
            resources.virtual_networks[tenant] = virtual_network_id
            assert response["object"]["metadata"]["tenant"] == tenant
            cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=virtual_network_id)
            resources.virtual_network_crs[tenant] = cr_name

        yield resources
    finally:
        cleanup_errors: list[Exception] = []
        for client, fabric_domain_id in reversed(resources.fabric_domains):
            try:
                _delete_fabric_domain(client, fabric_domain_id)
            except Exception as exc:
                exc.add_note(f"FabricDomain {fabric_domain_id} cleanup")
                cleanup_errors.append(exc)
        for tenant, virtual_network_id in reversed(list(resources.virtual_networks.items())):
            try:
                _delete_virtual_network(
                    resources.admin,
                    virtual_network_id=virtual_network_id,
                    k8s=k8s_hub_client,
                    cr_name=resources.virtual_network_crs.get(tenant),
                )
            except Exception as exc:
                exc.add_note(f"VirtualNetwork {virtual_network_id} cleanup")
                cleanup_errors.append(exc)
        if cleanup_errors:
            raise ExceptionGroup("FabricDomain fixture cleanup failed", cleanup_errors)


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
    fabric_domain_admin: GRPCClient,
    fabric_type: str | int,
    servers: list[str],
    virtual_network: str,
    expected_code: str,
) -> None:
    _assert_rejected(
        fabric_domain_admin,
        data={
            "object": {
                "metadata": {"name": f"fd-invalid-{uuid4().hex[:8]}", "tenant": "tenant1"},
                "spec": {"type": fabric_type, "servers": servers, "virtual_network": virtual_network},
            }
        },
        code=expected_code,
    )


@pytest.mark.parametrize("identity", ["tenant1", "tenant1_admin"])
@pytest.mark.parametrize("operation", ["Create", "Update", "Delete"])
def test_fabric_domain_tenant_writes_denied(
    fabric_domain_resources: FabricDomainResources, identity: str, operation: str
) -> None:
    """Even the owning tenant admin cannot write admin-supplied fabric membership."""
    resources = fabric_domain_resources
    domain_id = _create_fabric_domain(
        resources.admin,
        name=f"fd-protected-{uuid4().hex[:8]}",
        tenant="tenant1",
        virtual_network_id=resources.virtual_networks["tenant1"],
        servers=["server-a.example.test"],
    )
    resources.fabric_domains.append((resources.admin, domain_id))
    requests = {
        "Create": {
            "object": {
                "metadata": {"name": f"fd-denied-{uuid4().hex[:8]}", "tenant": "tenant1"},
                "spec": {
                    "type": _ETHERNET_EW,
                    "servers": ["server-b.example.test"],
                    "virtual_network": resources.virtual_networks["tenant1"],
                },
            }
        },
        "Update": {
            "object": {"id": domain_id, "spec": {"servers": ["server-b.example.test"]}},
            "updateMask": {"paths": ["spec.servers"]},
        },
        "Delete": {"id": domain_id},
    }
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        response = resources.clients[identity].call(service=f"{_FABRIC_DOMAINS}/{operation}", data=requests[operation])
        # Track unexpected successful creates so an authorization regression does not leak resources.
        if operation == "Create":
            resources.fabric_domains.append((resources.admin, response["object"]["id"]))
    assert_grpc_rejected(exc_info, "PermissionDenied")
    persisted = resources.admin.call(service=f"{_FABRIC_DOMAINS}/Get", data={"id": domain_id})["object"]
    assert persisted["spec"]["servers"] == ["server-a.example.test"]
    assert not persisted.get("metadata", {}).get("deletionTimestamp")


def test_fabric_domain_crud_and_tenant_filtered_list(fabric_domain_resources: FabricDomainResources) -> None:
    """Admin CRUD and tenant Get/List isolation, with backend provisioning disabled."""
    admin = fabric_domain_resources.admin
    tenant1_client = fabric_domain_resources.clients["tenant1"]
    tenant2_client = fabric_domain_resources.clients["tenant2"]
    tenant1_domain_id = _create_fabric_domain(
        admin,
        name=f"fd-tenant1-{uuid4().hex[:8]}",
        tenant="tenant1",
        virtual_network_id=fabric_domain_resources.virtual_networks["tenant1"],
        servers=["server-a.example.test"],
    )
    fabric_domain_resources.fabric_domains.append((admin, tenant1_domain_id))
    tenant2_domain_id = _create_fabric_domain(
        admin,
        name=f"fd-tenant2-{uuid4().hex[:8]}",
        tenant="tenant2",
        virtual_network_id=fabric_domain_resources.virtual_networks["tenant2"],
        servers=["server-b.example.test"],
    )
    fabric_domain_resources.fabric_domains.append((admin, tenant2_domain_id))

    update_response = admin.call(
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
        admin.call(
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

    for client, tenant, own_id, foreign_id in (
        (tenant1_client, "tenant1", tenant1_domain_id, tenant2_domain_id),
        (fabric_domain_resources.clients["tenant1_admin"], "tenant1", tenant1_domain_id, tenant2_domain_id),
        (tenant2_client, "tenant2", tenant2_domain_id, tenant1_domain_id),
    ):
        own = client.call(service=f"{_FABRIC_DOMAINS}/Get", data={"id": own_id})["object"]
        assert own["metadata"]["tenant"] == tenant
        assert own_id in _list_ids(client, service=_FABRIC_DOMAINS)
        assert foreign_id not in _list_ids(client, service=_FABRIC_DOMAINS)
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            client.call(service=f"{_FABRIC_DOMAINS}/Get", data={"id": foreign_id})
        assert_grpc_rejected(exc_info, "NotFound")

    _delete_fabric_domain(admin, tenant1_domain_id)
    assert tenant1_domain_id not in _list_ids(tenant1_client, service=_FABRIC_DOMAINS)
    assert tenant2_domain_id in _list_ids(tenant2_client, service=_FABRIC_DOMAINS)
