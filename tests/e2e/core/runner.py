from __future__ import annotations

import logging
import os
import re
import subprocess
import time
from collections.abc import Callable, Iterator, Sequence
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

from tests.e2e.core.sanitize import sanitize_args, sanitize_text

T = TypeVar("T")

logger = logging.getLogger(__name__)

# How often to log a "still waiting" progress line during a poll_until call,
# regardless of its own retry/delay settings -- long polls (e.g. waiting up
# to ~60 minutes for a bare-metal cluster to become Ready) would otherwise
# print nothing until they finish or time out.
_PROGRESS_LOG_INTERVAL_S = 30

# Attempt budget for transient Envoy "no healthy upstream" errors.
# These occur when Envoy momentarily has zero endpoints for the
# fulfillment backend -- the request is never delivered, so retrying
# is safe even for mutating RPCs.
_TRANSIENT_GRPC_MAX_ATTEMPTS: int = 3
_TRANSIENT_GRPC_RETRY_DELAY_S: float = 2.0


# Extract the gRPC status code from its structural position in
# the two observed output formats.  .search() returns the first
# match, which is the primary status code; later occurrences
# inside descriptions are ignored.
#   grpcurl  - ``Code: <STATUS>`` at the start of a line
_GRPCURL_CODE = re.compile(r"^\s*Code:\s*(\w+)", re.IGNORECASE | re.MULTILINE)
#   Go/osac  - ``rpc error: code = <STATUS>``
_GO_GRPC_CODE = re.compile(r"rpc error:\s*code\s*=\s*(\w+)", re.IGNORECASE)


def _is_transient_grpc_error(exc: subprocess.CalledProcessError) -> bool:
    """Return True when *exc* looks like a transient Envoy upstream outage.

    Matches only the narrow pattern where grpcurl (or the osac CLI,
    which speaks gRPC under the hood) reports ``Unavailable`` in the
    actual gRPC status-code field **and** the stderr mentions
    ``no healthy upstream``.  This combination means Envoy had zero
    endpoints for the backend cluster -- the request was never
    forwarded, so retrying is safe regardless of whether the RPC is
    idempotent.

    The status code is extracted from its structural position --
    ``Code:`` at the start of a line in grpcurl output, or
    ``rpc error: code =`` in Go gRPC status strings.  The first
    match is the primary status; later occurrences of ``code:``
    or ``code =`` inside a description or message field are
    ignored, so a PermissionDenied whose message happens to
    contain ``service unavailable`` does not trigger a retry.

    Other gRPC error codes (PermissionDenied, NotFound,
    InvalidArgument, etc.) are never matched even when the
    message contains ``unavailable``.
    """
    stderr = exc.stderr or ""
    m = _GRPCURL_CODE.search(stderr) or _GO_GRPC_CODE.search(stderr)
    if not m or m.group(1).lower() != "unavailable":
        return False
    return "no healthy upstream" in stderr.lower()


_OUTPUT_TAIL = 2000
_SCALAR_SUMMARY_LIMIT = 120


def _text(value: str | bytes | None) -> str:
    if isinstance(value, bytes):
        return value.decode(errors="replace")
    return value or ""


class CommandFailed(subprocess.CalledProcessError):
    """A command exited non-zero. `cmd` is sanitized; `stdout` and `stderr` stay as captured for callers."""

    def __init__(
        self, returncode: int, cmd: Sequence[str], output: str | None = None, stderr: str | None = None
    ) -> None:
        super().__init__(returncode, sanitize_args(cmd), output, stderr)
        # Exception repr and pickling use args, which would otherwise hold the raw stderr.
        self.args = (str(self),)

    def __str__(self) -> str:
        detail = sanitize_text(_text(self.stderr) or _text(self.stdout)).strip()[-_OUTPUT_TAIL:]
        return f"{' '.join(map(str, self.cmd))} exited {self.returncode}: {detail}"


class CommandTimedOut(subprocess.TimeoutExpired):
    """A command ran past its timeout. `cmd` is sanitized."""

    def __init__(
        self, cmd: Sequence[str], timeout: float, output: str | bytes | None = None, stderr: str | bytes | None = None
    ) -> None:
        super().__init__(sanitize_args(cmd), timeout, output, stderr)
        self.args = (str(self),)

    def __str__(self) -> str:
        return f"{' '.join(map(str, self.cmd))} timed out after {self.timeout}s"


