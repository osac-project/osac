from __future__ import annotations

import logging
import re
import subprocess
from collections.abc import Generator
from typing import Any
from uuid import uuid4

import pytest

from tests.core.grpc_client import PUBLIC_API, GRPCClient
from tests.core.helpers import (
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


@pytest.fixture(scope="session")
def ref_test_run_id() -> str:
    return uuid4().hex[:8]


@pytest.fixture(scope="session")
def ref_network_class(grpc: GRPCClient) -> str:
    response: dict[str, Any] = grpc.call(service=f"{PUBLIC_API}.NetworkClasses/List")
    items = response.get("items", [])
    assert items, "No network classes found; set OSAC_NETWORK_CLASS or deploy a NetworkClass"
    return items[0]["metadata"]["name"]


@pytest.fixture(scope="session")
def ref_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, ref_network_class: str, ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    vn_name = f"ref-vn-{ref_test_run_id}"
    vn_id: str | None = None
    vn_cr_name: str | None = None

    try:
        vn_id = grpc.create_virtual_network(name=vn_name, network_class=ref_network_class, ipv4_cidr="10.210.0.0/16")
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)
        yield {"id": vn_id, "name": vn_name, "cr_name": vn_cr_name}
    except Exception:
        if vn_id:
            try:
                grpc.delete_virtual_network(vn_id=vn_id)
            except Exception as e:
                logger.warning("Failed to cleanup VN %s: %s", vn_id, type(e).__name__)
        raise
    finally:
        if vn_id and vn_cr_name:
            _safe_delete_vn(grpc, k8s_hub_client, vn_id=vn_id, vn_cr_name=vn_cr_name)


@pytest.fixture(scope="session")
def ref_subnet(
    grpc: GRPCClient, k8s_hub_client: K8sClient, ref_virtual_network: dict[str, str], ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    subnet_name = f"ref-subnet-{ref_test_run_id}"
    subnet_id: str | None = None
    subnet_cr_name: str | None = None

    try:
        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.Subnets/Create",
            data={
                "object": {
                    "metadata": {"name": subnet_name},
                    "spec": {"virtual_network": {"name": ref_virtual_network["name"]}, "ipv4_cidr": "10.210.100.0/24"},
                }
            },
        )
        subnet_id = response["object"]["id"]
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)
        yield {"id": subnet_id, "name": subnet_name, "cr_name": subnet_cr_name}
    except Exception:
        if subnet_id:
            try:
                grpc.delete_subnet(subnet_id=subnet_id)
            except Exception as e:
                logger.warning("Failed to cleanup subnet %s: %s", subnet_id, type(e).__name__)
        raise
    finally:
        if subnet_id and subnet_cr_name:
            _safe_delete_subnet(grpc, k8s_hub_client, subnet_id=subnet_id, subnet_cr_name=subnet_cr_name)


@pytest.fixture(scope="session")
def ref_security_group(
    grpc: GRPCClient, k8s_hub_client: K8sClient, ref_virtual_network: dict[str, str], ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    sg_name = f"ref-sg-{ref_test_run_id}"
    sg_id: str | None = None
    sg_cr_name: str | None = None

    try:
        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.SecurityGroups/Create",
            data={
                "object": {
                    "metadata": {"name": sg_name},
                    "spec": {"virtual_network": {"name": ref_virtual_network["name"]}},
                }
            },
        )
        sg_id = response["object"]["id"]
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)
        yield {"id": sg_id, "name": sg_name, "cr_name": sg_cr_name}
    except Exception:
        if sg_id:
            try:
                grpc.delete_security_group(sg_id=sg_id)
            except Exception as e:
                logger.warning("Failed to cleanup SG %s: %s", sg_id, type(e).__name__)
        raise
    finally:
        if sg_id and sg_cr_name:
            _safe_delete_sg(grpc, k8s_hub_client, sg_id=sg_id, sg_cr_name=sg_cr_name)


def _safe_delete_vn(grpc: GRPCClient, k8s: K8sClient, *, vn_id: str, vn_cr_name: str) -> None:
    try:
        grpc.delete_virtual_network(vn_id=vn_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if not re.search(r"Code:\s*NotFound", combined):
            logger.warning("VN %s teardown failed: %s", vn_id, combined.strip())
            return
    if k8s.is_present(resource="virtualnetwork", name=vn_cr_name):
        wait_for_virtual_network_deletion(k8s=k8s, name=vn_cr_name)


def _safe_delete_subnet(grpc: GRPCClient, k8s: K8sClient, *, subnet_id: str, subnet_cr_name: str) -> None:
    try:
        grpc.delete_subnet(subnet_id=subnet_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if not re.search(r"Code:\s*NotFound", combined):
            logger.warning("Subnet %s teardown failed: %s", subnet_id, combined.strip())
            return
    if k8s.is_present(resource="subnet", name=subnet_cr_name):
        wait_for_subnet_deletion(k8s=k8s, name=subnet_cr_name)


def _safe_delete_sg(grpc: GRPCClient, k8s: K8sClient, *, sg_id: str, sg_cr_name: str) -> None:
    try:
        grpc.delete_security_group(sg_id=sg_id)
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if not re.search(r"Code:\s*NotFound", combined):
            logger.warning("SG %s teardown failed: %s", sg_id, combined.strip())
            return
    if k8s.is_present(resource="securitygroup", name=sg_cr_name):
        wait_for_security_group_deletion(k8s=k8s, name=sg_cr_name)
