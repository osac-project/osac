"""NAT gateway metering: a ready gateway bills allocation seconds.

The NAT gateway is the only billed resource whose dimensions are two
references (its virtual network and its external IP), and references render
by name in heartbeats, so this journey pins reference-based attribution.
"""

from __future__ import annotations

import contextlib
import itertools
from datetime import datetime
from decimal import Decimal
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_nat_gateway_deletion,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import (
    HEARTBEAT_TYPE,
    MeteringCollector,
    assert_exact_contiguous_life,
    interval_seconds,
    parse_time,
    usage_interval,
)
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.metering

HEARTBEAT_TIMEOUT: int = 300


def test_nat_gateway_bills_allocation(
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    ref_virtual_network: dict[str, str],
    ref_eip_pool: dict[str, str],
    metering: MeteringCollector,
) -> None:
    """A READY gateway bills per interval with its references as dimensions;
    deletion bills the final partial interval up to the recorded deletion, then
    ends billing."""
    tag = uuid4().hex[:8]
    eip_name = f"ref-nat-metering-eip-{tag}"

    eip_response: dict[str, Any] = grpc.call(
        service=f"{PUBLIC_API}.ExternalIPs/Create",
        data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
    )
    eip_id: str = eip_response["object"]["id"]
    eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
    nat_id: str | None = None
    nat_cr_name: str | None = None
    try:
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr_name)

        nat_name = f"ref-nat-metering-{tag}"
        nat_id = grpc.create_nat_gateway(
            name=nat_name, virtual_network_name=ref_virtual_network["name"], external_ip_name=eip_name
        )
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
        )
        # Resolve the CR before any delete can remove it: the deletion
        # assertions below need its recorded deletion timestamp.
        nat_cr_name = poll_until(
            fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
            until=lambda name: name != "",
            retries=30,
            delay=5,
            description=f"NATGateway CR for {nat_name}",
        )

        heartbeat = metering.heartbeat_with_dimensions(
            nat_id, {"virtual_network": ref_virtual_network["name"]}, timeout=HEARTBEAT_TIMEOUT
        )
        # The gateway was created without a project, so its heartbeats must
        # carry no project dimension: the engine emits only the facts a
        # resource carries, and one that names a project is misattributed.
        metering.expect_no_project()
        metering.verify_event(heartbeat, resource_id=nat_id)

        data = heartbeat["data"]
        assert data["meter"] == "allocation", f"meter is {data['meter']!r}, want 'allocation'"
        assert data["usage"]["unit"] == "second", f"unit is {data['usage']['unit']!r}"
        assert data["billing_dimensions"]["virtual_network"] == ref_virtual_network["name"]
        assert data["billing_dimensions"]["external_ip"] == eip_name

        poll_until(
            fn=lambda: len(metering.get_all_events(HEARTBEAT_TYPE, nat_id)),
            until=lambda count: count >= 2,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"two heartbeats of NATGateway {nat_id}",
        )
        events = sorted(
            metering.get_all_events(HEARTBEAT_TYPE, nat_id), key=lambda event: event["data"]["usage"]["from"]
        )
        for event in events:
            start, end, quantity = usage_interval(event)
            assert quantity == interval_seconds(start, end), (
                f"quantity {quantity} is not the exact length of its interval ({start}..{end})"
            )
        for previous, following in itertools.pairwise(events):
            previous_end = parse_time(previous["data"]["usage"]["to"])
            following_start = parse_time(following["data"]["usage"]["from"])
            assert following_start == previous_end, (
                f"billed intervals are not contiguous: {previous_end} then {following_start}"
            )
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

        # The billing end is the meter's own last interval end, read once the
        # heartbeat stream has gone quiet for a full round: the final partial
        # piece lands in the first round after the fold consumes the deletion,
        # and reading the end earlier anchors on an intermediate round, making
        # the final piece read as billing past the anchor.
        billing_end = metering.wait_for_billing_end(nat_id)

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


def _delete_nat_gateway(grpc: GRPCClient, k8s: K8sClient, *, nat_id: str, cr_name: str | None) -> datetime | None:
    """Delete the gateway and return the deletion time the API server recorded.

    The gateway's whole teardown can finish in under a second, so the deletion
    timestamp is read through a watch established before the delete request:
    the watch always delivers the modification that stamps it, where a plain
    get can arrive after the object is already gone. None when the gateway
    never had a CR to watch: the cleanup path after a failed setup.
    """
    deletion_recorded: datetime | None = None
    if cr_name is None:
        with contextlib.suppress(Exception):
            grpc.delete_nat_gateway(nat_gateway_id=nat_id)
    else:
        try:
            with k8s.watch_deletion_timestamp(resource="natgateway", name=cr_name) as deletion_stamp:
                with contextlib.suppress(Exception):
                    grpc.delete_nat_gateway(nat_gateway_id=nat_id)
                with contextlib.suppress(Exception):
                    deletion_recorded = parse_time(
                        poll_until(
                            fn=deletion_stamp,
                            until=lambda stamp: bool(stamp),
                            retries=30,
                            delay=2,
                            description=f"deletion timestamp of {cr_name}",
                        )
                    )
        except Exception:
            # A watch that never started must not leak the gateway: the
            # delete above never ran, so issue it now, then fail loudly.
            with contextlib.suppress(Exception):
                grpc.delete_nat_gateway(nat_gateway_id=nat_id)
            raise
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
        )
    return deletion_recorded
