"""No-cluster regressions for the metering collector and the billing journeys.

Each regression drives the real collector or journey body with a synthetic
valid heartbeat trace, so the assertions accept correct billing output:
corrections and reversals collapse into net usage, a project-less resource
bills with no project dimension (and a heartbeat that names one anyway fails
validation), and a setup wait that times out cannot leak the resource the
journey already created.

Run without a deployed cluster; ``--noconftest`` skips the session fixtures
that reach the cluster:

    uv run pytest --noconftest -o addopts='' tests/e2e/core/test_metering.py
"""

from __future__ import annotations

import contextlib
from collections.abc import Callable, Iterator
from datetime import UTC, datetime, timedelta
from decimal import Decimal
from http.client import RemoteDisconnected
from io import BytesIO
from json import dumps
from types import SimpleNamespace
from typing import Any

import pytest

from tests.e2e.core import metering as metering_core
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, billing_account_id
from tests.e2e.core.runner import StuckState
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.sanity import test_compute_instance_creation as creation
from tests.e2e.vmaas.sanity import test_metering_billing as billing

START = datetime(2026, 10, 5, 10, tzinfo=UTC)


def _heartbeat(
    event_id: str,
    start: datetime,
    end: datetime,
    quantity: str,
    *,
    meter: str = "compute",
    instance_type: str | None = "m-small",
    project: str | None = "billing",
) -> dict[str, Any]:
    """A well-formed compute heartbeat; a None dimension is simply absent."""
    dimensions: dict[str, Any] = {
        "deployment": "test/deployment",
        "tenant": "tenant1",
        "meter_type": "second",
        "image": "rhel-9",
        "boot_disk_size_gib": "30",
    }
    if project is not None:
        dimensions["project"] = project
    if instance_type is not None:
        dimensions["instance_type"] = instance_type
    return {
        "specversion": "1.0",
        "type": HEARTBEAT_TYPE,
        "source": "osac-metering",
        "id": event_id,
        "time": start.isoformat(),
        "osacresourceid": "vm-7",
        "osacresourcetype": "compute_instance",
        "osactenant": "tenant1",
        "data": {
            "resource_id": "vm-7",
            "resource_type": "compute_instance",
            "tenant_id": "tenant1",
            "meter": meter,
            "component": meter,
            "heartbeat_time": end.isoformat(),
            "rules_revision": "0001",
            "billing_dimensions": dimensions,
            "usage": {
                "semantics": "interval",
                "unit": "second",
                "precision": "second",
                "from": start.isoformat(),
                "to": end.isoformat(),
                "quantity": quantity,
            },
        },
    }


def _serve(monkeypatch: pytest.MonkeyPatch, events: list[dict[str, Any]]) -> MeteringCollector:
    """A collector whose adapter always returns the given events."""
    collector = MeteringCollector(base_url="http://echo-adapter.example.test")
    monkeypatch.setattr(collector, "_fetch_events", lambda *_args: events)
    return collector


def _patch_billing_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    """Make the billing journeys' Kubernetes and gRPC waits instant no-ops."""
    monkeypatch.setattr(billing, "wait_for_cr", lambda **_kwargs: "cr-vm-7")
    for wait_name in ("wait_for_provision", "wait_for_running", "wait_for_deletion", "wait_for_grpc_removal"):
        monkeypatch.setattr(billing, wait_name, lambda **_kwargs: None)


