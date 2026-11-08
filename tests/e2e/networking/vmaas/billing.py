"""Billing journeys of the Netris VMaaS suite. `test_vmaas_networking.py` runs them on its own resources.

They send no traffic. Each one needs the metering echo adapter and skips without it (`billing_metering`).

ExternalIP allocation: the allocation meter bills an ExternalIP while it is ALLOCATED and through
DELETING until it is physically deleted (osac-metering ``0001-meters.yaml``),
with the dimensions pool, ip_family and attached. The state time of DELETING is
the time the delete was accepted, so a delete must neither erase the life
billed before it nor reverse it with a negative heartbeat.

NATGateway: the NAT gateway is the only billed resource whose dimensions are two
references (its virtual network and its external IP), and references render
by name in heartbeats, so this journey pins reference-based attribution.
"""

from __future__ import annotations

import contextlib
from datetime import datetime
from decimal import Decimal
from typing import Any
from uuid import uuid4

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_nat_gateway_deletion,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import (
    CR_STAMP_TOLERANCE,
    HEARTBEAT_TYPE,
    MeteringCollector,
    assert_contiguous_exact_seconds,
    assert_dimension_sets_disjoint,
    assert_exact_contiguous_life,
    delete_recording_timestamp,
    parse_time,
    usage_interval,
)
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind

HEARTBEAT_TIMEOUT: int = 300


def external_ip_bills_allocation_through_deletion(
    *,
    metering: MeteringCollector,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    pool: dict[str, Any],
) -> None:
    """An ALLOCATED ExternalIP bills per interval with the allocation meter's
    dimensions; deletion bills the final partial interval up to the recorded
    deletion, then ends billing, and no negative heartbeat reverses billing
    from before the deletion."""
    pool_name: str = private_grpc.get_external_ip_pool(pool_id=pool["id"])["object"]["metadata"]["name"]
    eip_name = f"billing-eip-{uuid4().hex[:8]}"

    eip_response: dict[str, Any] = grpc.call(
        service=f"{PUBLIC_API}.ExternalIPs/Create",
        data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"id": pool["id"]}}}},
    )
    eip_id: str = eip_response["object"]["id"]
    pool.setdefault("suite_eip_ids", set()).add(eip_id)
    # Resolve the CR before any delete can remove it: the deletion assertions
    # below need its recorded deletion timestamp.
    eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
    try:
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr_name)

        heartbeat = metering.heartbeat_with_dimensions(
            eip_id,
            {"pool": pool_name, "attached": "false"},
            timeout=HEARTBEAT_TIMEOUT,
            resource_kind=WaitResourceKind.EXTERNAL_IP,
        )
        # The ExternalIP was created without a project, so its heartbeats must
        # carry no project dimension.
        metering.expect_no_project()
        metering.verify_event(heartbeat, resource_id=eip_id)

        data = heartbeat["data"]
        assert data["meter"] == "allocation", f"meter is {data['meter']!r}, want 'allocation'"
        assert data["usage"]["unit"] == "second", f"unit is {data['usage']['unit']!r}"
        assert data["billing_dimensions"]["pool"] == pool_name
        assert data["billing_dimensions"]["ip_family"] == "ipv4"
        assert data["billing_dimensions"]["attached"] == "false"

        poll_until(
            fn=lambda: len(metering.get_all_events(HEARTBEAT_TYPE, eip_id)),
            until=lambda count: count >= 2,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"two heartbeats of ExternalIP {eip_id}",
            resource_kind=WaitResourceKind.EXTERNAL_IP,
            resource_id=eip_id,
        )
        events = sorted(
            metering.get_all_events(HEARTBEAT_TYPE, eip_id), key=lambda event: event["data"]["usage"]["from"]
        )
        assert_contiguous_exact_seconds(events, "ExternalIP")
    finally:
        deletion_recorded = _delete_external_ip(grpc, k8s_hub_client, external_ip_id=eip_id, cr_name=eip_cr_name)

    assert deletion_recorded is not None, (
        "the ExternalIP CR never got a deletion timestamp, so no deletion time was recorded"
    )

    # The billing end is the meter's own last interval end, anchored on the
    # recorded deletion: a round skips its tick while the fold lags the event
    # topic, so the final partial piece can land several round intervals after
    # the deletion.
    billing_end = metering.wait_for_billing_end(eip_id, deletion=deletion_recorded)

    # The whole life stays billed: the heartbeats collapse into contiguous net
    # intervals, each billing exactly one second per second, and the last one
    # reaches the recorded deletion. A life that were erased would leave
    # nothing to assert on.
    all_events = metering.get_all_events(HEARTBEAT_TYPE, eip_id)
    assert_exact_contiguous_life(
        all_events, multiplier=Decimal(1), deletion=deletion_recorded, label=f"heartbeats of ExternalIP {eip_id}"
    )

    # A negative heartbeat may only cancel billing past the deletion (the tail
    # of a round that ticked before the fold consumed the deletion). One that
    # starts before the deletion reverses billing the ExternalIP earned.
    for event in all_events:
        start, end, quantity = usage_interval(event)
        if quantity < 0:
            assert start >= deletion_recorded - CR_STAMP_TOLERANCE, (
                f"a negative heartbeat {start}..{end} ({quantity}) reverses billing from before the "
                f"deletion at {deletion_recorded}"
            )

    # Nothing bills past the end, and past the final interval nothing more
    # arrives: the meter stopped.
    metering.assert_no_events(HEARTBEAT_TYPE, eip_id, since=billing_end.isoformat(), within=120)


