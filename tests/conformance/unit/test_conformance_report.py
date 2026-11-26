"""Contract tests for the conformance report and JUnit writer."""

from __future__ import annotations

from pathlib import Path
from xml.etree import ElementTree

import yaml

from tests.conformance.model import (
    Check,
    CheckResult,
    Classification,
    ProfileResult,
    Report,
    Result,
    Selector,
    TestIdentity,
    TestOutcome,
    Verdict,
)
from tests.conformance.redact import Redactor
from tests.conformance.report import write_junit, write_report

_REDACTION_MARKER = "redaction-marker"


def _report(message: str = "ok") -> Report:
    identity = TestIdentity(
        id="TC-1",
        name="TC-1: test",
        nodeid="tests/e2e/test_tc.py::test_tc",
        labels=frozenset({"conformance_generic"}),
        profiles=("generic",),
        classification=Classification.BLOCKING,
    )
    return Report(
        osac_release="1.4.0",
        suite_image_digest="sha256:test",
        selectors=(
            Selector("generic", "marker", ("conformance_generic",)),
            Selector("vmaas", "marker", ("conformance_vmaas",)),
        ),
        profiles=(ProfileResult("vm", ("vmaas",), True, Result.PASS),),
        tests=(TestOutcome(identity, Result.PASS, 1.5, message, Path()),),
        checks=(
            Check(
                "all-blocking-tests-pass",
                "every selected blocking test passed",
                "rule",
                "remedy",
                CheckResult.PASS,
                "1 blocking test passed",
            ),
            Check("no-leftovers", "nothing was left behind", "rule", "remedy", CheckResult.PASS, "no leftovers"),
            Check(
                "complete-plan",
                "the plan was complete",
                "rule",
                "remedy",
                CheckResult.PASS,
                "plan contained all 1 selected test",
            ),
        ),
        leftovers=(),
        complete=True,
        result=Verdict.CONFORMANT,
    )


def test_report_has_the_exact_top_level_keys(tmp_path: Path) -> None:
    path = write_report(_report(), tmp_path, Redactor())

    data = yaml.safe_load(path.read_text())
    assert list(data) == [
        "osacRelease",
        "suiteImageDigest",
        "selectors",
        "profiles",
        "tests",
        "checks",
        "leftovers",
        "complete",
        "result",
    ]
    assert data["tests"][0]["id"] == "TC-1"
    assert data["checks"][0]["message"] == "1 blocking test passed"
    assert data["tests"][0]["durationSeconds"] == 1.5
    assert data["profiles"][0] == {"id": "vm", "selectors": ["vmaas"], "required": True, "result": "pass"}
    assert data["selectors"][0] == {"id": "generic", "type": "marker", "markers": ["conformance_generic"]}


def test_report_redacts_messages(tmp_path: Path) -> None:
    path = write_report(_report(f"{_REDACTION_MARKER} leaked"), tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    assert _REDACTION_MARKER not in path.read_text()
    assert "<redacted>" in path.read_text()


def test_junit_has_one_testcase_per_test_and_failure_body(tmp_path: Path) -> None:
    report = _report("boom")
    path = write_junit(report, tmp_path, Redactor())

    root = ElementTree.parse(path).getroot()
    assert root.tag == "testsuite"
    assert len(root.findall("testcase")) == 1
    case = root.find("testcase")
    assert case is not None
    assert case.attrib["classname"] == "conformance"


def test_junit_marks_a_failure(tmp_path: Path) -> None:
    report = _report("boom")
    failed = Report(
        **{**report.__dict__, "tests": (TestOutcome(report.tests[0].identity, Result.FAIL, 1.0, "boom", Path()),)}
    )
    path = write_junit(failed, tmp_path, Redactor())

    root = ElementTree.parse(path).getroot()
    assert root.attrib["failures"] == "1"
    failure = root.find("testcase/failure")
    assert failure is not None
    assert failure.attrib["message"] == "boom"


def test_junit_failure_body_uses_the_test_log(tmp_path: Path) -> None:
    report = _report("boom")
    evidence = tmp_path / "evidence"
    evidence.mkdir()
    (evidence / "test.log").write_text("captured log body")
    failed = Report(
        **{**report.__dict__, "tests": (TestOutcome(report.tests[0].identity, Result.FAIL, 1.0, "boom", evidence),)}
    )

    path = write_junit(failed, tmp_path, Redactor())

    root = ElementTree.parse(path).getroot()
    failure = root.find("testcase/failure")
    assert failure is not None
    assert failure.text == "captured log body"


def test_report_redacts_check_messages_and_leftovers(tmp_path: Path) -> None:
    marker = "redaction-marker-z"
    report = _report()
    check = Check("extra", "extra", "rule", "remedy", CheckResult.FAIL, f"failed {marker}")
    report = Report(**{**report.__dict__, "checks": (*report.checks, check), "leftovers": (f"kind/{marker}",)})

    path = write_report(report, tmp_path, Redactor(secrets=(marker,)))

    text = path.read_text()
    assert marker not in text
    assert "<redacted>" in text


def test_junit_strips_xml_illegal_characters(tmp_path: Path) -> None:
    report = _report("boom")
    message = "boom \x1b[31mred\x1b[0m \x00 \x07 \ud800 \ufffe \uffff"
    failed = Report(
        **{**report.__dict__, "tests": (TestOutcome(report.tests[0].identity, Result.FAIL, 1.0, message, Path()),)}
    )

    path = write_junit(failed, tmp_path, Redactor())

    raw = path.read_bytes()
    assert b"\x1b" not in raw
    assert b"\x00" not in raw
    written = path.read_text(encoding="utf-8")
    assert "\ufffe" not in written
    assert "\uffff" not in written
    root = ElementTree.parse(path).getroot()
    failure = root.find("testcase/failure")
    assert failure is not None
    assert "\x1b" not in failure.attrib["message"]


def test_junit_redacts_a_secret_split_by_a_control_character(tmp_path: Path) -> None:
    secret = "alphaomega"
    split = f"{secret[:5]}\x00{secret[5:]}"
    report = _report(split)
    outcome = TestOutcome(report.tests[0].identity, Result.FAIL, 1.0, split, Path())
    failed = Report(**{**report.__dict__, "tests": (outcome,)})

    path = write_junit(failed, tmp_path, Redactor(secrets=(secret,)))

    assert secret not in path.read_text(encoding="utf-8")
    failure = ElementTree.parse(path).getroot().find("testcase/failure")
    assert failure is not None
    assert failure.text == "<redacted>"
