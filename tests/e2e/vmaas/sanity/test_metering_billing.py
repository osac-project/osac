from __future__ import annotations

import contextlib
import itertools
import time
from collections.abc import Iterator
from datetime import UTC, datetime, timedelta
from decimal import Decimal
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    delete_instance_type_if_present,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_grpc_security_group_ready,
    wait_for_grpc_subnet_ready,
    wait_for_provision,
    wait_for_restart,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import (
    HEARTBEAT_TYPE,
    MeteringCollector,
    interval_seconds,
    net_intervals,
    net_usage_past_by_dimension,
    parse_time,
    usage_interval,
)
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.sanity

# A round ticks every 60s; heartbeats may need a few rounds to surface.
HEARTBEAT_TIMEOUT: int = 180

# The recorded stamp and the heartbeat interval end come from different
# pods, each truncating to whole seconds: comparisons between the two allow
# one second, like the stop test's net-usage tolerance.
CR_STAMP_TOLERANCE = timedelta(seconds=1)

RESIZE_IT_VCPUS: int = 4
RESIZE_IT_MEMORY_GIB: int = 8


@pytest.fixture
def metering_project(grpc: GRPCClient) -> Iterator[str]:
    """A project the tier's billed resources are created in.

    Every resource carries its project as a heartbeat dimension, so creating
    the resources inside an explicit project makes the dimension present and
    its assertion a real attribution check.
    """
    name = unique_name("e2e-metering-project")
    project_id: str = grpc.create_project(name=name)
    yield name
    with contextlib.suppress(Exception):
        grpc.delete_project(project_id=project_id)


@pytest.fixture
def metering_network_attachment(
    grpc: GRPCClient, k8s_hub_client: K8sClient, metering_project: str
) -> Iterator[dict[str, object]]:
    """A network attachment inside the metering project.

    A compute instance's subnet reference resolves within the instance's
    project, so a VM created in the metering project needs networking from
    that project: the shared default networking lives outside it.
    """
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    sg_id: str | None = None
    sg_cr_name: str | None = None
    try:
        vn_id = grpc.create_virtual_network(
            name=unique_name("e2e-metering-vn"), ipv4_cidr="10.215.0.0/16", project=metering_project
        )
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)

        subnet_id = grpc.create_subnet(
            name=unique_name("e2e-metering-subnet"),
            virtual_network=vn_id,
            ipv4_cidr="10.215.100.0/24",
            project=metering_project,
        )
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)
        # The CR can report Ready while the fulfillment record is still pending;
        # the first VM creation fails with FailedPrecondition in that window.
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)

        sg_id = grpc.create_security_group_with_rules(
            name=unique_name("e2e-metering-sg"),
            virtual_network=vn_id,
            ingress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            project=metering_project,
        )
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        # The CR existing is not enough: the CR can be Ready and the fulfillment
        # record still PENDING until the controller feedback lands, and a VM
        # created against a PENDING security group is rejected.
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)
        wait_for_grpc_security_group_ready(grpc=grpc, sg_id=sg_id)
        yield {"subnet": subnet_id, "security_groups": [sg_id]}
    finally:
        with contextlib.suppress(Exception):
            if sg_id is not None:
                grpc.delete_security_group(sg_id=sg_id)
                wait_for_security_group_deletion(k8s=k8s_hub_client, name=sg_cr_name)
        with contextlib.suppress(Exception):
            if subnet_id is not None:
                grpc.delete_subnet(subnet_id=subnet_id)
                if subnet_cr_name is not None:
                    wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr_name)
        with contextlib.suppress(Exception):
            if vn_id is not None:
                grpc.delete_virtual_network(vn_id=vn_id)
                if vn_cr_name is not None:
                    wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr_name)


def _create_vm(
    cli: OsacCLI,
    *,
    vm_template: str,
    network_attachment: dict[str, Any],
    instance_type: str | None = None,
    project: str | None = None,
) -> str:
    """Create a ComputeInstance and return its UUID; cleanup is the caller's
    from the moment the resource exists, before any wait can time out."""
    name = unique_name("e2e-metering")
    return cli.create_compute_instance(
        name=name,
        template=vm_template,
        network_attachments=[network_attachment],
        instance_type=instance_type,
        project=project,
    )


def _wait_for_running_vm(k8s: K8sClient, *, uuid: str) -> str:
    """Wait for the CR, provisioning and RUNNING; return the CR name."""
    ci_name: str = wait_for_cr(k8s=k8s, uuid=uuid)
    wait_for_provision(k8s=k8s, name=ci_name)
    wait_for_running(k8s=k8s, name=ci_name)
    return ci_name


