"""Fixtures for resource-reference tests that need provider networking.

ExternalIPs, and so NATGateways, are offered only with provider networking enabled. The shared
reference VirtualNetwork comes from the standard reference suite's conftest.
"""

from __future__ import annotations

import re
import subprocess
from collections.abc import Generator
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.references.conftest import ref_test_run_id, ref_virtual_network  # noqa: F401


@pytest.fixture(scope="session")
def ref_eip_pool(
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    ref_test_run_id: str,  # noqa: F811
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
