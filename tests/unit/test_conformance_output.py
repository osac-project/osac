"""Contract tests for live output, evidence and the plan writer."""

from __future__ import annotations

import re
from pathlib import Path

import pytest
import yaml

from tests.conformance.model import Classification, Plan, Selector, TestIdentity
from tests.conformance.output import OutputSink, new_run_id, plan_key, write_plan
from tests.conformance.redact import Redactor

_REDACTION_MARKER = "redaction-marker"


def _test(test_id: str = "TC-1", *, labels: frozenset[str] = frozenset({"conformance_generic"})) -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"{test_id}: test",
        nodeid="tests/e2e/test_tc.py::test_tc",
        labels=labels,
        profiles=(),
        classification=Classification.BLOCKING,
    )


def test_run_id_is_eight_lowercase_alphanumeric() -> None:
    assert re.fullmatch(r"[a-z0-9]{8}", new_run_id())


def test_output_sink_writes_stdout_and_log_with_timestamp_and_redaction(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    sink.line(f"token {_REDACTION_MARKER}")

    log = (tmp_path / "conformance.log").read_text()
    assert _REDACTION_MARKER not in log
    assert "<redacted>" in log
    assert re.match(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d", log)


def test_evidence_is_redacted_and_confined(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    path = sink.evidence(_test(), Path("api.log"), f"token {_REDACTION_MARKER}")

    assert path.read_text() == "token <redacted>"
    with pytest.raises(ValueError):
        sink.evidence(_test(), Path("../escape"), "x")


def test_write_plan_records_selectors_and_tests(tmp_path: Path) -> None:
    plan = Plan((_test(),), ("generic",), (Selector("generic", "marker", ("conformance_generic",)),), True)

    data = yaml.safe_load(write_plan(plan, tmp_path, Redactor()).read_text())

    assert data["profiles"] == ["generic"]
    assert data["selectors"] == [{"id": "generic", "type": "marker", "markers": ["conformance_generic"]}]
    assert data["complete"] is True
    recorded = data["tests"][0]
    assert recorded["id"] == "TC-1"
    assert recorded["name"] == "TC-1: test"
    assert recorded["key"] == plan_key("TC-1")
    assert recorded["markers"] == ["conformance_generic"]


def test_write_plan_omits_the_non_blocking_marker(tmp_path: Path) -> None:
    test = _test(labels=frozenset({"conformance_network", "conformance_non_blocking"}))
    plan = Plan((test,), ("network",), (Selector("network", "marker", ("conformance_network",)),), True)

    data = yaml.safe_load(write_plan(plan, tmp_path, Redactor()).read_text())

    assert data["tests"][0]["markers"] == ["conformance_network"]


def test_write_plan_redacts_test_ids_and_names(tmp_path: Path) -> None:
    plan = Plan(
        (_test(f"TC-1[{_REDACTION_MARKER}]"),),
        ("generic",),
        (Selector("generic", "marker", ("conformance_generic",)),),
        True,
    )

    path = write_plan(plan, tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    text = path.read_text()
    assert _REDACTION_MARKER not in text
    assert "<redacted>" in text


def test_evidence_directory_does_not_repeat_the_test_id(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    path = sink.evidence(_test(f"TC-1[{_REDACTION_MARKER}]"), Path("test.log"), "x")

    assert _REDACTION_MARKER not in str(path)
    assert path.parent.name.isalnum()


def test_evidence_directory_name_is_bounded_for_long_test_ids(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor())
    long_id = "tests/test_sample.py::test_case[" + "x" * 400 + "]"

    path = sink.evidence(_test(long_id), Path("test.log"), "x")

    assert len(path.parent.name.encode("utf-8")) <= 255


def test_echo_does_not_create_the_run_log(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor())

    sink.echo("plan contents")

    assert not (tmp_path / "conformance.log").exists()


def test_evidence_directories_are_unique_per_test_id(tmp_path: Path) -> None:
    sink = OutputSink(tmp_path, Redactor())

    first = sink.evidence(_test("test_case[a/b]"), Path("test.log"), "first")
    second = sink.evidence(_test("test_case[a:b]"), Path("test.log"), "second")

    assert first.parent != second.parent
    assert first.read_text() == "first"
    assert second.read_text() == "second"
