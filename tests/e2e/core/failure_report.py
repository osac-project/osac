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


def _guarded(
    method: Callable[Concatenate[FailureReporter, P], None],
) -> Callable[Concatenate[FailureReporter, P], None]:
    """Log and swallow anything a hook body raises: reporting must never change a test's result."""

    @functools.wraps(method)
    def wrapper(self: FailureReporter, *args: P.args, **kwargs: P.kwargs) -> None:
        try:
            method(self, *args, **kwargs)
        except Exception:
            logger.warning("E2E failure reporting hit an internal error", exc_info=True)

    return wrapper


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
        self._started[nodeid] = time.time()

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
            logger.warning("E2E failure classification hit an internal error", exc_info=True)
            classified = Classified(Kind.UNCLASSIFIED, type(exc).__name__, {})
        entry.update(kind=str(classified.kind), summary=classified.summary, ids=classified.ids)

        first = self._remembered(exc)
        if first is not None:
            entry["repeat_of"] = first
            logger.error("E2E FAILURE %s [%s] repeats the failure of %s", test, classified.kind, first)
            return
        self._exceptions[id(exc)] = (exc, test)

        line = (
            f"E2E FAILURE {test} [{classified.kind}] phase={entry['phase']} ids={classified.ids} "
            f"{sanitize_text(classified.summary)[:_LOG_SUMMARY_LIMIT]}"
        )
        logger.error("%s", line)
        entry["snapshot"] = self._snapshot(test, classified.kind, started, entry)

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

    @_guarded
    def capture_fixture_failure(self, exc: BaseException, name: str, started: float | None) -> None:
        """Report a fixture failure now, while the system it built is still there.

        A generator fixture's `finally` runs before `makereport`, so waiting for the hook would snapshot a cleaned
        system. This classifies, logs, writes the record and takes the snapshot; `makereport` then finds the
        exception remembered and writes a repeat without a second snapshot.
        """
        if not self.enabled:
            return
        began = started if started is not None else time.time()
        entry: dict[str, Any] = {
            "ts": _utc(time.time()),
            "nodeid": name,
            "test": name,
            "phase": "setup",
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

    def _snapshot(self, test: str, kind: Kind, started: float, entry: dict[str, Any]) -> str | None:
        try:
            return self.snapshotter.take(test, kind, datetime.fromtimestamp(started, UTC), entry)
        except Exception:
            logger.warning("E2E failure snapshot hit an internal error", exc_info=True)
            return None


def register(config: pytest.Config, *, sources: SnapshotSources = SOURCES) -> FailureReporter:
    """Register the reporter once; later calls return the registered plugin."""
    existing = config.pluginmanager.get_plugin(PLUGIN_NAME)
    if existing is not None:
        return existing
    reporter = FailureReporter(config, sources=sources)
    config.pluginmanager.register(reporter, PLUGIN_NAME)
    return reporter


def capture_fixture_failure(config: pytest.Config, exc: BaseException, name: str, started: float | None = None) -> None:
    """Report a failing fixture before it cleans up. Never raises, and does nothing without the plugin."""
    try:
        reporter = config.pluginmanager.get_plugin(PLUGIN_NAME)
        if reporter is not None:
            reporter.capture_fixture_failure(exc, name, started)
    except Exception:
        logger.warning("E2E fixture failure capture hit an internal error", exc_info=True)
