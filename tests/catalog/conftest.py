from __future__ import annotations

import logging
import re
import subprocess
from typing import Any, Generator
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import env

logger = logging.getLogger(__name__)


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


def _delete_subnet_teardown(
    grpc: GRPCClient,
    k8s: K8sClient,
    *,
    subnet_id: str,
    subnet_cr_name: str,
) -> None:
    try:
        grpc.delete_subnet(subnet_id=subnet_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if re.search(r"Code:\s*NotFound", combined):
            logger.warning("Subnet %s already deleted via API", subnet_id)
        else:
            logger.warning("Subnet %s teardown delete failed: %s", subnet_id, combined.strip())
            return
    if k8s.is_present(resource="subnet", name=subnet_cr_name):
        wait_for_subnet_deletion(k8s=k8s, name=subnet_cr_name)


def _delete_virtual_network_teardown(
    grpc: GRPCClient,
    k8s: K8sClient,
    *,
    vn_id: str,
    vn_cr_name: str,
) -> None:
    try:
        grpc.delete_virtual_network(vn_id=vn_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if re.search(r"Code:\s*NotFound", combined):
            logger.warning("VirtualNetwork %s already deleted via API", vn_id)
        else:
            logger.warning("VirtualNetwork %s teardown delete failed: %s", vn_id, combined.strip())
            return
    if k8s.is_present(resource="virtualnetwork", name=vn_cr_name):
        wait_for_virtual_network_deletion(k8s=k8s, name=vn_cr_name)


@pytest.fixture(scope="module")
def catalog_networking(
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    network_class: str,
) -> Generator[dict[str, str], None, None]:
    """Create VirtualNetwork + Subnet for compute instance catalog item tests."""
    tag = uuid4().hex[:8]
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None

    try:
        vn_id = grpc.create_virtual_network(
            name=f"e2e-cat-vn-{tag}",
            network_class=network_class,
            ipv4_cidr="10.200.0.0/16",
        )
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)

        subnet_id = grpc.create_subnet(
            name=f"e2e-cat-subnet-{tag}",
            virtual_network=vn_id,
            ipv4_cidr="10.200.100.0/24",
        )
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)

        yield {"virtual_network_id": vn_id, "subnet_id": subnet_id}
    except Exception:
        # If setup fails, cleanup any resources that were created
        logger.warning("Setup failed, cleaning up partial catalog networking resources: %s", tag)
        if subnet_id:
            try:
                grpc.delete_subnet(subnet_id=subnet_id)
            except Exception as e:
                logger.warning("Failed to cleanup subnet %s: %s", subnet_id, type(e).__name__)
        if vn_id:
            try:
                grpc.delete_virtual_network(vn_id=vn_id)
            except Exception as e:
                logger.warning("Failed to cleanup virtual network %s: %s", vn_id, type(e).__name__)
        raise
    finally:
        # Normal cleanup runs regardless of setup success/failure
        if subnet_id and subnet_cr_name:
            _delete_subnet_teardown(
                grpc,
                k8s_hub_client,
                subnet_id=subnet_id,
                subnet_cr_name=subnet_cr_name,
            )
        if vn_id and vn_cr_name:
            _delete_virtual_network_teardown(
                grpc,
                k8s_hub_client,
                vn_id=vn_id,
                vn_cr_name=vn_cr_name,
            )


@pytest.fixture(scope="module")
def default_subnet_id(catalog_networking: dict[str, str]) -> str:
    return catalog_networking["subnet_id"]
