"""E2E tests for OSAC-2486: Auto ExternalIP attachment lifecycle.

Tests:
1. Auto ExternalIP + ExternalIPAttachment created with correct labels on CI creation
2. Auto-cleanup on ComputeInstance deletion (resources removed, pool capacity restored)
3. Pool exhaustion returns FailedPrecondition
"""

from __future__ import annotations

import logging
import subprocess
from collections.abc import Iterator
from uuid import uuid4

import pytest

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    assert_grpc_rejected,
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until
from tests.vmaas.external_ip.helpers import allocate_worker_subnet, pool_status

logger = logging.getLogger(__name__)

_AUTO_CREATED_LABEL = "osac.openshift.io/auto-created"
_AUTO_CREATED_FOR_LABEL = "osac.openshift.io/auto-created-for"


def _find_auto_created_resources(items: list[dict], ci_id: str) -> list[dict]:
    return [item for item in items if item.get("metadata", {}).get("labels", {}).get(_AUTO_CREATED_FOR_LABEL) == ci_id]


@pytest.fixture(scope="module")
def auto_eip_pool(private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> Iterator[tuple[str, str]]:
    pool_name = f"test-auto-eip-{uuid4().hex[:8]}"
    subnet = allocate_worker_subnet(prefix=24)
    pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(subnet)])
    pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
    wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
    wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
    print(f"\nExternalIPPool ready: {pool_name} ({pool_id})")

    yield pool_id, pool_cr_name

    if not k8s_hub_client.is_present(resource="externalippool", name=pool_cr_name):
        return
    try:
        private_grpc.delete_external_ip_pool(pool_id=pool_id)
    except subprocess.CalledProcessError as exc:
        stderr = exc.stderr or ""
        if "NotFound" not in stderr:
            logger.warning("ExternalIPPool %s teardown failed: %s", pool_id, stderr.strip())
            return
    wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


