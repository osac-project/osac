"""Pytest-backed test tree adapter for the conformance runner."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Protocol

from tests.conformance.model import Classification, Layer, Plan, Result, RunContext, TestIdentity, TestOutcome

_REPO_ROOT = Path(__file__).resolve().parents[2]


class TestTree(Protocol):
    def collect(self) -> tuple[TestIdentity, ...]: ...
    def run(self, plan: Plan, context: RunContext, *, keep: bool) -> tuple[TestOutcome, ...]: ...


def _safe_component(value: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", value).strip("._") or "test"


class PytestTestTree:
    def __init__(self, *, root: Path, python: str | None = None, cwd: Path | None = None) -> None:
        self.root = root
        self.python = python or sys.executable
        self.cwd = cwd or _REPO_ROOT

    def _env(self, name: str, path: Path) -> dict[str, str]:
        env = os.environ.copy()
        env[name] = str(path)
        return env

    def collect(self) -> tuple[TestIdentity, ...]:
        import tempfile

        with tempfile.TemporaryDirectory() as tmp:
            manifest = Path(tmp) / "manifest.json"
            proc = subprocess.run(
                [
                    self.python,
                    "-m",
                    "pytest",
                    "-n",
                    "0",
                    "--collect-only",
                    "-q",
                    "-p",
                    "tests.conformance.pytest_plugin",
                    str(self.root),
                ],
                cwd=self.cwd,
                env=self._env("OSAC_CONFORMANCE_MANIFEST", manifest),
                text=True,
                capture_output=True,
                check=False,
            )
            if proc.returncode == 5:
                return ()
            if proc.returncode != 0 or not manifest.exists():
                raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "pytest collection failed")
            items = json.loads(manifest.read_text())
        return tuple(self._identity(item) for item in items)

    def _identity(self, item: dict[str, object]) -> TestIdentity:
        name = str(item["name"])
        nodeid = str(item["nodeid"])
        file_part, separator, rest = nodeid.partition("::")
        if separator and not Path(file_part).is_absolute():
            nodeid = f"{(self.root / file_part).resolve()}::{rest}"
        markers = frozenset(str(marker) for marker in item.get("markers", []))
        area = "unknown"
        for marker in markers:
            if marker.startswith("sig/"):
                area = marker.split("/", 1)[1]
            elif marker.startswith("sig_"):
                area = marker.removeprefix("sig_")
        classification = (
            Classification.INFORMING
            if {"conformance_informing", "conformance/informing"} & markers
            else Classification.BLOCKING
        )
        return TestIdentity(
            id=name,
            name=name,
            nodeid=nodeid,
            labels=markers,
            area=area,
            layer=Layer.COMPONENT,
            profiles=(),
            classification=classification,
        )

    def run(self, plan: Plan, context: RunContext, *, keep: bool) -> tuple[TestOutcome, ...]:
        import tempfile

        nodeids = [test.nodeid for test in plan.tests]
        with tempfile.TemporaryDirectory() as tmp:
            results_path = Path(tmp) / "pytest-results.jsonl"
            proc = subprocess.run(
                [
                    self.python,
                    "-m",
                    "pytest",
                    "-n",
                    "0",
                    "-p",
                    "tests.conformance.pytest_plugin",
                    "-q",
                    "--tb=short",
                    *nodeids,
                ],
                cwd=self.cwd,
                env=self._env("OSAC_CONFORMANCE_RESULTS", results_path),
                text=True,
                capture_output=True,
                check=False,
            )
            if context.output is not None:
                for line in (proc.stdout + proc.stderr).splitlines():
                    context.output.line(line)
            entries: dict[str, dict[str, object]] = {}
            by_name: dict[str, dict[str, object]] = {}
            if results_path.exists():
                for line in results_path.read_text().splitlines():
                    entry = json.loads(line)
                    entries[str(entry["nodeid"])] = entry
                    by_name[str(entry["name"])] = entry
        outcomes: list[TestOutcome] = []
        for test in plan.tests:
            entry = entries.get(test.nodeid) or by_name.get(test.id)
            evidence = context.results_dir / "tests" / _safe_component(test.id)
            evidence.mkdir(parents=True, exist_ok=True)
            if entry is None:
                outcome = Result.SKIP
                message = "did not run"
                duration = 0.0
            else:
                raw = str(entry.get("outcome", "failed"))
                outcome = {"passed": Result.PASS, "failed": Result.FAIL, "skipped": Result.SKIP}.get(raw, Result.FAIL)
                message = str(entry.get("message", ""))
                duration = float(entry.get("duration", 0.0))
                if context.output is not None:
                    context.output.evidence(
                        test, Path("test.log"), str(entry.get("stdout", "")) + str(entry.get("stderr", ""))
                    )
            outcomes.append(TestOutcome(test, outcome, duration, message, evidence))
        return tuple(outcomes)
