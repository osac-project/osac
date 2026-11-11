"""Live output, evidence layout and plan files."""

from __future__ import annotations

import re
import secrets
import string
import sys
from datetime import UTC, datetime
from pathlib import Path
from typing import TextIO

import yaml

from tests.conformance.model import Plan, TestIdentity
from tests.conformance.redact import Redactor

_RUN_ID_ALPHABET = string.ascii_lowercase + string.digits


def new_run_id() -> str:
    return "".join(secrets.choice(_RUN_ID_ALPHABET) for _ in range(8))


def _safe_component(value: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", value).strip("._") or "test"


class OutputSink:
    def __init__(self, results_dir: Path, redactor: Redactor, *, stream: TextIO | None = None) -> None:
        self.results_dir = results_dir
        self.redactor = redactor
        self.stream = stream if stream is not None else sys.stdout
        self.results_dir.mkdir(parents=True, exist_ok=True)
        self.log_path = self.results_dir / "conformance.log"

    def line(self, text: str) -> None:
        stamp = datetime.now(UTC).isoformat(timespec="seconds")
        rendered = f"{stamp} {self.redactor.text(text)}"
        print(rendered, file=self.stream)
        with self.log_path.open("a", encoding="utf-8") as stream:
            stream.write(rendered + "\n")

    def evidence(self, test: TestIdentity, relative: Path, content: str) -> Path:
        base = (self.results_dir / "tests" / _safe_component(test.id)).resolve()
        candidate = (base / relative).resolve()
        if not candidate.is_relative_to(base):
            raise ValueError(f"evidence path escapes the test directory: {relative}")
        candidate.parent.mkdir(parents=True, exist_ok=True)
        candidate.write_text(self.redactor.text(content), encoding="utf-8")
        return candidate


def _test_selectors(test: TestIdentity) -> list[str]:
    selectors: list[str] = []
    for label in sorted(test.labels):
        if label.startswith("conformance/"):
            selectors.append(label.split("/", 1)[1])
        elif label.startswith("conformance_") and not label.startswith(
            ("conformance_blocking", "conformance_informing")
        ):
            selectors.append(label.removeprefix("conformance_"))
    return selectors


def plan_document(plan: Plan) -> dict[str, object]:
    return {
        "selectors": list(plan.selectors),
        "tests": [{"id": test.id, "name": test.name, "selectors": _test_selectors(test)} for test in plan.tests],
        "complete": plan.complete,
    }


def write_plan(plan: Plan, results_dir: Path) -> Path:
    results_dir.mkdir(parents=True, exist_ok=True)
    path = results_dir / "conformance-plan.yaml"
    path.write_text(yaml.safe_dump(plan_document(plan), sort_keys=False), encoding="utf-8")
    return path