def test_creation_journey_uses_the_typed_heartbeat(monkeypatch: pytest.MonkeyPatch) -> None:
    """An early untyped heartbeat stays the cached answer after its reversal and
    the typed replacement arrive: the journey must wait for the typed one
    instead of inspecting the first cached heartbeat."""
    early = _heartbeat("early", START, START + timedelta(seconds=15), "15.000000", instance_type=None, project=None)
    reversal = _heartbeat(
        "reverse-early", START, START + timedelta(seconds=15), "-15.000000", instance_type=None, project=None
    )
    typed = _heartbeat("typed", START, START + timedelta(minutes=1), "60.000000", project=None)
    collector = _serve(monkeypatch, [early, reversal, typed])
    cli = SimpleNamespace(
        create_compute_instance=lambda **_kwargs: "vm-7",
        delete_compute_instance=lambda **_kwargs: None,
        default_instance_type="m-small",
    )
    for wait_name in ("wait_for_provision", "wait_for_running", "wait_for_deletion", "wait_for_grpc_removal"):
        monkeypatch.setattr(creation, wait_name, lambda **_kwargs: None)
    monkeypatch.setattr(creation, "wait_for_cr", lambda **_kwargs: "cr-vm-7")
    monkeypatch.delenv("OSAC_FULFILLMENT_TRUST_E2E", raising=False)

    creation.test_compute_instance_lifecycle(
        cli=cli,
        grpc=SimpleNamespace(list_compute_instance_ids=lambda: ["vm-7"]),
        k8s_hub_client=SimpleNamespace(get_compute_instance_vm_namespace=lambda **_kwargs: "vm-ns"),
        k8s_virt_client=SimpleNamespace(get_vmi_creation_timestamp=lambda **_kwargs: "2026-10-05T10:00:05Z"),
        vm_template="vm-template",
        default_network_attachment={},
        metering=collector,
    )


def test_envelope_journey_waits_for_two_typed_heartbeats(monkeypatch: pytest.MonkeyPatch) -> None:
    """An early untyped heartbeat and its reversal satisfy a wait for any two
    events: the envelope journey's second-heartbeat poll must wait for two
    intervals of the typed meter."""
    early = _heartbeat("early", START, START + timedelta(seconds=15), "15.000000", instance_type=None)
    reversal = _heartbeat("reverse-early", START, START + timedelta(seconds=15), "-15.000000", instance_type=None)
    typed1 = _heartbeat("typed-1", START, START + timedelta(minutes=1), "60.000000")
    typed2 = _heartbeat("typed-2", START + timedelta(minutes=1), START + timedelta(minutes=2), "60.000000")
    calls = 0

    def fetch(*_args: object) -> list[dict[str, Any]]:
        nonlocal calls
        calls += 1
        return [early, reversal, typed1] if calls < 3 else [early, reversal, typed1, typed2]

    collector = MeteringCollector(base_url="http://echo-adapter.example.test")
    monkeypatch.setattr(collector, "_fetch_events", fetch)
    _patch_billing_waits(monkeypatch)
    monkeypatch.setattr(billing.time, "sleep", lambda *_args: None)

    billing.test_metering_heartbeat_envelope(
        cli=SimpleNamespace(
            default_instance_type="m-small",
            create_compute_instance=lambda **_kwargs: "vm-7",
            delete_compute_instance=lambda **_kwargs: None,
        ),
        grpc=SimpleNamespace(),
        # The journey's cleanup reads the CR's deletion timestamp before the
        # patched waits remove it.
        k8s_hub_client=SimpleNamespace(get_jsonpath=lambda **_kwargs: "2026-10-05T10:03:00Z"),
        vm_template="vm-template",
        metering_network_attachment={},
        metering=collector,
        metering_project="billing",
    )


def test_expect_no_project_rejects_a_project_dimension(monkeypatch: pytest.MonkeyPatch) -> None:
    """With the mode set, a heartbeat that carries a project dimension fails
    validation: the resource was created without a project, so the dimension
    is misattribution, not a permission to skip the check."""
    heartbeat = _heartbeat("with-project", START, START + timedelta(minutes=1), "60.000000")
    collector = _serve(monkeypatch, [heartbeat])
    collector.expect_no_project()

    with pytest.raises(AssertionError, match="project"):
        collector.verify_event(heartbeat, resource_id="vm-7")


def test_expect_no_project_accepts_a_heartbeat_without_a_project(monkeypatch: pytest.MonkeyPatch) -> None:
    """With the mode set, a heartbeat that carries no project dimension passes
    the full structure validation: absence is asserted, not tolerated."""
    heartbeat = _heartbeat("no-project", START, START + timedelta(minutes=1), "60.000000", project=None)
    collector = _serve(monkeypatch, [heartbeat])
    collector.expect_no_project()

    collector.verify_event(heartbeat, resource_id="vm-7")


