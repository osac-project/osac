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
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.metering

HEARTBEAT_TIMEOUT: int = 300


def _parse_time(value: str) -> datetime:
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    return datetime.fromisoformat(value)


def _usage_interval(event: dict[str, Any]) -> tuple[datetime, datetime, Decimal]:
    usage = event["data"]["usage"]
    return _parse_time(usage["from"]), _parse_time(usage["to"]), Decimal(usage["quantity"])


def _interval_seconds(start: datetime, end: datetime) -> Decimal:
    delta = end - start
    micros = (delta.days * 86400 + delta.seconds) * 1_000_000 + delta.microseconds
    return Decimal(micros) / Decimal(1_000_000)


@pytest.mark.requires_bmaas
def test_nat_gateway_bills_allocation(
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    ref_virtual_network: dict[str, str],
    ref_eip_pool: dict[str, str],
    metering: MeteringCollector,
) -> None:
    """A READY gateway bills per interval with its references as dimensions."""
    tag = uuid4().hex[:8]
    eip_name = f"ref-nat-metering-eip-{tag}"

    eip_response: dict[str, Any] = grpc.call(
        service=f"{PUBLIC_API}.ExternalIPs/Create",
        data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
    )
    eip_id: str = eip_response["object"]["id"]
    eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
    nat_id: str | None = None
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

        heartbeat = metering.heartbeat_with_dimensions(
            nat_id, {"virtual_network": ref_virtual_network["name"]}, timeout=HEARTBEAT_TIMEOUT
        )
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
            start, end, quantity = _usage_interval(event)
            assert quantity == _interval_seconds(start, end), (
                f"quantity {quantity} is not the exact length of its interval ({start}..{end})"
            )
        for previous, following in itertools.pairwise(events):
            previous_end = _parse_time(previous["data"]["usage"]["to"])
            following_start = _parse_time(following["data"]["usage"]["from"])
            assert following_start == previous_end, (
                f"billed intervals are not contiguous: {previous_end} then {following_start}"
            )
    finally:
        if nat_id is not None:
            with contextlib.suppress(Exception):
                grpc.delete_nat_gateway(nat_gateway_id=nat_id)
                poll_until(
                    fn=lambda: (
                        nat_id
                        not in [
                            item["id"] for item in grpc.call(service=f"{PUBLIC_API}.NATGateways/List").get("items", [])
                        ]
                    ),
                    until=lambda gone: gone is True,
                    retries=60,
                    delay=5,
                    description=f"NATGateway {nat_id} deletion",
                )
        with contextlib.suppress(Exception):
            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)

    deleted_after = datetime.now(datetime.UTC).isoformat()
    if nat_id is not None:
        metering.assert_no_events(HEARTBEAT_TYPE, nat_id, since=deleted_after, within=120)
