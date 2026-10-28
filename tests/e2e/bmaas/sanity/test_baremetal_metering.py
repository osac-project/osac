"""Bare-metal instance metering: allocation and consumption bill side by side.

The bare-metal instance is the only resource with two meters on one resource:
``allocation`` bills the host from the moment provisioning confirms it (it
keeps billing through STOPPED, because the host stays reserved), while
``consumption`` bills only the spans the instance actually ran. Both carry the
instance_type dimension, so attribution holds on each meter separately.
"""

from __future__ import annotations

import contextlib
import itertools
from datetime import datetime
from decimal import Decimal
from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
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
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = [pytest.mark.sanity, pytest.mark.metering]

HEARTBEAT_TIMEOUT: int = 600


def _typed_heartbeats(metering: MeteringCollector, bmi_id: str, meter: str) -> list[dict[str, Any]]:
    """The heartbeats of one meter that carry the instance_type dimension."""
    typed = [
        event
        for event in metering.get_all_events(HEARTBEAT_TYPE, bmi_id)
        if event.get("data", {}).get("meter") == meter
        and event.get("data", {}).get("billing_dimensions", {}).get("instance_type")
    ]
    typed.sort(key=lambda event: event["data"]["usage"]["from"])
    return typed


def _wait_for_meter(metering: MeteringCollector, bmi_id: str, meter: str, instance_type: str) -> dict[str, Any]:
    """Return the first heartbeat of the meter carrying the instance type."""
    return poll_until(
        fn=lambda: _typed_heartbeats(metering, bmi_id, meter),
        until=lambda events: len(events) > 0,
        retries=HEARTBEAT_TIMEOUT // 10,
        delay=10,
        description=f"first {meter} heartbeat of {bmi_id} with instance_type",
    )[0]


def _assert_contiguous_exact_seconds(events: list[dict[str, Any]], meter: str) -> None:
    """Every heartbeat bills exactly its interval; consecutive intervals touch."""
    assert events, f"no typed {meter} heartbeats to assert arithmetic on"
    for event in events:
        start, end, quantity = usage_interval(event)
        assert quantity == interval_seconds(start, end), (
            f"{meter} quantity {quantity} is not the exact length of its interval ({start}..{end})"
        )
    for previous, following in itertools.pairwise(events):
        previous_end = parse_time(previous["data"]["usage"]["to"])
        following_start = parse_time(following["data"]["usage"]["from"])
        assert following_start == previous_end, (
            f"{meter} billed intervals are not contiguous: {previous_end} then {following_start}"
        )


