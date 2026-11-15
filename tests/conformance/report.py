"""Write the conformance YAML report and JUnit XML."""

from __future__ import annotations

from pathlib import Path
from typing import Any
from xml.etree import ElementTree

import yaml

from tests.conformance.model import CheckResult, Report, Result
from tests.conformance.redact import Redactor


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
            {"name": redactor.text(test.identity.name), "classname": "conformance", "time": str(test.duration_seconds)},
        )
        if test.result is Result.FAIL:
            log_path = test.evidence_dir / "test.log"
            body = log_path.read_text(encoding="utf-8") if log_path.is_file() else test.message
            failure = ElementTree.SubElement(case, "failure", {"message": redactor.text(test.message)})
            failure.text = redactor.text(body)
        elif test.result is Result.SKIP:
            ElementTree.SubElement(case, "skipped", {"message": redactor.text(test.message)})
    tree = ElementTree.ElementTree(suite)
    path = results_dir / "junit.xml"
    results_dir.mkdir(parents=True, exist_ok=True)
    tree.write(path, encoding="utf-8", xml_declaration=True)
    return path


def checks_pass(report: Report) -> bool:
    return all(check.result is CheckResult.PASS for check in report.checks)
