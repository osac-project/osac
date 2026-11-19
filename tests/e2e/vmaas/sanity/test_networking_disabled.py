from __future__ import annotations

import logging
import os
import subprocess
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    assert_grpc_rejected,
    assert_no_network_jobs,
    grpc_error_message,
    security_group_cr_has_ingress_rule,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_ready,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import CommandTimedOut, StuckState, poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.sanity

logger = logging.getLogger(__name__)

_DISABLED_REASON = "ProvisioningDisabled"
_EXTERNAL_IP_DISABLED_MESSAGE = "provider networking is disabled"
_VIRTUAL_NETWORK_RESOURCE = "virtualnetwork"
_SUBNET_RESOURCE = "subnets.osac.openshift.io"
_SECURITY_GROUP_RESOURCE = "securitygroups.osac.openshift.io"
_EXTERNAL_IP_POOL_RESOURCE = "externalippool"
_TCP_22_RULE = {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"}


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
    """The update stored the rule and left the group READY with no update in progress."""
    security_group = grpc.get_security_group(sg_id=sg_id).get("object", {})
    spec = security_group.get("spec", {})
    ingress = spec.get("ingress") or []
    assert len(ingress) == 1, f"SecurityGroup {sg_id} must store exactly the requested ingress rule, got {ingress}"
    rule = ingress[0]
    assert rule.get("protocol") == "PROTOCOL_TCP", f"SecurityGroup {sg_id} stored protocol {rule.get('protocol')!r}"
    assert (rule.get("portFrom"), rule.get("portTo")) == (22, 22), (
        f"SecurityGroup {sg_id} stored ports {(rule.get('portFrom'), rule.get('portTo'))!r}"
    )
    assert rule.get("ipv4Cidr") == "0.0.0.0/0", f"SecurityGroup {sg_id} stored CIDR {rule.get('ipv4Cidr')!r}"
    assert not spec.get("egress"), f"SecurityGroup {sg_id} must keep the cleared egress list, got {spec.get('egress')}"

    status = security_group.get("status", {})
    assert status.get("state") == "SECURITY_GROUP_STATE_READY", (
        f"SecurityGroup {sg_id} must stay READY after a stored rule update, got {status.get('state')!r}"
    )
    assert status.get("message", "") != "Updating", (
        f"SecurityGroup {sg_id} must not report an update it never sent, got {status.get('message')!r}"
    )


def _wait_for_stored_security_group_rule(*, k8s: K8sClient, name: str) -> None:
    """Wait until the operator's CR carries the stored rule.

    A disabled-mode update sets no "Updating" status and the operator leaves the
    status untouched, so the CR spec is the only observable signal that the
    update was processed. Without it the readiness and job checks would re-read
    the pre-update state.
    """

    def _has_rule() -> bool:
        spec = k8s.get_json(resource=_SECURITY_GROUP_RESOURCE, name=name).get("spec", {})
        return security_group_cr_has_ingress_rule(spec)

    poll_until(
        fn=_has_rule,
        until=bool,
        retries=60,
        delay=2,
        description=f"{name} SecurityGroup CR carries the stored TCP/22 rule",
        retry_on_error=True,
        resource_kind=WaitResourceKind.SECURITY_GROUP,
        resource_id=name,
    )


def _pool_external_ip_ids(*, grpc: GRPCClient, pool_id: str) -> list[str]:
    return [
        item["id"]
        for item in grpc.list_private_external_ips()
        if item.get("spec", {}).get("pool", {}).get("id") == pool_id
    ]


def _cleanup_pool(*, private_grpc: GRPCClient, k8s: K8sClient, pool_id: str, pool_cr_name: str | None) -> None:
    """Best effort: remove any ExternalIP a regression left behind before the pool.

    A leftover ExternalIP blocks the pool delete, so each child is attempted
    independently, the pool delete waits for them to disappear, and a failure is
    logged instead of hiding the test result. A failed child discovery must not
    skip the pool delete itself.
    """
    try:
        child_ids = _pool_external_ip_ids(grpc=private_grpc, pool_id=pool_id)
    except Exception:
        logger.exception("Discovery of ExternalIPs in pool %s failed; deleting the pool anyway", pool_id)
        child_ids = []
    for external_ip_id in child_ids:
        try:
            private_grpc.call(service=f"{PRIVATE_API}.ExternalIPs/Delete", data={"id": external_ip_id})
        except (subprocess.CalledProcessError, CommandTimedOut):
            logger.exception("Cleanup of ExternalIP %s in pool %s failed", external_ip_id, pool_id)
    if child_ids:
        # A delete only accepts the request: the record disappears when its CR and
        # finalizer are gone, and the pool's dependent guard rejects a pool that
        # still has children.
        try:
            poll_until(
                fn=lambda: _pool_external_ip_ids(grpc=private_grpc, pool_id=pool_id),
                until=lambda remaining: not remaining,
                retries=30,
                delay=2,
                description=f"the ExternalIPPool {pool_id} children to be gone",
                retry_on_error=True,
                resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
                resource_id=pool_id,
            )
        except StuckState:
            logger.warning("children of ExternalIPPool %s are still listed; attempting the pool delete", pool_id)
    try:
        private_grpc.delete_external_ip_pool(pool_id=pool_id)
    except (subprocess.CalledProcessError, CommandTimedOut):
        logger.exception("Cleanup of ExternalIPPool %s failed", pool_id)
        return
    if pool_cr_name is not None:
        wait_for_external_ip_pool_deletion(k8s=k8s, name=pool_cr_name)


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
        # The ID is kept as soon as Create returns so a failing wait still leaves
        # teardown a resource it can delete.
        vn_id = grpc.create_virtual_network(name=f"disabled-vnet-{uuid4().hex[:8]}", ipv4_cidr=virtual_network_cidr)
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)
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
        grpc.update_security_group_rules(sg_id=sg_id, ingress=[_TCP_22_RULE], egress=[])
        _wait_for_stored_security_group_rule(k8s=k8s_hub_client, name=sg_cr_name)
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
        # Every deletion is attempted: one failing child must not strand its
        # parents, and cleanup errors are logged instead of replacing the test
        # result.
        pending = [
            ("SecurityGroup", sg_id, sg_cr_name, delete_and_wait_for_security_group),
            ("Subnet", subnet_id, subnet_cr_name, delete_and_wait_for_subnet),
            ("VirtualNetwork", vn_id, vn_cr_name, delete_and_wait_for_virtual_network),
        ]
        for kind, resource_id, cr_name, delete in pending:
            if resource_id is None:
                continue
            try:
                delete(grpc, k8s_hub_client, resource_id, cr_name)
            except Exception:
                logger.exception("Cleanup of %s %s failed", kind, resource_id)


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
        # Also removes an ExternalIP a regressed rejection left behind, which
        # would otherwise block the pool delete.
        try:
            _cleanup_pool(private_grpc=private_grpc, k8s=k8s_hub_client, pool_id=pool_id, pool_cr_name=pool_cr_name)
        except Exception:
            logger.exception("Cleanup of ExternalIPPool %s failed", pool_id)
