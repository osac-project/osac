from __future__ import annotations

import logging
import re
import subprocess
from collections.abc import Generator
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_subnet_ready,
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
from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


@pytest.fixture(scope="session")
def ref_test_run_id() -> str:
    return uuid4().hex[:8]


@pytest.fixture(scope="session")
def ref_eip_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient, ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    """Create a ready ExternalIPPool owned exclusively by reference tests."""
    pool_name = f"ref-eip-pool-{ref_test_run_id}"
    pool_id: str | None = None
    pool_cr_name: str | None = None

    try:
        # OSAC-5608: do not borrow a concurrently-created pool as this suite's prerequisite.
        pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(allocate_worker_subnet())])
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        yield {"id": pool_id, "name": pool_name, "cr_name": pool_cr_name}
    finally:
        if pool_id:
            errors: list[Exception] = []
            try:
                _cleanup_ref_pool_external_ips(private_grpc, k8s_hub_client, pool_id)
            except Exception as exc:
                errors.append(exc)

            pool_deleted = False
            try:
                private_grpc.delete_external_ip_pool(pool_id=pool_id)
            except subprocess.CalledProcessError as exc:
                combined = (exc.stderr or "") + (exc.stdout or "")
                if re.search(r"Code:\s*NotFound", combined):
                    pool_deleted = True
                else:
                    errors.append(exc)
            except Exception as exc:
                errors.append(exc)
            else:
                pool_deleted = True

            if pool_deleted and not pool_cr_name:
                pool_cr_name = k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False)

            if (
                pool_deleted
                and pool_cr_name
                and k8s_hub_client.is_present(resource="externalippool", name=pool_cr_name)
            ):
                try:
                    wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
                except Exception as exc:
                    errors.append(exc)

            if len(errors) == 1:
                raise errors[0]
            if errors:
                raise ExceptionGroup("ExternalIPPool teardown failed", errors)


def _cleanup_ref_pool_external_ips(private_grpc: GRPCClient, k8s: K8sClient, pool_id: str) -> None:
    """Delete and wait for ExternalIPs owned by a reference-test pool."""
    pool_ip_ids: list[str] = []
    page_token = ""
    while True:
        data: dict[str, str] | None = {"pageToken": page_token} if page_token else None
        response: dict[str, Any] = private_grpc.call(service=f"{PRIVATE_API}.ExternalIPs/List", data=data)
        for item in response.get("items", []):
            if item.get("object", item).get("spec", {}).get("pool", {}).get("id") != pool_id:
                continue

            ip_id = item.get("id") or item.get("object", {}).get("id")
            if ip_id:
                pool_ip_ids.append(ip_id)

        page_token = response.get("nextPageToken", "")
        if not page_token:
            break

    for ip_id in pool_ip_ids:
        try:
            private_grpc.delete_external_ip(external_ip_id=ip_id)
        except subprocess.CalledProcessError as exc:
            combined = (exc.stderr or "") + (exc.stdout or "")
            if not re.search(r"Code:\s*NotFound", combined):
                raise

        ip_cr_name = k8s.get_external_ip_name(uuid=ip_id, checked=False)
        if ip_cr_name:
            wait_for_external_ip_deletion(k8s=k8s, name=ip_cr_name)


def _create_ref_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, vn_name: str
) -> Generator[dict[str, str], None, None]:
    vn_id: str | None = None
    vn_cr_name: str | None = None

    try:
        vn_id = grpc.create_virtual_network(name=vn_name, ipv4_cidr="10.210.0.0/16")
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
def ref_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    yield from _create_ref_virtual_network(grpc, k8s_hub_client, f"ref-vn-{ref_test_run_id}")


@pytest.fixture
def networking_ref_virtual_network(
    grpc: GRPCClient, k8s_hub_client: K8sClient, ref_test_run_id: str
) -> Generator[dict[str, str], None, None]:
    # Keep subnet cleanup independent from the SecurityGroup used by compute references.
    yield from _create_ref_virtual_network(grpc, k8s_hub_client, f"ref-vn-networking-{ref_test_run_id}")


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
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)
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