def test_verify_accepts_reversed_history_returned_after_its_replacement(monkeypatch: pytest.MonkeyPatch) -> None:
    """Tied interval starts leave validation ordered by fetch order: a typed
    replacement returned before the untyped history it reverses must not make
    validation demand the type on that canceled history."""
    typed = _heartbeat("typed", START, START + timedelta(minutes=1), "60.000000")
    early = _heartbeat("early", START, START + timedelta(seconds=15), "15.000000", instance_type=None)
    reversal = _heartbeat("reverse-early", START, START + timedelta(seconds=15), "-15.000000", instance_type=None)
    collector = _serve(monkeypatch, [typed, early, reversal])

    collector.expect(HEARTBEAT_TYPE, "vm-7", resource_kind=WaitResourceKind.COMPUTE_INSTANCE)
    collector.verify()


def _corrected_stop_events() -> list[dict[str, Any]]:
    """A stop at second 45: a full round through second 60, then the negative
    correction that cancels the 15 seconds past the stop. Net usage: 45."""
    return [
        _heartbeat("one", START, START + timedelta(seconds=30), "30.000000"),
        _heartbeat("overshoot", START + timedelta(seconds=30), START + timedelta(seconds=60), "30.000000"),
        _heartbeat("correction", START + timedelta(seconds=45), START + timedelta(seconds=60), "-15.000000"),
    ]


def test_stop_journey_accepts_corrected_net_usage(monkeypatch: pytest.MonkeyPatch) -> None:
    """A full interval through second 60 plus a negative 15-second correction
    for a stop at second 45 is correct billing: the journey must judge the net
    per dimension, not the raw interval ends."""
    events = _corrected_stop_events()
    assert sum(Decimal(event["data"]["usage"]["quantity"]) for event in events) == Decimal("45")
    collector = _serve(monkeypatch, events)
    _patch_billing_waits(monkeypatch)
    # The journey waits for the net billed end to go quiet; clamp the quiet
    # window to zero so the check needs no wall time but still runs the real
    # helper against the synthetic trace.
    original_wait_for_billing_end = MeteringCollector.wait_for_billing_end

    def immediate_wait_for_billing_end(
        self: MeteringCollector, resource_id: str, *, deletion: datetime, timeout: int = 600
    ) -> datetime:
        return original_wait_for_billing_end(self, resource_id, deletion=deletion, quiet=0, timeout=timeout)

    monkeypatch.setattr(MeteringCollector, "wait_for_billing_end", immediate_wait_for_billing_end)
    # The silence window is polled for its whole duration; clamp it to zero so
    # the harness check needs no wall time (it still runs its polling loop once).
    original_assert_no_events = MeteringCollector.assert_no_events

    def immediate_assert_no_events(
        self: MeteringCollector, event_type: str, resource_id: str, *, since: str, within: int = 60
    ) -> None:
        original_assert_no_events(self, event_type, resource_id, since=since, within=0)

    monkeypatch.setattr(MeteringCollector, "assert_no_events", immediate_assert_no_events)
    monkeypatch.setattr(
        billing, "datetime", SimpleNamespace(now=lambda *_args, **_kwargs: START + timedelta(seconds=40))
    )
    grpc = SimpleNamespace(
        update_compute_instance_run_strategy=lambda **_kwargs: None,
        get_compute_instance=lambda **_kwargs: {
            "object": {
                "status": {
                    "state": "COMPUTE_INSTANCE_STATE_STOPPED",
                    # Fulfillment observed STOPPED 10s after the CR's Stopping
                    # phase; the journey must anchor on Stopping, not this.
                    "stateTransitionTime": (START + timedelta(seconds=55)).isoformat(),
                }
            }
        },
    )

    @contextlib.contextmanager
    def watch_phase_transition(**_kwargs: object) -> Iterator[Callable[[str], str]]:
        yield lambda phase: {
            "Stopping": (START + timedelta(seconds=45)).isoformat(),
            "Stopped": (START + timedelta(seconds=55)).isoformat(),
        }.get(phase, "")

    def get_jsonpath(**_kwargs: object) -> str:
        return "2026-10-05T10:03:00Z"

    billing.test_metering_stop_ends_billing(
        cli=SimpleNamespace(
            default_instance_type="m-small",
            create_compute_instance=lambda **_kwargs: "vm-7",
            delete_compute_instance=lambda **_kwargs: None,
        ),
        grpc=grpc,
        # The cleanup reads the CR's deletion timestamp before the patched
        # waits remove it.
        k8s_hub_client=SimpleNamespace(
            get_compute_instance_vm_namespace=lambda **_kwargs: "vm-ns",
            get_jsonpath=get_jsonpath,
            watch_phase_transition=watch_phase_transition,
        ),
        k8s_virt_client=SimpleNamespace(get_vm_printable_status=lambda **_kwargs: "Stopped"),
        vm_template="vm-template",
        metering_network_attachment={},
        metering=collector,
        metering_project="billing",
    )


