from __future__ import annotations

import json
import logging
import re
import time
import urllib.error
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any
from urllib.parse import urlencode
from urllib.request import Request, urlopen

from tests.e2e.core.runner import poll_until

logger = logging.getLogger(__name__)

HEARTBEAT_TYPE = "osac.resource.heartbeat.v1"

# The resource types the metering engine bills, in the shape heartbeats carry.
HEARTBEAT_RESOURCE_TYPES = (
    "compute_instance",
    "cluster_order",
    "bare_metal_instance",
    "external_ip",
    "nat_gateway",
    "volume",
)

# Every meter sends these dimensions; a meter's own dimensions ride along. Project is an
# identity field every resource carries.
COMMON_DIMENSIONS = ("deployment", "tenant", "meter_type")

# Dimensions that legitimately land after RUNNING: the operator reports the running
# instance type 15-20 s after the state, so a compute heartbeat that predates the fact
# bills on a dimensionless meter and a later heartbeat corrects it onto the typed one.
# Such a dimension is required only from the first heartbeat that carries it:
# heartbeats before that pass validation; heartbeats after it must carry it.
LATE_DIMENSIONS: dict[str, tuple[str, ...]] = {"compute_instance": ("instance_type",)}

_QUANTITY_RE = re.compile(r"^-?\d+\.\d{6}$")


def _parse_time(s: str) -> datetime:
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    return datetime.fromisoformat(s)


def _matches(event: dict[str, Any], expected: ExpectedEvent) -> bool:
    return event.get("type") == expected.event_type and (
        event.get("resource_id") == expected.resource_id
        or event.get("osacresourceid") == expected.resource_id
        or event.get("data", {}).get("resource_id") == expected.resource_id
    )


def _interval_start(event: dict[str, Any]) -> datetime:
    """The start of a heartbeat's usage interval, for ordering a sequence.

    A heartbeat whose interval start is missing or unparsable sorts last; the
    structure validation that follows fails it with the specific assert.
    """
    text = event.get("data", {}).get("usage", {}).get("from") or ""
    try:
        return _parse_time(text)
    except ValueError:
        return datetime.max.replace(tzinfo=UTC)


@dataclass
class ExpectedEvent:
    event_type: str
    resource_id: str
    timeout: int = 60


