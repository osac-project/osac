"""E2E tests for OSAC-2486: Default networking tenant onboarding.

Tests:
1. Tenant onboarding auto-provisions default VN, Subnet(s), SG, and optionally NATGateway
2. ComputeInstance lifecycle using default networking (no explicit network_attachments)
"""

from __future__ import annotations

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI

_DEFAULT_LABEL = "osac.openshift.io/default=true"
_TENANT_ANNOTATION = "osac.openshift.io/tenant"
_PRIVATE_GRPC_PACKAGE = "osac.private.v1"


def _has_default_network_class(private_grpc: GRPCClient) -> bool:
    """Return True if a default NetworkClass with spec.defaults exists."""
    resp = private_grpc.call(service=f"{_PRIVATE_GRPC_PACKAGE}.NetworkClasses/List", data={"filter": "this.is_default == true"})
    return any(nc.get("spec", {}).get("defaults") for nc in resp.get("items", []))


def _get_tenant_default_networking_condition(private_grpc: GRPCClient, name: str) -> tuple[str, str]:
    """Return (status, reason) for the DefaultNetworkingReady condition from the FS DB.

    Uses Tenants/List then Tenants/Get to fetch the full tenant including status.conditions.
    For long-lived tenants (like tenant1) the reconciler has run and conditions are set,
    so they appear in the gRPC response (non-empty repeated fields are serialized in JSON).
    Returns ('', '') if conditions are not set yet.
    """
    list_resp = private_grpc.call(
        service=f"{_PRIVATE_GRPC_PACKAGE}.Tenants/List", data={"filter": f'this.metadata.name == "{name}"', "limit": 1}
    )
    items = list_resp.get("items", [])
    if not items:
        return "", ""
    tenant_id = items[0].get("id", "")
    if not tenant_id:
        return "", ""
    get_resp = private_grpc.call(service=f"{_PRIVATE_GRPC_PACKAGE}.Tenants/Get", data={"id": tenant_id})
    tenant = get_resp.get("object", get_resp)
    for cond in tenant.get("status", {}).get("conditions", []):
        ctype = str(cond.get("type", ""))
        if "DEFAULT_NETWORKING_READY" in ctype or ctype == "DefaultNetworkingReady":
            raw = cond.get("status", "")
            status = "True" if raw == "CONDITION_STATUS_TRUE" else ("False" if raw == "CONDITION_STATUS_FALSE" else raw)
            return status, cond.get("reason", "")
    return "", ""


def test_compute_instance_lifecycle_default_networking(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
) -> None:
    if not _has_default_network_class(private_grpc):
        pytest.skip("No default NetworkClass with spec.defaults configured in this environment")

    # tenant1's default networking must be Ready before we can create CIs using it.
    condition, reason = _get_tenant_default_networking_condition(private_grpc, "tenant1")
    if condition != "True" or reason == "NoDefaultNetworking":
        pytest.skip(
            f"DefaultNetworkingReady={condition!r} (reason={reason!r}) for tenant1"
            " — default networking not ready in this environment"
        )

    name = unique_name("e2e-ci-defnet")
    uuid: str = cli.create_compute_instance(name=name, template=vm_template)
    assert uuid in grpc.list_compute_instance_ids()

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    try:
        cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        attachments = cr.get("spec", {}).get("networkAttachments", [])
        assert len(attachments) >= 1, f"Expected auto-injected networkAttachments, got {attachments}"
        print(f"Auto-injected {len(attachments)} network attachment(s)")

        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vmi_ns, compute_instance_name=ci_name)
        assert vmi_ts != "", f"No VMI found on virt cluster for {ci_name}"
    finally:
        cli.delete_compute_instance(uuid=uuid)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=uuid)