def test_auto_external_ip_lifecycle(
    auto_eip_pool: tuple[str, str],
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
) -> None:
    pool_id, _pool_cr_name = auto_eip_pool

    condition = private_grpc.get_tenant_condition_status(name="tenant1", condition_type="DefaultNetworkingReady")
    reason = private_grpc.get_tenant_condition_reason(name="tenant1", condition_type="DefaultNetworkingReady")
    if condition != "True" or reason == "NoDefaultNetworking":
        pytest.skip(
            f"DefaultNetworkingReady is {condition!r} (reason={reason!r}) for tenant1"
            " — default networking not ready in this environment"
        )

    initial_status = pool_status(private_grpc, pool_id)
    initial_available: int = initial_status["available"]
    print(f"Pool initial status: {initial_status}")

    # --- Create ComputeInstance with --external-ip-attachment ---
    ci_name = unique_name("e2e-ci-auto-eip")
    ci_uuid: str = cli.create_compute_instance(name=ci_name, template=vm_template, external_ip_attachment=True)
    cr_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
    wait_for_running(k8s=k8s_hub_client, name=cr_name)
    print(f"ComputeInstance {cr_name} is Running")

    # --- Verify auto-created ExternalIP ---
    auto_eips = _find_auto_created_resources(grpc.list_external_ips(), ci_uuid)
    assert len(auto_eips) == 1, f"Expected 1 auto-created ExternalIP for {ci_uuid}, found {len(auto_eips)}"
    eip = auto_eips[0]
    assert eip["metadata"]["labels"].get(_AUTO_CREATED_LABEL) == "true"
    assert eip["status"].get("attached") is True, "Auto-created ExternalIP should be attached"
    eip_id: str = eip["id"]
    print(f"Auto-created ExternalIP: {eip_id}")

    # --- Verify auto-created ExternalIPAttachment ---
    auto_atts = _find_auto_created_resources(grpc.list_external_ip_attachments(), ci_uuid)
    assert len(auto_atts) == 1, f"Expected 1 auto-created ExternalIPAttachment for {ci_uuid}, found {len(auto_atts)}"
    att = auto_atts[0]
    assert att["metadata"]["labels"].get(_AUTO_CREATED_LABEL) == "true"
    att_id: str = att["id"]
    print(f"Auto-created ExternalIPAttachment: {att_id}")

    # --- Verify pool capacity decremented ---
    after_create_status = pool_status(private_grpc, pool_id)
    assert after_create_status["available"] == initial_available - 1, (
        f"Pool available should have decremented by 1: {initial_status} -> {after_create_status}"
    )

    # --- Delete ComputeInstance and verify auto-cleanup ---
    cli.delete_compute_instance(uuid=ci_uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
    print(f"ComputeInstance {cr_name} deleted")

    # Verify ExternalIPAttachment cleaned up
    poll_until(
        fn=lambda: att_id not in [a["id"] for a in grpc.list_external_ip_attachments()],
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"ExternalIPAttachment {att_id} auto-cleanup",
    )
    print("Auto-created ExternalIPAttachment cleaned up")

    # Verify ExternalIP cleaned up
    poll_until(
        fn=lambda: eip_id not in grpc.list_external_ip_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"ExternalIP {eip_id} auto-cleanup",
    )
    print("Auto-created ExternalIP cleaned up")

    # Verify pool capacity restored
    poll_until(
        fn=lambda: pool_status(private_grpc, pool_id)["available"],
        until=lambda v: v == initial_available,
        retries=30,
        delay=5,
        description="Pool capacity restored",
    )
    print(f"Pool capacity restored: {pool_status(private_grpc, pool_id)}")


def test_auto_external_ip_pool_exhaustion(
    private_grpc: GRPCClient, grpc: GRPCClient, k8s_hub_client: K8sClient
) -> None:
    condition = private_grpc.get_tenant_condition_status(name="tenant1", condition_type="DefaultNetworkingReady")
    reason = private_grpc.get_tenant_condition_reason(name="tenant1", condition_type="DefaultNetworkingReady")
    if condition != "True" or reason == "NoDefaultNetworking":
        pytest.skip(
            f"DefaultNetworkingReady is {condition!r} (reason={reason!r}) for tenant1"
            " — default networking not ready in this environment"
        )

    # --- Create a tiny /30 pool (2 usable IPs) ---
    pool_name = f"test-exhaust-{uuid4().hex[:8]}"
    subnet = allocate_worker_subnet(prefix=30)
    pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(subnet)])
    pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
    wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
    wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)

    status = pool_status(private_grpc, pool_id)
    total_capacity: int = status["total"]
    print(f"\nExhaustion pool ready: {pool_name} (total={total_capacity})")

    created_ci_ids: list[str] = []
    created_ci_names: list[str] = []

    try:
        # --- Exhaust all pool capacity ---
        for i in range(total_capacity):
            ci_uuid = grpc.create_compute_instance(
                catalog_item=_get_any_catalog_item(grpc), auto_external_ip_attachment=True
            )
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
            created_ci_ids.append(ci_uuid)
            created_ci_names.append(ci_name)
            print(f"Created CI {i + 1}/{total_capacity}: {ci_uuid}")

        remaining = pool_status(private_grpc, pool_id)
        assert remaining["available"] == 0, f"Pool should be exhausted, but available={remaining['available']}"

        # --- Attempt creation when pool is exhausted ---
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_compute_instance(catalog_item=_get_any_catalog_item(grpc), auto_external_ip_attachment=True)
        assert_grpc_rejected(exc_info, "FailedPrecondition")
        print("Pool exhaustion correctly returned FailedPrecondition")

    finally:
        # --- Cleanup: delete CIs (auto-cleanup restores pool) ---
        for ci_uuid, ci_name in zip(created_ci_ids, created_ci_names, strict=True):
            try:
                grpc.delete_compute_instance(ci_id=ci_uuid)
            except subprocess.CalledProcessError:
                logger.warning("Failed to delete CI %s during cleanup", ci_uuid)
                continue
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)

        # --- Cleanup pool ---
        try:
            private_grpc.delete_external_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError as exc:
            stderr = exc.stderr or ""
            if "NotFound" not in stderr:
                logger.warning("Pool %s cleanup failed: %s", pool_id, stderr.strip())
        if k8s_hub_client.is_present(resource="externalippool", name=pool_cr_name):
            wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


def _get_any_catalog_item(grpc: GRPCClient) -> str:
    items = grpc.list_compute_instance_catalog_item_ids()
    assert items, "No ComputeInstanceCatalogItems found — cannot create compute instance"
    return items[0]
