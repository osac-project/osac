from __future__ import annotations

import itertools
import json
import logging
import re
import time
import urllib.error
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from decimal import Decimal
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

# Every meter sends these dimensions; a meter's own dimensions ride along.
# Project is an identity field a resource carries only when it was created
# inside one: expect_no_project() asserts that absence for a project-less
# resource instead of skipping the check.
COMMON_DIMENSIONS = ("deployment", "tenant", "meter_type")

# Dimensions that legitimately land after RUNNING: the operator reports the running
# instance type 15-20 s after the state, so a compute heartbeat that predates the fact
# bills on a dimensionless meter and a later heartbeat corrects it onto the typed one.
# Such a dimension is required only from the first heartbeat that carries it:
# heartbeats before that pass validation; heartbeats after it must carry it.
LATE_DIMENSIONS: dict[str, tuple[str, ...]] = {"compute_instance": ("instance_type",)}

_QUANTITY_RE = re.compile(r"^-?\d+\.\d{6}$")

# Net usage past a moment is a proportional overlap, and the division can
# leave a rounding tail smaller than this without being real billing.
_NET_USAGE_TOLERANCE = Decimal("0.000001")


def parse_time(s: str) -> datetime:
    """Parse an RFC3339 timestamp, accepting the ``Z`` suffix event payloads use."""
    if s.endswith("Z"):
        s = s[:-1] + "+00:00"
    return datetime.fromisoformat(s)


# The CR stamps the API server writes and the meter's interval boundaries both
# carry whole seconds, but they come from different clocks: comparisons between
# the two allow one second of cross-clock truncation.
CR_STAMP_TOLERANCE = timedelta(seconds=1)


def usage_interval(event: dict[str, Any]) -> tuple[datetime, datetime, Decimal]:
    """A heartbeat's usage interval and its quantity."""
    usage = event["data"]["usage"]
    return parse_time(usage["from"]), parse_time(usage["to"]), Decimal(usage["quantity"])


def interval_seconds(start: datetime, end: datetime) -> Decimal:
    """The exact interval length in seconds, without a float round-trip.

    Partial intervals carry quantities like ``37.000000``; ``total_seconds()``
    goes via a binary float and can differ from the exact decimal.
    """
    delta = end - start
    micros = (delta.days * 86400 + delta.seconds) * 1_000_000 + delta.microseconds
    return Decimal(micros) / Decimal(1_000_000)


def net_intervals(events: list[dict[str, Any]]) -> list[tuple[datetime, datetime, Decimal]]:
    """Collapse signed heartbeats into the intervals the meter actually bills.

    Each heartbeat bills its quantity over its usage interval and a negative
    correction cancels the overlap, so a canceled tail disappears from the net
    instead of showing up as a gap or an overlap between raw intervals.
    Return the contiguous net-billed intervals with their net quantity.
    """
    rates = [
        (start, end, quantity / interval_seconds(start, end))
        for start, end, quantity in (usage_interval(event) for event in events)
    ]
    boundaries = sorted({moment for start, end, _ in rates for moment in (start, end)})
    intervals: list[tuple[datetime, datetime, Decimal]] = []
    for start, end in itertools.pairwise(boundaries):
        rate = sum(r for r_start, r_end, r in rates if r_start <= start and r_end >= end)
        if rate <= 0:
            continue
        quantity = rate * interval_seconds(start, end)
        if intervals and intervals[-1][1] == start:
            previous_start, _, previous_quantity = intervals[-1]
            intervals[-1] = (previous_start, end, previous_quantity + quantity)
        else:
            intervals.append((start, end, quantity))
    return intervals


