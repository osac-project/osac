"""Pytest-backed test tree adapter for the conformance runner."""

from __future__ import annotations

import contextlib
import json
import os
import signal
import subprocess
import sys
from collections import Counter
from collections.abc import Callable
from pathlib import Path
from typing import Protocol

from tests.conformance.errors import ConfigError
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.model import Classification, Plan, Result, RunContext, TestIdentity, TestOutcome
from tests.conformance.output import safe_component

_REPO_ROOT = Path(__file__).resolve().parents[2]


class TestTree(Protocol):
    def collect(self) -> tuple[TestIdentity, ...]: ...

    def run(
        self, plan: Plan, context: RunContext, *, keep: bool, interrupt: InterruptGate | None = None
    ) -> tuple[TestOutcome, ...]: ...


def _stop(process: subprocess.Popen[str] | None) -> None:
    """Kill and reap a child the runner could not wait for normally.

    Reached only when something escaped before ``communicate`` returned, so a
    child that is still running here is one no one else will stop.
    """
    if process is None or process.poll() is not None:
        return
    process.kill()
    process.wait()


class PytestTestTree:
    def __init__(
        self,
        *,
        root: Path,
        python: str | None = None,
        cwd: Path | None = None,
        popen: Callable[..., subprocess.Popen[str]] = subprocess.Popen,
    ) -> None:
        self.python = python or sys.executable
        self.cwd = Path(cwd) if cwd is not None else _REPO_ROOT
        # Resolve once, against the install root the child runs in, so
        # collection and execution agree on every node ID.
        self.root = (self.cwd / Path(root)).resolve()
        self.popen = popen

    def _env(self, name: str, path: Path) -> dict[str, str]:
        env = os.environ.copy()
        env[name] = str(path)
        return env

    def _run_env(self, context: RunContext, results_path: Path) -> dict[str, str]:
        env = self._env("OSAC_CONFORMANCE_RESULTS", results_path)
        env["OSAC_CONFORMANCE_RUN_ID"] = context.run_id
        env["OSAC_CONFORMANCE_RESULTS_DIR"] = str(context.results_dir)
        env["OSAC_CONFORMANCE_CONFIG"] = str(context.config.path)
        if context.tenant:
            env["OSAC_CONFORMANCE_TENANT"] = context.tenant
        return env

    def _absolute_nodeid(self, nodeid: str) -> str:
        file_part, separator, rest = nodeid.partition("::")
        if not separator:
            return nodeid
        path = Path(file_part)
        absolute = path if path.is_absolute() else (self.root / path).resolve()
        return f"{absolute}::{rest}"

    def _canonical_id(self, nodeid: str) -> str:
        absolute = self._absolute_nodeid(nodeid)
        file_part, separator, rest = absolute.partition("::")
        if not separator:
            return absolute
        try:
            relative = Path(file_part).relative_to(self.root.resolve())
        except ValueError:
            relative = Path(file_part)
        return f"{relative.as_posix()}::{rest}"

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
                    "--rootdir",
                    str(self.root),
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
                detail = proc.stderr.strip() or proc.stdout.strip() or "no output"
                raise ConfigError([f"pytest collection failed: {detail}"])
            items = json.loads(manifest.read_text())
        tests = tuple(self._identity(item) for item in items)
        duplicates = sorted(item for item, count in Counter(test.id for test in tests).items() if count > 1)
        if duplicates:
            raise ConfigError([f"duplicate conformance test id {item}" for item in duplicates])
        return tests

    def _identity(self, item: dict[str, object]) -> TestIdentity:
        name = str(item["name"])
        manifest_nodeid = str(item["nodeid"])
        nodeid = self._absolute_nodeid(manifest_nodeid)
        markers = frozenset(str(marker) for marker in item.get("markers", []))
        classification = (
            Classification.INFORMING
            if {"conformance_informing", "conformance/informing"} & markers
            else Classification.BLOCKING
        )
        return TestIdentity(
            id=self._canonical_id(manifest_nodeid),
            name=name,
            nodeid=nodeid,
            labels=markers,
            profiles=(),
            classification=classification,
        )

    def run(
        self, plan: Plan, context: RunContext, *, keep: bool, interrupt: InterruptGate | None = None
    ) -> tuple[TestOutcome, ...]:
        import tempfile

        nodeids = [test.nodeid for test in plan.tests]
        with tempfile.TemporaryDirectory() as tmp:
            results_path = Path(tmp) / "pytest-results.jsonl"
            process: subprocess.Popen[str] | None = None
            # Cleanup is in place before the child is registered: ``hold``
            # replays a signal that arrived during ``Popen`` from its own
            # ``finally``, so it can raise for a second pending signal before
            # the body below starts. Without this ``try`` that path would drop
            # a live pytest process.
            try:
                with interrupt.hold() if interrupt is not None else contextlib.nullcontext():
                    process = self.popen(
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
                            "--rootdir",
                            str(self.root),
                            *nodeids,
                        ],
                        cwd=self.cwd,
                        env=self._run_env(context, results_path),
                        text=True,
                        stdout=subprocess.PIPE,
                        stderr=subprocess.PIPE,
                        start_new_session=True,
                    )
                    if interrupt is not None:
                        interrupt.child = process
                stdout, stderr = process.communicate()
                # The first signal is forwarded to the child, which can die from
                # SIGINT before pytest installs its own handler.
                interrupted = interrupt is not None and interrupt.first
                killed_by_interrupt = interrupted and process.returncode == -signal.SIGINT
                detail = (stderr or "").strip() or (stdout or "").strip() or "no output"
                if process.returncode not in (0, 1, 2) and not killed_by_interrupt:
                    raise ConfigError([f"pytest exited {process.returncode}: {detail}"])
                if context.output is not None:
                    for line in (stdout + stderr).splitlines():
                        context.output.line(line)
                outcomes = self._recorded_outcomes(plan, context, results_path)
                if interrupted:
                    raise Interrupted(outcomes)
                if process.returncode == 2:
                    # pytest reports a module that fails to collect during
                    # execution with the same status it uses for an interrupt.
                    raise ConfigError([f"pytest collection failed: {detail}"])
                return outcomes
            finally:
                # Kept attached until the outcomes are read: a signal inside this
                # block must not raise ahead of the recorded results.
                if interrupt is not None:
                    interrupt.child = None
                _stop(process)

    def _recorded_outcomes(self, plan: Plan, context: RunContext, results_path: Path) -> tuple[TestOutcome, ...]:
        """Read what pytest managed to record, including after an interrupt."""
        entries: dict[str, list[dict[str, object]]] = {}
        if results_path.exists():
            for line in results_path.read_text().splitlines():
                try:
                    entry = json.loads(line)
                except json.JSONDecodeError:
                    continue
                nodeid = entry.get("nodeid")
                if isinstance(nodeid, str):
                    entries.setdefault(self._canonical_id(nodeid), []).append(entry)
        outcomes: list[TestOutcome] = []
        for test in plan.tests:
            group = entries.get(test.id, [])
            evidence = context.results_dir / "tests" / safe_component(test.id)
            evidence.mkdir(parents=True, exist_ok=True)
            if not group:
                outcome = Result.SKIP
                message = "did not run"
                duration = 0.0
            else:
                failed = [entry for entry in group if entry.get("outcome") == "failed"]
                skipped = [entry for entry in group if entry.get("outcome") == "skipped"]
                outcome = Result.FAIL if failed else Result.SKIP if skipped else Result.PASS
                messages = [
                    str(entry.get("message", "")) for entry in (failed or skipped or group) if entry.get("message")
                ]
                message = "\n".join(dict.fromkeys(messages))
                duration = sum(float(entry.get("duration", 0.0)) for entry in group)
                if context.output is not None:
                    log = "".join(
                        str(entry.get(key) or "") for entry in group for key in ("caplog", "stdout", "stderr")
                    )
                    context.output.evidence(test, Path("test.log"), log)
            outcomes.append(TestOutcome(test, outcome, duration, message, evidence))
        return tuple(outcomes)