def external_ip_attach_detach_reattach(
    *,
    metering: MeteringCollector,
    pool_id: str,
    external_ip: tuple[str, str],
    ci1_uuid: str,
    ci2_uuid: str,
    attachments: list[str],
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    ip_project: str,
) -> None:
    """An address attached, detached and reattached keeps its address and
    attribution, and the attached and unattached meters bill disjoint spans."""
    pool_name = private_grpc.get_external_ip_pool(pool_id=pool_id)["object"]["metadata"]["name"]
    ip_id, ip_cr_name = external_ip

    assert pool_id in private_grpc.list_external_ip_pool_ids()
    assert ip_id in grpc.list_external_ip_ids()
    wait_for_external_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)
    # The IP's heartbeats carry the project it was created in, so the
    # project dimension is a real attribution check.
    metering.expect(HEARTBEAT_TYPE, resource_id=ip_id, resource_kind=WaitResourceKind.EXTERNAL_IP, timeout=180)
    metering.verify()
    allocated = metering.get_event(HEARTBEAT_TYPE, resource_id=ip_id, resource_kind=WaitResourceKind.EXTERNAL_IP)
    assert allocated["data"]["meter"] == "allocation", f"meter is {allocated['data']['meter']!r}, want 'allocation'"
    # References render by name in heartbeats: the pool dimension is the pool's
    # name, the same contract as the instance_type dimension.
    assert allocated["data"]["billing_dimensions"]["pool"] == pool_name
    assert allocated["data"]["billing_dimensions"]["ip_family"] == "ipv4"
    assert allocated["data"]["billing_dimensions"]["attached"] == "false"
    assert allocated["data"]["billing_dimensions"]["project"] == ip_project, (
        f"project dimension is {allocated['data']['billing_dimensions'].get('project')!r}, want {ip_project!r}"
    )

    # --- Attach ExternalIP to ComputeInstance 1 ---
    att_id: str = grpc.create_external_ip_attachment(
        name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci1_uuid
    )
    attachments.append(att_id)
    att_cr_name: str = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=att_id)
    wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=att_cr_name)

    private_ip_obj = poll_until(
        fn=lambda: private_grpc.get_private_external_ip(external_ip_id=ip_id)["object"],
        until=lambda item: (
            item["status"].get("attribution", {}).get("computeInstance", {}).get("id") == ci1_uuid
            and bool(item["status"].get("attachmentTransitionTime"))
        ),
        retries=30,
        delay=5,
        description="ExternalIP attribution settlement",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=ip_id,
    )
    ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
    assert ip_obj["object"]["status"].get("attached") is True
    attached_ip_address: str = ip_obj["object"]["status"]["address"]
    assert attached_ip_address, "ExternalIP should have an allocated address"
    assert private_ip_obj["status"]["attribution"]["computeInstance"]["id"] == ci1_uuid
    assert private_ip_obj["status"].get("attachmentTransitionTime")
    # The settled attachment is a dimension change, so the attached meter
    # starts billing: a heartbeat arrives with the attached dimensions.
    attached_event = metering.heartbeat_with_dimensions(
        ip_id, {"attached": "true"}, delay=5, resource_kind=WaitResourceKind.EXTERNAL_IP
    )
    assert attached_event is not None, "no heartbeat with the attached dimensions arrived"

    # --- Detach (delete attachment) ---
    grpc.delete_external_ip_attachment(attachment_id=att_id)
    wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att_cr_name)

    poll_until(
        fn=lambda: grpc.get_external_ip(external_ip_id=ip_id)["object"]["status"].get("attached"),
        until=lambda v: v is not True,
        retries=30,
        delay=5,
        description=f"ExternalIP {ip_id} detached",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=ip_id,
    )
    # Detaching is another dimension change: the unattached meter bills again. The
    # window starts at the attached heartbeat's interval end, so the check cannot be
    # satisfied by the unattached heartbeats from before the attachment.
    assert attached_event is not None
    attached_to = parse_time(str(attached_event["data"]["usage"]["to"]))
    detached_event = metering.heartbeat_with_dimensions(
        ip_id, {"attached": "false"}, after=attached_to, delay=5, resource_kind=WaitResourceKind.EXTERNAL_IP
    )
    assert detached_event is not None, "no heartbeat with the detached dimensions arrived"
    detached_to = parse_time(str(detached_event["data"]["usage"]["to"]))

    # --- Re-attach same IP to ComputeInstance 2 ---
    att2_id: str = grpc.create_external_ip_attachment(
        name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci2_uuid
    )
    attachments.append(att2_id)
    att2_cr_name: str = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=att2_id)
    wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=att2_cr_name)
    second_private_ip = poll_until(
        fn=lambda: private_grpc.get_private_external_ip(external_ip_id=ip_id)["object"],
        until=lambda item: (
            item["status"].get("attribution", {}).get("computeInstance", {}).get("id") == ci2_uuid
            and bool(item["status"].get("attachmentTransitionTime"))
        ),
        retries=30,
        delay=5,
        description="reattached ExternalIP attribution settlement",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=ip_id,
    )
    assert second_private_ip["status"]["attribution"]["computeInstance"]["id"] == ci2_uuid
    reattached_event = metering.heartbeat_with_dimensions(
        ip_id, {"attached": "true"}, after=detached_to, delay=5, resource_kind=WaitResourceKind.EXTERNAL_IP
    )
    assert reattached_event is not None, "no heartbeat with the reattached dimensions arrived"

    ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
    assert ip_obj["object"]["status"]["address"] == attached_ip_address, (
        "Re-attached ExternalIP should keep the same address"
    )

    # The attached and unattached meters are disjoint: the net intervals each
    # dimension set bills never overlap, so no second is billed on both during
    # a transition. A wrong attachment transition time bills both meters over
    # the same span, which the per-phase existence checks above cannot see.
    assert_dimension_sets_disjoint(metering.get_all_events(HEARTBEAT_TYPE, ip_id), label=f"ExternalIP {ip_id}")

    # --- Cleanup: delete the attachment and the ExternalIP ---
    grpc.delete_external_ip_attachment(attachment_id=att2_id)
    wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att2_cr_name)

    grpc.delete_external_ip(external_ip_id=ip_id)
    wait_for_external_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
    poll_until(
        fn=lambda: ip_id not in grpc.list_external_ip_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"ExternalIP {ip_id} removal from API",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=ip_id,
    )


