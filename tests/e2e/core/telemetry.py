"""E2E telemetry: worker attribution and fixture-reuse evidence, without changing outcomes.

pytest-opentelemetry (E2E image only) already emits one span per run, worker,
test, test phase and fixture setup. This module adds two things the ci-observe
dashboard needs on top of that trace.

`osac.ci.xdist_worker` on every span a worker process starts, read from
`PYTEST_XDIST_WORKER` (for example "gw0"). Exported traces regularly lose
worker spans and parent links (a four-worker run whose trace holds two
"test worker gwN" spans and several orphaned test spans), so the attribute is
the dashboard's worker grouping key and never depends on span parents; the
dashboard labels missing parents explicitly. A span processor attached to the
global tracer provider sets the attribute, so test, fixture, phase and wait
spans all carry it. The processor is attached at session start, not configure
time: pytest-opentelemetry installs the provider during its own
`pytest_configure`, which runs after this suite's conftest configure, so at
configure time the provider is still the API's inert proxy.

One zero-duration `pytest.span_type="fixture_use"` span per test for each
session- or module-scoped fixture it used, carrying `pytest.fixture_name`,
`pytest.fixture_scope`, `pytest.nodeid` and, in a worker, the attribute above.
A shared fixture instance is the pair (worker, fixture name) for session
scope, plus the module for module scope, because every xdist worker builds its
own fixture cache. Counting these spans per instance answers "which tests
reused which shared fixture instance, and how many times". Function- and
class-scoped fixtures emit nothing: one span per test per function-scoped
fixture would dominate the trace without adding reuse evidence.

Fixture setup spans ("<name> setup") are left untouched, and fixture spans
overlap the runner's wall time (a session fixture set up during one test's
setup phase is charged to that test's phase span), so never sum fixture spans
into runner wall time.

Everything here is optional telemetry. Without OpenTelemetry (a plain
checkout) or without a configured tracer provider, every hook no-ops. Test
selection, outcomes, retries, worker counts, cleanup and exit codes never
change; hook bodies are guarded like `failure_report`'s so a telemetry bug
cannot fail a run.
"""

from __future__ import annotations

import functools
import json
import logging
import os
from collections.abc import Callable, Generator, Sequence
from pathlib import Path
from typing import TYPE_CHECKING, Any, Concatenate, ParamSpec, cast

import pluggy
import pytest

try:
    # Only the E2E test image provides OpenTelemetry (pytest-opentelemetry
    # in osac-test-infra's lock); a plain checkout does not, and the suite
    # then runs without spans.
    from opentelemetry import trace as _otel_trace
    from opentelemetry.sdk.trace import SpanProcessor as _SdkSpanProcessor
except ImportError:
    _otel_trace = None
    _SdkSpanProcessor = None

if TYPE_CHECKING:
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.trace import Span

# Subclassing the SDK base supplies the no-op hooks of the installed SDK
# version (recent versions call SpanProcessor._on_ending when a span ends, so
# a hand-rolled processor missing it crashes every run). Without the SDK no
# provider accepts processors, so the stand-in base is definition-only.
_SpanProcessorBase: Any = _SdkSpanProcessor or object

logger = logging.getLogger(__name__)

P = ParamSpec("P")

PLUGIN_NAME = "e2e-telemetry"

XDIST_WORKER_ATTRIBUTE = "osac.ci.xdist_worker"

# One fixture_use span per test per fixture in these scopes. Function- and
# class-scoped fixtures stay out: per-test spans for them would multiply the
# trace size without adding reuse evidence.
SHARED_FIXTURE_SCOPES: Sequence[str] = ("session", "module")

# Providers the worker-attribute processor is attached to, so a repeated
# attach in one process stays single-attached.
_providers_attributed: list[TracerProvider] = []


def _guarded(method: Callable[Concatenate[E2ETelemetry, P], None]) -> Callable[Concatenate[E2ETelemetry, P], None]:
    """Log and swallow anything a hook body raises: telemetry must never change a test's result."""

    @functools.wraps(method)
    def wrapper(self: E2ETelemetry, *args: P.args, **kwargs: P.kwargs) -> None:
        try:
            method(self, *args, **kwargs)
        except Exception:
            logger.warning("E2E telemetry hit an internal error", exc_info=True)

    return wrapper


class _WorkerAttributeSpanProcessor(_SpanProcessorBase):
    """Sets the worker attribute on every span this process starts."""

    def __init__(self, worker_id: str) -> None:
        self._worker_id = worker_id

    def on_start(self, span: Span, parent_context: object = None) -> None:
        span.set_attribute(XDIST_WORKER_ATTRIBUTE, self._worker_id)


