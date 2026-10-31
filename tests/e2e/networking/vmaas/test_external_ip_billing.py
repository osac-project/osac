"""ExternalIP billing: an allocated address bills allocation seconds, and
deleting it keeps its whole billed life.

The test sends no traffic, so it needs provider networking and the metering
adapter but not the external return path: it lives outside the external_ip and
references directories and is not gated by OSAC_NETRIS_VM_EXTERNAL_PATH_ENABLED.
It uses an ExternalIPPool from OSAC_EXTERNAL_IP_POOL_CIDR, as the main suite
does, and runs after test_vmaas_networking.py (see conftest.py).

The allocation meter bills an ExternalIP while it is ALLOCATED and through
DELETING until it is physically deleted (osac-metering ``0001-meters.yaml``),
with the dimensions pool, ip_family and attached. The state time of DELETING is
the time the delete was accepted, so a delete must neither erase the life
billed before it nor reverse it with a negative heartbeat.
"""

from __future__ import annotations

import contextlib
import itertools
from collections.abc import Iterator
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
from tests.e2e.core.metering import (
    CR_STAMP_TOLERANCE,
    HEARTBEAT_TYPE,
    MeteringCollector,
    assert_exact_contiguous_life,
    interval_seconds,
    parse_time,
    usage_interval,
)
from tests.e2e.core.runner import poll_until
from tests.e2e.networking.teardown import wait_owned_gone
from tests.e2e.networking.vmaas import stack

# ExternalIPs are offered only with provider networking enabled
# (global.networking.provisioningEnabled: true); otherwise the Fulfillment
# Service rejects the ExternalIP create with FailedPrecondition.

HEARTBEAT_TIMEOUT: int = 300


@pytest.fixture
def billing_metering(request: pytest.FixtureRequest) -> MeteringCollector:
    """The metering collector, or a skip when this profile has no echo adapter.

    The test carries no ``metering`` marker on purpose: that marker makes the whole session fail at
    collection when no adapter is reachable (tests/e2e/conftest.py), which would take every other test of
    the run down for a profile that only lacks this one dependency.
    """
    try:
        request.getfixturevalue("metering_adapter_url")
    except (RuntimeError, OSError) as exc:
        pytest.skip(f"no metering echo adapter: set METERING_ADAPTER_URL or enable metering.echoAdapter ({exc})")
    return request.getfixturevalue("metering")


@pytest.fixture(scope="module")
def billing_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient, external_ip_pool_cidr: str, net_test_run_id: str
) -> Iterator[dict[str, Any]]:
    """The routed ExternalIPPool of the main suite: adopted when it exists, otherwise created and removed here."""
    pool: dict[str, Any] = {}
    try:
        stack.resolve_external_ip_pool(
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            name=f"netris-billing-pool-{net_test_run_id}",
            cidr=external_ip_pool_cidr,
            record=pool,
        )
        yield pool
    finally:
        if pool.get("owned"):
            pool_id = pool["id"]
            wait_owned_gone(
                description=f"ExternalIPPool {pool_id} gone",
                api_gone=lambda: pool_id not in private_grpc.list_external_ip_pool_ids(),
                cr_gone=lambda: k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False) == "",
                delete=lambda: private_grpc.delete_external_ip_pool(pool_id=pool_id),
            )


def test_external_ip_bills_allocation_through_deletion(
    billing_metering: MeteringCollector,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    billing_pool: dict[str, Any],
) -> None:
    """An ALLOCATED ExternalIP bills per interval with the allocation meter's
    dimensions; deletion bills the final partial interval up to the recorded
    deletion, then ends billing, and no negative heartbeat reverses billing
    from before the deletion."""
    metering = billing_metering
    pool_name: str = private_grpc.get_external_ip_pool(pool_id=billing_pool["id"])["object"]["metadata"]["name"]
    eip_name = f"billing-eip-{uuid4().hex[:8]}"

    eip_response: dict[str, Any] = grpc.call(
        service=f"{PUBLIC_API}.ExternalIPs/Create",
        data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"id": billing_pool["id"]}}}},
    )
    eip_id: str = eip_response["object"]["id"]
    # Resolve the CR before any delete can remove it: the deletion assertions
    # below need its recorded deletion timestamp.
    eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
    try:
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr_name)

        heartbeat = metering.heartbeat_with_dimensions(
            eip_id, {"pool": pool_name, "attached": "false"}, timeout=HEARTBEAT_TIMEOUT
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
        )
        events = sorted(
            metering.get_all_events(HEARTBEAT_TYPE, eip_id), key=lambda event: event["data"]["usage"]["from"]
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


def _delete_external_ip(grpc: GRPCClient, k8s: K8sClient, *, external_ip_id: str, cr_name: str) -> datetime | None:
    """Delete the ExternalIP and return the deletion time the API server recorded.

    The teardown can finish in under a second, so the deletion timestamp is
    read through a watch established before the delete request: the watch
    always delivers the modification that stamps it, where a plain get can
    arrive after the object is already gone. None when the CR never got a
    timestamp.
    """
    deletion_recorded: datetime | None = None
    try:
        with k8s.watch_deletion_timestamp(resource="externalip", name=cr_name) as deletion_stamp:
            with contextlib.suppress(Exception):
                grpc.delete_external_ip(external_ip_id=external_ip_id)
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
        # A watch that never started must not leak the ExternalIP: the delete
        # above never ran, so issue it now, then fail loudly.
        with contextlib.suppress(Exception):
            grpc.delete_external_ip(external_ip_id=external_ip_id)
        raise
    with contextlib.suppress(Exception):
        wait_for_external_ip_deletion(k8s=k8s, name=cr_name)
    return deletion_recorded
