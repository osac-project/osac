"""Contract tests for conformance result rules and named checks."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.model import (
    CheckResult,
    Classification,
    ComponentResult,
    Layer,
    Plan,
    ProfileResult,
    Report,
    Result,
    TestIdentity,
    TestOutcome,
    Verdict,
)
from tests.conformance.verdict import evaluate, exit_code


def _test(test_id: str, classification: Classification = Classification.BLOCKING) -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"[networking][component] {test_id}: test",
        nodeid=f"tests/e2e/test_{test_id}.py::test_{test_id}",
        labels=frozenset({"conformance_generic"}),
        area="networking",
        layer=Layer.COMPONENT,
        profile=None,
        classification=classification,
    )


def _outcome(test: TestIdentity, result: Result, message: str = "") -> TestOutcome:
    return TestOutcome(test, result, 1.0, message, Path())


def _evaluate(
    outcomes: list[TestOutcome],
    *,
    complete: bool = True,
    leftovers: tuple[str, ...] = (),
    components: tuple[ComponentResult, ...] | None = None,
    profiles: tuple[ProfileResult, ...] = (),
) -> Report:
    plan = Plan(tuple(outcome.identity for outcome in outcomes), ("generic",), complete)
    if components is None:
        components = (ComponentResult("networking", {"name": "mock"}, Verdict.CONFORMANT),)
    return evaluate(
        plan,
        outcomes,
        profiles,
        components,
        leftovers,
        osac_release="1.4.0",
        suite_image_digest="sha256:test",
        enabled_services=("vmaas",),
    )


def test_all_passing_checks_are_conformant() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)])

    assert [check.result for check in report.checks] == [CheckResult.PASS] * 4
    assert report.result is Verdict.CONFORMANT
    assert exit_code(report) == 0


def test_a_blocking_failure_fails_check_1() -> None:
    report = _evaluate([_outcome(_test("A"), Result.FAIL, "boom")])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report) == 1


def test_a_blocking_skip_is_not_a_pass() -> None:
    report = _evaluate([_outcome(_test("A"), Result.SKIP)])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS


def test_an_informing_failure_does_not_fail_a_check() -> None:
    report = _evaluate([_outcome(_test("A", Classification.INFORMING), Result.FAIL)])

    assert report.checks[0].result is CheckResult.PASS
    assert report.result is Verdict.CONFORMANT


def test_a_leftover_fails_check_3() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], leftovers=("vm/abc",))

    assert report.checks[2].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS


def test_an_incomplete_plan_fails_check_4() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], complete=False)

    assert report.checks[3].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS


def test_no_component_fails_check_2() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], components=())

    assert report.checks[1].result is CheckResult.FAIL


def test_an_in_progress_component_fails_check_2() -> None:
    report = _evaluate(
        [_outcome(_test("A"), Result.PASS)],
        components=(ComponentResult("networking", {"name": "mock"}, Verdict.IN_PROGRESS),),
    )

    assert report.checks[1].result is CheckResult.FAIL


def test_focused_run_exits_zero_when_selected_blocking_tests_pass() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], complete=False)

    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report, focused=True) == 0


def test_focused_run_exits_one_when_a_selected_blocking_test_fails() -> None:
    report = _evaluate([_outcome(_test("A"), Result.FAIL)], complete=False)

    assert exit_code(report, focused=True) == 1


def test_selected_profile_with_no_blocking_test_is_not_a_pass() -> None:
    profile = ProfileResult("vm", "networking", True, Result.SKIP)
    report = _evaluate([_outcome(_test("A"), Result.PASS)], profiles=(profile,))

    assert report.profiles[0].result is Result.SKIP