def _delete_vm(cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, *, uuid: str, ci_name: str | None) -> datetime | None:
    """Delete the VM and return the deletion time the API server recorded.

    The operator's delete feedback dates the billing stop at the CR's deletion
    timestamp, so the final interval ends at that value: anchoring the
    assertions on the recorded time compares one clock, never the test
    container's wall clock against the cluster's. None when the instance never
    had a CR to read (the cleanup path); callers that assert on billing always
    waited for the CR.
    """
    cli.delete_compute_instance(uuid=uuid)
    deletion_recorded: datetime | None = None
    if ci_name is not None:
        deletion_recorded = parse_time(
            poll_until(
                fn=lambda: k8s.get_jsonpath(
                    resource="computeinstance", name=ci_name, jsonpath="{.metadata.deletionTimestamp}"
                ),
                until=lambda stamp: bool(stamp),
                retries=30,
                delay=2,
                description=f"deletion timestamp of {ci_name}",
            )
        )
        wait_for_deletion(k8s=k8s, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
    return deletion_recorded


@pytest.mark.metering
def test_metering_heartbeat_envelope(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    metering_network_attachment: dict[str, object],
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """A RUNNING VM bills: heartbeats arrive well-formed and name the running instance type.

    The interval arithmetic itself (quantity equals the exact interval length,
    contiguity) is proven deterministically by the metering-service component
    suite; the deployed fact this test witnesses is that rounds keep arriving
    for a running VM, so a second typed heartbeat is required and nothing more.
    """
    uuid = _create_vm(
        cli, vm_template=vm_template, network_attachment=metering_network_attachment, project=metering_project
    )
    ci_name: str | None = None
    try:
        ci_name = _wait_for_running_vm(k8s_hub_client, uuid=uuid)
        # The running instance type lands 15-20 s after RUNNING, so early
        # heartbeats legitimately lack it: wait for the first heartbeat that
        # carries the dimension, as the resize scenario does, and verify that
        # event's envelope.
        heartbeat = metering.heartbeat_with_dimensions(
            uuid, {"instance_type": cli.default_instance_type}, timeout=HEARTBEAT_TIMEOUT
        )
        # verify_event checks the full envelope: CloudEvent type, source,
        # extensions, interval semantics, unit, precision, fixed-point quantity,
        # and the compute dimension set.
        metering.verify_event(heartbeat, resource_id=uuid)

        data = heartbeat["data"]
        assert data["meter"] == "compute", f"meter is {data['meter']!r}, want 'compute'"
        assert data["usage"]["unit"] == "second", f"unit is {data['usage']['unit']!r}"
        assert data["billing_dimensions"]["instance_type"] == cli.default_instance_type, (
            f"instance_type dimension is {data['billing_dimensions'].get('instance_type')!r}"
        )
        assert data["billing_dimensions"]["project"] == metering_project, (
            f"project dimension is {data['billing_dimensions'].get('project')!r}, want {metering_project!r}"
        )
        _, _, quantity = usage_interval(heartbeat)
        assert quantity > 0, f"quantity is not positive: {quantity}"

        # Billing continues while the VM runs: a second typed heartbeat arrives.
        # The early untyped heartbeat and its reversal count as events but not
        # as typed ones, so two typed heartbeats mean at least one full round
        # after the dimension landed.
        poll_until(
            fn=lambda: len(
                [
                    event
                    for event in metering.get_all_events(HEARTBEAT_TYPE, uuid)
                    if event["data"]["billing_dimensions"].get("instance_type") == cli.default_instance_type
                ]
            ),
            until=lambda count: count >= 2,
            retries=45,
            delay=10,
            description=f"two heartbeats of {uuid} on the {cli.default_instance_type} meter",
        )
    finally:
        _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)


@pytest.mark.metering
def test_metering_stop_ends_billing(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    metering_network_attachment: dict[str, object],
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """Stopping the VM ends its billing: a final partial interval arrives, then silence."""
    uuid = _create_vm(
        cli, vm_template=vm_template, network_attachment=metering_network_attachment, project=metering_project
    )
    ci_name: str | None = None
    try:
        ci_name = _wait_for_running_vm(k8s_hub_client, uuid=uuid)
        # Anchor on a heartbeat that carries the running instance type: early
        # heartbeats legitimately lack it until the fact lands.
        heartbeat = metering.heartbeat_with_dimensions(
            uuid, {"instance_type": cli.default_instance_type}, timeout=HEARTBEAT_TIMEOUT
        )
        metering.verify_event(heartbeat, resource_id=uuid)

        halt_requested = datetime.now(UTC)
        grpc.update_compute_instance_run_strategy(ci_id=uuid, run_strategy="Halted")
        vm_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        poll_until(
            fn=lambda: k8s_virt_client.get_vm_printable_status(name=ci_name, vm_namespace=vm_ns, checked=False),
            until=lambda status: status == "Stopped",
            retries=30,
            delay=10,
            description=f"{ci_name} VM stopped",
        )

        # Anchor the final interval on the stop time fulfillment recorded, not on the
        # wall clock of the request: an interval that overruns the actual stop by up to
        # a round would pass a wall-clock check.
        instance = poll_until(
            fn=lambda: grpc.get_compute_instance(ci_id=uuid),
            until=lambda inst: (
                inst.get("object", {}).get("status", {}).get("state") == "COMPUTE_INSTANCE_STATE_STOPPED"
            ),
            retries=30,
            delay=5,
            description=f"{ci_name} instance STOPPED in fulfillment",
        )
        stopped_at = parse_time(instance["object"]["status"]["stateTransitionTime"])

        # The stop reaches metering through several steps: operator feedback, a
        # fulfillment status change, the event topic, the fold, and a round. A round that
        # runs before the stop is folded still sends a full interval, and the real final
        # partial interval arrives only in the next round, so wait past a full round
        # before picking the final heartbeat.
        time.sleep(90)

        # The final heartbeat covers the partial interval up to the stop, so its
        # interval ends at or after the halt was requested.
        final = poll_until(
            fn=lambda: _final_heartbeat(metering, uuid, halt_requested),
            until=lambda event: event is not None,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"final partial-interval heartbeat of {uuid}",
        )
        _, final_to, _ = usage_interval(final)

        # The net billing ends at the recorded stop, not at the round that
        # observed it: a round that ran before the stop was folded sends a full
        # interval and the negative correction that follows cancels its tail, so
        # the check is on net usage per dimension, not on the raw interval end.
        # A full second of tolerance covers the whole-second CR stamps.
        poll_until(
            fn=lambda: net_usage_past_by_dimension(metering.get_all_events(HEARTBEAT_TYPE, uuid), past=stopped_at),
            until=lambda nets: all(abs(net) <= 1 for net in nets.values()),
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"net billing of {uuid} to end at the stop at {stopped_at}",
        )

        # Past the final interval nothing more arrives: the meter stopped.
        metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since=final_to.isoformat(), within=90)
    finally:
        _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)


