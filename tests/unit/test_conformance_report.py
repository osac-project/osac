"""Contract tests for the conformance report and JUnit writer."""

from __future__ import annotations

from pathlib import Path
from xml.etree import ElementTree

import yaml

from tests.conformance.model import (
    Check,
    CheckResult,
    Classification,
    ComponentResult,
    Layer,
    ProfileResult,
    Report,
    Result,
    TestIdentity,
    TestOutcome,
    Verdict,
)
from tests.conformance.redact import Redactor
from tests.conformance.report import write_junit, write_report

_FAKE_SECRET = "not-a-real-secret"


def _report(message: str = "ok") -> Report:
    identity = TestIdentity(
        id="TC-1",
        name="[networking][component] TC-1: test",
        nodeid="tests/e2e/test_tc.py::test_tc",
        labels=frozenset({"conformance_generic"}),
        domain="networking",
        layer=Layer.COMPONENT,
        profiles=(),
        classification=Classification.BLOCKING,
    )
    return Report(
        osac_release="1.4.0",
        suite_image_digest="sha256:test",
        selectors=("generic", "vmaas"),
        components=(ComponentResult("networking", {"name": "mock"}, Verdict.CONFORMANT),),
        enabled_services=("vmaas",),
        profiles=(ProfileResult("vm", ("vmaas",), True, Result.PASS),),
        tests=(TestOutcome(identity, Result.PASS, 1.5, message, Path()),),
        checks=(
            Check("all-blocking-tests-pass", "every selected blocking test passed", "rule", "remedy", CheckResult.PASS),
            Check("all-components-conformant", "every component is conformant", "rule", "remedy", CheckResult.PASS),
            Check("no-leftovers", "nothing was left behind", "rule", "remedy", CheckResult.PASS),
            Check("complete-plan", "the plan was complete", "rule", "remedy", CheckResult.PASS),
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
        "components",
        "enabledServices",
        "profiles",
        "tests",
        "checks",
        "leftovers",
        "complete",
        "result",
    ]
    assert data["tests"][0]["id"] == "TC-1"
    assert data["tests"][0]["durationSeconds"] == 1.5
    assert data["profiles"][0] == {"name": "vm", "selectors": ["vmaas"], "required": True, "result": "pass"}


def test_report_redacts_messages(tmp_path: Path) -> None:
    path = write_report(_report(f"{_FAKE_SECRET} leaked"), tmp_path, Redactor(secrets=(_FAKE_SECRET,)))

    assert _FAKE_SECRET not in path.read_text()
    assert "<redacted>" in path.read_text()


def test_junit_has_one_testcase_per_test_and_failure_body(tmp_path: Path) -> None:
    report = _report("boom")
    path = write_junit(report, tmp_path, Redactor())

    root = ElementTree.parse(path).getroot()
    assert root.tag == "testsuite"
    assert len(root.findall("testcase")) == 1
    case = root.find("testcase")
    assert case is not None
    assert case.attrib["classname"] == "networking.component"


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
