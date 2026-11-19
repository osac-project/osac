"""A pytest plugin that reports each E2E failure at once and leaves a snapshot, without changing any outcome.

It appends one JSON line per test outcome to `results-<worker>.jsonl` (fsynced, so a killed run keeps what it
knew), logs a one-line `E2E FAILURE` message when a test fails, and takes a snapshot through `failure_snapshot`.
Every hook body is wrapped: a reporting error is logged and swallowed, and `report.outcome` is never touched.
"""

from __future__ import annotations

import functools
import json
import logging
import os
import time
import traceback
from collections.abc import Callable, Generator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Concatenate, ParamSpec

import pluggy
import pytest

from tests.e2e.core.failure_snapshot import SOURCES, SnapshotSources, Snapshotter
from tests.e2e.core.failures import Classified, Kind, classify
from tests.e2e.core.sanitize import sanitize_obj, sanitize_text

logger = logging.getLogger(__name__)

P = ParamSpec("P")

PLUGIN_NAME = "e2e-failure-report"
OUTPUT_DIR_ENV = "OSAC_E2E_OUTPUT_DIR"
DEFAULT_OUTPUT_DIR = "/tmp/test-output"
_LOG_SUMMARY_LIMIT = 300


def output_dir(config: pytest.Config) -> Path:
    configured = os.environ.get(OUTPUT_DIR_ENV)
    if configured:
        return Path(configured)
    log_file = str(config.getini("log_file") or "")
    return Path(log_file).parent if log_file else Path(DEFAULT_OUTPUT_DIR)


def _utc(epoch: float) -> str:
    return datetime.fromtimestamp(epoch, UTC).isoformat(timespec="seconds")


def _log_internal_error(message: str) -> None:
    """Retain reporting-error tracebacks without handing raw exception data to log handlers."""
    logger.warning("%s\n%s", message, sanitize_text(traceback.format_exc()))


def _guarded(
    method: Callable[Concatenate[FailureReporter, P], None],
) -> Callable[Concatenate[FailureReporter, P], None]:
    """Log and swallow anything a hook body raises: reporting must never change a test's result."""

    @functools.wraps(method)
    def wrapper(self: FailureReporter, *args: P.args, **kwargs: P.kwargs) -> None:
        try:
            method(self, *args, **kwargs)
        except Exception:
            _log_internal_error("E2E failure reporting hit an internal error")

    return wrapper


def _module_of(nodeid: str) -> str:
    """The test file of a node ID; a fixture failure's name has no "::" and is its own key."""
    return nodeid.split("::", 1)[0]