def test_baremetal_instance_bills_allocation_and_consumption(
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    metering: MeteringCollector,
    catalog_item: str,
    bmi_instance_type: str,
    bmi_disk_image: str,
    ssh_public_key: str,
    test_run_id: str,
) -> None:
    """Both meters bill a running instance and stop after its deletion, each
    ending at the recorded deletion with its final partial interval billed."""
    name = f"e2e-bmi-metering-{test_run_id}"
    bmi_id, _ = jwt_cli_user.create_baremetal_instance(
        name=name, catalog_item=catalog_item, ssh_key=ssh_public_key, disk_image=bmi_disk_image
    )
    bmi_cr_name: str | None = None

    try:
        bmi_cr_name = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=jwt_grpc_tenant1, bmi_id=bmi_id)

        allocation = _wait_for_meter(metering, bmi_id, "allocation", bmi_instance_type)
        # The instance is created without a project, so its heartbeats must
        # carry no project dimension: the engine emits only the facts a
        # resource carries, and one that names a project is misattributed.
        metering.expect_no_project()
        metering.verify_event(allocation, resource_id=bmi_id)
        consumption = _wait_for_meter(metering, bmi_id, "consumption", bmi_instance_type)
        metering.verify_event(consumption, resource_id=bmi_id)

        for heartbeat, meter in ((allocation, "allocation"), (consumption, "consumption")):
            data = heartbeat["data"]
            assert data["meter"] == meter
            assert data["usage"]["unit"] == "second", f"{meter} unit is {data['usage']['unit']!r}"
            assert data["billing_dimensions"]["instance_type"] == bmi_instance_type, (
                f"{meter} instance_type is {data['billing_dimensions'].get('instance_type')!r}"
            )

        # Wait for a second heartbeat of each meter, then pin the arithmetic.
        for meter in ("allocation", "consumption"):
            poll_until(
                fn=lambda meter=meter: len(_typed_heartbeats(metering, bmi_id, meter)),
                until=lambda count: count >= 2,
                retries=HEARTBEAT_TIMEOUT // 10,
                delay=10,
                description=f"two typed {meter} heartbeats of {bmi_id}",
            )
            _assert_contiguous_exact_seconds(_typed_heartbeats(metering, bmi_id, meter), meter)

        # Allocation bills from the moment the host was confirmed, which is at or
        # before the moment the instance started running: the allocation meter's
        # first interval cannot start after the consumption meter's.
        allocation_start = parse_time(allocation["data"]["usage"]["from"])
        consumption_start = parse_time(consumption["data"]["usage"]["from"])
        assert allocation_start <= consumption_start, (
            f"allocation starts billing at {allocation_start}, after consumption "
            f"starts at {consumption_start}: the host reservation must bill first"
        )
    finally:
        deletion_recorded = _delete_bmi(
            jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, uuid=bmi_id, cr_name=bmi_cr_name
        )

    assert deletion_recorded is not None, "the instance's CR was never deleted, so no deletion timestamp was recorded"

    # The billing end is the meter's own last interval end across both meters,
    # read once the heartbeat stream has gone quiet for a full round: the final
    # partial pieces land in the first round after the fold consumes the
    # deletion, and reading the end earlier anchors on an intermediate round,
    # making the final piece read as billing past the anchor.
    billing_end = metering.wait_for_billing_end(bmi_id)

    # Each meter ends at or after the recorded deletion: its final partial
    # interval is billed, not dropped, and its typed heartbeats collapse into
    # contiguous net intervals billing exactly one second per second.
    for meter in ("allocation", "consumption"):
        assert_exact_contiguous_life(
            _typed_heartbeats(metering, bmi_id, meter),
            multiplier=Decimal(1),
            deletion=deletion_recorded,
            label=f"typed {meter} heartbeats of {bmi_id}",
        )

    # Nothing bills past the end, and past the final interval nothing more
    # arrives: both meters stopped.
    metering.assert_no_events(HEARTBEAT_TYPE, bmi_id, since=billing_end.isoformat(), within=180)


def _delete_bmi(cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, *, uuid: str, cr_name: str | None) -> datetime | None:
    """Delete the instance and return the deletion time the API server recorded.

    The deletion timestamp is read through a watch established before the
    delete request: the watch always delivers the modification that stamps
    it, where a plain get can arrive after the object is already gone. None
    when the instance never had a CR to watch: the cleanup path after a
    failed setup.
    """
    deletion_recorded: datetime | None = None
    if cr_name is None:
        with contextlib.suppress(Exception):
            cli.delete_baremetal_instance(uuid=uuid)
    else:
        try:
            with k8s.watch_deletion_timestamp(resource="baremetalinstance", name=cr_name) as deletion_stamp:
                with contextlib.suppress(Exception):
                    cli.delete_baremetal_instance(uuid=uuid)
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
            # A watch that never started must not leak the instance: the
            # delete above never ran, so issue it now, then fail loudly.
            with contextlib.suppress(Exception):
                cli.delete_baremetal_instance(uuid=uuid)
            raise
        with contextlib.suppress(Exception):
            wait_for_bmi_deletion(k8s=k8s, name=cr_name)
    with contextlib.suppress(Exception):
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=uuid)
    return deletion_recorded
