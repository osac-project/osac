from __future__ import annotations

import logging
import os
import subprocess
import time
from collections.abc import Callable, Sequence
from typing import TypeVar

from tests.e2e.core.sanitize import sanitize_args, sanitize_text

T = TypeVar("T")

logger = logging.getLogger(__name__)

# How often to log a "still waiting" progress line during a poll_until call,
# regardless of its own retry/delay settings -- long polls (e.g. waiting up
# to ~60 minutes for a bare-metal cluster to become Ready) would otherwise
# print nothing until they finish or time out.
_PROGRESS_LOG_INTERVAL_S = 30


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
    # pytest's long traceback prints each frame's arguments, and these hold the Authorization header and payload.
    __tracebackhide__ = True
    # The raw subprocess exceptions carry the unsanitized command line, so they are never chained.
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=True)
    except subprocess.CalledProcessError as exc:
        raise CommandFailed(exc.returncode, args, exc.stdout, exc.stderr) from None
    except subprocess.TimeoutExpired as exc:
        raise CommandTimedOut(args, exc.timeout, exc.stdout, exc.stderr) from None
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
    # Descriptions name resources and conditions, so they are logged after sanitizing.
    # Polled values, errors and tracebacks stay out of logs; only the StuckState message
    # carries a summary of the last scalar value, never a container.
    label = sanitize_text(description)
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
                    logger.info("Done waiting for %s after %.0fs", label, time.monotonic() - start)
                    return value
        else:
            value = fn()
            if until(value):
                logger.info("Done waiting for %s after %.0fs", label, time.monotonic() - start)
                return value
        now = time.monotonic()
        if now - last_logged >= _PROGRESS_LOG_INTERVAL_S:
            logger.info("Still waiting for %s (attempt %d/%d, %.0fs elapsed)", label, attempt + 1, retries, now - start)
            last_logged = now
        if attempt + 1 < retries:
            time.sleep(delay)
    # `from None`: a raw subprocess exception holds the command line and its output, which may hold secrets.
    raise StuckState(
        description,
        _summarize(value),
        time.monotonic() - start,
        _describe_failure(last_error) if last_error is not None else "",
    ) from None


def env(name: str, default: str | None = None) -> str:
    value = os.environ.get(name, default)
    if value is None:
        msg = f"Required environment variable {name} is not set"
        raise RuntimeError(msg)
    return value
