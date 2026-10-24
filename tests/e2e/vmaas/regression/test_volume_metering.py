"""Volume capacity metering: a volume bills gibibyte-seconds while it exists.

The volume is the only meter whose quantity multiplies a size (the provisioned
GiB) by time, so this journey pins the multiplier: each heartbeat's quantity
must equal GiB x interval seconds exactly, not approximately.

The volume is created through the private API the way the OSAC CSI driver
creates one, because a node-local (LVMS) volume needs the node topology
segment and the public VolumeSpec carries no topology field: node-local
volumes are CSI-only by design.
"""

from __future__ import annotations

import contextlib
import itertools
import subprocess
from datetime import UTC, datetime
from decimal import Decimal
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.runner import poll_until, run

pytestmark = [pytest.mark.regression, pytest.mark.metering]

VOLUME_SIZE_GIB = 5
HEARTBEAT_TIMEOUT: int = 300

# The tenant the JWT fixtures authenticate as (conftest maps tenant1_admin to
# tenant1); the private Create carries it explicitly, like the CSI driver does.
METERING_TENANT = "tenant1"

# The topology segment the LVMS provisioner requires: a node-local volume pins
# its node, and the cluster under test is single-node.
NODE_TOPOLOGY_SEGMENT = "osac.io/node"


@pytest.fixture
def metering_project(grpc: GRPCClient):
    """A project the volume's billed dimensions are attributed to."""
    name = unique_name("e2e-volume-metering-project")
    project_id: str = grpc.create_project(name=name)
    yield name
    with contextlib.suppress(Exception):
        grpc.delete_project(project_id=project_id)


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


def test_volume_bills_gibibyte_seconds(
    grpc: GRPCClient, private_grpc: GRPCClient, metering: MeteringCollector, metering_project: str
) -> None:
    """An AVAILABLE volume bills capacity x size per interval; deletion ends billing."""
    node = run("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].metadata.name}")
    name = unique_name("e2e-volume-metering")
    response: dict[str, Any] = private_grpc.call(
        service=f"{PRIVATE_API}.Volumes/Create",
        data={
            "object": {
                "metadata": {"name": name, "tenant": METERING_TENANT, "project": metering_project},
                "spec": {
                    "storageTier": "local",
                    "sizeGib": VOLUME_SIZE_GIB,
                    "accessMode": "VOLUME_ACCESS_MODE_READ_WRITE_ONCE",
                    "topology": {"segments": {NODE_TOPOLOGY_SEGMENT: node}},
                },
            }
        },
    )
    volume_id: str = response["object"]["id"]

    try:
        volume = poll_until(
            fn=lambda: private_grpc.call(service=f"{PRIVATE_API}.Volumes/Get", data={"id": volume_id}),
            until=lambda v: v.get("object", {}).get("status", {}).get("state") == "VOLUME_STATE_AVAILABLE",
            retries=60,
            delay=5,
            description=f"{name} volume AVAILABLE",
        )
        # grpcurl renders proto int64 fields as strings.
        assert int(volume["object"]["spec"]["sizeGib"]) == VOLUME_SIZE_GIB

        # The first heartbeat that carries the storage_tier dimension is the first
        # one billed under the capacity meter.
        heartbeat = metering.heartbeat_with_dimensions(volume_id, {"storage_tier": "local"}, timeout=HEARTBEAT_TIMEOUT)
        metering.verify_event(heartbeat, resource_id=volume_id)

        data = heartbeat["data"]
        assert data["meter"] == "capacity", f"meter is {data['meter']!r}, want 'capacity'"
        assert data["usage"]["unit"] == "gibibyte_second", f"unit is {data['usage']['unit']!r}"
        assert data["billing_dimensions"]["storage_tier"] == "local"
        assert data["billing_dimensions"]["project"] == metering_project

        # The multiplier: wait for a second heartbeat, then every typed heartbeat's
        # quantity must equal size x the exact microsecond length of its interval,
        # and consecutive intervals must be contiguous with no billed gap.
        poll_until(
            fn=lambda: len(
                [
                    event
                    for event in metering.get_all_events(HEARTBEAT_TYPE, volume_id)
                    if event.get("data", {}).get("billing_dimensions", {}).get("storage_tier")
                ]
            ),
            until=lambda count: count >= 2,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"two typed heartbeats of volume {volume_id}",
        )
        typed = [
            event
            for event in metering.get_all_events(HEARTBEAT_TYPE, volume_id)
            if event.get("data", {}).get("billing_dimensions", {}).get("storage_tier")
        ]
        typed.sort(key=lambda event: event["data"]["usage"]["from"])
        for event in typed:
            start, end, quantity = _usage_interval(event)
            expected = Decimal(VOLUME_SIZE_GIB) * _interval_seconds(start, end)
            assert quantity == expected, (
                f"quantity {quantity} is not {VOLUME_SIZE_GIB} GiB x "
                f"{_interval_seconds(start, end)}s: the size multiplier is wrong"
            )
        for previous, following in itertools.pairwise(typed):
            previous_end = _parse_time(previous["data"]["usage"]["to"])
            following_start = _parse_time(following["data"]["usage"]["from"])
            assert following_start == previous_end, (
                f"billed intervals are not contiguous: {previous_end} then {following_start}"
            )
    finally:
        with contextlib.suppress(Exception):
            private_grpc.call(service=f"{PRIVATE_API}.Volumes/Delete", data={"id": volume_id})
        poll_until(
            fn=lambda: _volume_gone(private_grpc, volume_id),
            until=lambda gone: gone is True,
            retries=60,
            delay=5,
            description=f"{volume_id} volume removed",
        )

    deleted_after = datetime.now(UTC).isoformat()
    metering.assert_no_events(HEARTBEAT_TYPE, volume_id, since=deleted_after, within=120)


def _volume_gone(grpc: GRPCClient, volume_id: str) -> bool:
    try:
        grpc.call(service=f"{PUBLIC_API}.Volumes/Get", data={"id": volume_id})
    except subprocess.CalledProcessError:
        return True
    return False
