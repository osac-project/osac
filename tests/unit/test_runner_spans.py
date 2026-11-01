"""The poll_until wait span: one span per wait in the ci-observe trace.

The OpenTelemetry SDK tests run only where the SDK is installed (the E2E
image provides it through pytest-opentelemetry); everywhere else they skip
and the no-OpenTelemetry tests below still exercise the inert path.
"""

import subprocess

import pytest

from tests.e2e.core import runner
from tests.e2e.core.wait_kinds import WaitResourceKind


def test_poll_without_opentelemetry_waits_and_returns(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner, "_otel_trace", None)
    values = iter(["pending", "ready"])

    assert (
        runner.poll_until(
            fn=lambda: next(values),
            until=lambda value: value == "ready",
            retries=3,
            delay=0,
            description="no otel",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
        )
        == "ready"
    )


def test_poll_without_opentelemetry_still_raises_on_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner, "_otel_trace", None)

    with pytest.raises(TimeoutError, match="not reached after"):
        runner.poll_until(
            fn=lambda: "pending",
            until=lambda _: False,
            retries=1,
            delay=0,
            description="no otel",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
        )


def test_poll_without_opentelemetry_with_resource_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    """Resource identity is a span attribute only: without telemetry the poll is unchanged."""
    monkeypatch.setattr(runner, "_otel_trace", None)
    values = iter(["pending", "ready"])

    assert (
        runner.poll_until(
            fn=lambda: next(values),
            until=lambda value: value == "ready",
            retries=3,
            delay=0,
            description="no otel",
            resource_kind=WaitResourceKind.SUBNET,
            resource_id="subnet-7",
        )
        == "ready"
    )


def test_poll_without_a_tracer_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    """OpenTelemetry present but no provider configured: no crash, poll works."""
    otel_trace = pytest.importorskip("opentelemetry.trace")
    # Simulates a process that never configured telemetry: the API package
    # then hands out proxy tracers that fall back to a no-op tracer.
    monkeypatch.setattr(otel_trace, "_TRACER_PROVIDER", None)

    assert (
        runner.poll_until(
            fn=lambda: "ready",
            until=lambda value: value == "ready",
            retries=2,
            delay=0,
            description="no provider",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
        )
        == "ready"
    )


def test_poll_accepts_enum_member(monkeypatch: pytest.MonkeyPatch) -> None:
    """The kind must be a WaitResourceKind member."""
    monkeypatch.setattr(runner, "_otel_trace", None)

    assert (
        runner.poll_until(
            fn=lambda: "ready",
            until=lambda value: value == "ready",
            retries=1,
            delay=0,
            description="kind acceptance",
            resource_kind=WaitResourceKind.BARE_METAL_INSTANCE,
            resource_id="bmi-1",
        )
        == "ready"
    )


@pytest.mark.parametrize(
    "kind", ["ComputeInstence", str(WaitResourceKind.BARE_METAL_INSTANCE)], ids=["unknown-string", "equal-value-string"]
)
def test_poll_rejects_non_enum_kind(kind: str, monkeypatch: pytest.MonkeyPatch) -> None:
    """A kind must be an enum member, even when a string has a valid value."""
    monkeypatch.setattr(runner, "_otel_trace", None)

    with pytest.raises(ValueError, match=r"unknown resource_kind.*ComputeInstance"):
        runner.poll_until(
            fn=lambda: "ready",
            until=lambda value: value == "ready",
            retries=1,
            delay=0,
            description="bad kind",
            resource_kind=kind,
            resource_id="ci-1",
        )


@pytest.mark.parametrize("resource_id", ["", "   ", None], ids=["empty", "whitespace", "none"])
def test_poll_rejects_empty_resource_id(resource_id: object, monkeypatch: pytest.MonkeyPatch) -> None:
    """An id that names nothing fails fast instead of emitting an empty attribute."""
    monkeypatch.setattr(runner, "_otel_trace", None)

    with pytest.raises(ValueError, match="non-empty resource_id"):
        runner.poll_until(
            fn=lambda: "ready",
            until=lambda value: value == "ready",
            retries=1,
            delay=0,
            description="bad id",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id=resource_id,  # type: ignore[arg-type]
        )


@pytest.fixture(scope="module")
def provider() -> object:
    """The SDK provider poll_until's spans land in: an existing global one
    (pytest-opentelemetry configures it in the E2E image) or a fresh one
    this fixture registers when no telemetry was configured yet."""
    pytest.importorskip("opentelemetry.sdk", reason="OpenTelemetry ships only in the E2E test image")
    from opentelemetry import trace as otel_trace
    from opentelemetry.sdk.trace import TracerProvider

    current = otel_trace.get_tracer_provider()
    if isinstance(current, TracerProvider):
        return current
    tracer_provider = TracerProvider()
    otel_trace.set_tracer_provider(tracer_provider)
    return tracer_provider