def test_billing_end_wait_requires_the_late_correction(monkeypatch: pytest.MonkeyPatch) -> None:
    """An overshoot that stays visible is not the billed end: the helper must
    wait for the correction even after the quiet window has elapsed."""
    overshoot = _corrected_stop_events()[:2]
    correction = _corrected_stop_events()[2]
    stop = START + timedelta(seconds=45)
    calls = 0

    def fetch(*_args: object) -> list[dict[str, Any]]:
        nonlocal calls
        calls += 1
        return overshoot if calls == 1 else [*overshoot, correction]

    collector = MeteringCollector(base_url="http://echo-adapter.example.test")
    monkeypatch.setattr(collector, "_fetch_events", fetch)
    monkeypatch.setattr(metering_core.time, "sleep", lambda *_args: None)

    end = collector.wait_for_billing_end("vm-7", deletion=stop, quiet=0, timeout=5)

    assert calls == 2, f"the helper returned before the correction landed on call {calls}"
    assert end == stop


def test_billing_end_wait_retries_transient_fetch_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    """A dropped echo-adapter connection is retried by the polling wait, not
    raised as a test failure."""
    events = _corrected_stop_events()
    calls = 0

    def fetch(*_args: object) -> list[dict[str, Any]]:
        nonlocal calls
        calls += 1
        if calls == 1:
            raise OSError("Remote end closed connection without response")
        return events

    collector = MeteringCollector(base_url="http://echo-adapter.example.test")
    monkeypatch.setattr(collector, "_fetch_events", fetch)
    monkeypatch.setattr(metering_core.time, "sleep", lambda *_args: None)

    end = collector.wait_for_billing_end("vm-7", deletion=START + timedelta(seconds=45), quiet=0, timeout=5)

    assert calls == 2, f"the helper gave up after {calls} fetch attempt(s)"
    assert end == START + timedelta(seconds=45)


def test_billing_end_wait_allows_a_named_late_meter(monkeypatch: pytest.MonkeyPatch) -> None:
    """BMaaS allocation may bill until the host is released, after the
    recorded deletion; consumption must still end at the anchor."""
    events = [
        _heartbeat("allocation", START, START + timedelta(minutes=1), "60.000000", meter="allocation"),
        _heartbeat("consumption", START, START + timedelta(seconds=45), "45.000000", meter="consumption"),
    ]
    collector = _serve(monkeypatch, events)
    monkeypatch.setattr(metering_core.time, "sleep", lambda *_args: None)

    end = collector.wait_for_billing_end(
        "vm-7", deletion=START + timedelta(seconds=45), late_meters={"allocation"}, quiet=0, timeout=5
    )

    assert end == START + timedelta(minutes=1)


