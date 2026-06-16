from __future__ import annotations

import time
from typing import Any, Generator
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.runner import env


@pytest.fixture(scope="session")
def cluster_template() -> str:
    return env("OSAC_CLUSTER_TEMPLATE", "osac.templates.ocp_4_17_small")


@pytest.fixture(scope="session")
def compute_instance_template() -> str:
    return env("OSAC_VM_TEMPLATE", "osac.templates.ocp_virt_vm")


@pytest.fixture(scope="session")
def network_class(grpc: GRPCClient) -> str:
    configured = env("OSAC_NETWORK_CLASS", "")
    if configured:
        return configured
    response: dict[str, Any] = grpc.call(service="osac.public.v1.NetworkClasses/List")
    items = response.get("items", [])
    assert items, "No network classes found; set OSAC_NETWORK_CLASS"
    return items[0]["id"]


def _wait_virtual_network_ready(grpc: GRPCClient, vn_id: str, timeout: int = 120) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        vn = grpc.get_virtual_network(vn_id=vn_id)
        if vn.get("object", {}).get("status", {}).get("state") == "VIRTUAL_NETWORK_STATE_READY":
            return
        time.sleep(3)
    raise TimeoutError(f"VirtualNetwork {vn_id} not ready after {timeout}s")


def _wait_subnet_ready(grpc: GRPCClient, subnet_id: str, timeout: int = 120) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        subnet = grpc.get_subnet(subnet_id=subnet_id)
        if subnet.get("object", {}).get("status", {}).get("state") == "SUBNET_STATE_READY":
            return
        time.sleep(3)
    raise TimeoutError(f"Subnet {subnet_id} not ready after {timeout}s")


@pytest.fixture(scope="module")
def catalog_networking(grpc: GRPCClient, network_class: str) -> Generator[dict[str, str], None, None]:
    """Create VirtualNetwork + Subnet for compute instance catalog item tests."""
    tag = uuid4().hex[:8]
    vn_id = ""
    subnet_id = ""

    try:
        vn_id = grpc.create_virtual_network(
            name=f"e2e-cat-vn-{tag}",
            network_class=network_class,
            ipv4_cidr="10.200.0.0/16",
        )
        _wait_virtual_network_ready(grpc, vn_id)

        subnet_id = grpc.create_subnet(
            name=f"e2e-cat-subnet-{tag}",
            virtual_network=vn_id,
            ipv4_cidr="10.200.100.0/24",
        )
        _wait_subnet_ready(grpc, subnet_id)

        yield {"virtual_network_id": vn_id, "subnet_id": subnet_id}
    finally:
        if subnet_id:
            try:
                grpc.delete_subnet(subnet_id=subnet_id)
            except Exception:
                pass
        if vn_id:
            try:
                grpc.delete_virtual_network(vn_id=vn_id)
            except Exception:
                pass


@pytest.fixture(scope="module")
def default_subnet_id(catalog_networking: dict[str, str]) -> str:
    return catalog_networking["subnet_id"]