class FailureReporter:
    def __init__(self, config: pytest.Config, *, sources: SnapshotSources = SOURCES) -> None:
        self.worker: str = os.environ.get("PYTEST_XDIST_WORKER") or "main"
        # The xdist controller runs no tests; its workers each write their own file.
        self.enabled: bool = not (
            not hasattr(config, "workerinput") and getattr(config.option, "dist", "no") not in ("no", None)
        )
        self.output_dir: Path = output_dir(config)
        self.snapshotter: Snapshotter = Snapshotter(self.output_dir, self.worker, sources)
        self._path: Path = self.output_dir / f"results-{self.worker}.jsonl"
        self._started: dict[str, float] = {}
        # First test start per module: the snapshot logs begin there, where an ordered suite built its state.
        self._module_started: dict[str, float] = {}
        self._exceptions: dict[int, tuple[BaseException, str]] = {}
        self._counts: dict[str, int] = {}

    # Output

    def _append(self, entry: dict[str, Any]) -> None:
        self.output_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        line = json.dumps(sanitize_obj(entry), sort_keys=True, default=str)
        fd = os.open(self._path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
        try:
            os.write(fd, (line + "\n").encode())
            os.fsync(fd)
        finally:
            os.close(fd)

    # Session

    @_guarded
    def pytest_collection_finish(self, session: pytest.Session) -> None:
        if self.enabled:
            self._append(
                {
                    "event": "session-start",
                    "ts": _utc(time.time()),
                    "worker": self.worker,
                    "collected": len(session.items),
                }
            )

    @_guarded
    def pytest_sessionfinish(self, session: pytest.Session, exitstatus: int) -> None:
        if self.enabled:
            self._append(
                {
                    "event": "session-finish",
                    "ts": _utc(time.time()),
                    "worker": self.worker,
                    "exitstatus": int(exitstatus),
                    "counts": dict(self._counts),
                }
            )

    @_guarded
    def pytest_runtest_logstart(self, nodeid: str, location: tuple[str, int | None, str]) -> None:
        now = time.time()
        self._started[nodeid] = now
        self._module_started.setdefault(_module_of(nodeid), now)

    # Per test

    @pytest.hookimpl(hookwrapper=True)
    def pytest_runtest_makereport(
        self, item: pytest.Item, call: pytest.CallInfo[None]
    ) -> Generator[None, pluggy.Result[pytest.TestReport], None]:
        outcome = yield
        self._on_report(item, call, outcome)

    @_guarded
    def _on_report(
        self, item: pytest.Item, call: pytest.CallInfo[None], outcome: pluggy.Result[pytest.TestReport]
    ) -> None:
        if not self.enabled:
            return
        report: pytest.TestReport = outcome.get_result()
        if report.passed and report.when != "call":
            return
        started = self._started.get(item.nodeid, report.start)
        entry: dict[str, Any] = {
            "ts": _utc(time.time()),
            "nodeid": item.nodeid,
            "test": self._short_name(item),
            "phase": report.when,
            "outcome": report.outcome,
            "duration_s": round(report.duration, 3),
            "started_at": _utc(started),
            "kind": None,
            "summary": None,
            "ids": {},
            "repeat_of": None,
            "snapshot": None,
        }
        if report.failed and call.excinfo is not None:
            self._on_failure(item, call.excinfo.value, entry, started)
        self._counts[report.outcome] = self._counts.get(report.outcome, 0) + 1
        self._append(entry)

    @staticmethod
    def _short_name(item: pytest.Item) -> str:
        return str(getattr(item, "originalname", None) or item.name)

    def _on_failure(self, item: pytest.Item | None, exc: BaseException, entry: dict[str, Any], started: float) -> None:
        test = entry["test"]
        try:
            classified = classify(exc)
        except Exception:
            # A bug in classification must not lose the failure: report it as unclassified.
            _log_internal_error("E2E failure classification hit an internal error")
            classified = Classified(Kind.UNCLASSIFIED, type(exc).__name__, {})
        entry.update(kind=str(classified.kind), summary=classified.summary, ids=classified.ids)

        first = self._remembered(exc)
        if first is not None:
            entry["repeat_of"] = first
            logger.error("%s", sanitize_text(f"E2E FAILURE {test} [{classified.kind}] repeats the failure of {first}"))
            return
        self._exceptions[id(exc)] = (exc, test)

        line = (
            f"E2E FAILURE {test} [{classified.kind}] phase={entry['phase']} ids={classified.ids} "
            f"{sanitize_text(classified.summary)[:_LOG_SUMMARY_LIMIT]}"
        )
        logger.error("%s", sanitize_text(line))
        entry["snapshot"] = self._snapshot(sanitize_text(test), classified.kind, started, entry)

    def _remembered(self, exc: BaseException) -> str | None:
        """The test that already reported `exc`, or an exception in its chain (a cleanup error replaces it)."""
        link: BaseException | None = exc
        for _ in range(6):
            if link is None:
                return None
            first = self._exceptions.get(id(link))
            if first is not None and first[0] is link:
                return first[1]
            link = link.__cause__ or link.__context__
        return None

    def capture_fixture_failure(self, exc: BaseException, name: str, started: float | None) -> None:
        """Report a fixture failure before its cleanup starts."""
        self.capture_failure(exc, name, started, phase="setup")

    @_guarded
    def capture_failure(
        self, exc: BaseException, name: str, started: float | None = None, *, phase: str = "call"
    ) -> None:
        """Report before in-body cleanup; the later pytest hook recognizes the remembered exception."""
        if not self.enabled:
            return
        began = started if started is not None else self._started.get(name, time.time())
        entry: dict[str, Any] = {
            "ts": _utc(time.time()),
            "nodeid": name,
            "test": name,
            "phase": phase,
            "outcome": "failed",
            "duration_s": round(time.time() - began, 3),
            "started_at": _utc(began),
            "kind": None,
            "summary": None,
            "ids": {},
            "repeat_of": None,
            "snapshot": None,
        }
        self._on_failure(None, exc, entry, began)
        self._append(entry)

    @_guarded
    def capture_snapshot(self, name: str) -> None:
        """Take a bounded pre-delete checkpoint while leaving the test's outcome and failure cap untouched."""
        if self.enabled:
            started = datetime.fromtimestamp(self._started.get(name, time.time()), UTC)
            module_started = self._module_started.get(_module_of(name))
            logs_since = datetime.fromtimestamp(module_started, UTC) if module_started is not None else None
            path = self.snapshotter.take(
                name, Kind.UNCLASSIFIED, started, {"reason": "before-delete"}, logs_since=logs_since, checkpoint=True
            )
            logger.info("E2E snapshot before delete: %s", sanitize_text(path))

    def _snapshot(self, test: str, kind: Kind, started: float, entry: dict[str, Any]) -> str | None:
        try:
            module_started = self._module_started.get(_module_of(str(entry.get("nodeid", ""))))
            logs_since = datetime.fromtimestamp(module_started, UTC) if module_started is not None else None
            return self.snapshotter.take(test, kind, datetime.fromtimestamp(started, UTC), entry, logs_since=logs_since)
        except Exception:
            _log_internal_error("E2E failure snapshot hit an internal error")
            return None


def register(config: pytest.Config, *, sources: SnapshotSources = SOURCES) -> FailureReporter:
    """Register the reporter once; later calls return the registered plugin."""
    existing = config.pluginmanager.get_plugin(PLUGIN_NAME)
    if existing is not None:
        return existing
    reporter = FailureReporter(config, sources=sources)
    config.pluginmanager.register(reporter, PLUGIN_NAME)
    return reporter


def capture_failure(config: pytest.Config, exc: BaseException, name: str) -> None:
    """Report a test-body failure before cleanup. Never raises, and does nothing without the plugin."""
    try:
        reporter = config.pluginmanager.get_plugin(PLUGIN_NAME)
        if reporter is not None:
            reporter.capture_failure(exc, name)
    except Exception:
        _log_internal_error("E2E test failure capture hit an internal error")


def capture_snapshot(config: pytest.Config, name: str) -> None:
    """Capture state immediately before intentional deletion without reporting a failure."""
    try:
        reporter = config.pluginmanager.get_plugin(PLUGIN_NAME)
        if reporter is not None:
            reporter.capture_snapshot(name)
    except Exception:
        _log_internal_error("E2E checkpoint capture hit an internal error")


def capture_fixture_failure(config: pytest.Config, exc: BaseException, name: str, started: float | None = None) -> None:
    """Report a failing fixture before it cleans up. Never raises, and does nothing without the plugin."""
    try:
        reporter = config.pluginmanager.get_plugin(PLUGIN_NAME)
        if reporter is not None:
            reporter.capture_fixture_failure(exc, name, started)
    except Exception:
        _log_internal_error("E2E fixture failure capture hit an internal error")