def assert_exact_contiguous_life(
    events: list[dict[str, Any]], *, multiplier: Decimal, deletion: datetime, label: str
) -> None:
    """One resource's billed life is exact, contiguous, and reaches the deletion.

    Every net interval bills exactly ``multiplier`` per second of its length
    (for a volume the multiplier is its size in GiB, for allocation and
    consumption meters it is one), consecutive intervals touch with no billed
    gap, and the last interval reaches the recorded deletion, so a dropped
    final partial interval or a stale-phase overrun cannot pass.
    """
    net = net_intervals(events)
    assert net, f"no {label} to assert the billed life on"
    for start, end, quantity in net:
        assert quantity == multiplier * interval_seconds(start, end), (
            f"net interval {start}..{end} of {label} bills {quantity}, not {multiplier} per second of its exact length"
        )
    for (_, previous_end, _), (start, _, _) in itertools.pairwise(net):
        assert start == previous_end, f"billed intervals of {label} are not contiguous: {previous_end} then {start}"
    assert net[-1][1] >= deletion - CR_STAMP_TOLERANCE, (
        f"the final interval of {label} ends at {net[-1][1]}, before the recorded "
        f"deletion at {deletion}: the partial final interval was dropped"
    )


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
        return parse_time(text)
    except ValueError:
        return datetime.max.replace(tzinfo=UTC)


def _dimension_key(event: dict[str, Any]) -> tuple[tuple[str, str], ...]:
    """The dimension set a heartbeat bills on, as a comparable key.

    A negative correction reverses the same set it cancels, so the set groups
    a heartbeat with its corrections.
    """
    dimensions = event.get("data", {}).get("billing_dimensions", {})
    return tuple(sorted((name, str(value)) for name, value in dimensions.items()))


