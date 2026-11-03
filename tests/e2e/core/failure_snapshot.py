"""Snapshot of the system at the moment a test fails, taken by named sources into one directory per failure.

A source is a callable `fn(directory, window)` registered by name, optionally for some failure kinds only.
Sources run concurrently under a total time budget and can never fail the test: an exception, or running
past the budget, is written to `snapshot-errors.txt`. Everything written passes the sanitizer. Sources write
through `write_text`/`write_json`; `Kubernetes Secrets` are never read.
"""

from __future__ import annotations

import concurrent.futures
import json
import logging
import os
import re
import shlex
import subprocess
import threading
import time
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

from tests.e2e.core.failures import Kind
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import run
from tests.e2e.core.sanitize import sanitize_obj, sanitize_text

logger = logging.getLogger(__name__)

ERRORS_FILE = "snapshot-errors.txt"
CAP_REACHED = "cap reached"
MAX_SNAPSHOTS_PER_WORKER = 12
BUDGET_S = 60.0
MAX_WORKERS = 6
SOURCE_COMMAND_TIMEOUT_S = 30
GUEST_KINDS = (Kind.TRAFFIC_BLOCKED, Kind.TEST_SSH_FAILED, Kind.UNCLASSIFIED, Kind.ASSERTION)
GUEST_COMMAND = "ip -br addr; ip route; ip neigh; ss -tln; cat /etc/resolv.conf"
GUEST_PER_VM_TIMEOUT_S = 10
GUEST_TOTAL_BUDGET_S = 45
FABRIC_COMMAND_ENV = "OSAC_E2E_FABRIC_SNAPSHOT_CMD"

# Private API resources and the CR plural each one maps to.
_RESOURCES = (
    ("ComputeInstances", "computeinstances.osac.openshift.io"),
    ("VirtualNetworks", "virtualnetworks.osac.openshift.io"),
    ("Subnets", "subnets.osac.openshift.io"),
    ("SecurityGroups", "securitygroups.osac.openshift.io"),
    ("NATGateways", "natgateways.osac.openshift.io"),
    ("ExternalIPs", "externalips.osac.openshift.io"),
    ("ExternalIPAttachments", "externalipattachments.osac.openshift.io"),
    ("ExternalIPPools", "externalippools.osac.openshift.io"),
    ("BareMetalInstances", "baremetalinstances.osac.openshift.io"),
)
# Deployment names in the OSAC namespace (fulfillment-service and osac-operator charts).
_LOG_DEPLOYMENTS = ("fulfillment-grpc-server", "fulfillment-controller", "osac-operator")


@dataclass(frozen=True)
class Window:
    """The period to look at: from the start of the failed test."""

    started_at: datetime


SourceFn = Callable[[Path, Window], None]