@pytest.fixture
def exporter(provider: object) -> object:
    """A per-test in-memory sink for the spans poll_until creates."""
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

    memory_exporter = InMemorySpanExporter()
    provider.add_span_processor(SimpleSpanProcessor(memory_exporter))  # type: ignore[attr-defined]
    return memory_exporter


def wait_spans(exporter: object, name: str) -> list:
    """The finished spans named ``name``: only ours without a plugin, ours
    among pytest-opentelemetry's own spans with one active."""
    return [span for span in exporter.get_finished_spans() if span.name == name]  # type: ignore[attr-defined]


def test_wait_span_named_after_description_with_outcome(exporter: object, monkeypatch: pytest.MonkeyPatch) -> None:
    from opentelemetry.trace import SpanKind

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)
    values = iter(["pending", "ready"])

    runner.poll_until(
        fn=lambda: next(values),
        until=lambda value: value == "ready",
        retries=3,
        delay=5,
        description="BareMetalInstance to become Running",
        resource_kind=WaitResourceKind.BARE_METAL_INSTANCE,
        resource_id="bmi-42",
    )

    spans = wait_spans(exporter, "BareMetalInstance to become Running")
    assert len(spans) == 1
    span = spans[0]
    assert span.kind is SpanKind.INTERNAL
    assert span.attributes["poll.delay"] == 5
    assert span.attributes["poll.retries"] == 3
    assert span.attributes["poll.attempts"] == 2
    assert span.attributes["poll.condition_met"] is True


def test_wait_span_carries_resource_identity(exporter: object, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)
    values = iter(["pending", "ready"])

    runner.poll_until(
        fn=lambda: next(values),
        until=lambda value: value == "ready",
        retries=3,
        delay=5,
        description="BareMetalInstance bmi-42 to become Ready",
        resource_kind=WaitResourceKind.BARE_METAL_INSTANCE,
        resource_id="bmi-42",
    )

    span = wait_spans(exporter, "BareMetalInstance bmi-42 to become Ready")[0]
    assert span.attributes["osac.wait.resource_kind"] == "BareMetalInstance"
    assert span.attributes["osac.wait.resource_id"] == "bmi-42"


def test_wait_span_timeout_sets_error_status_and_raises(exporter: object, monkeypatch: pytest.MonkeyPatch) -> None:
    from opentelemetry.trace import StatusCode

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    with pytest.raises(TimeoutError, match="not reached after"):
        runner.poll_until(
            fn=lambda: "pending",
            until=lambda _: False,
            retries=2,
            delay=5,
            description="ClusterOrder to become Ready",
            resource_kind=WaitResourceKind.CLUSTER_ORDER,
            resource_id="order-7",
        )

    spans = wait_spans(exporter, "ClusterOrder to become Ready")
    assert len(spans) == 1
    span = spans[0]
    assert span.attributes["poll.attempts"] == 2
    assert span.attributes["poll.condition_met"] is False
    assert span.status.status_code is StatusCode.ERROR


def test_wait_span_nests_under_the_current_span(exporter: object, monkeypatch: pytest.MonkeyPatch) -> None:
    otel_trace = pytest.importorskip("opentelemetry.trace")
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    with otel_trace.get_tracer("test").start_as_current_span("test call") as call_span:
        runner.poll_until(
            fn=lambda: "ready",
            until=lambda value: value == "ready",
            retries=2,
            delay=0,
            description="nested wait",
            resource_kind=WaitResourceKind.SUBNET,
            resource_id="subnet-7",
        )

    spans = wait_spans(exporter, "nested wait")
    assert len(spans) == 1
    wait = spans[0]
    assert wait.parent is not None
    assert wait.parent.span_id == call_span.get_span_context().span_id


def test_wait_span_records_no_exception_and_no_command_on_escaping_error(
    exporter: object, monkeypatch: pytest.MonkeyPatch
) -> None:
    """An escaping CalledProcessError must not leak its command into the trace.

    The SDK records escaping exceptions as span events by default; a
    CalledProcessError's message carries the command and output, which may
    contain secrets. poll_until suppresses them in its own TimeoutError for
    the same reason, so the wait span must record neither the event nor the
    text -- only a generic ERROR status.
    """
    from opentelemetry.trace import StatusCode

    def fail() -> str:
        raise subprocess.CalledProcessError(
            returncode=1, cmd=["grpcurl", "-H", "authorization: Bearer SECRET-TOKEN", "api/Method"]
        )

    with pytest.raises(subprocess.CalledProcessError):
        runner.poll_until(
            fn=fail,
            until=lambda _: True,
            retries=2,
            delay=0,
            description="flaky grpcurl call",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
        )

    spans = wait_spans(exporter, "flaky grpcurl call")
    assert len(spans) == 1
    span = spans[0]
    assert span.status.status_code is StatusCode.ERROR
    assert span.status.description == "poll failed"
    assert span.events == ()
    assert "SECRET-TOKEN" not in str(span.attributes)
    assert "grpcurl" not in str(span.attributes)