def _final_heartbeat(metering: MeteringCollector, resource_id: str, halt: datetime) -> dict[str, Any] | None:
    latest_end: datetime | None = None
    latest: dict[str, Any] | None = None
    for event in metering.get_all_events(HEARTBEAT_TYPE, resource_id):
        _, to, _ = usage_interval(event)
        if latest_end is None or to > latest_end:
            latest_end, latest = to, event
    if latest is None or latest_end is None or latest_end < halt - CR_STAMP_TOLERANCE:
        return None
    return latest


@pytest.mark.metering
def test_metering_resize_splits_dimensions(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    metering_network_attachment: dict[str, object],
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """A resize splits usage across the two instance_type dimensions.

    The old meter stops at the transition, the new one starts there, the two
    never overlap, and the gap between them is exactly the restart: the VM
    bills nothing while STOPPING, STOPPED, or STARTING.
    """
    old_type = cli.default_instance_type
    new_type = f"e2e-metering-resize-{uuid4().hex[:8]}"
    private_grpc.create_instance_type(
        name=new_type, vcpus=RESIZE_IT_VCPUS, memory_gib=RESIZE_IT_MEMORY_GIB, description="E2E metering resize target"
    )
    uuid: str | None = None
    ci_name: str | None = None
    try:
        uuid = _create_vm(
            cli, vm_template=vm_template, network_attachment=metering_network_attachment, project=metering_project
        )
        ci_name = _wait_for_running_vm(k8s_hub_client, uuid=uuid)
        metering.heartbeat_with_dimensions(uuid, {"instance_type": old_type}, timeout=HEARTBEAT_TIMEOUT)

        # Resize and restart, so the running instance type actually changes: the
        # billing dimension follows the type the VM runs, not the request.
        update = grpc.update_compute_instance_instance_type(ci_id=uuid, instance_type=new_type)
        assert update.get("warnings", []) == []
        poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_condition_status(
                name=ci_name, condition_type="RestartRequired", checked=False
            ),
            until=lambda status: status == "True",
            retries=60,
            delay=5,
            description=f"{ci_name} RestartRequired after resize",
        )
        previous_restart = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
        restart_requested = datetime.now(tz=UTC)
        restart_ts = restart_requested.replace(microsecond=0).strftime("%Y-%m-%dT%H:%M:%SZ")
        grpc.update_restart(uuid=uuid, template=vm_template, timestamp=restart_ts)
        # Read the VM's state timeline through the API while it restarts: the
        # non-billable window opens when the phase first leaves Running.
        window_start = _observe_phase_leaves_running(k8s_hub_client, name=ci_name) or restart_requested
        wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=previous_restart, restart_ts=restart_ts)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_condition_status(
                name=ci_name, condition_type="RestartRequired", checked=False
            ),
            until=lambda status: status == "False",
            retries=60,
            delay=5,
            description=f"{ci_name} RestartRequired cleared after restart",
        )
        running_observed = datetime.now(tz=UTC)

        # The new meter starts: a heartbeat with the new dimension arrives.
        metering.heartbeat_with_dimensions(uuid, {"instance_type": new_type})

        def split_intervals() -> tuple[list, list]:
            events = metering.get_all_events(HEARTBEAT_TYPE, uuid)
            old = [e for e in events if e["data"]["billing_dimensions"].get("instance_type") == old_type]
            new = [e for e in events if e["data"]["billing_dimensions"].get("instance_type") == new_type]
            return net_intervals(old), net_intervals(new)

        # The old meter's tail correction is a separate event from the new
        # meter's first heartbeat and can arrive a round later: poll until the
        # nets are disjoint before asserting on them.
        old_intervals, new_intervals = poll_until(
            fn=split_intervals,
            until=lambda split: (
                bool(split[0])
                and bool(split[1])
                and max(to for _, to, _ in split[0]) <= min(start for start, _, _ in split[1])
            ),
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"old meter of {uuid} to end before the new meter starts",
        )
        assert old_intervals, "no heartbeat arrived on the old instance_type meter"
        assert new_intervals, "no heartbeat arrived on the new instance_type meter"

        # Each meter's own net intervals stay contiguous: nothing within one
        # meter's billing is lost or billed twice. A round that ran before the
        # resize was folded bills the old type past the transition and its
        # correction cancels the tail, so the raw intervals would look
        # noncontiguous and overlapping; the net does not.
        for label, intervals in (("old", old_intervals), ("new", new_intervals)):
            for previous, following in itertools.pairwise(intervals):
                assert following[0] == previous[1], (
                    f"the {label} meter's heartbeats are not contiguous: "
                    f"{previous[1].isoformat()} then {following[0].isoformat()}"
                )

        # Disjoint meters: everything the old meter billed ends before the new
        # meter's first interval starts, so no second is billed twice.
        old_end = max(to for _, to, _ in old_intervals)
        new_start = min(start for start, _, _ in new_intervals)
        assert old_end <= new_start, (
            f"the meters overlap: old meter billed until {old_end.isoformat()}, "
            f"new meter started at {new_start.isoformat()}"
        )

        # The gap between the meters is the restart, and only the restart: the
        # old meter billed until the restart actually stopped the VM, and the
        # whole gap fits inside the observed stop-to-running window, within
        # which the VM was STOPPING, STOPPED, or STARTING and billed nothing.
        assert old_end >= restart_requested, (
            f"the old meter stopped billing at {old_end.isoformat()}, before the restart was "
            f"requested at {restart_requested.isoformat()}: running time went unbilled"
        )
        gap = new_start - old_end
        window = running_observed - window_start
        assert gap <= window, (
            f"the billing gap {gap} between the meters exceeds the observed non-billable "
            f"window {window} ({window_start.isoformat()}..{running_observed.isoformat()}): "
            f"billable running time went unbilled"
        )

        # The old meter stopped: after the new meter's heartbeat, no further old
        # dimension heartbeats arrive within a full round past the split.
        time.sleep(90)
        events = metering.get_all_events(HEARTBEAT_TYPE, uuid)
        old_after = net_intervals(
            [event for event in events if event["data"]["billing_dimensions"].get("instance_type") == old_type]
        )
        assert all(to <= new_start for _, to, _ in old_after), (
            f"the old meter billed past the resize: net usage past {new_start.isoformat()}"
        )
    finally:
        if uuid is not None:
            with contextlib.suppress(Exception):
                _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)
        delete_instance_type_if_present(grpc=private_grpc, name=new_type)


