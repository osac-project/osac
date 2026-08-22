from __future__ import annotations

import json
import logging
import time
import urllib.error
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any
from urllib.parse import urlencode
from urllib.request import Request, urlopen

from tests.core.runner import poll_until

logger = logging.getLogger(__name__)


def _parse_time(s: str) -> datetime:
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    return datetime.fromisoformat(s)


@dataclass
class ExpectedEvent:
    event_type: str
    resource_id: str
    timeout: int = 60


class MeteringCollector:
    """Collects CloudEvents from the metering test adapter HTTP API and verifies expectations.

    The test adapter runs inside the Kubernetes cluster and exposes
    ``GET /events?type={event_type}&resource_id={resource_id}&since={RFC3339}``
    which returns a JSON array of CloudEvents.

    Usage in tests::

        metering.expect("osac.resource.created.v1", resource_id=uuid)
        # ... test actions ...
        # metering.verify() runs automatically on fixture teardown
    """

    def __init__(self, *, base_url: str) -> None:
        self._base_url = base_url.rstrip("/")
        self._expectations: list[ExpectedEvent] = []
        self._matched: set[str] = set()
        self._verified: dict[tuple[str, str], dict[str, Any]] = {}
        self._start_time: str = ""

    def start(self) -> None:
        """Record the start time so event queries only return events after this point."""
        self._start_time = datetime.now(UTC).isoformat()

    def stop(self) -> None:
        """No-op -- no background resources to clean up."""

    def expect(self, event_type: str, resource_id: str, *, timeout: int = 60) -> None:
        self._expectations.append(ExpectedEvent(event_type=event_type, resource_id=resource_id, timeout=timeout))

    def verify(self) -> None:
        if not self._expectations:
            if not self._verified:
                logger.warning("verify() called with no expectations — did you forget to call expect()?")
            return
        for exp in self._expectations:
            event = self._poll_for_event(exp)
            self._validate_structure(event, exp)
            self._verified[(exp.event_type, exp.resource_id)] = event
        self._expectations.clear()

    def get_event(self, event_type: str, resource_id: str, *, timeout: int = 60) -> dict[str, Any]:
        """Return a verified event, or poll for one if not yet verified."""
        key = (event_type, resource_id)
        if key in self._verified:
            return self._verified[key]
        exp = ExpectedEvent(event_type=event_type, resource_id=resource_id, timeout=timeout)
        return self._poll_for_event(exp)

    def assert_no_events(self, event_type: str, resource_id: str, *, since: str, within: int = 60) -> None:
        """Assert that no events of the given type appear for the resource within a time window."""
        time.sleep(within)
        events = self._fetch_events(event_type, resource_id)
        since_dt = datetime.fromisoformat(since)
        matching = [
            ev
            for ev in events
            if ev.get("type") == event_type
            and (
                ev.get("resource_id") == resource_id
                or ev.get("osacresourceid") == resource_id
                or ev.get("data", {}).get("resource_id") == resource_id
            )
            and _parse_time(ev.get("time", "")) >= since_dt
        ]
        assert not matching, (
            f"Expected no {event_type} events for {resource_id} after {since}, "
            f"but found {len(matching)}: {[{'id': e.get('id'), 'time': e.get('time')} for e in matching]}"
        )

    def get_all_events(self, event_type: str, resource_id: str) -> list[dict[str, Any]]:
        """Return all events matching type and resource_id (for N+1 count assertions)."""
        return self._fetch_events(event_type, resource_id)

    def _fetch_events(self, event_type: str, resource_id: str) -> list[dict[str, Any]]:
        params = urlencode({"type": event_type, "resource_id": resource_id, "since": self._start_time})
        url = f"{self._base_url}/events?{params}"
        last_exc: OSError | None = None
        for attempt in range(3):
            try:
                req = Request(url)
                with urlopen(req, timeout=10) as resp:
                    return json.loads(resp.read().decode("utf-8"))
            except (urllib.error.URLError, OSError) as exc:
                last_exc = exc
                logger.warning("Transient HTTP error fetching events (attempt %d/3): %s", attempt + 1, exc)
                time.sleep(2)
        raise last_exc  # type: ignore[misc]

    def _poll_for_event(self, expected: ExpectedEvent) -> dict[str, Any]:
        result: list[dict[str, Any]] = []

        def find() -> bool:
            try:
                events = self._fetch_events(expected.event_type, expected.resource_id)
            except (urllib.error.URLError, json.JSONDecodeError, OSError) as exc:
                logger.warning("Transient error fetching metering events, will retry: %s", exc)
                return False
            for ev in events:
                ev_id = ev.get("id", "")
                if ev_id and ev_id in self._matched:
                    continue
                if ev.get("type") == expected.event_type and (
                    ev.get("resource_id") == expected.resource_id
                    or ev.get("osacresourceid") == expected.resource_id
                    or ev.get("data", {}).get("resource_id") == expected.resource_id
                ):
                    if ev_id:
                        self._matched.add(ev_id)
                    result.append(ev)
                    return True
            return False

        poll_until(
            fn=find,
            until=lambda found: found is True,
            retries=expected.timeout // 2,
            delay=2,
            description=f"metering event type={expected.event_type} resource_id={expected.resource_id}",
        )
        return result[0]

    @staticmethod
    def _validate_structure(event: dict[str, Any], expected: ExpectedEvent) -> None:
        assert event.get("specversion") == "1.0", f"Wrong specversion: {event.get('specversion')}"
        assert event.get("source") in ("osac-metering", "osac-metering/reconciler"), (
            f"Wrong source: {event.get('source')}"
        )
        assert event.get("id"), "Missing event id"
        assert event.get("time"), "Missing event time"
        assert event.get("osacresourceid") == expected.resource_id, (
            f"Wrong osacresourceid: {event.get('osacresourceid')}"
        )
        assert event.get("osacresourcetype") in ("compute_instance", "cluster_order"), (
            f"Wrong osacresourcetype: {event.get('osacresourcetype')}"
        )
        assert event.get("osactenant"), "Missing osactenant"

        data = event.get("data", {})
        assert data.get("resource_id") == expected.resource_id, f"Wrong resource_id in data: {data.get('resource_id')}"
        assert data.get("resource_type"), "Missing resource_type in data"
        assert data.get("tenant_id"), "Missing tenant_id in data"
        assert data.get("schema_version") == "v1", f"Wrong schema_version: {data.get('schema_version')}"
        assert "project_id" in data, "Missing project_id in data"
        assert "billing_dimensions" in data, "Missing billing_dimensions in data"

        if expected.event_type != "osac.resource.heartbeat.v1":
            assert data.get("transition_time"), "Missing transition_time in data"
            try:
                datetime.fromisoformat(data["transition_time"])
            except (ValueError, TypeError) as exc:
                raise AssertionError(f"Invalid RFC3339 transition_time: {data.get('transition_time')}") from exc

        transition_types = {"osac.resource.started.v1", "osac.resource.suspended.v1", "osac.resource.resumed.v1"}
        if expected.event_type in transition_types:
            assert "previous_state" in data, f"Missing previous_state in {expected.event_type}"
            assert "duration_seconds" in data, f"Missing duration_seconds in {expected.event_type}"

        if expected.event_type == "osac.resource.suspended.v1":
            valid = ("RUNNING", "STOPPING", "STARTING", "PROGRESSING", "READY")
            assert data.get("previous_state") in valid, (
                f"suspended.v1 previous_state should be one of {valid}, got {data.get('previous_state')!r}"
            )

        if expected.event_type == "osac.resource.resumed.v1":
            valid = ("STOPPED", "PAUSED", "FAILED", "DELETE_FAILED")
            assert data.get("previous_state") in valid, (
                f"resumed.v1 previous_state should be one of {valid}, got {data.get('previous_state')!r}"
            )

        resource_type = event.get("osacresourcetype")
        if resource_type == "compute_instance":
            _validate_vmaas_billing(event)
        elif resource_type == "cluster_order":
            _validate_caas_billing(event, expected.event_type)