def run(*args: str, timeout: int = 300) -> str:
    # pytest's long traceback prints each frame's arguments,
    # and these hold the Authorization header and payload.
    __tracebackhide__ = True
    # The raw subprocess exceptions carry the unsanitized command
    # line, so they are never chained.
    for attempt in range(_TRANSIENT_GRPC_MAX_ATTEMPTS):
        try:
            result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=True)
            return result.stdout.strip()
        except subprocess.CalledProcessError as exc:
            if attempt < _TRANSIENT_GRPC_MAX_ATTEMPTS - 1 and _is_transient_grpc_error(exc):
                logger.warning(
                    "Transient gRPC upstream error (attempt %d/%d), retrying...",
                    attempt + 1,
                    _TRANSIENT_GRPC_MAX_ATTEMPTS,
                )
                time.sleep(_TRANSIENT_GRPC_RETRY_DELAY_S)
                continue
            raise CommandFailed(exc.returncode, args, exc.stdout, exc.stderr) from None
        except subprocess.TimeoutExpired as exc:
            raise CommandTimedOut(args, exc.timeout, exc.stdout, exc.stderr) from None
    raise AssertionError("unreachable")  # pragma: no cover


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
    __tracebackhide__ = True
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as exc:
        raise CommandTimedOut(args, exc.timeout, exc.stdout, exc.stderr) from None
    if result.returncode == 0 and not include_stderr:
        return result.stdout.strip(), 0
    combined = (result.stdout.strip() + "\n" + result.stderr.strip()).strip()
    return combined, result.returncode


class StuckState(TimeoutError):
    """An object never reached the state `poll_until` waited for."""

    def __init__(self, description: str, last_value: str, elapsed_s: float, last_call_failure: str = "") -> None:
        self.description: str = sanitize_text(description)
        self.last_value: str = last_value
        self.elapsed_s: float = elapsed_s
        message = f"{self.description} not reached after {elapsed_s:.0f}s; last value: {last_value}"
        if last_call_failure:
            message += f"; last call failed: {last_call_failure}"
        super().__init__(message)


def _summarize(value: object) -> str:
    """A short description of a polled value. Polled values may be whole API objects: never their content."""
    if isinstance(value, dict):
        return f"<dict with {len(value)} keys>"
    if isinstance(value, (list, tuple, set, frozenset)):
        return f"<list of {len(value)}>"
    if value is None or isinstance(value, (str, int, float, bool)):
        return sanitize_text(repr(value))[:_SCALAR_SUMMARY_LIMIT]
    return f"<{type(value).__name__}>"


def _describe_failure(exc: BaseException) -> str:
    # Only a CommandFailed has a sanitized str(); any other exception may carry the raw command line.
    return f"{type(exc).__name__}: {exc}" if isinstance(exc, CommandFailed) else type(exc).__name__


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
    label = sanitize_text(description)
    with _wait_span(label, delay=delay, retries=retries) as span:
        # Descriptions name resources and conditions, so they are logged after sanitizing.
        # Polled values, errors and tracebacks stay out of logs; only the StuckState message
        # carries a summary of the last scalar value, never a container.
        logger.info("Waiting for %s...", label)
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
                        logger.info("Done waiting for %s after %.0fs", label, time.monotonic() - start)
                        return value
            else:
                value = fn()
                if until(value):
                    _end_wait_span(span, attempt + 1, True)
                    logger.info("Done waiting for %s after %.0fs", label, time.monotonic() - start)
                    return value
            now = time.monotonic()
            if now - last_logged >= _PROGRESS_LOG_INTERVAL_S:
                logger.info(
                    "Still waiting for %s (attempt %d/%d, %.0fs elapsed)", label, attempt + 1, retries, now - start
                )
                last_logged = now
            if attempt + 1 < retries:
                time.sleep(delay)
        _end_wait_span(span, retries, False)
        # `from None`: a raw subprocess exception holds the command line and its output, which may hold secrets.
        raise StuckState(
            description,
            _summarize(value),
            time.monotonic() - start,
            _describe_failure(last_error) if last_error is not None else "",
        ) from None


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

    Exception recording is disabled: an escaping CalledProcessError carries
    the command and its output, which may contain secrets -- poll_until's
    own TimeoutError messages suppress them for the same reason. A generic
    ERROR status still marks the failed wait.
    """
    if _otel_trace is None:
        yield _NO_OP_WAIT_SPAN
        return
    with _otel_trace.get_tracer(__name__).start_as_current_span(
        description, kind=_otel_trace.SpanKind.INTERNAL, record_exception=False, set_status_on_exception=False
    ) as span:
        span.set_attribute("poll.delay", delay)
        span.set_attribute("poll.retries", retries)
        try:
            yield span
        except TimeoutError:
            span.set_status(_otel_trace.Status(_otel_trace.StatusCode.ERROR, "poll timed out"))
            raise
        except BaseException:
            span.set_status(_otel_trace.Status(_otel_trace.StatusCode.ERROR, "poll failed"))
            raise


def _end_wait_span(span: Span | _NoOpWaitSpan, attempts: int, met: bool) -> None:
    """Record the wait's outcome: bounded counters and flags, no polled data."""
    span.set_attribute("poll.attempts", attempts)
    span.set_attribute("poll.condition_met", met)


def env(name: str, default: str | None = None) -> str:
    value = os.environ.get(name, default)
    if value is None:
        msg = f"Required environment variable {name} is not set"
        raise RuntimeError(msg)
    return value
