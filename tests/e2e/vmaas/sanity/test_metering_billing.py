from __future__ import annotations

import contextlib
import itertools
import time
from collections.abc import Iterator
from datetime import UTC, datetime
from decimal import Decimal
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    delete_instance_type_if_present,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_restart,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, net_usage_past_by_dimension, parse_time
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.sanity

# A round ticks every 60s; heartbeats may need a few rounds to surface.
HEARTBEAT_TIMEOUT: int = 180

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

        sg_id = grpc.create_security_group_with_rules(
            name=unique_name("e2e-metering-sg"),
            virtual_network=vn_id,
            ingress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            project=metering_project,
        )
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
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


def _delete_vm(cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, *, uuid: str, ci_name: str | None) -> None:
    cli.delete_compute_instance(uuid=uuid)
    if ci_name is not None:
        wait_for_deletion(k8s=k8s, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)


def _usage_interval(event: dict[str, Any]) -> tuple[datetime, datetime, Decimal]:
    """Return one heartbeat's usage interval and quantity."""
    usage = event["data"]["usage"]
    return parse_time(usage["from"]), parse_time(usage["to"]), Decimal(usage["quantity"])


def _interval_seconds(start: datetime, end: datetime) -> Decimal:
    """Return the exact interval length in seconds.

    Heartbeat precision is microsecond and partial intervals carry quantities like
    ``37.123456``; building the value through ``total_seconds()`` goes via a binary
    float and can differ from the exact decimal, failing correct billing.
    """
    delta = end - start
    micros = (delta.days * 86400 + delta.seconds) * 1_000_000 + delta.microseconds
    return Decimal(micros) / Decimal(1_000_000)


def _net_intervals(events: list[dict[str, Any]]) -> list[tuple[datetime, datetime, Decimal]]:
    """Collapse signed heartbeats into the intervals the meter actually bills.

    Each heartbeat bills its quantity over its usage interval and a negative
    correction cancels the overlap, so a canceled tail disappears from the net
    instead of showing up as a gap or an overlap between raw intervals.
    Return the contiguous net-billed intervals with their net quantity.
    """
    rates = [
        (start, end, quantity / _interval_seconds(start, end))
        for start, end, quantity in (_usage_interval(event) for event in events)
    ]
    boundaries = sorted({moment for start, end, _ in rates for moment in (start, end)})
    intervals: list[tuple[datetime, datetime, Decimal]] = []
    for start, end in itertools.pairwise(boundaries):
        rate = sum(r for r_start, r_end, r in rates if r_start <= start and r_end >= end)
        if rate <= 0:
            continue
        quantity = rate * _interval_seconds(start, end)
        if intervals and intervals[-1][1] == start:
            previous_start, _, previous_quantity = intervals[-1]
            intervals[-1] = (previous_start, end, previous_quantity + quantity)
        else:
            intervals.append((start, end, quantity))
    return intervals


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
    """A RUNNING VM bills: heartbeats arrive well-formed and name the running instance type."""
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
        _, _, quantity = _usage_interval(heartbeat)
        assert quantity > 0, f"quantity is not positive: {quantity}"
    finally:
        _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)


