"""Apply conformance result rules and named checks."""

from __future__ import annotations

from collections.abc import Sequence
from pathlib import Path

from tests.conformance.model import (
    Check,
    CheckResult,
    Classification,
    ComponentResult,
    Plan,
    ProfileResult,
    Report,
    Result,
    TestOutcome,
    Verdict,
)


def _outcome_for(plan: Plan, by_id: dict[str, TestOutcome], test_id: str) -> TestOutcome:
    outcome = by_id.get(test_id)
    if outcome is not None:
        return outcome
    identity = next(test for test in plan.tests if test.id == test_id)
    return TestOutcome(identity, Result.SKIP, 0.0, "did not run", Path())


def evaluate(
    plan: Plan,
    outcomes: Sequence[TestOutcome],
    profiles: Sequence[ProfileResult],
    components: Sequence[ComponentResult],
    leftovers: Sequence[str],
    *,
    osac_release: str,
    suite_image_digest: str,
    enabled_services: Sequence[str],
    area_checks: Sequence[Check] = (),
) -> Report:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    tests = tuple(_outcome_for(plan, by_id, test.id) for test in plan.tests)
    blocking_pass = all(
        outcome.result is Result.PASS for outcome in tests if outcome.identity.classification is Classification.BLOCKING
    )
    checks = [
        Check(
            id="CHECK-1",
            name="every selected blocking test passed",
            rule="every blocking test in the plan has result pass",
            remedy="see the failed tests and their evidence",
            result=CheckResult.PASS if blocking_pass else CheckResult.FAIL,
        ),
        Check(
            id="CHECK-2",
            name="every component is conformant",
            rule="at least one component is registered and every component has result conformant",
            remedy="see the component's failed tests",
            result=CheckResult.PASS
            if components and all(component.result is Verdict.CONFORMANT for component in components)
            else CheckResult.FAIL,
        ),
        Check(
            id="CHECK-3",
            name="nothing was left behind",
            rule="leftovers is empty",
            remedy="delete each leftover",
            result=CheckResult.PASS if not leftovers else CheckResult.FAIL,
        ),
        Check(
            id="CHECK-4",
            name="the plan was complete",
            rule="complete is true",
            remedy="run the plan a full list computes",
            result=CheckResult.PASS if plan.complete else CheckResult.FAIL,
        ),
        *area_checks,
    ]
    overall = Verdict.CONFORMANT if all(check.result is CheckResult.PASS for check in checks) else Verdict.IN_PROGRESS
    return Report(
        osac_release=osac_release,
        suite_image_digest=suite_image_digest,
        selectors=plan.selectors,
        components=tuple(components),
        enabled_services=tuple(enabled_services),
        profiles=tuple(profiles),
        tests=tests,
        checks=tuple(checks),
        leftovers=tuple(leftovers),
        complete=plan.complete,
        result=overall,
    )


def exit_code(report: Report, *, focused: bool = False) -> int:
    if report.result is Verdict.CONFORMANT:
        return 0
    if focused and not report.leftovers:
        selected_blocking = [test for test in report.tests if test.identity.classification is Classification.BLOCKING]
        if all(test.result is Result.PASS for test in selected_blocking):
            return 0
    return 1
