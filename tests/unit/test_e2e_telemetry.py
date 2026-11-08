"""E2E telemetry: worker attribution and fixture-reuse spans, driven by real `python -m pytest` subprocesses.

The subprocesses run throwaway projects whose conftest mirrors the telemetry
wiring of tests/e2e/conftest.py plus a JSONL recording attach. The OpenTelemetry
span assertions run only where opentelemetry-sdk and pytest-opentelemetry are
installed (the E2E test image provides both); everywhere else they skip, and
the inert and outcome-preservation tests below still run, because telemetry
must stay inert and outcome-preserving without those packages.
"""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import textwrap
from dataclasses import dataclass
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

from tests.e2e.core import telemetry

REPO_ROOT = Path(__file__).resolve().parents[2]

# Mirrors the wiring in tests/e2e/conftest.py (telemetry.register in
# pytest_configure); the recording attach is test-only.
TELEMETRY_CONFTEST = """
import os

from tests.e2e.core import telemetry


def pytest_configure(config):
    telemetry.register(config)


def pytest_sessionstart(session):
    telemetry.record_spans_to(os.environ["SPAN_DIR"])
"""

# Session and module fixtures must produce fixture_use spans, class and
# function fixtures must not, and the two consumers of shared_session must
# still count as one use of it.
SPAN_TESTS = """
import pytest

from tests.e2e.core import runner
from tests.e2e.core.wait_kinds import WaitResourceKind


@pytest.fixture(scope="session")
def shared_session():
    return {"value": "session"}


@pytest.fixture(scope="module")
def shared_module():
    return {"value": "module"}


@pytest.fixture(scope="class")
def shared_class():
    return {"value": "class"}


@pytest.fixture(scope="function")
def per_test():
    return {"value": "function"}


@pytest.fixture
def first_session_consumer(shared_session):
    return shared_session["value"]


@pytest.fixture
def second_session_consumer(shared_session):
    return shared_session["value"]


def test_a(first_session_consumer, second_session_consumer, shared_module, shared_class, per_test):
    assert first_session_consumer == second_session_consumer == "session"
    runner.poll_until(
        fn=lambda: "ready",
        until=lambda value: value == "ready",
        retries=2,
        delay=0,
        description="worker attribute wait",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id="ci-1",
    )


def test_b(shared_session, shared_module):
    assert shared_session["value"] == "session"
    assert shared_module["value"] == "module"


def test_c(shared_session):
    assert shared_session["value"] == "session"
"""

# One passing, one failing and one skipped test: outcomes must survive the
# telemetry wiring unchanged, and only the first two may count fixture uses.
OUTCOME_TESTS = """
import pytest


@pytest.fixture(scope="session")
def shared_session():
    return 1


def test_passes(shared_session):
    assert shared_session == 1


def test_fails(shared_session):
    assert shared_session == 2


@pytest.mark.skip(reason="counted as skipped")
def test_skips(shared_session):
    assert shared_session == 1
"""


@dataclass
class Result:
    returncode: int
    stdout: str
    span_dir: Path

    def spans(self, source: str) -> list[dict[str, Any]]:
        path = self.span_dir / f"spans-{source}.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()]

    def workers(self) -> list[str]:
        return sorted(path.stem.removeprefix("spans-") for path in self.span_dir.glob("spans-*.jsonl"))

    def summary(self) -> str:
        """The counts line of the pytest output, without its trailing duration.

        Searched from the end: pytest-opentelemetry's git probe and pytest's
        temp-cleanup warnings print after it.
        """
        for line in reversed(self.stdout.splitlines()):
            if " passed" in line or " failed" in line or " error" in line or " no tests ran" in line:
                return line.rsplit(" in ", 1)[0]
        return ""


def run_pytest(
    tmp_path: Path, tests: str, *, env: dict[str, str] | None = None, args: tuple[str, ...] = (), wired: bool = True
) -> Result:
    project = tmp_path / "project"
    project.mkdir(exist_ok=True)
    (project / "conftest.py").write_text(TELEMETRY_CONFTEST if wired else "")
    (project / "test_sample.py").write_text(textwrap.dedent(tests))
    span_dir = tmp_path / "spans"
    merged = {
        **os.environ,
        "PYTHONPATH": str(REPO_ROOT),
        "PYTHONDONTWRITEBYTECODE": "1",
        "SPAN_DIR": str(span_dir),
        **(env or {}),
    }
    merged.pop("PYTEST_XDIST_WORKER", None)
    # A private basetemp keeps pytest from warning about unrelated stale
    # directories under the shared /tmp/pytest-of-<user> root.
    basetemp = tmp_path / ("basetemp-wired" if wired else "basetemp-baseline")
    command = [
        sys.executable,
        "-m",
        "pytest",
        "-p",
        "no:cacheprovider",
        "-q",
        "--tb=no",
        "--basetemp",
        str(basetemp),
        *args,
    ]
    completed = subprocess.run(command, cwd=project, env=merged, capture_output=True, text=True, timeout=180)
    return Result(completed.returncode, completed.stdout + completed.stderr, span_dir)


