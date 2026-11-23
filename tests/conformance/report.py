"""Write the conformance YAML report and JUnit XML."""

from __future__ import annotations

from pathlib import Path
from typing import Any
from xml.etree import ElementTree

import yaml

from tests.conformance.model import CheckResult, Report, Result
from tests.conformance.redact import Redactor

# XML 1.0 forbids most C0 controls, lone surrogates and the two non-characters at
# the end of the BMP. Captured test output routinely carries ANSI escapes (0x1B),
# which would otherwise make the JUnit report invalid XML.
_XML_ILLEGAL = {
    code: None for code in (*range(0x00, 0x09), 0x0B, 0x0C, *range(0x0E, 0x20), *range(0xD800, 0xE000), 0xFFFE, 0xFFFF)
}


def _xml_text(value: str) -> str:
    return value.translate(_XML_ILLEGAL)


def _redacted_xml(value: str, redactor: Redactor) -> str:
    """Strip XML-illegal characters, then redact.

    Stripping first: a control character inside a secret splits it so the
    redactor misses it, and the character is dropped from the file anyway.
    """
    return _xml_text(redactor.text(_xml_text(value)))


def report_document(report: Report, redactor: Redactor) -> dict[str, Any]:
    return {
        "osacRelease": report.osac_release,
        "suiteImageDigest": report.suite_image_digest,
        "selectors": [
            {"id": selector.id, "type": selector.type, "markers": list(selector.markers)}
            for selector in report.selectors
        ],
        "profiles": [
            {
                "id": profile.id,
                "selectors": list(profile.selectors),
                "required": profile.required,
                "result": profile.result.value,
            }
            for profile in report.profiles
        ],
        "tests": [
            {
                "id": test.identity.id,
                "name": redactor.text(test.identity.name),
                "profiles": list(test.identity.profiles),
                "classification": test.identity.classification.value,
                "result": test.result.value,
                "durationSeconds": test.duration_seconds,
                "message": redactor.text(test.message),
            }
            for test in report.tests
        ],
        "checks": [
            {
                "id": check.id,
                "name": check.name,
                "rule": check.rule,
                "remedy": check.remedy,
                "result": check.result.value,
                "message": check.message,
            }
            for check in report.checks
        ],
        "leftovers": list(report.leftovers),
        "complete": report.complete,
        "result": report.result.value,
    }


def write_report(report: Report, results_dir: Path, redactor: Redactor) -> Path:
    results_dir.mkdir(parents=True, exist_ok=True)
    path = results_dir / "conformance-report.yaml"
    document = redactor.obj(report_document(report, redactor))
    path.write_text(yaml.safe_dump(document, sort_keys=False), encoding="utf-8")
    return path


def write_junit(report: Report, results_dir: Path, redactor: Redactor) -> Path:
    failures = sum(1 for test in report.tests if test.result is Result.FAIL)
    skipped = sum(1 for test in report.tests if test.result is Result.SKIP)
    suite = ElementTree.Element(
        "testsuite",
        {
            "name": "osac-conformance",
            "tests": str(len(report.tests)),
            "failures": str(failures),
            "skipped": str(skipped),
        },
    )
    for test in report.tests:
        case = ElementTree.SubElement(
            suite,
            "testcase",
            {
                "name": _redacted_xml(test.identity.name, redactor),
                "classname": "conformance",
                "time": str(test.duration_seconds),
            },
        )
        if test.result is Result.FAIL:
            log_path = test.evidence_dir / "test.log"
            body = log_path.read_text(encoding="utf-8") if log_path.is_file() else test.message
            failure = ElementTree.SubElement(case, "failure", {"message": _redacted_xml(test.message, redactor)})
            failure.text = _redacted_xml(body, redactor)
        elif test.result is Result.SKIP:
            ElementTree.SubElement(case, "skipped", {"message": _redacted_xml(test.message, redactor)})
    tree = ElementTree.ElementTree(suite)
    path = results_dir / "junit.xml"
    results_dir.mkdir(parents=True, exist_ok=True)
    tree.write(path, encoding="utf-8", xml_declaration=True)
    return path


def checks_pass(report: Report) -> bool:
    return all(check.result is CheckResult.PASS for check in report.checks)
