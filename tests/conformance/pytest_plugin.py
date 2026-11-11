"""Pytest hooks that expose conformance collection and per-test outcomes."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


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
    if not path or report.when not in ("call", "setup"):
        return
    if report.when == "setup" and not (report.failed or report.skipped):
        return
    message = ""
    if report.failed or report.skipped:
        message = str(report.longrepr)
    _append(
        path,
        {
            "nodeid": report.nodeid,
            "name": report.nodeid.rsplit("::", 1)[-1],
            "outcome": report.outcome,
            "duration": float(report.duration),
            "message": message,
            "stdout": report.capstdout,
            "stderr": report.capstderr,
        },
    )