def nat_gateway_bills_allocation(
    *,
    metering: MeteringCollector,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    virtual_network: str,
    pool: dict[str, Any],
) -> None:
    """A READY gateway bills per interval with its references as dimensions;
    deletion bills the final partial interval up to the recorded deletion, then
    ends billing."""
    tag = uuid4().hex[:8]
    eip_name = f"billing-nat-eip-{tag}"

    eip_response: dict[str, Any] = grpc.call(
        service=f"{PUBLIC_API}.ExternalIPs/Create",
        data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"id": pool["id"]}}}},
    )
    eip_id: str = eip_response["object"]["id"]
    pool.setdefault("suite_eip_ids", set()).add(eip_id)
    eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
    nat_id: str | None = None
    nat_cr_name: str | None = None
    try:
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr_name)

        nat_name = f"billing-nat-{tag}"
        nat_id = grpc.create_nat_gateway(name=nat_name, virtual_network_name=virtual_network, external_ip_name=eip_name)
        poll_until(
            fn=lambda: (
                grpc.call(service=f"{PUBLIC_API}.NATGateways/Get", data={"id": nat_id})
                .get("object", {})
                .get("status", {})
                .get("state", "")
            ),
            until=lambda state: state in ("NAT_GATEWAY_STATE_READY", "Ready"),
            retries=60,
            delay=5,
            description=f"NATGateway {nat_name} READY",
            resource_kind=WaitResourceKind.NAT_GATEWAY,
            resource_id=nat_id,
        )
        # Resolve the CR before any delete can remove it: the deletion
        # assertions below need its recorded deletion timestamp.
        nat_cr_name = poll_until(
            fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
            until=lambda name: name != "",
            retries=30,
            delay=5,
            description=f"NATGateway CR for {nat_name}",
            resource_kind=WaitResourceKind.NAT_GATEWAY,
            resource_id=nat_id,
        )

        heartbeat = metering.heartbeat_with_dimensions(
            nat_id,
            {"virtual_network": virtual_network},
            timeout=HEARTBEAT_TIMEOUT,
            resource_kind=WaitResourceKind.NAT_GATEWAY,
        )
        # The gateway was created without a project, so its heartbeats must
        # carry no project dimension: the engine emits only the facts a
        # resource carries, and one that names a project is misattributed.
        metering.expect_no_project()
        metering.verify_event(heartbeat, resource_id=nat_id)

        data = heartbeat["data"]
        assert data["meter"] == "allocation", f"meter is {data['meter']!r}, want 'allocation'"
        assert data["usage"]["unit"] == "second", f"unit is {data['usage']['unit']!r}"
        assert data["billing_dimensions"]["virtual_network"] == virtual_network
        assert data["billing_dimensions"]["external_ip"] == eip_name

        poll_until(
            fn=lambda: len(metering.get_all_events(HEARTBEAT_TYPE, nat_id)),
            until=lambda count: count >= 2,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"two heartbeats of NATGateway {nat_id}",
            resource_kind=WaitResourceKind.NAT_GATEWAY,
            resource_id=nat_id,
        )
        events = sorted(
            metering.get_all_events(HEARTBEAT_TYPE, nat_id), key=lambda event: event["data"]["usage"]["from"]
        )
        assert_contiguous_exact_seconds(events, "NATGateway")
    finally:
        deletion_recorded = (
            _delete_nat_gateway(grpc, k8s_hub_client, nat_id=nat_id, cr_name=nat_cr_name)
            if nat_id is not None
            else None
        )
        with contextlib.suppress(Exception):
            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)

    if nat_id is not None:
        assert deletion_recorded is not None, (
            "the NATGateway CR was never deleted, so no deletion timestamp was recorded"
        )

        # The billing end is the meter's own last interval end, anchored on the
        # recorded deletion: a round skips its tick while the fold lags the
        # event topic, so the final partial piece can land several round
        # intervals after the deletion, and reading the end before the piece
        # lands anchors on an intermediate round, making the piece read as
        # billing past the anchor.
        billing_end = metering.wait_for_billing_end(nat_id, deletion=deletion_recorded)

        # The billed life is exact: the heartbeats collapse into contiguous net
        # intervals, each billing exactly one second per second, and the last
        # interval reaches the recorded deletion.
        assert_exact_contiguous_life(
            metering.get_all_events(HEARTBEAT_TYPE, nat_id),
            multiplier=Decimal(1),
            deletion=deletion_recorded,
            label=f"heartbeats of NATGateway {nat_id}",
        )

        # Nothing bills past the end, and past the final interval nothing more
        # arrives: the meter stopped.
        metering.assert_no_events(HEARTBEAT_TYPE, nat_id, since=billing_end.isoformat(), within=120)


