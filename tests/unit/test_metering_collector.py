"""MeteringCollector event verification without a cluster."""

from __future__ import annotations

from datetime import datetime
from typing import Any

import pytest

from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, parse_time


def _heartbeat(
    *,
    instance_type: str | None = "m-small",
    event_id: str = "01JM8Z9X4KQW7N2R3T5V8B6C0D",
    minute: int = 0,
    meter: str = "compute",
    start: str | None = None,
    end: str | None = None,
    quantity: str | None = None,
) -> dict[str, Any]:
    """A well-formed heartbeat. ``minute`` places a one-minute interval at
    10:mm billing one per second; ``start``, ``end``, and ``quantity``
    override the interval, for partial pieces and corrections."""
    dimensions: dict[str, Any] = {
        "deployment": "us-east-1",
        "tenant": "tenant1",
        "project": "billing",
        "meter_type": "second",
        "image": "rhel-9",
        "boot_disk_size_gib": "30",
    }
    if instance_type is not None:
        dimensions["instance_type"] = instance_type
    interval_start = start or f"2026-10-05T10:{minute:02d}:00Z"
    interval_end = end or f"2026-10-05T10:{minute + 1:02d}:00Z"
    interval_quantity = quantity or "60.000000"
    return {
        "specversion": "1.0",
        "type": HEARTBEAT_TYPE,
        "source": "osac-metering",
        "id": event_id,
        "time": interval_start,
        "osacresourceid": "vm-7",
        "osacresourcetype": "compute_instance",
        "osactenant": "tenant1",
        "data": {
            "resource_id": "vm-7",
            "resource_type": "compute_instance",
            "tenant_id": "tenant1",
            "meter": meter,
            "component": "compute",
            "heartbeat_time": interval_end,
            "rules_revision": "0001",
            "usage": {
                "semantics": "interval",
                "unit": "second",
                "precision": "second",
                "from": interval_start,
                "to": interval_end,
                "quantity": interval_quantity,
            },
            "billing_dimensions": dimensions,
        },
    }


@pytest.fixture
def collector() -> MeteringCollector:
    return MeteringCollector(base_url="http://echo-adapter.example.test")


def _serve(collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch, events: list[dict[str, Any]]) -> None:
    """Make the collector's fetch return the given events, in the given order."""

    def fetch(event_type: str, resource_id: str) -> list[dict[str, Any]]:
        return events

    monkeypatch.setattr(collector, "_fetch_events", fetch)


def test_verify_event_accepts_a_well_formed_heartbeat(collector: MeteringCollector) -> None:
    event = _heartbeat()

    collector.verify_event(event, resource_id="vm-7")

    assert collector.get_event(HEARTBEAT_TYPE, "vm-7") is event


def test_verify_event_rejects_a_heartbeat_missing_its_dimension(collector: MeteringCollector) -> None:
    # The running instance type lands 15-20 s after RUNNING, so the first
    # heartbeat of a VM can legitimately lack it; verify_event still enforces
    # the dimension on the event it is given, which is why the tests wait for
    # the heartbeat that carries the dimension first.
    event = _heartbeat(instance_type=None)

    with pytest.raises(AssertionError, match="instance_type"):
        collector.verify_event(event, resource_id="vm-7")


