"""Idempotent delete-and-wait helpers shared by the Netris networking suites.

Each helper re-issues the delete while the object lingers and tolerates an object
that is already gone, so a test and a fixture finalizer can both call it.
"""

from __future__ import annotations

import subprocess
from collections.abc import Callable
from typing import Any

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind

# Poll often; proceed as soon as API + CR are gone. Caps match existing helpers (~5m / ~10m).
POLL_DELAY = 2
CHILD_RETRIES = 150
FABRIC_RETRIES = 300


def nat_gateway_ids(grpc: GRPCClient) -> list[str]:
    return [str(item.get("id") or "") for item in grpc.call(service="osac.public.v1.NATGateways/List").get("items", [])]


def describe_delete_error(exc: Exception) -> str:
    """Summarize a failed delete without the command line, which carries the bearer token."""
    detail = f"{exc.stderr or ''} {exc.stdout or ''}" if isinstance(exc, subprocess.CalledProcessError) else str(exc)
    return f"{type(exc).__name__}: {' '.join(detail.split())[:300]}"


def _cr_absent(k8s: K8sClient, kind: str, uuid: str, resource: str | None = None) -> bool:
    """True only when a successful list finds no CR labelled with `uuid`; a kubectl error raises and is retried."""
    return k8s.is_absent_by_label(resource=resource or kind, label=f"osac.openshift.io/{kind}-uuid={uuid}")


def wait_owned_gone(
    *,
    description: str,
    api_gone: Callable[[], bool],
    cr_gone: Callable[[], bool],
    resource_kind: WaitResourceKind,
    resource_id: str,
    delete: Callable[[], object] | None = None,
    retries: int = CHILD_RETRIES,
    delay: int = POLL_DELAY,
) -> None:
    """Poll until API and K8s CR are gone. Re-issue delete while they linger."""

    last_error: list[str] = []

    def _tick() -> bool:
        if delete is not None:
            # Already gone, or still deprovisioning: the next tick re-checks. A rejected delete is kept so
            # that a timeout can name it.
            try:
                delete()
            except Exception as exc:
                last_error[:] = [describe_delete_error(exc)]
        return bool(api_gone()) and bool(cr_gone())

    try:
        poll_until(
            fn=_tick,
            until=lambda gone: gone is True,
            retries=retries,
            delay=delay,
            description=description,
            # A failed API or CR read is a blip on a live lab, not proof either way: read again.
            retry_on_error=True,
            resource_kind=resource_kind,
            resource_id=resource_id,
        )
    except TimeoutError as exc:
        if last_error:
            raise TimeoutError(f"{exc} Last delete error: {last_error[0]}") from exc
        raise


def teardown_nat(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    nat_id = net.get("nat_id")
    if not nat_id:
        return
    vnet_name = net.get("vnet_name") or nat_id
    wait_owned_gone(
        description=f"{vnet_name} NATGateway gone",
        api_gone=lambda nid=nat_id: nid not in nat_gateway_ids(grpc),
        cr_gone=lambda nid=nat_id: _cr_absent(k8s, "natgateway", nid),
        delete=lambda nid=nat_id: grpc.delete_nat_gateway(nat_gateway_id=nid),
        retries=FABRIC_RETRIES,
        resource_kind=WaitResourceKind.NAT_GATEWAY,
        resource_id=nat_id,
    )


def teardown_nat_eip(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    eip_id = net.get("nat_eip_id")
    if not eip_id:
        return
    teardown_external_ip(grpc, k8s, eip_id, f"{net.get('vnet_name') or eip_id} NAT EIP")


def teardown_external_ip(grpc: GRPCClient, k8s: K8sClient, eip_id: str, label: str) -> None:
    """Delete an ExternalIP until its API record and CR are gone.

    The server refuses the delete while an attachment or NATGateway record still points at it, so the delete is
    re-issued until the dependent record is gone too.
    """
    wait_owned_gone(
        description=f"{label} ExternalIP {eip_id} gone",
        api_gone=lambda: eip_id not in grpc.list_external_ip_ids(),
        cr_gone=lambda: _cr_absent(k8s, "externalip", eip_id),
        delete=lambda: grpc.delete_external_ip(external_ip_id=eip_id),
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=eip_id,
    )


def teardown_external_ip_attachment(grpc: GRPCClient, k8s: K8sClient, attachment_id: str, label: str) -> None:
    """Delete an ExternalIPAttachment until its API record and CR are gone."""
    wait_owned_gone(
        description=f"{label} ExternalIPAttachment {attachment_id} gone",
        api_gone=lambda: attachment_id not in grpc.list_external_ip_attachment_ids(),
        cr_gone=lambda: _cr_absent(k8s, "externalipattachment", attachment_id),
        delete=lambda: grpc.delete_external_ip_attachment(attachment_id=attachment_id),
        resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
        resource_id=attachment_id,
    )


def teardown_sg(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    sg_id = net.get("sg_id")
    if not sg_id:
        return
    vnet_name = net.get("vnet_name") or sg_id
    wait_owned_gone(
        description=f"{vnet_name} SecurityGroup gone",
        api_gone=lambda sid=sg_id: sid not in grpc.list_security_group_ids(),
        cr_gone=lambda sid=sg_id: _cr_absent(k8s, "securitygroup", sid, "securitygroups.osac.openshift.io"),
        delete=lambda sid=sg_id: grpc.delete_security_group(sg_id=sid),
        resource_kind=WaitResourceKind.SECURITY_GROUP,
        resource_id=sg_id,
    )


def teardown_subnets(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    vnet_name = net.get("vnet_name") or "subnet"
    for sub_id in (net.get("sub_a_id"), net.get("sub_b_id")):
        if not sub_id:
            continue
        wait_owned_gone(
            description=f"{vnet_name} subnet {sub_id} gone",
            api_gone=lambda sid=sub_id: sid not in grpc.list_subnet_ids(),
            cr_gone=lambda sid=sub_id: _cr_absent(k8s, "subnet", sid, "subnets.osac.openshift.io"),
            delete=lambda sid=sub_id: grpc.delete_subnet(subnet_id=sid),
            retries=FABRIC_RETRIES,
            resource_kind=WaitResourceKind.SUBNET,
            resource_id=sub_id,
        )


def teardown_vnet(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    vnet_id = net.get("vnet_id")
    if not vnet_id:
        return
    vnet_name = net.get("vnet_name") or vnet_id
    wait_owned_gone(
        description=f"{vnet_name} VirtualNetwork gone",
        api_gone=lambda vid=vnet_id: vid not in grpc.list_virtual_network_ids(),
        cr_gone=lambda vid=vnet_id: _cr_absent(k8s, "virtualnetwork", vid),
        delete=lambda vid=vnet_id: grpc.delete_virtual_network(vn_id=vid),
        retries=FABRIC_RETRIES,
        resource_kind=WaitResourceKind.VIRTUAL_NETWORK,
        resource_id=vnet_id,
    )