def _delete_external_ip(grpc: GRPCClient, k8s: K8sClient, *, external_ip_id: str, cr_name: str) -> datetime | None:
    """Delete the ExternalIP and return the deletion time the API server recorded."""
    deletion_recorded = delete_recording_timestamp(
        k8s,
        resource="externalip",
        cr_name=cr_name,
        delete=lambda: grpc.delete_external_ip(external_ip_id=external_ip_id),
        resource_kind=WaitResourceKind.EXTERNAL_IP,
    )
    with contextlib.suppress(Exception):
        wait_for_external_ip_deletion(k8s=k8s, name=cr_name)
    return deletion_recorded


def _delete_nat_gateway(grpc: GRPCClient, k8s: K8sClient, *, nat_id: str, cr_name: str | None) -> datetime | None:
    """Delete the gateway and return the deletion time the API server recorded.

    None when the gateway never had a CR to watch: the cleanup path after a failed setup.
    """
    deletion_recorded: datetime | None = None
    if cr_name is None:
        with contextlib.suppress(Exception):
            grpc.delete_nat_gateway(nat_gateway_id=nat_id)
    else:
        deletion_recorded = delete_recording_timestamp(
            k8s,
            resource="natgateway",
            cr_name=cr_name,
            delete=lambda: grpc.delete_nat_gateway(nat_gateway_id=nat_id),
            resource_kind=WaitResourceKind.NAT_GATEWAY,
        )
        with contextlib.suppress(Exception):
            wait_for_nat_gateway_deletion(k8s=k8s, name=cr_name)
    with contextlib.suppress(Exception):
        poll_until(
            fn=lambda: (
                nat_id
                not in [item["id"] for item in grpc.call(service=f"{PUBLIC_API}.NATGateways/List").get("items", [])]
            ),
            until=lambda gone: gone is True,
            retries=60,
            delay=5,
            description=f"NATGateway {nat_id} deletion",
            resource_kind=WaitResourceKind.NAT_GATEWAY,
            resource_id=nat_id,
        )
    return deletion_recorded
