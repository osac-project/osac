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

from datetime import UTC, datetime, timedelta
from decimal import Decimal
from types import SimpleNamespace
from typing import Any

import pytest

from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.vmaas.regression import test_compute_instance_creation as creation
from tests.e2e.vmaas.sanity import test_metering_billing as billing

START = datetime(2026, 10, 5, 10, tzinfo=UTC)


def _heartbeat(
    event_id: str,
    start: datetime,
    end: datetime,
    quantity: str,
    *,
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
            "meter": "compute",
            "component": "compute",
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

    collector.expect(HEARTBEAT_TYPE, "vm-7")
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
    monkeypatch.setattr(billing.time, "sleep", lambda *_args: None)
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
                    "stateTransitionTime": (START + timedelta(seconds=45)).isoformat(),
                }
            }
        },
    )

    billing.test_metering_stop_ends_billing(
        cli=SimpleNamespace(
            default_instance_type="m-small",
            create_compute_instance=lambda **_kwargs: "vm-7",
            delete_compute_instance=lambda **_kwargs: None,
        ),
        grpc=grpc,
        # The journey's cleanup reads the CR's deletion timestamp before the
        # patched waits remove it.
        k8s_hub_client=SimpleNamespace(
            get_compute_instance_vm_namespace=lambda **_kwargs: "vm-ns",
            get_jsonpath=lambda **_kwargs: "2026-10-05T10:03:00Z",
        ),
        k8s_virt_client=SimpleNamespace(get_vm_printable_status=lambda **_kwargs: "Stopped"),
        vm_template="vm-template",
        metering_network_attachment={},
        metering=collector,
        metering_project="billing",
    )


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
