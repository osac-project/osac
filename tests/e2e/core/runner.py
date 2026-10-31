from __future__ import annotations

import logging
import os
import subprocess
import time
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from typing import TYPE_CHECKING, TypeVar

try:
    # Only the E2E test image provides OpenTelemetry (pytest-opentelemetry
    # in osac-test-infra's lock); a plain checkout does not, and poll_until
    # then waits without spans.
    from opentelemetry import trace as _otel_trace
except ImportError:
    _otel_trace = None

if TYPE_CHECKING:
    from opentelemetry.trace import Span

T = TypeVar("T")

logger = logging.getLogger(__name__)

# How often to log a "still waiting" progress line during a poll_until call,
# regardless of its own retry/delay settings -- long polls (e.g. waiting up
# to ~60 minutes for a bare-metal cluster to become Ready) would otherwise
# print nothing until they finish or time out.
_PROGRESS_LOG_INTERVAL_S = 30


def run(*args: str, timeout: int = 300) -> str:
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=True)
    return result.stdout.strip()


def run_unchecked(*args: str, timeout: int = 300, include_stderr: bool = False) -> tuple[str, int]:
    """Run a command without raising on failure.

    By default, a successful command (rc 0) returns only stdout so callers
    that parse structured output (e.g. JSON from ``kubectl -o json``) are
    not broken by warnings or transient diagnostics written to stderr.

    Pass ``include_stderr=True`` when the caller needs to inspect stderr
    content even on success -- for example to assert that a deprecation
    warning was emitted.

    On failure (rc != 0), stdout and stderr are always combined so that
    error messages written to either stream are available for diagnostics.
    """
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode == 0 and not include_stderr:
        return result.stdout.strip(), 0
    combined = (result.stdout.strip() + "\n" + result.stderr.strip()).strip()
    return combined, result.returncode


def poll_until(
    *,
    fn: Callable[[], T],
    until: Callable[[T], bool],
    retries: int = 60,
    delay: int = 5,
    description: str,
    retry_on_error: bool = False,
) -> T:
    value: T | None = None
    last_error: subprocess.CalledProcessError | None = None
    start = time.monotonic()
    last_logged = start
    with _wait_span(description, delay=delay, retries=retries) as span:
        # Descriptions and polled values may contain API responses or credentials.
        logger.info("Waiting for resource condition...")
        for attempt in range(retries):
            # retry_on_error is opt-in: most callers' fn() raising CalledProcessError
            # indicates a real bug (bad namespace, typo'd resource, auth
            # misconfig) and should fail fast, not be swallowed for the whole
            # retry budget. Only pollers built on a checked (raising) subprocess
            # call where a transient hiccup is expected -- e.g. a flaky grpcurl
            # call hitting a momentarily-busy route -- should set this.
            if retry_on_error:
                try:
                    value = fn()
                except subprocess.CalledProcessError as exc:
                    last_error = exc
                    value = None
                else:
                    last_error = None
                    if until(value):
                        _end_wait_span(span, attempt + 1, True)
                        logger.info("Resource condition met after %.0fs", time.monotonic() - start)
                        return value
            else:
                value = fn()
                if until(value):
                    _end_wait_span(span, attempt + 1, True)
                    logger.info("Resource condition met after %.0fs", time.monotonic() - start)
                    return value
            now = time.monotonic()
            if now - last_logged >= _PROGRESS_LOG_INTERVAL_S:
                logger.info("Still waiting (attempt %d/%d, %.0fs elapsed)", attempt + 1, retries, now - start)
                last_logged = now
            if attempt + 1 < retries:
                time.sleep(delay)
        _end_wait_span(span, retries, False)
        if last_error is not None:
            # Suppress the subprocess exception: its command and output may contain secrets.
            raise TimeoutError(
                f"Resource condition timed out after {max(retries - 1, 0) * delay}s (last call failed)"
            ) from None
        raise TimeoutError(f"Resource condition timed out after {max(retries - 1, 0) * delay}s")


class _NoOpWaitSpan:
    """Absorbs span updates when OpenTelemetry is not installed."""

    def set_attribute(self, name: str, value: object) -> None:
        pass

    def set_status(self, status: object) -> None:
        pass


_NO_OP_WAIT_SPAN = _NoOpWaitSpan()


@contextmanager
def _wait_span(description: str, *, delay: int, retries: int) -> Iterator[Span | _NoOpWaitSpan]:
    """Track one poll_until wait as a span named after its description.

    The span nests under whatever span is current when poll_until runs (the
    test's call span in the ci-observe trace), so each wait shows with its
    own duration in the trace waterfall.
    """
    if _otel_trace is None:
        yield _NO_OP_WAIT_SPAN
        return
    with _otel_trace.get_tracer(__name__).start_as_current_span(
        description, kind=_otel_trace.SpanKind.INTERNAL
    ) as span:
        span.set_attribute("poll.delay", delay)
        span.set_attribute("poll.retries", retries)
        yield span


def _end_wait_span(span: Span | _NoOpWaitSpan, attempts: int, met: bool) -> None:
    """Record the wait's outcome: bounded counters and flags, no polled data."""
    span.set_attribute("poll.attempts", attempts)
    span.set_attribute("poll.condition_met", met)
    if not met and _otel_trace is not None:
        span.set_status(_otel_trace.Status(_otel_trace.StatusCode.ERROR, "poll timed out"))


def env(name: str, default: str | None = None) -> str:
    value = os.environ.get(name, default)
    if value is None:
        msg = f"Required environment variable {name} is not set"
        raise RuntimeError(msg)
    return value