def test_billing_end_wait_accepts_one_second_of_clock_slop(monkeypatch: pytest.MonkeyPatch) -> None:
    """A final interval one second past the recorded boundary is the clock
    truncation the callers already tolerate, not an uncorrected overshoot."""
    collector = _serve(monkeypatch, _corrected_stop_events())
    monkeypatch.setattr(metering_core.time, "sleep", lambda *_args: None)

    end = collector.wait_for_billing_end("vm-7", deletion=START + timedelta(seconds=44), quiet=0, timeout=5)

    assert end == START + timedelta(seconds=45)


def test_billing_end_wait_honors_the_quiet_window(monkeypatch: pytest.MonkeyPatch) -> None:
    """The helper must keep watching for a correction for the quiet window,
    not return on the first stable sample."""
    collector = _serve(monkeypatch, _corrected_stop_events())
    monkeypatch.setattr(metering_core.time, "sleep", lambda *_args: None)
    stop = START + timedelta(seconds=45)
    started = metering_core.time.monotonic()

    end = collector.wait_for_billing_end("vm-7", deletion=stop, quiet=1, timeout=5)

    elapsed = metering_core.time.monotonic() - started
    assert end == stop
    assert elapsed >= 1, f"the helper returned after {elapsed:.3f}s, before the quiet window"


def test_assert_no_events_accepts_canceled_net_usage(monkeypatch: pytest.MonkeyPatch) -> None:
    """A full interval past the halt and the correction that cancels it are both
    immutable history: silence must be judged on the net, not on raw ends."""
    collector = _serve(monkeypatch, _corrected_stop_events())

    collector.assert_no_events(HEARTBEAT_TYPE, "vm-7", since=(START + timedelta(seconds=45)).isoformat(), within=0)


def test_resize_journey_nets_canceled_old_type_tails(monkeypatch: pytest.MonkeyPatch) -> None:
    """A round that ran before the resize was folded bills the old type past the
    transition, and a negative correction cancels the tail: the raw old-type
    intervals look noncontiguous and overlapping, the net does not."""
    new_type = "e2e-metering-resize-deadbeef"
    old_events = [
        _heartbeat("old-1", START, START + timedelta(minutes=1), "60.000000", instance_type="m-small"),
        _heartbeat(
            "old-2", START + timedelta(minutes=1), START + timedelta(minutes=2), "60.000000", instance_type="m-small"
        ),
        _heartbeat(
            "old-cancel",
            START + timedelta(seconds=75),
            START + timedelta(minutes=2),
            "-45.000000",
            instance_type="m-small",
        ),
    ]
    new_events = [
        _heartbeat(
            "new-1",
            START + timedelta(seconds=90),
            START + timedelta(minutes=2, seconds=30),
            "60.000000",
            instance_type=new_type,
        )
    ]
    collector = _serve(monkeypatch, old_events + new_events)
    _patch_billing_waits(monkeypatch)
    monkeypatch.setattr(billing, "wait_for_restart", lambda **_kwargs: None)
    monkeypatch.setattr(
        billing, "_observe_phase_leaves_running", lambda *_args, **_kwargs: START + timedelta(seconds=61)
    )
    monkeypatch.setattr(billing, "delete_instance_type_if_present", lambda **_kwargs: None)
    monkeypatch.setattr(billing, "uuid4", lambda: SimpleNamespace(hex="deadbeefcafe"))
    monkeypatch.setattr(billing.time, "sleep", lambda *_args: None)
    moments = iter([START + timedelta(seconds=60), START + timedelta(seconds=90)])
    monkeypatch.setattr(billing, "datetime", SimpleNamespace(now=lambda *_args, **_kwargs: next(moments)))
    conditions = iter(["True", "False"])
    k8s_hub_client = SimpleNamespace(
        get_compute_instance_condition_status=lambda **_kwargs: next(conditions),
        get_compute_instance_last_restarted_at=lambda **_kwargs: "2026-10-05T10:00:00Z",
    )

    billing.test_metering_resize_splits_dimensions(
        cli=SimpleNamespace(
            default_instance_type="m-small",
            create_compute_instance=lambda **_kwargs: "vm-7",
            delete_compute_instance=lambda **_kwargs: None,
        ),
        grpc=SimpleNamespace(
            update_compute_instance_instance_type=lambda **_kwargs: {"warnings": []},
            update_restart=lambda **_kwargs: None,
        ),
        private_grpc=SimpleNamespace(create_instance_type=lambda **_kwargs: None),
        k8s_hub_client=k8s_hub_client,
        vm_template="vm-template",
        metering_network_attachment={},
        metering=collector,
        metering_project="billing",
    )


