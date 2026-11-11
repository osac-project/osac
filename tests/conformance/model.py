"""Data model shared by the conformance core."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from tests.conformance.config import Config
    from tests.conformance.output import OutputSink
    from tests.conformance.redact import Redactor


class Layer(str, Enum):
    COMPONENT = "component"
    PROFILE = "profile"


class Result(str, Enum):
    PASS = "pass"
    FAIL = "fail"
    SKIP = "skip"


class Verdict(str, Enum):
    CONFORMANT = "conformant"
    IN_PROGRESS = "in progress"


class CheckResult(str, Enum):
    PASS = "pass"
    FAIL = "fail"


class Classification(str, Enum):
    BLOCKING = "blocking"
    INFORMING = "informing"


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
    area: str
    layer: Layer
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
    selectors: tuple[str, ...]
    complete: bool


@dataclass(frozen=True)
class Check:
    id: str
    name: str
    rule: str
    remedy: str
    result: CheckResult


@dataclass(frozen=True)
class ProfileResult:
    name: str
    selectors: tuple[str, ...]
    required: bool
    result: Result


@dataclass(frozen=True)
class ComponentResult:
    area: str
    identity: Mapping[str, Any]
    result: Verdict


@dataclass(frozen=True)
class Report:
    osac_release: str
    suite_image_digest: str
    selectors: tuple[str, ...]
    components: tuple[ComponentResult, ...]
    enabled_services: tuple[str, ...]
    profiles: tuple[ProfileResult, ...]
    tests: tuple[TestOutcome, ...]
    checks: tuple[Check, ...]
    leftovers: tuple[str, ...]
    complete: bool
    result: Verdict
