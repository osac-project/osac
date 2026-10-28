"""MeteringCollector event verification without a cluster."""

from __future__ import annotations

from typing import Any

import pytest

from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, parse_time


def _heartbeat(
    *, instance_type: str | None = "m-small", event_id: str = "01JM8Z9X4KQW7N2R3T5V8B6C0D", minute: int = 0
) -> dict[str, Any]:
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
    start = f"2026-10-05T10:{minute:02d}:00Z"
    end = f"2026-10-05T10:{minute + 1:02d}:00Z"
    return {
        "specversion": "1.0",
        "type": HEARTBEAT_TYPE,
        "source": "osac-metering",
        "id": event_id,
        "time": start,
        "osacresourceid": "vm-7",
        "osacresourcetype": "compute_instance",
        "osactenant": "tenant1",
        "data": {
            "resource_id": "vm-7",
            "resource_type": "compute_instance",
            "tenant_id": "tenant1",
            "meter": "compute",
            "component": "compute",
            "heartbeat_time": end,
            "rules_revision": "0001",
            "usage": {
                "semantics": "interval",
                "unit": "second",
                "precision": "second",
                "from": start,
                "to": end,
                "quantity": "60.000000",
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


def test_wait_for_billing_end_returns_the_latest_end_once_quiet(
    collector: MeteringCollector, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The billing end is the latest interval end, read only once no new heartbeat
    arrives for the quiet window: a late final piece moves the end instead of
    reading as billing past an intermediate round's end."""
    rounds = [
        [_heartbeat(minute=minute) for minute in range(5)],
        [_heartbeat(minute=minute) for minute in range(5)],
        [_heartbeat(minute=minute) for minute in range(7)],
        [_heartbeat(minute=minute) for minute in range(7)],
    ]
    calls = list(rounds)

    def fetch(event_type: str, resource_id: str) -> list[dict[str, Any]]:
        return calls.pop(0) if calls else rounds[-1]

    clock = _AdvancingClock(step_seconds=30)
    monkeypatch.setattr(collector, "_fetch_events", fetch)
    monkeypatch.setattr("time.sleep", lambda seconds: None)
    monkeypatch.setattr("time.monotonic", clock)

    end = collector.wait_for_billing_end("vm-7", quiet=90, timeout=300)
    assert end == parse_time("2026-10-05T10:07:00Z")


class _AdvancingClock:
    """A monotonic clock that advances a fixed step per call, so quiet windows pass
    without the test sleeping through them."""

    def __init__(self, *, step_seconds: int) -> None:
        self._step = step_seconds
        self._now = 1000.0

    def __call__(self) -> float:
        self._now += self._step
        return self._now
