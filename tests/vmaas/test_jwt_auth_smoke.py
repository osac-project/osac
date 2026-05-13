from __future__ import annotations

import time
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import run_unchecked

CLIENT_LISTABLE_RESOURCES = [
    "clustertemplates",
    "clusters",
    "computeinstancetemplates",
    "computeinstances",
    "hosttypes",
    "networkclasses",
    "publicips",
    "rolebindings",
    "roles",
    "securitygroups",
    "subnets",
    "virtualnetworks",
]


# JWT List access for all resource types


@pytest.mark.parametrize("resource", CLIENT_LISTABLE_RESOURCES)
def test_jwt_user_can_list(jwt_cli_user: OsacCLI, resource: str) -> None:
    jwt_cli_user.get(resource)


@pytest.mark.parametrize("resource", CLIENT_LISTABLE_RESOURCES)
def test_jwt_admin_can_list(jwt_cli_admin: OsacCLI, resource: str) -> None:
    jwt_cli_admin.get(resource)


# Authorization boundary: Users API


def test_jwt_user_denied_users(jwt_cli_user: OsacCLI) -> None:
    jwt_cli_user.relogin()
    output, rc = jwt_cli_user.get_unchecked("users")
    assert rc != 0, f"Regular user should be denied access to users, got: {output}"
    assert "denied" in output.lower() or "permission" in output.lower()


def test_jwt_admin_can_list_users(jwt_cli_admin: OsacCLI) -> None:
    jwt_cli_admin.relogin()
    jwt_cli_admin.get("users")


# Invalid token rejection


def test_invalid_token_rejected(fulfillment_address: str) -> None:
    output, rc = run_unchecked(
        "grpcurl",
        "-insecure",
        "-H",
        "Authorization: Bearer invalid",
        fulfillment_address,
        "osac.public.v1.ComputeInstances/List",
    )
    assert rc != 0, f"Invalid token should be rejected, got: {output}"


# JWT CRUD lifecycle


def test_jwt_virtual_network_lifecycle(jwt_grpc_tenant1: GRPCClient) -> None:
    vn_name: str = f"jwt-smoke-{uuid4().hex[:8]}"
    vn_id: str = jwt_grpc_tenant1.create_virtual_network(
        name=vn_name, network_class="cudn_net", ipv4_cidr="10.200.0.0/16"
    )
    assert vn_id in jwt_grpc_tenant1.list_virtual_network_ids()

    vn: dict = jwt_grpc_tenant1.get_virtual_network(vn_id=vn_id)
    assert vn["object"]["metadata"]["name"] == vn_name

    jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)


def test_jwt_security_group_lifecycle(jwt_grpc_tenant1: GRPCClient) -> None:
    vn_name: str = f"jwt-sg-vnet-{uuid4().hex[:8]}"
    vn_id: str = jwt_grpc_tenant1.create_virtual_network(
        name=vn_name, network_class="cudn_net", ipv4_cidr="10.202.0.0/16"
    )

    for _ in range(30):
        vn = jwt_grpc_tenant1.get_virtual_network(vn_id=vn_id)
        if vn["object"].get("status", {}).get("state") == "VIRTUAL_NETWORK_STATE_READY":
            break
        time.sleep(2)
    else:
        pytest.fail(f"VirtualNetwork {vn_id} did not reach READY state within 60s")

    sg_name: str = f"jwt-sg-{uuid4().hex[:8]}"
    sg_id: str = jwt_grpc_tenant1.create_security_group(name=sg_name, virtual_network=vn_id)
    assert sg_id in jwt_grpc_tenant1.list_security_group_ids()

    jwt_grpc_tenant1.delete_security_group(sg_id=sg_id)
    jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)


# Multi-tenant isolation


def test_jwt_tenant_isolation(jwt_grpc_tenant1: GRPCClient, jwt_grpc_tenant2: GRPCClient) -> None:
    vn_name: str = f"tenant-iso-{uuid4().hex[:8]}"
    vn_id: str = jwt_grpc_tenant1.create_virtual_network(
        name=vn_name, network_class="cudn_net", ipv4_cidr="10.201.0.0/16"
    )

    assert vn_id in jwt_grpc_tenant1.list_virtual_network_ids()
    assert vn_id not in jwt_grpc_tenant2.list_virtual_network_ids()

    jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)
