from __future__ import annotations

import os
import subprocess
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    assert_grpc_rejected,
    assert_no_network_jobs,
    grpc_error_message,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_ready,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_security_group_update,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.sanity

_DISABLED_REASON = "ProvisioningDisabled"
_EXTERNAL_IP_DISABLED_MESSAGE = "provider networking is disabled"
_VIRTUAL_NETWORK_RESOURCE = "virtualnetwork"
_SUBNET_RESOURCE = "subnets.osac.openshift.io"
_SECURITY_GROUP_RESOURCE = "securitygroups.osac.openshift.io"
_EXTERNAL_IP_POOL_RESOURCE = "externalippool"


def _virtual_network_cidrs() -> tuple[str, str]:
    """Return a VirtualNetwork and Subnet CIDR pair unique to this xdist worker.

    The pair is derived from the worker instead of a random value so two workers
    cannot pick the same range; the 172.40.0.0/16 space is unused by the other
    suites, which start at 172.20.
    """
    worker_id = os.environ.get("PYTEST_XDIST_WORKER", "gw0")
    worker_num = int(worker_id.removeprefix("gw")) if worker_id.startswith("gw") else 0
    second_octet = 40 + worker_num
    return f"172.{second_octet}.0.0/16", f"172.{second_octet}.1.0/24"


def _assert_ready_without_provider_work(*, k8s: K8sClient, resource: str, name: str) -> None:
    """The resource is READY because the provider operation was skipped, and no job ran."""
    condition = k8s.get_condition(resource=resource, name=name, condition_type="Ready")
    assert condition.get("status") == "True", f"{resource}/{name} must be READY while disabled, got {condition}"
    assert condition.get("reason") == _DISABLED_REASON, (
        f"{resource}/{name} READY reason {condition.get('reason')!r}, expected {_DISABLED_REASON!r}: {condition}"
    )
    assert_no_network_jobs(k8s=k8s, resource=resource, name=name, context=f"{resource}/{name} disabled mode")


def _assert_tcp_rule_stored(*, grpc: GRPCClient, sg_id: str) -> None:
    status = grpc.get_security_group(sg_id=sg_id).get("object", {}).get("status", {})
    assert status.get("state") == "SECURITY_GROUP_STATE_READY", (
        f"SecurityGroup {sg_id} must stay READY after a stored rule update, got {status.get('state')!r}"
    )
    assert status.get("message", "") != "Updating", (
        f"SecurityGroup {sg_id} must not report an update it never sent, got {status.get('message')!r}"
    )


def _pool_external_ip_ids(*, grpc: GRPCClient, pool_id: str) -> list[str]:
    return [
        item["id"]
        for item in grpc.list_private_external_ips()
        if item.get("spec", {}).get("pool", {}).get("id") == pool_id
    ]


def test_network_resources_are_ready_without_provider_work(
    grpc: GRPCClient, k8s_hub_client: K8sClient, provider_networking_disabled: None
) -> None:
    """VirtualNetwork, Subnet, and SecurityGroup are logical resources while disabled.

    The suite is the deployed half of the disabled contract: the resources keep
    the normal API lifecycle and readiness gates, and the operator records no
    provider job for any of them.
    """
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    sg_id: str | None = None
    sg_cr_name: str | None = None

    virtual_network_cidr, subnet_cidr = _virtual_network_cidrs()
    try:
        vn_id, vn_cr_name = create_and_wait_for_virtual_network(
            grpc, k8s_hub_client, f"disabled-vnet-{uuid4().hex[:8]}", virtual_network_cidr
        )
        _assert_ready_without_provider_work(k8s=k8s_hub_client, resource=_VIRTUAL_NETWORK_RESOURCE, name=vn_cr_name)

        subnet_id, subnet_cr_name = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_id, subnet_cidr, name_prefix="disabled-subnet"
        )
        _assert_ready_without_provider_work(k8s=k8s_hub_client, resource=_SUBNET_RESOURCE, name=subnet_cr_name)

        sg_id = grpc.create_security_group(name=f"disabled-sg-{uuid4().hex[:8]}", virtual_network=vn_id)
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)
        _assert_ready_without_provider_work(k8s=k8s_hub_client, resource=_SECURITY_GROUP_RESOURCE, name=sg_cr_name)

        # A rule update is stored and starts no job, and the group stays READY
        # without the "Updating" message an enabled deployment reports.
        grpc.update_security_group_rules(
            sg_id=sg_id,
            ingress=[{"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"}],
            egress=[],
        )
        wait_for_security_group_update(grpc=grpc, sg_id=sg_id)
        _assert_tcp_rule_stored(grpc=grpc, sg_id=sg_id)
        _assert_ready_without_provider_work(k8s=k8s_hub_client, resource=_SECURITY_GROUP_RESOURCE, name=sg_cr_name)

        delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        sg_id = None
        sg_cr_name = None
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        subnet_id = None
        subnet_cr_name = None
        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        vn_id = None
        vn_cr_name = None
    finally:
        if sg_id is not None:
            delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        if subnet_id is not None:
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        if vn_id is not None:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)


def test_external_ip_create_is_rejected_without_provider_networking(
    grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient, provider_networking_disabled: None
) -> None:
    """An ExternalIPPool is logical, and an ExternalIP create is refused and stores nothing.

    The pool proves the disabled path for the resource that would otherwise
    allocate from a provider; the rejection is the accepted contract, so no
    ExternalIPAttachment or NATGateway can exist either.
    """
    pool_name = f"disabled-pool-{uuid4().hex[:8]}"
    pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(allocate_worker_subnet(prefix=30))])
    pool_cr_name: str | None = None
    try:
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        _assert_ready_without_provider_work(k8s=k8s_hub_client, resource=_EXTERNAL_IP_POOL_RESOURCE, name=pool_cr_name)

        before = _pool_external_ip_ids(grpc=private_grpc, pool_id=pool_id)
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_external_ip(name=f"disabled-eip-{uuid4().hex[:8]}", pool=pool_id)
        assert_grpc_rejected(exc_info, "FailedPrecondition")
        assert _EXTERNAL_IP_DISABLED_MESSAGE in grpc_error_message(exc_info.value), (
            f"expected {_EXTERNAL_IP_DISABLED_MESSAGE!r}, got: {grpc_error_message(exc_info.value)!r}"
        )
        assert _pool_external_ip_ids(grpc=private_grpc, pool_id=pool_id) == before, (
            "a rejected ExternalIP create must store nothing"
        )
    finally:
        private_grpc.delete_external_ip_pool(pool_id=pool_id)
        if pool_cr_name is not None:
            wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