class MeteringCollector:
    """Collects heartbeat CloudEvents from the metering test adapter HTTP API and verifies
    expectations.

    The test adapter runs inside the Kubernetes cluster, reads every tenant's
    heartbeat topic, and exposes
    ``GET /events?type={event_type}&resource_id={resource_id}&since={RFC3339}``
    which returns a JSON array of CloudEvents.

    Metering sends one event type, ``osac.resource.heartbeat.v1``: a quantity of
    usage over an interval, positive or negative, on the resource's heartbeat
    topic. Lifecycle and correction events do not exist; a state change shows up
    as the next heartbeat's dimensions or as a negative correction.

    Usage in tests::

        metering.expect("osac.resource.heartbeat.v1", resource_id=uuid)
        # ... test actions ...
        # metering.verify() runs automatically on fixture teardown
    """

    def __init__(self, *, base_url: str) -> None:
        self._base_url = base_url.rstrip("/")
        self._expectations: list[ExpectedEvent] = []
        self._matched: set[str] = set()
        self._verified: dict[tuple[str, str], dict[str, Any]] = {}
        self._require_project = True

    def start(self) -> None:
        """Initialize collection without comparing clocks across runner and pod."""

    def stop(self) -> None:
        """No-op -- no background resources to clean up."""

    def without_project(self) -> "MeteringCollector":
        """Stop requiring the project dimension: resources created without a project
        send none, because the engine emits only the facts a resource carries."""
        self._require_project = False
        return self

    def expect(self, event_type: str, resource_id: str, *, timeout: int = 60) -> None:
        if event_type != HEARTBEAT_TYPE:
            raise ValueError(
                f"metering sends only {HEARTBEAT_TYPE}; lifecycle and correction "
                f"events do not exist, got {event_type!r}"
            )
        self._expectations.append(ExpectedEvent(event_type=event_type, resource_id=resource_id, timeout=timeout))

    def verify(self) -> None:
        if not self._expectations:
            if not self._verified:
                logger.warning("verify() called with no expectations — did you forget to call expect()?")
            return
        for exp in self._expectations:
            event = self._poll_for_event(exp)
            self._validate_resource_events(exp)
            self._verified[(exp.event_type, exp.resource_id)] = event
        self._expectations.clear()

    def _validate_resource_events(self, expected: ExpectedEvent) -> None:
        """Validate every captured heartbeat of the resource, in interval order.

        verify() runs at teardown over everything captured, which includes the
        early heartbeats of a resource whose late dimensions have not landed
        yet; a late dimension is required only from the first heartbeat that
        carries it (see LATE_DIMENSIONS).
        """
        events = [ev for ev in self._fetch_events(expected.event_type, expected.resource_id) if _matches(ev, expected)]
        events.sort(key=_interval_start)
        carried: set[str] = set()
        for event in events:
            dimensions = event.get("data", {}).get("billing_dimensions", {})
            carried.update(
                dimension
                for dimension in LATE_DIMENSIONS.get(event.get("osacresourcetype", ""), ())
                if dimensions.get(dimension)
            )
            self._validate_structure(event, expected, late_required=carried, require_project=self._require_project)

    def verify_event(self, event: dict[str, Any], *, resource_id: str) -> None:
        """Validate one event's envelope and dimension set.

        Use where the first heartbeat of a resource legitimately lacks a
        dimension that lands later, such as the running instance type, which
        the operator reports 15-20 s after RUNNING: wait for the event that
        carries the dimension, then verify that one, instead of verify(),
        which polls for and validates the first match. The event given here is
        validated with every late dimension required, so passing it an early
        heartbeat that predates the fact fails.
        """
        expected = ExpectedEvent(event_type=HEARTBEAT_TYPE, resource_id=resource_id)
        late = LATE_DIMENSIONS.get(event.get("osacresourcetype", ""), ())
        self._validate_structure(event, expected, late_required=set(late), require_project=self._require_project)
        if event_id := event.get("id", ""):
            self._matched.add(event_id)
        self._verified[(HEARTBEAT_TYPE, resource_id)] = event

    def get_event(self, event_type: str, resource_id: str, *, timeout: int = 60) -> dict[str, Any]:
        """Return a verified event, or poll for one if not yet verified."""
        key = (event_type, resource_id)
        if key in self._verified:
            return self._verified[key]
        exp = ExpectedEvent(event_type=event_type, resource_id=resource_id, timeout=timeout)
        return self._poll_for_event(exp)

    def assert_no_events(self, event_type: str, resource_id: str, *, since: str, within: int = 60) -> None:
        """Assert that no events of the given type bill past ``since`` for the resource.

        The CloudEvent time is the start of the usage interval, so filtering on it would
        miss a heartbeat whose interval started before ``since`` but continued after it:
        an event bills past the halt exactly when its interval's end does.
        """
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
            and (usage := ev.get("data", {}).get("usage", {}))
            and usage.get("to")
            and _parse_time(usage["to"]) > since_dt
        ]
        assert not matching, (
            f"Expected no {event_type} events for {resource_id} billing past {since}, "
            f"but found {len(matching)}: "
            f"{[{'id': e.get('id'), 'to': e.get('data', {}).get('usage', {}).get('to')} for e in matching]}"
        )

    def get_all_events(self, event_type: str, resource_id: str) -> list[dict[str, Any]]:
        """Return all events matching type and resource_id (for N+1 count assertions)."""
        return self._fetch_events(event_type, resource_id)

    def _fetch_events(self, event_type: str, resource_id: str) -> list[dict[str, Any]]:
        # Resource IDs are unique per test resource. Avoid filtering by a
        # runner-side timestamp because the echo adapter uses pod time.
        params = urlencode({"type": event_type, "resource_id": resource_id})
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
                if _matches(ev, expected):
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
    def _validate_structure(
        event: dict[str, Any], expected: ExpectedEvent, *, late_required: set[str] | None = None,
        require_project: bool = True,
    ) -> None:
        late_required = late_required or set()
        assert event.get("specversion") == "1.0", f"Wrong specversion: {event.get('specversion')}"
        assert event.get("type") == HEARTBEAT_TYPE, f"Wrong type: {event.get('type')}"
        assert event.get("source") == "osac-metering", f"Wrong source: {event.get('source')}"
        assert event.get("id"), "Missing event id"
        assert event.get("time"), "Missing event time"
        assert event.get("osacresourceid") == expected.resource_id, (
            f"Wrong osacresourceid: {event.get('osacresourceid')}"
        )
        assert event.get("osacresourcetype") in HEARTBEAT_RESOURCE_TYPES, (
            f"Wrong osacresourcetype: {event.get('osacresourcetype')}"
        )
        assert event.get("osactenant"), "Missing osactenant"

        data = event.get("data", {})
        assert data.get("resource_id") == expected.resource_id, f"Wrong resource_id in data: {data.get('resource_id')}"
        assert data.get("resource_type") == event.get("osacresourcetype"), (
            f"Mismatched resource types: {data.get('resource_type')!r} != {event.get('osacresourcetype')!r}"
        )
        assert data.get("tenant_id"), "Missing tenant_id in data"
        assert data.get("meter"), "Missing meter in data"
        assert data.get("component"), "Missing component in data"
        assert data.get("heartbeat_time"), "Missing heartbeat_time in data"
        assert data.get("rules_revision"), "Missing rules_revision in data"
        _parse_time(data["heartbeat_time"])

        usage = data.get("usage", {})
        assert usage.get("semantics") == "interval", f"Wrong usage semantics: {usage.get('semantics')}"
        assert usage.get("unit"), "Missing usage unit"
        assert usage.get("precision") == "microsecond", f"Wrong usage precision: {usage.get('precision')}"
        assert usage.get("from") and usage.get("to"), "Missing usage interval"
        from_dt = _parse_time(usage["from"])
        to_dt = _parse_time(usage["to"])
        assert from_dt < to_dt, f"Usage interval is not forward: {usage['from']}..{usage['to']}"
        assert _parse_time(event["time"]) == from_dt, "CloudEvent time must be the interval's start"
        assert _QUANTITY_RE.match(usage.get("quantity", "")), (
            f"Quantity is not exact fixed-point text: {usage.get('quantity')!r}"
        )

        bd = data.get("billing_dimensions", {})
        for dimension in COMMON_DIMENSIONS:
            assert bd.get(dimension), f"Missing {dimension} in billing_dimensions"
        if require_project:
            assert bd.get("project"), "Missing project in billing_dimensions"
        for dimension in LATE_DIMENSIONS.get(event["osacresourcetype"], ()):
            if dimension in late_required:
                assert bd.get(dimension), f"Missing or empty {dimension} in billing_dimensions"
        validator = _DIMENSION_VALIDATORS.get(event["osacresourcetype"])
        if validator is not None:
            validator(bd)


