"""The poll_until wait span: one span per wait in the ci-observe trace.

The OpenTelemetry SDK tests run only where the SDK is installed (the E2E
image provides it through pytest-opentelemetry); everywhere else they skip
and the no-OpenTelemetry tests below still exercise the inert path.
"""

import pytest

from tests.e2e.core import runner


def test_poll_without_opentelemetry_waits_and_returns(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner, "_otel_trace", None)
    values = iter(["pending", "ready"])

    assert (
        runner.poll_until(
            fn=lambda: next(values), until=lambda value: value == "ready", retries=3, delay=0, description="no otel"
        )
        == "ready"
    )


def test_poll_without_opentelemetry_still_raises_on_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner, "_otel_trace", None)

    with pytest.raises(TimeoutError, match="timed out"):
        runner.poll_until(fn=lambda: "pending", until=lambda _: False, retries=1, delay=0, description="no otel")


def test_poll_without_a_tracer_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    """OpenTelemetry present but no provider configured: no crash, poll works."""
    otel_trace = pytest.importorskip("opentelemetry.trace")
    # Simulates a process that never configured telemetry: the API package
    # then hands out proxy tracers that fall back to a no-op tracer.
    monkeypatch.setattr(otel_trace, "_TRACER_PROVIDER", None)

    assert (
        runner.poll_until(
            fn=lambda: "ready", until=lambda value: value == "ready", retries=2, delay=0, description="no provider"
        )
        == "ready"
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
    )

    spans = wait_spans(exporter, "BareMetalInstance to become Running")
    assert len(spans) == 1
    span = spans[0]
    assert span.kind is SpanKind.INTERNAL
    assert span.attributes["poll.delay"] == 5
    assert span.attributes["poll.retries"] == 3
    assert span.attributes["poll.attempts"] == 2
    assert span.attributes["poll.condition_met"] is True


def test_wait_span_timeout_sets_error_status_and_raises(exporter: object, monkeypatch: pytest.MonkeyPatch) -> None:
    from opentelemetry.trace import StatusCode

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    with pytest.raises(TimeoutError, match="timed out"):
        runner.poll_until(
            fn=lambda: "pending", until=lambda _: False, retries=2, delay=5, description="ClusterOrder to become Ready"
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
            fn=lambda: "ready", until=lambda value: value == "ready", retries=2, delay=0, description="nested wait"
        )

    spans = wait_spans(exporter, "nested wait")
    assert len(spans) == 1
    wait = spans[0]
    assert wait.parent is not None
    assert wait.parent.span_id == call_span.get_span_context().span_id
