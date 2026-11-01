"""E2E test for tenant storage onboarding lifecycle (OSAC-77).

Validates the full storage provisioning lifecycle managed by the Storage Controller:
Stage 1 (StorageBackendReady) -> Stage 2 (ClusterStorageReady) -> ordered teardown.

Requires: storage controller enabled, AAP templates configured, storage backend
reachable (real VAST or mock VMS server).
"""

from __future__ import annotations

from uuid import uuid4

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_tenant_condition, wait_for_tenant_cr, wait_for_tenant_deletion
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.storage.helpers import delete_storage_tenant


def test_tenant_storage_lifecycle(
    k8s_hub_client: K8sClient, private_grpc: GRPCClient, storage_config_namespace: str
) -> None:
    """Verify the full tenant storage onboarding and teardown lifecycle.

    Creates a Tenant, waits for storage provisioning stages (StorageBackendReady
    and ClusterStorageReady), validates storage classes and hub secrets, then
    verifies ordered teardown including tenant secret cleanup.
    """
    tenant_name: str = f"test-storage-{uuid4().hex[:8]}"

    try:
        private_grpc.ensure_tenant(name=tenant_name)
        _verify_provisioning(
            k8s=k8s_hub_client, tenant_name=tenant_name, storage_config_namespace=storage_config_namespace
        )
    finally:
        # Each teardown step is wrapped individually so that a failure in one
        # step does not prevent subsequent verification from running.
        # Cleanup failures must fail the test, including API/namespace timeouts.
        teardown_error: Exception | None = None

        try:
            delete_storage_tenant(grpc=private_grpc, k8s=k8s_hub_client, tenant_name=tenant_name)
        except Exception as exc:
            teardown_error = exc

        try:
            _verify_teardown(
                k8s=k8s_hub_client, tenant_name=tenant_name, storage_config_namespace=storage_config_namespace
            )
        except Exception as exc:
            if teardown_error is None:
                teardown_error = exc

        if teardown_error is not None:
            raise teardown_error


def _verify_provisioning(*, k8s: K8sClient, tenant_name: str, storage_config_namespace: str) -> None:
    """Verify storage provisioning stages: finalizer, backend ready, and storage classes."""
    # --- Tenant CR reconciled from the API-created tenant ---
    wait_for_tenant_cr(k8s=k8s, name=tenant_name)

    # --- Storage finalizer set (requires Phase=Ready first, then storage controller reconcile) ---
    poll_until(
        fn=lambda: "osac.openshift.io/storage" in k8s.get_tenant_finalizers(name=tenant_name, checked=False),
        until=lambda v: v is True,
        retries=30,
        delay=2,
        description=f"Storage finalizer on {tenant_name}",
    )

    # --- Stage 1: StorageBackendReady ---
    wait_for_tenant_condition(k8s=k8s, name=tenant_name, condition_type="StorageBackendReady")

    hub_secret_count: int = k8s.count_secrets_by_tenant(tenant_name=tenant_name, namespace=storage_config_namespace)
    assert hub_secret_count >= 1, (
        f"Expected hub Secret for tenant {tenant_name} in {storage_config_namespace}, found {hub_secret_count}"
    )

    # --- Stage 2: ClusterStorageReady ---
    wait_for_tenant_condition(k8s=k8s, name=tenant_name, condition_type="ClusterStorageReady")

    # --- status.storageClasses populated with name + tier ---
    resolved_scs: list[dict[str, str]] = k8s.get_tenant_storage_classes(name=tenant_name)
    assert len(resolved_scs) >= 1, f"Expected status.storageClasses populated, got {resolved_scs}"
    for sc in resolved_scs:
        assert sc.get("name"), f"Resolved SC missing name: {sc}"
        assert sc.get("tier"), f"Resolved SC missing tier: {sc}"

    # --- Resolved SCs reference real StorageClasses ---
    for sc in resolved_scs:
        assert k8s.is_present(resource="storageclass", name=sc["name"]), (
            f"status.storageClasses references non-existent SC: {sc['name']}"
        )

    # --- If per-tenant SCs exist, verify labels ---
    tenant_sc_names: list[str] = k8s.list_storage_class_names_by_tenant(tenant_name=tenant_name)
    for sc_name in tenant_sc_names:
        labels: dict[str, str] = k8s.get_storage_class_labels(name=sc_name)
        assert labels.get("osac.openshift.io/storage-tier"), (
            f"StorageClass {sc_name} missing osac.openshift.io/storage-tier label"
        )


def _verify_teardown(*, k8s: K8sClient, tenant_name: str, storage_config_namespace: str) -> None:
    """Verify tenant deletion and confirm tenant-scoped secrets are cleaned up."""
    # --- Tenant CR fully deleted (storage finalizer released = all cleanup done) ---
    wait_for_tenant_deletion(k8s=k8s, name=tenant_name)

    # Finalizer removal confirms deprovision jobs succeeded (SCs + Secrets cleaned up by AAP).

    # --- Verify tenant-scoped secrets removed from storage config namespace ---
    remaining_secrets: int = k8s.count_secrets_by_tenant(tenant_name=tenant_name, namespace=storage_config_namespace)
    assert remaining_secrets == 0, (
        f"Expected tenant-scoped secrets removed after teardown, "
        f"but found {remaining_secrets} in {storage_config_namespace}"
    )
