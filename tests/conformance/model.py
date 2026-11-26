"""Data model shared by the conformance core."""

from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from tests.conformance.config import Config
    from tests.conformance.output import OutputSink
    from tests.conformance.redact import Redactor


# Pytest markers that the core reads by name. Selection markers come from the
# catalog; these two spell the family prefix and the one classification marker
# a test can carry (a failure is reported but never fails a check).
MARKER_PREFIX = "conformance_"
NON_BLOCKING_MARKER = f"{MARKER_PREFIX}non_blocking"


class Result(StrEnum):
    PASS = "pass"
    FAIL = "fail"
    SKIP = "skip"


class Verdict(StrEnum):
    CONFORMANT = "conformant"
    IN_PROGRESS = "in progress"


class CheckResult(StrEnum):
    PASS = "pass"
    FAIL = "fail"


class Classification(StrEnum):
    BLOCKING = "blocking"
    NON_BLOCKING = "non_blocking"


@dataclass(frozen=True)
class Selector:
    id: str
    type: str
    markers: tuple[str, ...]


@dataclass(frozen=True)
class RunContext:
    run_id: str
    results_dir: Path
    config: Config
    tenant: str | None = None
    redactor: Redactor | None = None
    output: OutputSink | None = None


@dataclass(frozen=True)
class TestIdentity:
    __test__ = False

    id: str
    name: str
    nodeid: str
    labels: frozenset[str]
    profiles: tuple[str, ...]
    classification: Classification


@dataclass(frozen=True)
class TestOutcome:
    __test__ = False

    identity: TestIdentity
    result: Result
    duration_seconds: float
    message: str
    evidence_dir: Path


@dataclass(frozen=True)
class Plan:
    tests: tuple[TestIdentity, ...]
    profiles: tuple[str, ...]
    selectors: tuple[Selector, ...]
    complete: bool


@dataclass(frozen=True)
class Check:
    id: str
    name: str
    rule: str
    remedy: str
    result: CheckResult
    message: str = ""


@dataclass(frozen=True)
class ProfileResult:
    id: str
    selectors: tuple[str, ...]
    required: bool
    result: Result


@dataclass(frozen=True)
class Report:
    osac_release: str
    suite_image_digest: str
    selectors: tuple[Selector, ...]
    profiles: tuple[ProfileResult, ...]
    tests: tuple[TestOutcome, ...]
    checks: tuple[Check, ...]
    leftovers: tuple[str, ...]
    complete: bool
    result: Verdict
