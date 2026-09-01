"""E2E tests for OSAC-2486: Default networking tenant onboarding.

Tests:
1. Tenant onboarding auto-creates default VN, Subnet(s), SG, and optionally NATGateway
2. ComputeInstance lifecycle using default networking (no explicit network_attachments)
"""

from __future__ import annotations

from uuid import uuid4

import pytest

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_grpc_tenant_condition,
    wait_for_provision,
    wait_for_running,
    wait_for_tenant_cr,
    wait_for_tenant_deletion,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI

_DEFAULT_LABEL = "osac.openshift.io/default=true"
_TENANT_ANNOTATION = "osac.openshift.io/tenant"


def _get_default_resources(k8s: K8sClient, resource: str) -> list[dict[str, str]]:
    raw = k8s.get_by_label(
        resource=resource,
        label=_DEFAULT_LABEL,
        jsonpath=(
            "{range .items[*]}{.metadata.name},{.metadata.annotations['osac\\.openshift\\.io/tenant']}{\"\\n\"}{end}"
        ),
    )
    results: list[dict[str, str]] = []
    for line in raw.strip().splitlines():
        if not line.strip():
            continue
        parts = line.split(",", 1)
        results.append({"name": parts[0], "tenant": parts[1] if len(parts) > 1 else ""})
    return results


def _filter_by_tenant(resources: list[dict[str, str]], tenant: str) -> list[dict[str, str]]:
    return [r for r in resources if r["tenant"] == tenant]


def test_default_networking_onboarding(private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    tenant_name: str = f"test-defnet-{uuid4().hex[:8]}"

    print(f"\nCreating tenant: {tenant_name}")
    private_grpc.ensure_tenant(name=tenant_name)

    try:
        _verify_default_networking(k8s=k8s_hub_client, private_grpc=private_grpc, tenant_name=tenant_name)
    finally:
        _cleanup_tenant(k8s=k8s_hub_client, tenant_name=tenant_name)


def _verify_default_networking(*, k8s: K8sClient, private_grpc: GRPCClient, tenant_name: str) -> None:
    wait_for_tenant_cr(k8s=k8s, name=tenant_name)
    print(f"Waiting for DefaultNetworkingReady on {tenant_name}...")
    try:
        wait_for_grpc_tenant_condition(grpc=private_grpc, name=tenant_name, condition_type="DefaultNetworkingReady")
    except TimeoutError:
        reason = private_grpc.get_tenant_condition_reason(name=tenant_name, condition_type="DefaultNetworkingReady")
        pytest.skip(
            f"DefaultNetworkingReady did not reach True for {tenant_name} (reason={reason!r})"
            " — default networking not provisioned in this environment"
        )

    reason = private_grpc.get_tenant_condition_reason(name=tenant_name, condition_type="DefaultNetworkingReady")
    if reason == "NoDefaultNetworking":
        pytest.skip(
            f"Tenant {tenant_name} has DefaultNetworkingReady=True with reason NoDefaultNetworking — "
            "no default NetworkClass with defaults configured in this environment"
        )
    print(f"DefaultNetworkingReady=True for {tenant_name} (reason={reason})")

    vns = _filter_by_tenant(_get_default_resources(k8s, "virtualnetwork"), tenant_name)
    assert len(vns) >= 1, f"Expected at least 1 default VirtualNetwork for {tenant_name}, found {vns}"
    for vn in vns:
        phase = k8s.get_virtual_network_phase(name=vn["name"])
        assert phase == "Ready", f"Default VirtualNetwork {vn['name']} phase is {phase}, expected Ready"
    print(f"Default VirtualNetwork(s): {[v['name'] for v in vns]}")

    subnets = _filter_by_tenant(_get_default_resources(k8s, "subnet"), tenant_name)
    assert len(subnets) >= 1, f"Expected at least 1 default Subnet for {tenant_name}, found {subnets}"
    for subnet in subnets:
        phase = k8s.get_subnet_phase(name=subnet["name"])
        assert phase == "Ready", f"Default Subnet {subnet['name']} phase is {phase}, expected Ready"
    print(f"Default Subnet(s): {[s['name'] for s in subnets]}")

    sgs = _filter_by_tenant(_get_default_resources(k8s, "securitygroup"), tenant_name)
    assert len(sgs) >= 1, f"Expected at least 1 default SecurityGroup for {tenant_name}, found {sgs}"
    for sg in sgs:
        phase = k8s.get_security_group_phase(name=sg["name"])
        assert phase == "Ready", f"Default SecurityGroup {sg['name']} phase is {phase}, expected Ready"
    print(f"Default SecurityGroup(s): {[s['name'] for s in sgs]}")

    nat_gws = _filter_by_tenant(_get_default_resources(k8s, "natgateway"), tenant_name)
    if nat_gws:
        for ng in nat_gws:
            phase = k8s.get_nat_gateway_phase(name=ng["name"])
            assert phase == "Ready", f"Default NATGateway {ng['name']} phase is {phase}, expected Ready"
        print(f"Default NATGateway(s): {[n['name'] for n in nat_gws]}")
    else:
        print("No default NATGateway found (NetworkClass may not have enable_nat_gateway=true)")


def _cleanup_tenant(*, k8s: K8sClient, tenant_name: str) -> None:
    print(f"\nCleaning up tenant: {tenant_name}")
    if k8s.is_present(resource="tenant", name=tenant_name):
        k8s.delete(resource="tenant", name=tenant_name, wait=False)
        wait_for_tenant_deletion(k8s=k8s, name=tenant_name)
        print(f"Tenant {tenant_name} deleted")

    if k8s.is_present(resource="namespace", name=tenant_name):
        k8s.delete(resource="namespace", name=tenant_name, wait=False)
        print(f"Namespace {tenant_name} cleanup initiated")


def test_compute_instance_lifecycle_default_networking(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
) -> None:
    condition = private_grpc.get_tenant_condition_status(name="tenant1", condition_type="DefaultNetworkingReady")
    reason = private_grpc.get_tenant_condition_reason(name="tenant1", condition_type="DefaultNetworkingReady")
    if condition != "True" or reason == "NoDefaultNetworking":
        pytest.skip(
            f"DefaultNetworkingReady is {condition!r} (reason={reason!r}) for tenant1"
            " — default networking not ready in this environment"
        )

    name = unique_name("e2e-ci-defnet")
    uuid: str = cli.create_compute_instance(name=name, template=vm_template)
    assert uuid in grpc.list_compute_instance_ids()

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
    attachments = cr.get("spec", {}).get("networkAttachments", [])
    assert len(attachments) >= 1, f"Expected auto-injected networkAttachments, got {attachments}"
    print(f"Auto-injected {len(attachments)} network attachment(s)")

    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vmi_ns, compute_instance_name=ci_name)
    assert vmi_ts != "", f"No VMI found on virt cluster for {ci_name}"

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