@pytest.fixture(scope="module")
def require_otel() -> None:
    pytest.importorskip("opentelemetry.sdk", reason="OpenTelemetry ships only in the E2E test image")
    pytest.importorskip("pytest_opentelemetry", reason="pytest-opentelemetry ships only in the E2E test image")


@pytest.fixture(scope="module")
def xdist_run(tmp_path_factory: pytest.TempPathFactory, require_otel: None) -> Result:
    return run_pytest(tmp_path_factory.mktemp("telemetry-spans"), SPAN_TESTS, args=("-n", "2"))


class _FakeProvider:
    """Plain-data stand-in for an SDK TracerProvider: records attached processors."""

    def __init__(self) -> None:
        self.processors: list[object] = []

    def add_span_processor(self, processor: object) -> None:
        self.processors.append(processor)


class _ProxyProvider:
    """Stand-in for the API's inert proxy provider: accepts no span processors."""


class _FakeTracer:
    """Records the spans record_fixture_use starts."""

    def __init__(self) -> None:
        self.spans: list[_FakeSpan] = []

    def start_span(self, name: str, attributes: dict[str, str] | None = None) -> _FakeSpan:
        span = _FakeSpan(name, attributes or {})
        self.spans.append(span)
        return span


class _FakeSpan:
    def __init__(self, name: str, attributes: dict[str, str]) -> None:
        self.name = name
        self.attributes = attributes
        self.ended = False

    def end(self) -> None:
        self.ended = True


# Inert paths: these run everywhere, including a plain checkout without OpenTelemetry.