def _validate_compute_dimensions(bd: dict[str, Any]) -> None:
    # instance_type is not checked here: it lands 15-20 s after RUNNING, so the
    # collector enforces it as a late dimension, from the first heartbeat that
    # carries it onward.
    assert bd.get("image"), "Missing or empty image in billing_dimensions"
    assert bd.get("boot_disk_size_gib"), "Missing or empty boot_disk_size_gib in billing_dimensions"


def _validate_cluster_dimensions(bd: dict[str, Any]) -> None:
    assert bd.get("template"), "Missing or empty template in billing_dimensions"
    assert bd.get("version"), "Missing or empty version in billing_dimensions"


def _validate_bare_metal_dimensions(bd: dict[str, Any]) -> None:
    assert bd.get("instance_type"), "Missing or empty instance_type in billing_dimensions"


def _validate_volume_dimensions(bd: dict[str, Any]) -> None:
    assert bd.get("storage_tier"), "Missing or empty storage_tier in billing_dimensions"


def _validate_external_ip_dimensions(bd: dict[str, Any]) -> None:
    assert bd.get("pool"), "Missing or empty pool in billing_dimensions"
    assert bd.get("ip_family") in ("ipv4", "ipv6"), f"Wrong ip_family: {bd.get('ip_family')!r}"
    assert isinstance(bd.get("attached"), bool) or bd.get("attached") in ("true", "false"), (
        f"attached should be boolean, got {bd.get('attached')!r}"
    )
    if bd.get("attached") in (True, "true"):
        assert bd.get("attribution"), "Missing attribution for an attached ExternalIP"


def _validate_nat_gateway_dimensions(bd: dict[str, Any]) -> None:
    assert bd.get("virtual_network"), "Missing or empty virtual_network in billing_dimensions"
    assert bd.get("external_ip"), "Missing or empty external_ip in billing_dimensions"


_DIMENSION_VALIDATORS = {
    "compute_instance": _validate_compute_dimensions,
    "cluster_order": _validate_cluster_dimensions,
    "bare_metal_instance": _validate_bare_metal_dimensions,
    "volume": _validate_volume_dimensions,
    "external_ip": _validate_external_ip_dimensions,
    "nat_gateway": _validate_nat_gateway_dimensions,
}
