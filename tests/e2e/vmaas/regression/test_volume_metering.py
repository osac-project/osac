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
from datetime import datetime
from decimal import Decimal
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import (
    HEARTBEAT_TYPE,
    MeteringCollector,
    assert_exact_contiguous_life,
    interval_seconds,
    parse_time,
    usage_interval,
)
from tests.e2e.core.runner import poll_until, run

pytestmark = [pytest.mark.regression, pytest.mark.metering]

VOLUME_SIZE_GIB = 5
HEARTBEAT_TIMEOUT: int = 300

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


def test_volume_bills_gibibyte_seconds(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    metering: MeteringCollector,
    metering_project: str,
    jwt_tenant: str,
) -> None:
    """An AVAILABLE volume bills capacity x size per interval; deletion bills the
    final partial interval up to the recorded deletion, then ends billing."""
    node = run("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].metadata.name}")
    name = unique_name("e2e-volume-metering")
    response: dict[str, Any] = private_grpc.call(
        service=f"{PRIVATE_API}.Volumes/Create",
        data={
            "object": {
                # The tenant the JWT authenticates as; the private Create carries
                # it explicitly, like the CSI driver does.
                "metadata": {"name": name, "tenant": jwt_tenant, "project": metering_project},
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
    volume_cr_name: str | None = None

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

        # Resolve the Volume CR before any delete can remove it: the feedback
        # controller finds it by the volume-uuid label, and the deletion
        # assertions below need its recorded deletion timestamp.
        volume_cr_name = poll_until(
            fn=lambda: k8s_hub_client.get_by_label(
                resource="volume",
                label=f"osac.openshift.io/volume-uuid={volume_id}",
                jsonpath="{.items[0].metadata.name}",
            ),
            until=lambda name: name != "",
            retries=30,
            delay=2,
            description=f"Volume CR for {volume_id}",
        )

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
        # quantity must equal size x the exact length of its interval in seconds,
        # and consecutive intervals must be contiguous with no billed gap.
        poll_until(
            fn=lambda: len(metering.typed_heartbeats(volume_id, dimension="storage_tier")),
            until=lambda count: count >= 2,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"two typed heartbeats of volume {volume_id}",
        )
        typed = metering.typed_heartbeats(volume_id, dimension="storage_tier")
        for event in typed:
            start, end, quantity = usage_interval(event)
            expected = Decimal(VOLUME_SIZE_GIB) * interval_seconds(start, end)
            assert quantity == expected, (
                f"quantity {quantity} is not {VOLUME_SIZE_GIB} GiB x "
                f"{interval_seconds(start, end)}s: the size multiplier is wrong"
            )
        for previous, following in itertools.pairwise(typed):
            previous_end = parse_time(previous["data"]["usage"]["to"])
            following_start = parse_time(following["data"]["usage"]["from"])
            assert following_start == previous_end, (
                f"billed intervals are not contiguous: {previous_end} then {following_start}"
            )
    finally:
        deletion_recorded = _delete_volume(private_grpc, k8s_hub_client, volume_id=volume_id, cr_name=volume_cr_name)

    assert deletion_recorded is not None, "the Volume CR was never deleted, so no deletion timestamp was recorded"

    # The billing end is the meter's own last interval end, anchored on the
    # recorded deletion: a round skips its tick while the fold lags the event
    # topic, so the final partial piece can land several round intervals after
    # the deletion, and reading the end before the piece lands anchors on an
    # intermediate round, making the piece read as billing past the anchor.
    billing_end = metering.wait_for_billing_end(volume_id, deletion=deletion_recorded)

    # The billed life is exact: the typed heartbeats collapse into contiguous
    # net intervals, each billing exactly its size in GiB per second, and the
    # last interval reaches the recorded deletion.
    assert_exact_contiguous_life(
        metering.typed_heartbeats(volume_id, dimension="storage_tier"),
        multiplier=Decimal(VOLUME_SIZE_GIB),
        deletion=deletion_recorded,
        label=f"typed heartbeats of volume {volume_id}",
    )

    # Nothing bills past the end, and past the final interval nothing more
    # arrives: the meter stopped.
    metering.assert_no_events(HEARTBEAT_TYPE, volume_id, since=billing_end.isoformat(), within=120)


def _delete_volume(grpc: GRPCClient, k8s: K8sClient, *, volume_id: str, cr_name: str | None) -> datetime | None:
    """Delete the volume and return the deletion time the API server recorded.

    The Volume CR's whole teardown can finish in under a second (the vendor
    deprovisioning of a node-local thin volume is fast), so the deletion
    timestamp is read through a watch established before the delete request:
    the watch always delivers the modification that stamps it, where a plain
    get can arrive after the object is already gone. None when the volume
    never had a CR to watch: the cleanup path after a failed setup.
    """
    deletion_recorded: datetime | None = None
    if cr_name is None:
        with contextlib.suppress(Exception):
            grpc.call(service=f"{PRIVATE_API}.Volumes/Delete", data={"id": volume_id})
    else:
        try:
            with k8s.watch_deletion_timestamp(resource="volume", name=cr_name) as deletion_stamp:
                with contextlib.suppress(Exception):
                    grpc.call(service=f"{PRIVATE_API}.Volumes/Delete", data={"id": volume_id})
                with contextlib.suppress(Exception):
                    deletion_recorded = parse_time(
                        poll_until(
                            fn=deletion_stamp,
                            until=lambda stamp: bool(stamp),
                            retries=30,
                            delay=2,
                            description=f"deletion timestamp of Volume CR {cr_name}",
                        )
                    )
        except Exception:
            # A watch that never started must not leak the volume: the
            # delete above never ran, so issue it now, then fail loudly.
            with contextlib.suppress(Exception):
                grpc.call(service=f"{PRIVATE_API}.Volumes/Delete", data={"id": volume_id})
            raise
        with contextlib.suppress(Exception):
            poll_until(
                fn=lambda: not k8s.is_present(resource="volume", name=cr_name),
                until=lambda gone: gone is True,
                retries=60,
                delay=5,
                description=f"{cr_name} Volume CR deletion",
            )
    poll_until(
        fn=lambda: _volume_gone(grpc, volume_id),
        until=lambda gone: gone is True,
        retries=60,
        delay=5,
        description=f"{volume_id} volume removed",
    )
    return deletion_recorded


def _volume_gone(grpc: GRPCClient, volume_id: str) -> bool:
    try:
        grpc.call(service=f"{PUBLIC_API}.Volumes/Get", data={"id": volume_id})
    except subprocess.CalledProcessError:
        return True
    return False