def _observe_phase_leaves_running(k8s: K8sClient, *, name: str) -> datetime | None:
    """Sample the ComputeInstance phase until it first leaves Running.

    Returns the observation time, or None when the phase never left Running
    within the bounded window: a fast restart can slip between samples, and
    callers fall back to the restart request time.
    """
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        phase = k8s.get_compute_instance_phase(name=name, checked=False)
        if phase and phase != "Running":
            return datetime.now(tz=UTC)
        time.sleep(5)
    return None


@pytest.mark.metering
def test_metering_delete_ends_billing(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    metering_network_attachment: dict[str, object],
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """Deleting the VM bills the final partial interval exactly once, then stops.

    The billed life is internally consistent and complete: the typed heartbeats
    collapse into contiguous net intervals whose quantities equal their exact
    lengths, the last interval reaches the recorded deletion (the final
    partial interval is billed, not dropped), and nothing bills past it.
    """
    uuid = _create_vm(
        cli, vm_template=vm_template, network_attachment=metering_network_attachment, project=metering_project
    )
    ci_name: str | None = None
    try:
        ci_name = _wait_for_running_vm(k8s_hub_client, uuid=uuid)
        # Anchor on a heartbeat that carries the running instance type: early
        # heartbeats legitimately lack it until the fact lands.
        heartbeat = metering.heartbeat_with_dimensions(
            uuid, {"instance_type": cli.default_instance_type}, timeout=HEARTBEAT_TIMEOUT
        )
        metering.verify_event(heartbeat, resource_id=uuid)
        running_since, _, _ = usage_interval(heartbeat)

        deletion_recorded = _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)
        assert deletion_recorded is not None, "the running VM's CR vanished before its deletion timestamp was read"

        # The deletion reaches metering through the same steps as a stop, so wait
        # past a full round before picking the final heartbeat.
        time.sleep(90)

        # The final heartbeat covers the partial interval up to the recorded
        # deletion, so its interval ends at that timestamp.
        final = poll_until(
            fn=lambda: _final_heartbeat(metering, uuid, deletion_recorded),
            until=lambda event: event is not None,
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"final partial-interval heartbeat of {uuid}",
        )
        _, final_to, _ = usage_interval(final)

        # The billed life is exact: the typed heartbeats collapse into contiguous
        # net intervals, each billing exactly its length in seconds, so the total
        # equals the span from the first typed interval to the final one.
        typed = [
            event
            for event in metering.get_all_events(HEARTBEAT_TYPE, uuid)
            if event["data"]["billing_dimensions"].get("instance_type")
        ]
        net = net_intervals(typed)
        assert net, f"no typed heartbeats of {uuid} to assert the billed life on"
        for start, end, quantity in net:
            assert quantity == interval_seconds(start, end), (
                f"net interval {start}..{end} bills {quantity}, not its exact length"
            )
        for (_, previous_end, _), (start, _, _) in itertools.pairwise(net):
            assert start == previous_end, f"billed intervals are not contiguous: {previous_end} then {start}"
        billed_total = sum((quantity for _, _, quantity in net), Decimal(0))
        assert billed_total == interval_seconds(net[0][0], net[-1][1]), (
            f"the billed total {billed_total} does not equal the billed span {interval_seconds(net[0][0], net[-1][1])}"
        )
        assert net[-1][1] >= deletion_recorded - CR_STAMP_TOLERANCE, (
            f"the final interval ends at {net[-1][1]}, before the recorded deletion "
            f"at {deletion_recorded}: the partial final interval was dropped"
        )
        assert net[0][0] <= running_since, (
            f"billing starts at {net[0][0]}, after the first typed heartbeat at "
            f"{running_since}: the start of the billed life was dropped"
        )

        # Nothing bills past the recorded deletion: the final interval ends at
        # that timestamp, so the net usage past it is zero.
        poll_until(
            fn=lambda: net_usage_past_by_dimension(
                metering.get_all_events(HEARTBEAT_TYPE, uuid), past=deletion_recorded
            ),
            until=lambda nets: all(abs(net) <= 1 for net in nets.values()),
            retries=HEARTBEAT_TIMEOUT // 10,
            delay=10,
            description=f"net billing of {uuid} to end at the recorded deletion",
        )

        # Past the final interval nothing more arrives: the meter stopped.
        metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since=final_to.isoformat(), within=120)
    except BaseException:
        with contextlib.suppress(Exception):
            _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)
        raise
