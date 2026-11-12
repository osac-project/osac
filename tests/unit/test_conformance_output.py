"""Contract tests for live output, evidence and the plan writer."""

from __future__ import annotations

import re
from pathlib import Path

import pytest

from tests.conformance.model import Classification, Layer, Plan, TestIdentity
from tests.conformance.output import OutputSink, new_run_id, write_plan
from tests.conformance.redact import Redactor

_FAKE_SECRET = "not-a-real-secret"


def _test() -> TestIdentity:
    return TestIdentity(
        id="TC-1",
        name="[networking][component] TC-1: test",
        nodeid="tests/e2e/test_tc.py::test_tc",
        labels=frozenset({"conformance_generic"}),
        area="networking",
        layer=Layer.COMPONENT,
        profiles=(),
        classification=Classification.BLOCKING,
    )


def test_run_id_is_eight_lowercase_alphanumeric() -> None:
    assert re.fullmatch(r"[a-z0-9]{8}", new_run_id())


def test_output_sink_writes_stdout_and_log_with_timestamp_and_redaction(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor(secrets=(_FAKE_SECRET,)))

    sink.line(f"token {_FAKE_SECRET}")

    log = (tmp_path / "conformance.log").read_text()
    assert _FAKE_SECRET not in log
    assert "<redacted>" in log
    assert re.match(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d", log)


def test_evidence_is_redacted_and_confined(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor(secrets=(_FAKE_SECRET,)))

    path = sink.evidence(_test(), Path("api.log"), f"token {_FAKE_SECRET}")

    assert path.read_text() == "token <redacted>"
    with pytest.raises(ValueError):
        sink.evidence(_test(), Path("../escape"), "x")


def test_write_plan_records_selectors_and_tests(tmp_path: Path) -> None:
    plan = Plan((_test(),), ("generic",), ("generic",), True)

    path = write_plan(plan, tmp_path)

    text = path.read_text()
    assert "selectors:" in text
    assert "TC-1" in text
    assert "complete: true" in text