class SnapshotSources:
    """Named snapshot sources. Registering a name again replaces the earlier source."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._sources: dict[str, tuple[SourceFn, frozenset[Kind] | None]] = {}

    def register(self, name: str, fn: SourceFn, kinds: Iterable[Kind] | None = None) -> None:
        with self._lock:
            self._sources[name] = (fn, frozenset(kinds) if kinds is not None else None)

    def unregister(self, name: str) -> None:
        with self._lock:
            self._sources.pop(name, None)

    def for_kind(self, kind: Kind) -> list[tuple[str, SourceFn]]:
        with self._lock:
            return [(n, fn) for n, (fn, kinds) in self._sources.items() if kinds is None or kind in kinds]


SOURCES = SnapshotSources()

_errors_lock = threading.Lock()


def write_text(directory: Path, name: str, text: str) -> None:
    (directory / name).write_text(sanitize_text(text), encoding="utf-8")


def write_json(directory: Path, name: str, value: object) -> None:
    write_text(directory, name, json.dumps(sanitize_obj(value), indent=2, sort_keys=True, default=str))


def record_error(directory: Path, name: str, message: str) -> None:
    line = sanitize_text(f"{name}: {message}").replace("\n", " ")
    with _errors_lock, (directory / ERRORS_FILE).open("a", encoding="utf-8") as handle:
        handle.write(line + "\n")


def _safe(name: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", name)[:80]


class Snapshotter:
    def __init__(
        self,
        output_dir: Path,
        worker: str,
        sources: SnapshotSources = SOURCES,
        *,
        budget_s: float = BUDGET_S,
        cap: int = MAX_SNAPSHOTS_PER_WORKER,
    ) -> None:
        self.output_dir = output_dir
        self.worker = worker
        self.sources = sources
        self.budget_s = budget_s
        self.cap = cap
        self._taken = 0

    def take(self, test: str, kind: Kind, started_at: datetime, record: dict[str, Any] | None = None) -> str:
        """Snapshot into `<output>/failures/<worker>/<nn>-<test>/`; return its path relative to the output."""
        if self._taken >= self.cap:
            return CAP_REACHED
        self._taken += 1
        relative = f"failures/{self.worker}/{self._taken:02d}-{_safe(test)}"
        directory = self.output_dir / relative
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        write_json(directory, "failure.json", {**(record or {}), "test": test, "kind": str(kind), "snapshot": relative})
        self._run_sources(directory, Window(started_at), self.sources.for_kind(kind))
        self._scrub(directory)
        return relative

    def _run_sources(self, directory: Path, window: Window, sources: list[tuple[str, SourceFn]]) -> None:
        if not sources:
            return
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS, thread_name_prefix="e2e-snapshot")
        futures = {executor.submit(fn, directory, window): name for name, fn in sources}
        try:
            done, pending = concurrent.futures.wait(futures, timeout=self.budget_s)
            for future in done:
                exc = future.exception()
                if exc is not None:
                    record_error(directory, futures[future], f"{type(exc).__name__}: {exc}")
            if pending:
                names = ", ".join(sorted(futures[f] for f in pending))
                record_error(directory, "budget", f"{self.budget_s:.0f}s budget expired; unfinished sources: {names}")
        finally:
            # Do not wait: an unfinished source is bounded by its own subprocess timeouts.
            executor.shutdown(wait=False, cancel_futures=True)

    @staticmethod
    def _scrub(directory: Path) -> None:
        """Second pass over what finished sources wrote, for any that wrote without the helpers."""
        for path in directory.iterdir():
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
                clean = sanitize_text(text)
                if clean != text:
                    path.write_text(clean, encoding="utf-8")
            except OSError:
                logger.warning("snapshot scrub could not read one file")


# Generic sources


def api_source(private_grpc: GRPCClient) -> SourceFn:
    """Private gRPC List of every resource the networking and compute suites create."""

    def source(directory: Path, window: Window) -> None:
        for service, _ in _RESOURCES:
            try:
                data = private_grpc.call(service=f"{PRIVATE_API}.{service}/List", timeout=SOURCE_COMMAND_TIMEOUT_S)
            except Exception as exc:  # one failed List must not stop the others
                record_error(directory, f"api:{service}", f"{type(exc).__name__}: {exc}")
            else:
                write_json(directory, f"api-{service.lower()}.json", data)

    return source


def k8s_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """The matching CRs and the events of the OSAC namespace. Secrets are never read."""

    def get_json(*args: str) -> object:
        return json.loads(run(*k8s.base_args(), "get", *args, "-o", "json", timeout=SOURCE_COMMAND_TIMEOUT_S))

    def source(directory: Path, window: Window) -> None:
        for service, resource in _RESOURCES:
            try:
                write_json(directory, f"k8s-{service.lower()}.json", get_json(resource, "-n", namespace))
            except Exception as exc:
                record_error(directory, f"k8s:{service}", f"{type(exc).__name__}: {exc}")
        try:
            write_json(directory, "k8s-events.json", get_json("events", "-n", namespace))
        except Exception as exc:
            record_error(directory, "k8s:events", f"{type(exc).__name__}: {exc}")

    return source


def logs_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """Logs of the fulfillment API server, the fulfillment controller and the operator since the test began."""

    def source(directory: Path, window: Window) -> None:
        since = window.started_at.isoformat().replace("+00:00", "Z")
        for deployment in _LOG_DEPLOYMENTS:
            try:
                output = run(
                    *k8s.base_args(),
                    "logs",
                    f"deployment/{deployment}",
                    "-n",
                    namespace,
                    "--all-containers",
                    "--timestamps",
                    f"--since-time={since}",
                    "--tail=20000",
                    timeout=SOURCE_COMMAND_TIMEOUT_S,
                )
            except Exception as exc:
                record_error(directory, f"logs:{deployment}", f"{type(exc).__name__}: {exc}")
            else:
                write_text(directory, f"log-{deployment}.txt", output)

    return source


def fabric_source(command: str | None = None) -> SourceFn | None:
    """Run the backend's own snapshot command. None when `OSAC_E2E_FABRIC_SNAPSHOT_CMD` is unset."""
    command = command if command is not None else os.environ.get(FABRIC_COMMAND_ENV, "")
    if not command.strip():
        return None

    def source(directory: Path, window: Window) -> None:
        try:
            result = subprocess.run(
                shlex.split(command), capture_output=True, text=True, timeout=SOURCE_COMMAND_TIMEOUT_S, check=False
            )
        except subprocess.TimeoutExpired:
            record_error(directory, "fabric", f"timed out after {SOURCE_COMMAND_TIMEOUT_S}s")
            return
        write_text(directory, "fabric.json", result.stdout)
        if result.stderr.strip():
            record_error(directory, "fabric", result.stderr.strip())
        if result.returncode != 0:
            record_error(directory, "fabric", f"exited {result.returncode}")

    return source


def guest_source(guests: Callable[[], list[tuple[str, Callable[[str, int], tuple[str, int]]]]]) -> SourceFn:
    """Network state of each guest, one after another: 10s per guest and a stop after 45s in total.

    `guests` returns (name, run) pairs, where `run(command, timeout)` returns (output, status).
    """

    def source(directory: Path, window: Window) -> None:
        deadline = time.monotonic() + GUEST_TOTAL_BUDGET_S
        for name, run_command in guests():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                record_error(directory, f"guest:{name}", "skipped: guest budget used up")
                continue
            try:
                output, status = run_command(GUEST_COMMAND, int(min(GUEST_PER_VM_TIMEOUT_S, remaining)))
            except Exception as exc:
                record_error(directory, f"guest:{name}", f"{type(exc).__name__}: {exc}")
            else:
                write_text(directory, f"guest-{_safe(name)}.txt", f"exit status {status}\n{output}\n")

    return source


def register_generic_sources(
    sources: SnapshotSources, *, private_grpc: GRPCClient, k8s: K8sClient, namespace: str
) -> list[str]:
    """Register api, k8s, logs and (when configured) fabric; return the names so the caller can remove them."""
    registered = {
        "api": api_source(private_grpc),
        "k8s": k8s_source(k8s, namespace),
        "logs": logs_source(k8s, namespace),
    }
    fabric = fabric_source()
    if fabric is not None:
        registered["fabric"] = fabric
    for name, fn in registered.items():
        sources.register(name, fn)
    return list(registered)