class _RecordingSpanProcessor(_SpanProcessorBase):
    """Appends finished spans to a JSONL file.

    A debugging and unit-test aid attached through `record_spans_to`; a normal
    run exports through OTLP instead and never attaches it.
    """

    def __init__(self, path: Path) -> None:
        self._path = path

    def on_end(self, span: Span) -> None:
        context = span.get_span_context()
        record = {
            "name": span.name,
            "trace_id": f"{context.trace_id:032x}",
            "span_id": f"{context.span_id:016x}",
            "parent_span_id": f"{span.parent.span_id:016x}" if span.parent else None,
            "attributes": dict(span.attributes or {}),
        }
        with self._path.open("a") as records:
            records.write(json.dumps(record, sort_keys=True, default=str) + "\n")


def _active_provider() -> TracerProvider | None:
    """The global tracer provider when it accepts span processors, else None.

    None means OpenTelemetry is absent or telemetry was never configured (the
    API hands out an inert proxy provider), and the caller stays inert.
    """
    if _otel_trace is None:
        return None
    provider = _otel_trace.get_tracer_provider()
    if not hasattr(provider, "add_span_processor"):
        return None
    return cast("TracerProvider", provider)


def attach_worker_attribute() -> None:
    """Attribute every span this process starts to its xdist worker.

    No-op outside a worker (`PYTEST_XDIST_WORKER` unset: the controller and
    serial runs) and without a configured provider.
    """
    worker_id = os.environ.get("PYTEST_XDIST_WORKER")
    if worker_id is None:
        return
    provider = _active_provider()
    if provider is None or provider in _providers_attributed:
        return
    provider.add_span_processor(_WorkerAttributeSpanProcessor(worker_id))
    _providers_attributed.append(provider)


def record_spans_to(directory: Path | str) -> None:
    """Record this pytest process's finished spans to JSONL files under ``directory``.

    Debugging and unit-test aid: attach it from a conftest's session start,
    after pytest-opentelemetry configured the provider. One file per process:
    ``spans-gw0.jsonl`` per worker, ``spans-main.jsonl`` for the controller
    and serial runs. Never attached by a normal run.
    """
    provider = _active_provider()
    if provider is None:
        return
    worker = os.environ.get("PYTEST_XDIST_WORKER") or "main"
    path = Path(directory) / f"spans-{worker}.jsonl"
    path.parent.mkdir(parents=True, exist_ok=True)
    provider.add_span_processor(_RecordingSpanProcessor(path))


def record_fixture_use(item: pytest.Item) -> None:
    """Emit one zero-duration fixture_use span per shared fixture ``item`` used.

    The span is a counter, not an interval: the fixture's own setup span
    carries the time. Fixtures requested dynamically inside a test body
    (`request.getfixturevalue`) are not in the item's fixture closure and are
    not counted.
    """
    if _otel_trace is None:
        return
    fixturenames = getattr(item, "fixturenames", None)
    if not fixturenames:
        # Not every item kind has a fixture closure (doctests, for example).
        return
    tracer = _otel_trace.get_tracer(__name__)
    for name in fixturenames:
        fixturedefs = item.session._fixturemanager.getfixturedefs(name, item)
        if not fixturedefs:
            # None (no such fixture) and () (none applicable to this node)
            # both mean there is no definition to read a scope from.
            continue
        scope = fixturedefs[-1].scope
        if scope not in SHARED_FIXTURE_SCOPES:
            continue
        span = tracer.start_span(
            f"{name} use",
            attributes={
                "pytest.span_type": "fixture_use",
                "pytest.fixture_name": name,
                "pytest.fixture_scope": scope,
                "pytest.nodeid": item.nodeid,
            },
        )
        span.end()


class E2ETelemetry:
    """Hooks that attribute spans to workers and count shared-fixture reuse."""

    @pytest.hookimpl(tryfirst=True)
    @_guarded
    def pytest_sessionstart(self, session: pytest.Session) -> None:
        # tryfirst so the attribute also lands on pytest-opentelemetry's
        # worker span ("test worker gwN"), which that plugin starts in its own
        # sessionstart hook right after this one.
        attach_worker_attribute()

    @pytest.hookimpl(hookwrapper=True)
    def pytest_runtest_setup(self, item: pytest.Item) -> Generator[None, pluggy.Result[None], None]:
        outcome = yield
        if outcome.excinfo is not None:
            # A skipped or failed setup never used the item's fixtures.
            return
        self._record_fixture_use(item)

    @_guarded
    def _record_fixture_use(self, item: pytest.Item) -> None:
        record_fixture_use(item)


def register(config: pytest.Config) -> E2ETelemetry:
    """Register the telemetry plugin once; later calls return the registered plugin."""
    existing = config.pluginmanager.get_plugin(PLUGIN_NAME)
    if existing is not None:
        return existing
    plugin = E2ETelemetry()
    config.pluginmanager.register(plugin, PLUGIN_NAME)
    return plugin