def test_attach_worker_attribute_is_inert_without_opentelemetry(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(telemetry, "_otel_trace", None)
    monkeypatch.setattr(telemetry, "_providers_attributed", [])
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw0")

    telemetry.attach_worker_attribute()

    assert telemetry._providers_attributed == []


def test_attach_worker_attribute_is_inert_outside_a_worker(monkeypatch: pytest.MonkeyPatch) -> None:
    provider = _FakeProvider()
    monkeypatch.setattr(telemetry, "_otel_trace", SimpleNamespace(get_tracer_provider=lambda: provider))
    monkeypatch.setattr(telemetry, "_providers_attributed", [])
    monkeypatch.delenv("PYTEST_XDIST_WORKER", raising=False)

    telemetry.attach_worker_attribute()

    assert provider.processors == []


def test_attach_worker_attribute_is_inert_without_a_configured_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(telemetry, "_otel_trace", SimpleNamespace(get_tracer_provider=lambda: _ProxyProvider()))
    monkeypatch.setattr(telemetry, "_providers_attributed", [])
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw0")

    telemetry.attach_worker_attribute()

    assert telemetry._providers_attributed == []


def test_attach_worker_attribute_attaches_once_and_sets_the_attribute(monkeypatch: pytest.MonkeyPatch) -> None:
    provider = _FakeProvider()
    monkeypatch.setattr(telemetry, "_otel_trace", SimpleNamespace(get_tracer_provider=lambda: provider))
    monkeypatch.setattr(telemetry, "_providers_attributed", [])
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw7")

    telemetry.attach_worker_attribute()
    telemetry.attach_worker_attribute()

    assert len(provider.processors) == 1
    recorded: list[tuple[str, str]] = []
    span = SimpleNamespace(set_attribute=lambda key, value: recorded.append((key, value)))
    provider.processors[0].on_start(span, None)  # type: ignore[attr-defined]
    assert recorded == [(telemetry.XDIST_WORKER_ATTRIBUTE, "gw7")]


def test_record_spans_to_is_inert_without_a_configured_provider(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(telemetry, "_otel_trace", SimpleNamespace(get_tracer_provider=lambda: _ProxyProvider()))

    telemetry.record_spans_to(tmp_path)

    assert not list(tmp_path.iterdir())


def test_record_fixture_use_emits_one_span_per_shared_fixture(monkeypatch: pytest.MonkeyPatch) -> None:
    tracer = _FakeTracer()
    monkeypatch.setattr(telemetry, "_otel_trace", SimpleNamespace(get_tracer=lambda _name: tracer))
    scopes = {"shared_session": "session", "shared_module": "module", "shared_class": "class"}

    def getfixturedefs(name: str, node: object) -> list[SimpleNamespace] | None:
        if name == "empty_fixturedefs":
            return []
        scope = scopes.get(name)
        return [SimpleNamespace(scope=scope)] if scope else None

    item = SimpleNamespace(
        fixturenames=["shared_session", "shared_module", "shared_class", "empty_fixturedefs", "unknown_fixture"],
        nodeid="test_sample.py::test_a",
        session=SimpleNamespace(_fixturemanager=SimpleNamespace(getfixturedefs=getfixturedefs)),
    )

    telemetry.record_fixture_use(item)  # type: ignore[arg-type]

    assert [(span.name, span.attributes["pytest.fixture_scope"]) for span in tracer.spans] == [
        ("shared_session use", "session"),
        ("shared_module use", "module"),
    ]
    assert all(span.ended for span in tracer.spans)
    assert tracer.spans[0].attributes["pytest.span_type"] == "fixture_use"
    assert tracer.spans[0].attributes["pytest.fixture_name"] == "shared_session"
    assert tracer.spans[0].attributes["pytest.nodeid"] == "test_sample.py::test_a"


def test_e2e_conftest_registers_telemetry() -> None:
    conftest = (REPO_ROOT / "tests" / "e2e" / "conftest.py").read_text()
    assert "telemetry.register(config)" in conftest


# Behavior preservation: outcomes with and without the telemetry wiring must match exactly.


def test_telemetry_never_changes_outcomes(tmp_path: Path) -> None:
    wired = run_pytest(tmp_path, OUTCOME_TESTS, args=("-n", "2"))
    baseline = run_pytest(tmp_path, OUTCOME_TESTS, args=("-n", "2"), wired=False)

    assert wired.returncode == 1
    assert baseline.returncode == 1
    assert wired.summary() == baseline.summary()
    assert wired.summary().startswith("1 failed, 1 passed, 1 skipped")


def test_telemetry_stays_silent_without_opentelemetry(tmp_path: Path) -> None:
    if importlib.util.find_spec("opentelemetry") is not None:
        pytest.skip("OpenTelemetry is installed; the silent path needs it absent")

    result = run_pytest(tmp_path, OUTCOME_TESTS, args=("-n", "2"))

    assert result.returncode == 1
    assert result.summary().startswith("1 failed, 1 passed, 1 skipped")
    assert result.workers() == []


# Span contracts: these run only where OpenTelemetry and pytest-opentelemetry are installed.


def test_every_worker_span_carries_the_worker_attribute(xdist_run: Result) -> None:
    assert xdist_run.returncode == 0
    workers = [source for source in xdist_run.workers() if source != "main"]
    assert len(workers) == 2

    names: set[str] = set()
    for worker in workers:
        spans = xdist_run.spans(worker)
        assert spans, f"worker {worker} recorded no spans"
        for span in spans:
            assert span["attributes"][telemetry.XDIST_WORKER_ATTRIBUTE] == worker
        names.update(span["name"] for span in spans)

    # The controller's spans stay unattributed: only workers carry the attribute.
    for span in xdist_run.spans("main"):
        assert telemetry.XDIST_WORKER_ATTRIBUTE not in span["attributes"]

    # pytest-opentelemetry's own span kinds all carry it: the worker span,
    # the test spans, the phase spans, the fixture setup spans, and the
    # runner's wait span, which this module did not create.
    assert any(name.startswith("test worker ") for name in names)
    assert "test_sample.py::test_a" in names
    for phase in ("setup", "call", "teardown"):
        assert f"test_sample.py::test_a::{phase}" in names
    assert "shared_session setup" in names
    assert "worker attribute wait" in names


def test_fixture_use_spans_count_shared_fixture_reuse(xdist_run: Result) -> None:
    workers = [source for source in xdist_run.workers() if source != "main"]
    by_id: dict[str, dict[str, Any]] = {}
    uses: list[dict[str, Any]] = []
    for worker in workers:
        for span in xdist_run.spans(worker):
            by_id[span["span_id"]] = span
            if span["attributes"].get("pytest.span_type") == "fixture_use":
                uses.append(span)

    counted = sorted((span["attributes"]["pytest.nodeid"], span["attributes"]["pytest.fixture_name"]) for span in uses)
    assert counted == [
        ("test_sample.py::test_a", "shared_module"),
        ("test_sample.py::test_a", "shared_session"),
        ("test_sample.py::test_b", "shared_module"),
        ("test_sample.py::test_b", "shared_session"),
        ("test_sample.py::test_c", "shared_session"),
    ]

    for span in uses:
        attributes = span["attributes"]
        assert attributes["pytest.fixture_scope"] in telemetry.SHARED_FIXTURE_SCOPES
        assert attributes[telemetry.XDIST_WORKER_ATTRIBUTE]
        # Each use nests under its test's setup phase span.
        parent = by_id[span["parent_span_id"]]
        assert parent["name"] == f"{attributes['pytest.nodeid']}::setup"

    # Reuse counting: three tests used the session fixture, twice through
    # distinct consumers in test_a, and each use is one span.
    session_uses = [span for span in uses if span["attributes"]["pytest.fixture_name"] == "shared_session"]
    assert len(session_uses) == 3


def test_skipped_setup_is_not_counted_and_serial_spans_stay_unattributed(tmp_path: Path, require_otel: None) -> None:
    result = run_pytest(tmp_path, OUTCOME_TESTS, args=("-n", "0"))

    assert result.returncode == 1
    spans = result.spans("main")
    assert all(telemetry.XDIST_WORKER_ATTRIBUTE not in span["attributes"] for span in spans)

    # test_skips never reached its fixtures, so only test_passes and test_fails count.
    uses = sorted(
        span["attributes"]["pytest.nodeid"]
        for span in spans
        if span["attributes"].get("pytest.span_type") == "fixture_use"
    )
    assert uses == ["test_sample.py::test_fails", "test_sample.py::test_passes"]