def _deduplicate_by_id(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Drop repeated CloudEvent IDs, keeping the first copy.

    The relay resends a heartbeat with the same ID after a crash, by design:
    delivery is at-least-once and the provider drops repeats. The echo store
    keeps every copy it consumed, so counting both would report correct
    behavior as over-billing. One copy per ID is what the provider bills.
    """
    seen: set[str] = set()
    unique: list[dict[str, Any]] = []
    for event in events:
        event_id = event.get("id", "")
        if event_id in seen:
            continue
        seen.add(event_id)
        unique.append(event)
    return unique


def _net_usage_by_dimension(events: list[dict[str, Any]]) -> dict[tuple[tuple[str, str], ...], Decimal]:
    """Net signed usage per dimension set: each meter's quantities summed.

    A meter whose net is zero is reversed history: it was billed and taken
    back, as when a dimensionless early heartbeat is reversed onto the typed
    meter that replaces it. Events whose quantity does not parse are skipped;
    structure validation reports them with its specific assert.
    """
    nets: dict[tuple[tuple[str, str], ...], Decimal] = {}
    for event in events:
        try:
            quantity = Decimal(event["data"]["usage"]["quantity"])
        except (KeyError, ValueError, ArithmeticError):
            continue
        key = _dimension_key(event)
        nets[key] = nets.get(key, Decimal(0)) + quantity
    return nets


def net_usage_past_by_dimension(
    events: list[dict[str, Any]], *, past: datetime
) -> dict[tuple[tuple[str, str], ...], Decimal]:
    """Net billed usage past a moment, per dimension set.

    Each heartbeat bills its quantity over its usage interval, and a negative
    correction cancels the part of an earlier heartbeat past the moment, so
    the net collapses corrections and reversals: a full interval that
    overruns a stop followed by its negative correction nets to zero.
    """
    nets: dict[tuple[tuple[str, str], ...], Decimal] = {}
    for event in events:
        try:
            start = parse_time(event["data"]["usage"]["from"])
            end = parse_time(event["data"]["usage"]["to"])
            quantity = Decimal(event["data"]["usage"]["quantity"])
        except (KeyError, ValueError, ArithmeticError):
            continue
        if end <= past:
            continue
        # The quantity accrues over the interval, so the part past the moment
        # counts in the interval's proportion.
        overlap = interval_seconds(max(start, past), end)
        length = interval_seconds(start, end)
        key = _dimension_key(event)
        nets[key] = nets.get(key, Decimal(0)) + quantity * overlap / length
    return nets


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
        self._expect_no_project = False
        self._expected_tenant: str | None = None

    def start(self) -> None:
        """Initialize collection without comparing clocks across runner and pod."""

    def stop(self) -> None:
        """No-op -- no background resources to clean up."""

    def expect_no_project(self) -> MeteringCollector:
        """Require the project dimension to be absent.

        A resource created without a project sends none, because the engine
        emits only the facts a resource carries: a heartbeat that names one
        anyway is misattributed, so validation fails instead of skipping the
        project check.
        """
        self._expect_no_project = True
        return self

    def with_tenant(self, tenant: str) -> MeteringCollector:
        """Require every heartbeat to name this tenant.

        The echo adapter aggregates every tenant's topics, so presence alone
        cannot catch an event published to the wrong tenant's topic: only the
        value can.
        """
        self._expected_tenant = tenant
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
        # Reversed history: a meter whose quantities net to zero was billed and
        # taken back, so no late dimension is demanded of it. Tied interval
        # starts order events by fetch order, and a typed replacement can be
        # returned before the untyped history it reverses.
        reversed_meters = {key for key, net in _net_usage_by_dimension(events).items() if net == 0}
        carried: set[str] = set()
        for event in events:
            dimensions = event.get("data", {}).get("billing_dimensions", {})
            carried.update(
                dimension
                for dimension in LATE_DIMENSIONS.get(event.get("osacresourcetype", ""), ())
                if dimensions.get(dimension)
            )
            late_required = carried
            if _dimension_key(event) in reversed_meters:
                late_required = set()
            self._validate_structure(
                event,
                expected,
                late_required=late_required,
                expect_no_project=self._expect_no_project,
                expected_tenant=self._expected_tenant,
            )

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
        self._validate_structure(
            event,
            expected,
            late_required=set(late),
            expect_no_project=self._expect_no_project,
            expected_tenant=self._expected_tenant,
        )
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
        """Assert that no net usage of the given type bills past ``since`` for the resource.

        A round that runs before a halt is folded still sends a full interval
        past the halt, and the negative correction that follows cancels it, so
        the check is on net usage per dimension set: the resource bills past
        the halt exactly when a meter's net usage past it is not zero.

        The window is polled, not slept through: a late round whose correction
        lands after one final fetch would otherwise pass, and a round that
        overruns the window would be missed entirely.
        """
        since_dt = parse_time(since)
        expected = ExpectedEvent(event_type=event_type, resource_id=resource_id)
        deadline = time.monotonic() + within
        while True:
            events = self._fetch_events(event_type, resource_id)
            matching = [ev for ev in events if _matches(ev, expected)]
            nets = net_usage_past_by_dimension(matching, past=since_dt)
            if any(abs(net) > _NET_USAGE_TOLERANCE for net in nets.values()):
                raise AssertionError(
                    f"Expected no {event_type} events for {resource_id} billing past {since}, "
                    f"but net usage past it is {nets}"
                )
            if time.monotonic() >= deadline:
                return
            time.sleep(min(5, max(1, deadline - time.monotonic())))

    def get_all_events(self, event_type: str, resource_id: str) -> list[dict[str, Any]]:
        """Return all events matching type and resource_id (for N+1 count assertions)."""
        return self._fetch_events(event_type, resource_id)

    def wait_for_billing_end(self, resource_id: str, *, quiet: int = 90, timeout: int = 300) -> datetime:
        """The end of the resource's billed life: its latest interval end, read only
        once the heartbeat stream has gone quiet for longer than one round.

        The final partial piece lands in the first round after the fold consumes the
        deletion, and the deletion boundary (the archive time) can sit a whole
        deprovisioning past the CR deletion stamp the earlier rounds already reached.
        Reading the end before the stream goes quiet anchors on an intermediate
        round, and the final piece then reads as billing past the anchor. A round
        emits nothing for a resource whose run ended, so a quiet window longer than
        the round interval means the meter stopped and the latest end is final.
        """
        deadline = time.monotonic() + timeout
        latest_end: datetime | None = None
        unchanged_since: float | None = None
        while True:
            end: datetime | None = None
            for event in self.get_all_events(HEARTBEAT_TYPE, resource_id):
                to = parse_time(event["data"]["usage"]["to"])
                if end is None or to > end:
                    end = to
            now = time.monotonic()
            if end != latest_end:
                latest_end, unchanged_since = end, now
            if latest_end is not None and unchanged_since is not None and now - unchanged_since >= quiet:
                return latest_end
            if now >= deadline:
                raise TimeoutError(
                    f"billing of {resource_id} did not go quiet within {timeout}s: last interval end {latest_end}"
                )
            time.sleep(10)

    def heartbeat_with_dimensions(
        self,
        resource_id: str,
        wanted: dict[str, str],
        *,
        after: datetime | None = None,
        timeout: int = 300,
        delay: int = 10,
    ) -> dict[str, Any]:
        """Return the first heartbeat of the resource whose dimensions match, waiting for one.

        ``wanted`` maps billing-dimension names to the values the heartbeat must
        carry. ``after`` bounds the search: only heartbeats whose usage interval
        starts at or after that moment match, so a check that follows an event
        cannot be satisfied by a heartbeat from before it.
        """

        def find() -> dict[str, Any] | None:
            for event in self.get_all_events(HEARTBEAT_TYPE, resource_id):
                dimensions = event.get("data", {}).get("billing_dimensions", {})
                if not all(dimensions.get(key) == value for key, value in wanted.items()):
                    continue
                if after is not None and parse_time(event["data"]["usage"]["from"]) < after:
                    continue
                return event
            return None

        return poll_until(
            fn=find,
            until=lambda event: event is not None,
            retries=timeout // delay,
            delay=delay,
            description=f"heartbeat of {resource_id} with dimensions {wanted}",
        )

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
                    events = json.loads(resp.read().decode("utf-8"))
                    return _deduplicate_by_id(events)
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
        event: dict[str, Any],
        expected: ExpectedEvent,
        *,
        late_required: set[str] | None = None,
        expect_no_project: bool = False,
        expected_tenant: str | None = None,
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
        if expected_tenant is not None:
            assert data.get("tenant_id") == expected_tenant, (
                f"Heartbeat names tenant {data.get('tenant_id')!r}, want {expected_tenant!r}: "
                "the event may be published to the wrong tenant's topic"
            )
        assert data.get("meter"), "Missing meter in data"
        assert data.get("component"), "Missing component in data"
        assert data.get("heartbeat_time"), "Missing heartbeat_time in data"
        assert data.get("rules_revision"), "Missing rules_revision in data"
        parse_time(data["heartbeat_time"])

        usage = data.get("usage", {})
        assert usage.get("semantics") == "interval", f"Wrong usage semantics: {usage.get('semantics')}"
        assert usage.get("unit"), "Missing usage unit"
        assert usage.get("precision") == "second", f"Wrong usage precision: {usage.get('precision')}"
        assert usage.get("from") and usage.get("to"), "Missing usage interval"
        from_dt = parse_time(usage["from"])
        to_dt = parse_time(usage["to"])
        assert from_dt < to_dt, f"Usage interval is not forward: {usage['from']}..{usage['to']}"
        assert parse_time(event["time"]) == from_dt, "CloudEvent time must be the interval's start"
        assert _QUANTITY_RE.match(usage.get("quantity", "")), (
            f"Quantity is not exact fixed-point text: {usage.get('quantity')!r}"
        )

        bd = data.get("billing_dimensions", {})
        for dimension in COMMON_DIMENSIONS:
            assert bd.get(dimension), f"Missing {dimension} in billing_dimensions"
        if expect_no_project:
            assert not bd.get("project"), f"Unexpected project in billing_dimensions: {bd.get('project')!r}"
        else:
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
