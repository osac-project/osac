"""E2E test for CaaS cluster storage provisioning lifecycle (OSAC-1123).

Validates the CaaS storage path (Stage 3) of the Storage Controller:
1. ClusterOrder reaches Ready with tenant annotation
2. Storage controller adds cluster-storage finalizer to ClusterOrder
3. AAP provisioning job installs CSI + StorageClass on CaaS cluster
4. ClusterStorageReady condition set on ClusterOrder
5. Tenant.status.clusterStorage[] updated with cluster entry
6. Teardown removes finalizer and clusterStorage entry on ClusterOrder deletion

Requires: storage controller enabled, AAP templates configured, CaaS
infrastructure configured (cluster template, pull secret).
"""

from __future__ import annotations

import contextlib
import json
import logging
from pathlib import Path
from uuid import uuid4

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.helpers import (
    wait_for_cluster_deletion,
    wait_for_cluster_order_condition,
    wait_for_cluster_order_cr,
    wait_for_cluster_ready,
    wait_for_tenant_cluster_storage_entry,
    wait_for_tenant_cluster_storage_entry_removed,
    wait_for_tenant_condition,
    wait_for_tenant_cr,
    wait_for_tenant_deletion,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.storage.conftest import NAMESPACE_MANIFEST, TENANT_MANIFEST

logger = logging.getLogger(__name__)


def test_caas_cluster_storage_lifecycle(
    k8s_hub_client: K8sClient, cli: OsacCLI, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
) -> None:
    """Verify the CaaS cluster storage provisioning and teardown lifecycle.

    Creates a Tenant, provisions a CaaS cluster with storage, verifies the
    storage controller adds the cluster-storage finalizer and sets conditions,
    then validates that teardown correctly removes the finalizer and cluster
    storage entries.
    """
    tenant_name: str = f"test-caas-storage-{uuid4().hex[:8]}"
    namespace: str = k8s_hub_client.namespace
    co_name: str | None = None
    cluster_uuid: str | None = None

    try:
        # --- Setup: create Tenant and its namespace ---
        k8s_hub_client.apply(manifest=NAMESPACE_MANIFEST.format(name=tenant_name))
        k8s_hub_client.apply(manifest=TENANT_MANIFEST.format(name=tenant_name, namespace=namespace))
        wait_for_tenant_cr(k8s=k8s_hub_client, name=tenant_name)

        # --- VMaaS storage stages must complete before CaaS path runs ---
        wait_for_tenant_condition(k8s=k8s_hub_client, name=tenant_name, condition_type="StorageBackendReady")

        # --- Create ClusterOrder and associate with our Tenant ---
        name = unique_name("e2e-cluster")
        cluster_uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
        )
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_uuid)

        _, rc = k8s_hub_client.patch(
            resource="clusterorder",
            name=co_name,
            patch=json.dumps({"metadata": {"annotations": {"osac.openshift.io/tenant": tenant_name}}}),
        )
        assert rc == 0, f"Failed to patch ClusterOrder {co_name} with tenant annotation"

        # --- Wait for cluster provisioning ---
        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

        # --- Verify CaaS storage provisioning ---
        _verify_provisioning(k8s=k8s_hub_client, tenant_name=tenant_name, co_name=co_name)

    finally:
        # --- Teardown: delete ClusterOrder and verify storage cleanup ---
        # Each teardown step is wrapped individually so that a failure in one
        # step does not prevent subsequent cleanup from running.  Assertion
        # errors from verification are collected and re-raised after all
        # cleanup completes so they can fail the test.
        teardown_assertion_error: AssertionError | None = None

        if cluster_uuid is not None:
            with contextlib.suppress(Exception):
                cli.delete_cluster(uuid=cluster_uuid)

        # Always verify cluster removal when co_name is known, even when the
        # ClusterOrder CR has already been removed by a fast deletion path.
        if co_name is not None:
            try:
                _verify_teardown(k8s=k8s_hub_client, tenant_name=tenant_name, co_name=co_name)
            except AssertionError as exc:
                teardown_assertion_error = exc
            except Exception:
                logger.warning("ClusterOrder teardown verification failed for %s", co_name, exc_info=True)

        try:
            if k8s_hub_client.is_present(resource="tenant", name=tenant_name):
                k8s_hub_client.delete(resource="tenant", name=tenant_name, wait=False)
                wait_for_tenant_deletion(k8s=k8s_hub_client, name=tenant_name)
        except Exception:
            logger.warning("Tenant teardown failed for %s", tenant_name, exc_info=True)

        try:
            if k8s_hub_client.is_present(resource="namespace", name=tenant_name):
                k8s_hub_client.delete(resource="namespace", name=tenant_name, wait=False)
        except Exception:
            logger.warning("Namespace teardown failed for %s", tenant_name, exc_info=True)

        if teardown_assertion_error is not None:
            raise teardown_assertion_error


def _verify_provisioning(*, k8s: K8sClient, tenant_name: str, co_name: str) -> None:
    """Verify CaaS storage provisioning: finalizer, condition, and tenant entry."""
    # --- cluster-storage finalizer added to ClusterOrder ---
    poll_until(
        fn=lambda: "osac.openshift.io/cluster-storage" in k8s.get_cluster_order_finalizers(name=co_name, checked=False),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"cluster-storage finalizer on {co_name}",
    )

    # --- ClusterStorageReady=True on ClusterOrder ---
    wait_for_cluster_order_condition(k8s=k8s, name=co_name, condition_type="ClusterStorageReady")

    # --- Tenant.status.clusterStorage has entry for this cluster ---
    entry = wait_for_tenant_cluster_storage_entry(k8s=k8s, tenant_name=tenant_name, cluster_name=co_name)
    assert entry.get("ready") is True, f"clusterStorage entry for {co_name} is not ready: {entry}"


def _verify_teardown(*, k8s: K8sClient, tenant_name: str, co_name: str) -> None:
    """Verify storage teardown: ClusterOrder removal and tenant entry cleanup."""
    # --- ClusterOrder fully deleted (storage finalizer released) ---
    wait_for_cluster_deletion(k8s=k8s, name=co_name)

    # --- Tenant.status.clusterStorage entry removed ---
    if k8s.is_present(resource="tenant", name=tenant_name):
        wait_for_tenant_cluster_storage_entry_removed(k8s=k8s, tenant_name=tenant_name, cluster_name=co_name)

        # --- Tenant still healthy ---
        phase: str = k8s.get_tenant_phase(name=tenant_name, checked=False)
        assert phase != "Failed", f"Tenant {tenant_name} entered Failed phase after ClusterOrder deletion"