def test_verify_accepts_early_heartbeats_that_precede_the_late_dimension(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    # The running instance type lands 15-20 s after RUNNING, so the first
    # beats of a VM legitimately lack it. verify() runs at teardown over
    # everything captured and must accept them: the dimension is required
    # only from the first heartbeat that carries it.
    events = [
        _heartbeat(instance_type=None, event_id="beat-1", minute=0),
        _heartbeat(instance_type=None, event_id="beat-2", minute=1),
        _heartbeat(event_id="beat-3", minute=2),
        _heartbeat(event_id="beat-4", minute=3),
    ]
    # The adapter's order must not matter: the collector orders by interval.
    _serve(collector, monkeypatch, list(reversed(events)))

    collector.expect(HEARTBEAT_TYPE, "vm-7")
    collector.verify()


def test_verify_rejects_a_heartbeat_that_loses_the_dimension_after_it_landed(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    # From the first heartbeat that carries the late dimension onward it is
    # required: a later heartbeat that drops it fails validation.
    events = [
        _heartbeat(instance_type=None, event_id="beat-1", minute=0),
        _heartbeat(event_id="beat-2", minute=1),
        _heartbeat(instance_type=None, event_id="beat-3", minute=2),
    ]
    _serve(collector, monkeypatch, events)

    collector.expect(HEARTBEAT_TYPE, "vm-7")
    with pytest.raises(AssertionError, match="instance_type"):
        collector.verify()


class _AdvancingClock:
    """A monotonic clock that advances a fixed step per call, so quiet windows pass
    without the test sleeping through them."""

    def __init__(self, *, step_seconds: int) -> None:
        self._step = step_seconds
        self._now = 1000.0

    def __call__(self) -> float:
        self._now += self._step
        return self._now


# The recorded deletion every wait test anchors on: one second past the
# 10:07 round boundary, inside the one-second cross-clock tolerance.
DELETION = "2026-10-05T10:07:41Z"


def _wait_for_end(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch, rounds: list[list[dict[str, Any]]]
) -> datetime:
    """Serve one fetch round per poll against a stepping clock, and wait for
    the billing end anchored on the recorded deletion."""
    calls = list(rounds)

    def fetch(event_type: str, resource_id: str) -> list[dict[str, Any]]:
        return calls.pop(0) if calls else rounds[-1]

    clock = _AdvancingClock(step_seconds=30)
    monkeypatch.setattr(collector, "_fetch_events", fetch)
    monkeypatch.setattr("time.sleep", lambda seconds: None)
    monkeypatch.setattr("time.monotonic", clock)
    return collector.wait_for_billing_end("vm-7", deletion=parse_time(DELETION), quiet=60, timeout=300)


def test_wait_for_billing_end_waits_for_the_final_piece(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The wait returns only once the latest interval reaches the deletion,
    so a final piece that lands rounds after the deletion cannot be missed."""
    final = _heartbeat(event_id="beat-final", minute=7, end="2026-10-05T10:07:42Z", quantity="42.000000")
    rounds = [
        [_heartbeat(minute=minute) for minute in range(5)],
        [_heartbeat(minute=minute) for minute in range(5)],
        # An intermediate round still short of the deletion.
        [_heartbeat(minute=minute) for minute in range(7)],
        [_heartbeat(minute=minute) for minute in range(7)] + [final],
    ]

    assert _wait_for_end(collector, monkeypatch, rounds) == parse_time("2026-10-05T10:07:42Z")


def test_wait_for_billing_end_waits_for_a_stale_overrun_to_be_corrected(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A round that ticks before the fold consumes the deletion sends a full
    interval past it; the wait must hold out for the negative correction
    instead of returning on the stale end."""
    # The stale round billed [10:05, 10:07:50]; the deletion over-billed the
    # last 9 seconds, which the next round reverses.
    stale = _heartbeat(event_id="beat-stale", minute=5, end="2026-10-05T10:07:50Z", quantity="170.000000")
    correction = _heartbeat(
        event_id="beat-correction", start="2026-10-05T10:07:41Z", end="2026-10-05T10:07:50Z", quantity="-9.000000"
    )
    rounds = [
        [_heartbeat(minute=minute) for minute in range(5)],
        [_heartbeat(minute=minute) for minute in range(5)] + [stale],
        [_heartbeat(minute=minute) for minute in range(5)] + [stale, correction],
    ]

    assert _wait_for_end(collector, monkeypatch, rounds) == parse_time("2026-10-05T10:07:41Z")


def test_wait_for_billing_end_waits_for_every_meter(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A bare-metal instance bills under two meters; one meter's final piece
    landing must not end the wait while the other meter's is still pending."""
    allocation_final = _heartbeat(
        event_id="beat-alloc", minute=7, meter="allocation", end="2026-10-05T10:07:42Z", quantity="42.000000"
    )
    consumption_final = _heartbeat(
        event_id="beat-cons", minute=7, meter="consumption", end="2026-10-05T10:07:45Z", quantity="45.000000"
    )
    rounds = [
        [_heartbeat(minute=minute, meter="allocation") for minute in range(5)]
        + [_heartbeat(minute=minute, meter="consumption") for minute in range(5)],
        # The allocation meter converges; consumption still falls short.
        [_heartbeat(minute=minute, meter="allocation") for minute in range(7)]
        + [allocation_final]
        + [_heartbeat(minute=minute, meter="consumption") for minute in range(5)],
        # Consumption converges, to a later end than allocation's.
        [_heartbeat(minute=minute, meter="allocation") for minute in range(7)]
        + [allocation_final]
        + [_heartbeat(minute=minute, meter="consumption") for minute in range(7)]
        + [consumption_final],
    ]

    assert _wait_for_end(collector, monkeypatch, rounds) == parse_time("2026-10-05T10:07:45Z")


def test_wait_for_billing_end_spans_a_quantity_only_correction(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A correction can change billed usage inside the life without moving
    the latest end; the quiet window must restart for it, or the wait can
    return while further billing is still arriving."""
    final = _heartbeat(event_id="beat-final", minute=7, end="2026-10-05T10:07:42Z", quantity="42.000000")
    # A mid-life reversal: 30s inside [10:02, 10:03] taken back without
    # touching the latest end, then a piece that extends past it.
    correction = _heartbeat(event_id="beat-correction", minute=2, quantity="-30.000000")
    late = _heartbeat(
        event_id="beat-late", start="2026-10-05T10:07:42Z", end="2026-10-05T10:07:45Z", quantity="3.000000"
    )
    first_round = [_heartbeat(minute=minute) for minute in range(7)] + [final]
    rounds = [first_round, first_round, [*first_round, correction], [*first_round, correction, late]]

    assert _wait_for_end(collector, monkeypatch, rounds) == parse_time("2026-10-05T10:07:45Z")


def test_wait_for_billing_end_times_out_when_the_deletion_is_never_reached(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Billing that never reaches the deletion fails the wait instead of
    returning a stale quiet-window end."""
    rounds = [[_heartbeat(minute=minute) for minute in range(5)]]

    with pytest.raises(TimeoutError, match="did not reach the deletion"):
        _wait_for_end(collector, monkeypatch, rounds)
