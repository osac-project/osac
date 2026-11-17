"""Pytest hooks that expose conformance collection and per-test outcomes."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


def _conformance_mode() -> bool:
    return bool(os.environ.get("OSAC_CONFORMANCE_RESULTS") or os.environ.get("OSAC_CONFORMANCE_MANIFEST"))


def pytest_configure(config: Any) -> None:  # noqa: ANN401
    """Stop pytest writing its own unfiltered log during conformance runs.

    The repository's pytest configuration sets ``log_file``. A conformance
    child would write that file before the runner can redact captured output,
    so the parent's single redaction boundary would be bypassed. The conformance
    log and per-test evidence already carry the same records.
    """
    if _conformance_mode():
        # pytest falls back to the ini value when the option is None, so point
        # the file handler at the null device instead.
        config.option.log_file = os.devnull


def _append(path: str, entry: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(entry, sort_keys=True) + "\n")


def pytest_collection_finish(session: Any) -> None:  # noqa: ANN401
    path = os.environ.get("OSAC_CONFORMANCE_MANIFEST")
    if not path:
        return
    items = [
        {"nodeid": item.nodeid, "name": item.name, "markers": sorted({marker.name for marker in item.iter_markers()})}
        for item in session.items
    ]
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(items, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def pytest_runtest_logreport(report: Any) -> None:  # noqa: ANN401
    path = os.environ.get("OSAC_CONFORMANCE_RESULTS")
    if not path or report.when not in ("call", "setup", "teardown"):
        return
    # A setup or teardown report matters only when it failed or skipped: an
    # ordinary passing phase would otherwise duplicate the call report.
    if report.when in ("setup", "teardown") and not (report.failed or report.skipped):
        return
    message = ""
    if report.failed or report.skipped:
        message = str(report.longrepr)
    _append(
        path,
        {
            "nodeid": report.nodeid,
            "name": report.nodeid.rsplit("::", 1)[-1],
            "when": report.when,
            "outcome": report.outcome,
            "duration": float(report.duration),
            "message": message,
            "stdout": report.capstdout,
            "stderr": report.capstderr,
            # Captured logging is separate from stdout and stderr.
            "caplog": report.caplog,
        },
    )