def test_envelope_journey_cleans_up_the_vm_when_a_setup_wait_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    """A setup wait that times out after the VM exists must still delete it:
    cleanup is registered before the first wait, not after the last."""

    def never_created(**_kwargs: object) -> str:
        raise TimeoutError("Kubernetes resource did not appear")

    deleted: list[str] = []
    cli = SimpleNamespace(
        create_compute_instance=lambda **_kwargs: "vm-7",
        delete_compute_instance=lambda **kwargs: deleted.append(kwargs["uuid"]),
    )
    monkeypatch.setattr(billing, "wait_for_cr", never_created)
    for wait_name in ("wait_for_provision", "wait_for_running", "wait_for_deletion", "wait_for_grpc_removal"):
        monkeypatch.setattr(billing, wait_name, lambda **_kwargs: None)

    with pytest.raises(TimeoutError, match="did not appear"):
        billing.test_metering_heartbeat_envelope(
            cli=cli,
            grpc=SimpleNamespace(),
            k8s_hub_client=SimpleNamespace(),
            vm_template="vm-template",
            metering_network_attachment={},
            metering=None,
            metering_project="billing",
        )
    assert deleted == ["vm-7"], f"created resource escaped cleanup: deletes were {deleted}"


def test_billing_account_id_treats_missing_and_empty_as_absent() -> None:
    """The topic omits the key when the tenant has no id; an empty string is the same."""
    assert billing_account_id({"data": {}}) is None
    assert billing_account_id({"data": {"external_billing_account_id": ""}}) is None
    assert billing_account_id({"data": {"external_billing_account_id": "acct-1"}}) == "acct-1"


def test_published_heartbeats_keep_their_billing_account_when_later_ones_change() -> None:
    """FR-4 on the topic: a later heartbeat with a new account does not rewrite earlier ones."""
    earlier = _heartbeat("acct-1-beat", START, START + timedelta(minutes=1), "60.000000")
    earlier["data"]["external_billing_account_id"] = "acct-1"
    later = _heartbeat("acct-2-beat", START + timedelta(minutes=1), START + timedelta(minutes=2), "60.000000")
    later["data"]["external_billing_account_id"] = "acct-2"
    published = {earlier["id"]: billing_account_id(earlier)}
    topic = [earlier, later]
    still = {event["id"]: billing_account_id(event) for event in topic}
    assert still[later["id"]] == "acct-2"
    for event_id, account in published.items():
        assert still[event_id] == account


def _script_event_reads(
    monkeypatch: pytest.MonkeyPatch, *responses: list[dict[str, Any]] | None
) -> tuple[MeteringCollector, list[float]]:
    """Script GET responses and disconnects below the collector's inner retry loop."""
    remaining = iter(responses)
    now = 0.0
    read_times: list[float] = []

    def sleep(seconds: float) -> None:
        nonlocal now
        now += seconds

    def urlopen(_request: object, *, timeout: int) -> BytesIO:
        read_times.append(now)
        response = next(remaining)
        if response is None:
            raise RemoteDisconnected("Remote end closed connection without response")
        return BytesIO(dumps(response).encode())

    clock = SimpleNamespace(monotonic=lambda: now, sleep=sleep)
    monkeypatch.setattr("tests.e2e.core.metering.time", clock)
    monkeypatch.setattr("tests.e2e.core.runner.time", clock)
    monkeypatch.setattr("tests.e2e.core.metering.urlopen", urlopen)
    return MeteringCollector(base_url="http://echo-adapter.example.test"), read_times