def _validate_vmaas_billing(event: dict[str, Any]) -> None:
    bd = event.get("data", {}).get("billing_dimensions", {})
    assert bd.get("instance_type"), "Missing or empty instance_type in billing_dimensions"
    assert bd.get("image_ref"), "Missing or empty image_ref in billing_dimensions"
    assert "boot_disk_size_gib" in bd, "Missing boot_disk_size_gib in billing_dimensions"
    assert isinstance(bd["boot_disk_size_gib"], (int, float)), (
        f"boot_disk_size_gib should be numeric, got {type(bd['boot_disk_size_gib']).__name__}"
    )


def _validate_caas_billing(event: dict[str, Any], event_type: str) -> None:
    bd = event.get("data", {}).get("billing_dimensions", {})
    assert bd.get("cluster_template"), "Missing cluster_template in billing_dimensions"
    # created.v1 and deleted.v1 use topLevelDims (cluster_template +
    # release_image only, no per-component decomposition)
    if event_type in ("osac.resource.created.v1", "osac.resource.deleted.v1"):
        assert bd.get("release_image"), "Missing release_image in billing_dimensions"
        return
    assert bd.get("component") in ("control_plane", "worker"), (
        f"component should be control_plane or worker, got {bd.get('component')!r}"
    )
    assert bd.get("node_set"), "Missing node_set in billing_dimensions"
    assert bd.get("host_type"), "Missing host_type in billing_dimensions"
    assert isinstance(bd.get("node_count"), (int, float)) and bd["node_count"] > 0, (
        f"node_count should be positive number, got {bd.get('node_count')!r}"
    )
