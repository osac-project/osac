"""Contract tests for conformance result rules and named checks."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.model import (
    CheckResult,
    Classification,
    Plan,
    ProfileResult,
    Report,
    Result,
    Selector,
    TestIdentity,
    TestOutcome,
    Verdict,
)
from tests.conformance.verdict import evaluate, exit_code


def _test(test_id: str, classification: Classification = Classification.BLOCKING) -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"{test_id}: test",
        nodeid=f"tests/e2e/test_{test_id}.py::test_{test_id}",
        labels=frozenset({"conformance_generic"}),
        profiles=("generic",),
        classification=classification,
    )


def _outcome(test: TestIdentity, result: Result, message: str = "") -> TestOutcome:
    return TestOutcome(test, result, 1.0, message, Path())


def _evaluate(
    outcomes: list[TestOutcome],
    *,
    complete: bool = True,
    leftovers: tuple[str, ...] = (),
    profiles: tuple[ProfileResult, ...] = (),
) -> Report:
    plan = Plan(
        tuple(outcome.identity for outcome in outcomes),
        ("generic",),
        (Selector("generic", "marker", ("conformance_generic",)),),
        complete,
    )
    return evaluate(plan, outcomes, profiles, leftovers, osac_release="1.4.0", suite_image_digest="sha256:test")


def test_all_passing_checks_are_conformant() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)])

    assert [check.result for check in report.checks] == [CheckResult.PASS] * 3
    assert report.checks[0].message == "1 blocking test passed"
    assert report.checks[1].message == "no leftovers"
    assert report.checks[2].message == "plan contained all 1 selected test"
    assert report.result is Verdict.CONFORMANT
    assert exit_code(report) == 0


def test_a_blocking_failure_fails_all_blocking_tests_pass() -> None:
    report = _evaluate([_outcome(_test("A"), Result.FAIL, "boom")])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.checks[0].message == "1 blocking test failed: A"
    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report) == 1


def test_a_blocking_skip_is_not_a_pass() -> None:
    report = _evaluate([_outcome(_test("A"), Result.SKIP)])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS


def test_a_plan_without_blocking_tests_fails_the_blocking_check() -> None:
    report = _evaluate([_outcome(_test("A", Classification.INFORMING), Result.FAIL)])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.checks[0].message == "no blocking tests in the plan"
    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report) == 1


def test_informing_tests_alone_are_not_a_conformance_verdict() -> None:
    report = _evaluate([_outcome(_test("A", Classification.INFORMING), Result.PASS)])

    assert report.checks[0].result is CheckResult.FAIL
    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report, focused=True) == 1


def test_a_leftover_fails_no_leftovers() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], leftovers=("vm/abc",))

    assert report.checks[1].result is CheckResult.FAIL
    assert report.checks[1].message == "1 leftover: vm/abc"
    assert report.result is Verdict.IN_PROGRESS


def test_an_incomplete_plan_fails_complete_plan() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], complete=False)

    assert report.checks[2].result is CheckResult.FAIL
    assert report.checks[2].message == "the plan is a focused subset and is not a conformance verdict"
    assert report.checks[2].remedy == "run without --plan, or use the full plan that osac-conformance list prints"
    assert report.result is Verdict.IN_PROGRESS


def test_focused_run_exits_zero_when_selected_blocking_tests_pass() -> None:
    report = _evaluate([_outcome(_test("A"), Result.PASS)], complete=False)

    assert report.result is Verdict.IN_PROGRESS
    assert exit_code(report, focused=True) == 0


def test_focused_run_exits_one_when_a_selected_blocking_test_fails() -> None:
    report = _evaluate([_outcome(_test("A"), Result.FAIL)], complete=False)

    assert exit_code(report, focused=True) == 1


def test_selected_profile_with_no_blocking_test_is_not_a_pass() -> None:
    profile = ProfileResult("vm", ("vmaas",), True, Result.SKIP)
    report = _evaluate([_outcome(_test("A"), Result.PASS)], profiles=(profile,))

    assert report.profiles[0].result is Result.SKIP
