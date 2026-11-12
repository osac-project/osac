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


def _plural(count: int, singular: str, plural: str | None = None) -> str:
    return singular if count == 1 else (plural or singular + "s")


def _summary(items: Sequence[str], limit: int = 5, separator: str = ", ") -> str:
    shown = list(items[:limit])
    text = separator.join(shown)
    if len(items) > limit:
        text += f"{separator}and {len(items) - limit} more"
    return text


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
    domain_checks: Sequence[Check] = (),
) -> Report:
    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    tests = tuple(_outcome_for(plan, by_id, test.id) for test in plan.tests)
    blocking = [outcome for outcome in tests if outcome.identity.classification is Classification.BLOCKING]
    failed_blocking = [outcome.identity.id for outcome in blocking if outcome.result is not Result.PASS]
    blocking_pass = not failed_blocking
    if not blocking:
        blocking_message = "no blocking tests in the plan"
    elif failed_blocking:
        blocking_message = (
            f"{len(failed_blocking)} blocking {_plural(len(failed_blocking), 'test')} "
            f"failed: {_summary(failed_blocking)}"
        )
    else:
        blocking_message = f"{len(blocking)} blocking {_plural(len(blocking), 'test')} passed"

    in_progress_components = [component for component in components if component.result is not Verdict.CONFORMANT]
    components_pass = bool(components) and not in_progress_components
    if not components:
        components_message = "no components registered"
    elif in_progress_components:
        details = [f"{component.domain}: {component.reason}" for component in in_progress_components]
        components_message = (
            f"{len(in_progress_components)} of {len(components)} components in progress: "
            f"{_summary(details, separator='; ')}"
        )
    else:
        components_message = f"{len(components)} {_plural(len(components), 'component')} conformant"

    if leftovers:
        leftovers_message = (
            f"{len(leftovers)} {_plural(len(leftovers), 'leftover')}: {_summary(list(leftovers))}"
        )
    else:
        leftovers_message = "no leftovers"

    complete_message = (
        f"plan contained all {len(plan.tests)} selected {_plural(len(plan.tests), 'test')}"
        if plan.complete
        else "the plan is a focused subset and is not a conformance verdict"
    )

    checks = [
        Check(
            id="all-blocking-tests-pass",
            name="every selected blocking test passed",
            rule="every blocking test in the plan has result pass",
            remedy="see the failed tests and their evidence",
            result=CheckResult.PASS if blocking_pass else CheckResult.FAIL,
            message=blocking_message,
        ),
        Check(
            id="all-components-conformant",
            name="every component is conformant",
            rule="at least one component is registered and every component has result conformant",
            remedy="see the component's failed tests",
            result=CheckResult.PASS if components_pass else CheckResult.FAIL,
            message=components_message,
        ),
        Check(
            id="no-leftovers",
            name="nothing was left behind",
            rule="leftovers is empty",
            remedy="delete each leftover",
            result=CheckResult.PASS if not leftovers else CheckResult.FAIL,
            message=leftovers_message,
        ),
        Check(
            id="complete-plan",
            name="the plan was complete",
            rule="complete is true",
            remedy="run the plan a full list computes",
            result=CheckResult.PASS if plan.complete else CheckResult.FAIL,
            message=complete_message,
        ),
        *domain_checks,
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
