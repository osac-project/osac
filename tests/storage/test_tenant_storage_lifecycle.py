"""E2E test for tenant storage onboarding lifecycle (OSAC-77).

Validates the full storage provisioning lifecycle managed by the Storage Controller:
Stage 1 (StorageBackendReady) -> Stage 2 (ClusterStorageReady) -> ordered teardown.

Requires: storage controller enabled, AAP templates configured, storage backend
reachable (real VAST or mock VMS server).
"""

from __future__ import annotations

import textwrap
from uuid import uuid4

from tests.core.helpers import (
    wait_for_tenant_condition,
    wait_for_tenant_cr,
    wait_for_tenant_deletion,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


_NAMESPACE_MANIFEST = textwrap.dedent("""\
    apiVersion: v1
    kind: Namespace
    metadata:
      name: {name}
""")

_TENANT_MANIFEST = textwrap.dedent("""\
    apiVersion: osac.openshift.io/v1alpha1
    kind: Tenant
    metadata:
      name: {name}
      namespace: {namespace}
    spec: {{}}
""")


def test_tenant_storage_lifecycle(
    k8s_hub_client: K8sClient, storage_config_namespace: str
) -> None:
    tenant_name: str = f"test-storage-{uuid4().hex[:8]}"
    namespace: str = k8s_hub_client.namespace

    k8s_hub_client.apply(manifest=_NAMESPACE_MANIFEST.format(name=tenant_name))
    k8s_hub_client.apply(manifest=_TENANT_MANIFEST.format(name=tenant_name, namespace=namespace))

    try:
        _verify_provisioning(
            k8s=k8s_hub_client, tenant_name=tenant_name, storage_config_namespace=storage_config_namespace
        )
    finally:
        _trigger_teardown(k8s=k8s_hub_client, tenant_name=tenant_name)
        _verify_teardown(k8s=k8s_hub_client, tenant_name=tenant_name)


def _verify_provisioning(*, k8s: K8sClient, tenant_name: str, storage_config_namespace: str) -> None:
    # --- Tenant CR exists (we created it directly) ---
    wait_for_tenant_cr(k8s=k8s, name=tenant_name)

    # --- Storage finalizer set (requires Phase=Ready first, then storage controller reconcile) ---
    poll_until(
        fn=lambda: "osac.openshift.io/storage" in k8s.get_tenant_finalizers(name=tenant_name),
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


def _trigger_teardown(*, k8s: K8sClient, tenant_name: str) -> None:
    k8s.delete(resource="tenant", name=tenant_name, wait=False)


def _verify_teardown(*, k8s: K8sClient, tenant_name: str) -> None:
    # --- Tenant CR fully deleted (storage finalizer released = all cleanup done) ---
    wait_for_tenant_deletion(k8s=k8s, name=tenant_name)

    # Finalizer removal confirms deprovision jobs succeeded (SCs + Secrets cleaned up by AAP).

    # --- Clean up tenant namespace ---
    k8s.delete(resource="namespace", name=tenant_name, wait=False)