@pytest.mark.metering
def test_metering_per_interval_continuation(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    metering_network_attachment: dict[str, object],
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """Billing continues per interval while the VM runs.

    Consecutive heartbeats cover contiguous intervals, and each quantity equals
    its interval's length in seconds.
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

        # The heartbeats of the typed meter, whose dimension is stable: an early
        # heartbeat that predates the running-type fact bills on a dimensionless
        # meter and is corrected onto the typed one, so only the typed meter's
        # own intervals are contiguous. Wait for two of them: the early
        # heartbeat and its reversal count as events but not as typed intervals.
        def typed_intervals() -> list[tuple[datetime, datetime, Decimal]]:
            return sorted(
                _usage_interval(event)
                for event in metering.get_all_events(HEARTBEAT_TYPE, uuid)
                if event["data"]["billing_dimensions"].get("instance_type") == cli.default_instance_type
            )

        intervals = poll_until(
            fn=typed_intervals,
            until=lambda found: len(found) >= 2,
            retries=45,
            delay=10,
            description=f"at least two heartbeats of {uuid} on the {cli.default_instance_type} meter",
        )
        for previous, following in itertools.pairwise(intervals):
            previous_to = previous[1]
            next_from = following[0]
            assert next_from == previous_to, (
                f"heartbeats are not contiguous: {previous_to.isoformat()} then {next_from.isoformat()}"
            )
        for start, end, quantity in intervals:
            assert quantity == _interval_seconds(start, end), (
                f"quantity {quantity} does not match the interval {start.isoformat()}..{end.isoformat()}"
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
            until=lambda inst: inst.get("status", {}).get("state") == "COMPUTE_INSTANCE_STATE_STOPPED",
            retries=30,
            delay=5,
            description=f"{ci_name} instance STOPPED in fulfillment",
        )
        stopped_at = parse_time(instance["status"]["stateTransitionTime"])

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
        _, final_to, _ = _usage_interval(final)

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


def _final_heartbeat(metering: MeteringCollector, resource_id: str, halt_requested: datetime) -> dict[str, Any] | None:
    latest_end: datetime | None = None
    latest: dict[str, Any] | None = None
    for event in metering.get_all_events(HEARTBEAT_TYPE, resource_id):
        _, to, _ = _usage_interval(event)
        if latest_end is None or to > latest_end:
            latest_end, latest = to, event
    if latest is None or latest_end is None or latest_end < halt_requested:
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

        events = metering.get_all_events(HEARTBEAT_TYPE, uuid)
        old_intervals = _net_intervals(
            [event for event in events if event["data"]["billing_dimensions"].get("instance_type") == old_type]
        )
        new_intervals = _net_intervals(
            [event for event in events if event["data"]["billing_dimensions"].get("instance_type") == new_type]
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
        old_after = _net_intervals(
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
    """Deleting the VM ends its billing: no heartbeats arrive after the deletion."""
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

        _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)
        # Capture the silence window only after the deletion is fully through, so
        # the final partial-interval heartbeat and any correction it triggers land
        # before the window starts; the stop test uses the same ordering.
        deleted_after = datetime.now(UTC)

        # Past the deletion nothing more arrives. The window spans a full round
        # plus drain.
        metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since=deleted_after.isoformat(), within=120)
    except BaseException:
        with contextlib.suppress(Exception):
            _delete_vm(cli, grpc, k8s_hub_client, uuid=uuid, ci_name=ci_name)
        raise


@pytest.mark.metering
def test_metering_external_ip_bills(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    metering: MeteringCollector,
    metering_project: str,
) -> None:
    """One non-compute resource bills on its own meter and dimensions.

    An allocated ExternalIP bills per second on the allocation meter, with the
    pool, IP family, and attachment state as its dimensions.
    """
    pool_name = f"e2e-metering-pool-{uuid4().hex[:8]}"
    subnet = allocate_worker_subnet(prefix=24)
    pool_id: str = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(subnet)])
    pool_cr_name: str | None = None
    ip_id: str | None = None
    ip_cr_name: str | None = None
    try:
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)

        ip_id = grpc.create_external_ip(
            name=f"e2e-metering-ip-{uuid4().hex[:8]}", pool=pool_id, project=metering_project
        )
        ip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=ip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

        # verify() checks the envelope and the ExternalIP dimension set: pool,
        # ip_family, attached, and attribution when attached.
        metering.expect(HEARTBEAT_TYPE, resource_id=ip_id, timeout=HEARTBEAT_TIMEOUT)
        metering.verify()

        heartbeat = metering.get_event(HEARTBEAT_TYPE, resource_id=ip_id)
        data = heartbeat["data"]
        assert data["meter"] == "allocation", f"meter is {data['meter']!r}, want 'allocation'"
        assert data["usage"]["unit"] == "second", f"unit is {data['usage']['unit']!r}"
        assert heartbeat["osacresourcetype"] == "external_ip", f"resource type is {heartbeat['osacresourcetype']!r}"
        dimensions = data["billing_dimensions"]
        # References render by name in heartbeats: the pool dimension is the pool's
        # name, the same contract as the instance_type dimension.
        assert dimensions["pool"] == pool_name, f"pool dimension is {dimensions.get('pool')!r}"
        assert dimensions["ip_family"] == "ipv4", f"ip_family dimension is {dimensions.get('ip_family')!r}"
        assert dimensions["project"] == metering_project, (
            f"project dimension is {dimensions.get('project')!r}, want {metering_project!r}"
        )
        _, _, quantity = _usage_interval(heartbeat)
        assert quantity > 0, f"quantity is not positive: {quantity}"
    finally:
        if ip_id is not None:
            with contextlib.suppress(Exception):
                grpc.delete_external_ip(external_ip_id=ip_id)
                if ip_cr_name is not None:
                    wait_for_external_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
        private_grpc.delete_external_ip_pool(pool_id=pool_id)
        if pool_cr_name is not None:
            wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
