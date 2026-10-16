from __future__ import annotations

import logging
import os
import subprocess
import time
from collections.abc import Callable
from typing import TypeVar

T = TypeVar("T")

logger = logging.getLogger(__name__)

# How often to log a "still waiting" progress line during a poll_until call,
# regardless of its own retry/delay settings -- long polls (e.g. waiting up
# to ~60 minutes for a bare-metal cluster to become Ready) would otherwise
# print nothing until they finish or time out.
_PROGRESS_LOG_INTERVAL_S = 30

# Retry budget for transient Envoy "no healthy upstream" errors.  These
# occur when Envoy momentarily has zero endpoints for the fulfillment
# backend -- the request is never delivered, so retrying is safe even
# for mutating RPCs.
_TRANSIENT_GRPC_MAX_RETRIES: int = 3
_TRANSIENT_GRPC_RETRY_DELAY_S: float = 2.0


def _is_transient_grpc_error(exc: subprocess.CalledProcessError) -> bool:
    """Return True when *exc* looks like a transient Envoy upstream outage.

    Matches only the narrow pattern where grpcurl (or the osac CLI,
    which speaks gRPC under the hood) reports ``Unavailable`` **and**
    the stderr mentions ``no healthy upstream``.  This combination
    means Envoy had zero endpoints for the backend cluster -- the
    request was never forwarded, so retrying is safe regardless of
    whether the RPC is idempotent.

    Other gRPC error codes (PermissionDenied, NotFound, InvalidArgument,
    etc.) are never matched.
    """
    stderr = (exc.stderr or "").lower()
    return "unavailable" in stderr and "no healthy upstream" in stderr


def run(*args: str, timeout: int = 300) -> str:
    for attempt in range(_TRANSIENT_GRPC_MAX_RETRIES + 1):
        try:
            result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=True)
            return result.stdout.strip()
        except subprocess.CalledProcessError as exc:
            if attempt < _TRANSIENT_GRPC_MAX_RETRIES and _is_transient_grpc_error(exc):
                logger.warning(
                    "Transient gRPC upstream error (attempt %d/%d), retrying...",
                    attempt + 1,
                    _TRANSIENT_GRPC_MAX_RETRIES + 1,
                )
                time.sleep(_TRANSIENT_GRPC_RETRY_DELAY_S)
                continue
            raise
    raise AssertionError("unreachable")  # pragma: no cover


def run_unchecked(*args: str, timeout: int = 300) -> tuple[str, int]:
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
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
                    logger.info("Resource condition met after %.0fs", time.monotonic() - start)
                    return value
        else:
            value = fn()
            if until(value):
                logger.info("Resource condition met after %.0fs", time.monotonic() - start)
                return value
        now = time.monotonic()
        if now - last_logged >= _PROGRESS_LOG_INTERVAL_S:
            logger.info("Still waiting (attempt %d/%d, %.0fs elapsed)", attempt + 1, retries, now - start)
            last_logged = now
        if attempt + 1 < retries:
            time.sleep(delay)
    if last_error is not None:
        # Suppress the subprocess exception: its command and output may contain secrets.
        raise TimeoutError(
            f"Resource condition timed out after {max(retries - 1, 0) * delay}s (last call failed)"
        ) from None
    raise TimeoutError(f"Resource condition timed out after {max(retries - 1, 0) * delay}s")


def env(name: str, default: str | None = None) -> str:
    value = os.environ.get(name, default)
    if value is None:
        msg = f"Required environment variable {name} is not set"
        raise RuntimeError(msg)
    return value