def test_heartbeat_with_dimensions_recovers_after_disconnected_read(monkeypatch: pytest.MonkeyPatch) -> None:
    after = START + timedelta(minutes=1)
    early = _heartbeat("early", START, after, "60.000000")
    untyped = _heartbeat("untyped", after, after + timedelta(minutes=1), "60.000000", instance_type=None)
    matching = _heartbeat("matching", after, after + timedelta(minutes=1), "60.000000")
    nonmatching = [early, untyped]
    collector, read_times = _script_event_reads(monkeypatch, None, None, None, nonmatching, [*nonmatching, matching])

    event = collector.heartbeat_with_dimensions(
        "vm-7", {"instance_type": "m-small"}, resource_kind=WaitResourceKind.COMPUTE_INSTANCE, after=after, timeout=30
    )

    assert event == matching
    assert read_times == [0, 2, 4, 16, 26]


@pytest.mark.parametrize("disconnected", [True, False], ids=["unavailable", "missing"])
def test_heartbeat_with_dimensions_timeout_distinguishes_disconnected_reads(
    monkeypatch: pytest.MonkeyPatch, disconnected: bool
) -> None:
    responses: list[list[dict[str, Any]] | None] = [None] * 6 if disconnected else [[], []]
    collector, read_times = _script_event_reads(monkeypatch, *responses)

    with pytest.raises(StuckState, match="heartbeat of vm-7 with dimensions") as error:
        collector.heartbeat_with_dimensions(
            "vm-7", {"instance_type": "m-small"}, resource_kind=WaitResourceKind.COMPUTE_INSTANCE, timeout=20
        )

    if disconnected:
        assert "RemoteDisconnected" in str(error.value)
        assert isinstance(error.value.__cause__, RemoteDisconnected)
        assert read_times == [0, 2, 4, 16, 18, 20]
    else:
        assert error.value.__cause__ is None
        assert read_times == [0, 10]


def test_assert_no_events_recovers_after_disconnected_read(monkeypatch: pytest.MonkeyPatch) -> None:
    collector, read_times = _script_event_reads(monkeypatch, None, None, None, [], [], [])

    collector.assert_no_events(HEARTBEAT_TYPE, "vm-7", since=START.isoformat(), within=18)

    assert read_times == [0, 2, 4, 11, 16, 18]


@pytest.mark.parametrize("initial_read_succeeds", [True, False], ids=["earlier-sample", "persistent"])
def test_assert_no_events_fails_if_deadline_read_disconnects(
    monkeypatch: pytest.MonkeyPatch, initial_read_succeeds: bool
) -> None:
    initial: list[list[dict[str, Any]] | None] = [[]] if initial_read_succeeds else [None] * 3
    collector, read_times = _script_event_reads(monkeypatch, *initial, None, None, None)

    with pytest.raises(TimeoutError, match="metering event read failed at the 10s deadline") as error:
        collector.assert_no_events(HEARTBEAT_TYPE, "vm-7", since=START.isoformat(), within=10)

    assert isinstance(error.value.__cause__, RemoteDisconnected)
    assert read_times == ([0, 5, 7, 9] if initial_read_succeeds else [0, 2, 4, 10, 12, 14])


@pytest.mark.parametrize("within, recovered_at", [(30, 11), (10, 10)], ids=["before-deadline", "at-deadline"])
def test_assert_no_events_checks_for_overbilling_after_disconnected_read(
    monkeypatch: pytest.MonkeyPatch, within: int, recovered_at: int
) -> None:
    overbilled = _heartbeat("overbilled", START, START + timedelta(minutes=1), "60.000000")
    collector, read_times = _script_event_reads(monkeypatch, None, None, None, [overbilled])

    with pytest.raises(AssertionError, match="but net usage past it is"):
        collector.assert_no_events(HEARTBEAT_TYPE, "vm-7", since=START.isoformat(), within=within)

    assert read_times == [0, 2, 4, recovered_at]
