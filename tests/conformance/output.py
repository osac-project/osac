"""Live output, evidence layout and plan files."""

from __future__ import annotations

import hashlib
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


def _digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def safe_component(value: str) -> str:
    """An opaque, bounded path component for a test ID.

    A parametrized ID can embed a value copied from the target environment, so
    the directory name must not repeat it. The digest also keeps IDs such as
    ``test_case[a/b]`` and ``test_case[a:b]`` in separate directories and stays
    well inside the filesystem's name limit no matter how long the ID is.
    """
    return _digest(value)[:16]


def plan_key(test_id: str) -> str:
    """A stable, non-reversible handle that lets a written plan be replayed."""
    return _digest(test_id)


class OutputSink:
    def __init__(self, results_dir: Path, redactor: Redactor, *, stream: TextIO | None = None) -> None:
        self.results_dir = results_dir
        self.redactor = redactor
        self.stream = stream if stream is not None else sys.stdout
        self.results_dir.mkdir(parents=True, exist_ok=True)
        self.log_path = self.results_dir / "conformance.log"

    def _render(self, text: str) -> str:
        stamp = datetime.now(UTC).isoformat(timespec="seconds")
        return f"{stamp} {self.redactor.text(text)}"

    def line(self, text: str) -> None:
        rendered = self._render(text)
        print(rendered, file=self.stream)
        with self.log_path.open("a", encoding="utf-8") as stream:
            stream.write(rendered + "\n")

    def echo(self, text: str) -> None:
        """Print redacted text without creating the run log.

        ``list`` must not leave a ``conformance.log`` behind, because the same
        results directory has to accept the later ``run --plan``.
        """
        print(self._render(text), file=self.stream)

    def evidence(self, test: TestIdentity, relative: Path, content: str) -> Path:
        base = (self.results_dir / "tests" / safe_component(test.id)).resolve()
        candidate = (base / relative).resolve()
        if not candidate.is_relative_to(base):
            raise ValueError(f"evidence path escapes the test directory: {relative}")
        candidate.parent.mkdir(parents=True, exist_ok=True)
        redacted = self.redactor.text(content)
        candidate.write_text(redacted, encoding="utf-8")
        return candidate


def _test_markers(test: TestIdentity) -> list[str]:
    markers: list[str] = []
    for label in sorted(test.labels):
        if (
            label.startswith("conformance/") and not label.startswith(("conformance/blocking", "conformance/informing"))
        ) or (
            label.startswith("conformance_") and not label.startswith(("conformance_blocking", "conformance_informing"))
        ):
            markers.append(label)
    return markers


def plan_document(plan: Plan, redactor: Redactor) -> dict[str, object]:
    return {
        "profiles": list(plan.profiles),
        "selectors": [
            {"id": selector.id, "type": selector.type, "markers": list(selector.markers)} for selector in plan.selectors
        ],
        "tests": [
            {
                "id": redactor.text(test.id),
                "key": plan_key(test.id),
                "name": redactor.text(test.name),
                "profiles": list(test.profiles),
                "markers": _test_markers(test),
            }
            for test in plan.tests
        ],
        "complete": plan.complete,
    }


def write_plan(plan: Plan, results_dir: Path, redactor: Redactor) -> Path:
    results_dir.mkdir(parents=True, exist_ok=True)
    path = results_dir / "conformance-plan.yaml"
    path.write_text(yaml.safe_dump(plan_document(plan, redactor), sort_keys=False), encoding="utf-8")
    return path
