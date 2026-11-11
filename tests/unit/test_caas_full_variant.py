"""Service suites have full variants that run sanity and regression together on a pull request."""

from __future__ import annotations

import ast
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40  # the commit a pull request or a merge queue entry is tested on
FULL_MARKER = "sanity or regression or requires_caas or reference_common"
SERVICES = {
    "vmaas": "sanity or regression or requires_vmaas or reference_common or reference_networking",
    "caas": FULL_MARKER,
    "bmaas": "sanity or regression or serial or requires_bmaas or reference_common or reference_networking",
}


def _load(path: str) -> dict:
    return yaml.safe_load((ROOT / path).read_text())


def _evaluate(node: ast.AST) -> object:
    """The one thing the selectors do: string literals, == and !=, and the short-circuiting && and || (which, as in
    Actions, give back the operand that decided). Anything else is refused, so nothing from the YAML is executed."""
    if isinstance(node, ast.Expression):
        return _evaluate(node.body)
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
        stop_on = isinstance(node.op, ast.Or)  # `or` stops at the first truthy operand, `and` at the first falsy
        value: object = None
        for operand in node.values:
            value = _evaluate(operand)
            if bool(value) is stop_on:
                return value
        return value
    if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], (ast.Eq, ast.NotEq)):
        same = _evaluate(node.left) == _evaluate(node.comparators[0])
        return same if isinstance(node.ops[0], ast.Eq) else not same
    raise ValueError(f"unsupported in a selector: {ast.dump(node)[:80]}")


def _selection(
    expression: str, *, sha: str = "", tier: str = "", event: str = "", suite: str = "", marker: str = ""
) -> str:
    """Evaluate one of the wrapper's selectors with the contexts given. They use only string comparisons and the
    short-circuiting && and ||, so the real YAML is evaluated rather than a copy of its rules."""
    values = {
        "github.event_name": event,
        "inputs.test-suite": suite,
        "inputs.test-marker": marker,
        "inputs.sha": sha,
        "inputs.tier": tier,
    }
    code = expression.removeprefix("${{").removesuffix("}}").strip()
    for name, value in values.items():
        code = code.replace(name, repr(value))
    code = " ".join(code.replace("&&", " and ").replace("||", " or ").split())
    return str(_evaluate(ast.parse(code, mode="eval")))


@pytest.mark.parametrize(
    "expression",
    [
        "${{ __import__('os').getcwd() }}",
        "${{ inputs.tier.upper() }}",
        "${{ unknown }}",
        "${{ 1 + 1 }}",
        "${{ x if y else z }}",
    ],
)
def test_the_selector_evaluator_refuses_anything_but_comparisons_and_the_short_circuiting_operators(
    expression: str,
) -> None:
    with pytest.raises((ValueError, SyntaxError)):
        _selection(expression)


def test_the_selector_evaluator_gives_back_the_operand_that_decided_like_actions_do() -> None:
    assert _selection("${{ inputs.sha != '' && 'a' || 'b' }}", sha=SHA) == "a"
    assert _selection("${{ inputs.sha != '' && 'a' || 'b' }}") == "b"
    assert (
        _selection(
            "${{ inputs.tier == 'full' && 'x' || inputs.test-suite != '' && inputs.test-suite || 'z' }}", suite="s"
        )
        == "s"
    )


@pytest.fixture
def given() -> dict:
    return _load(".github/workflows/e2e-caas-full-install.yml")["jobs"]["e2e-caas-full-install"]["with"]


@pytest.mark.parametrize(("tier", "suite", "marker"), [("", "caas/sanity", "sanity"), ("full", "caas", FULL_MARKER)])
def test_a_commit_gets_the_sanity_tests_unless_sanity_and_regression_are_asked_for(
    given: dict, tier: str, suite: str, marker: str
) -> None:
    assert _selection(given["test-suite"], sha=SHA, tier=tier) == suite
    assert _selection(given["test-marker"], sha=SHA, tier=tier) == marker


def test_the_full_tier_runs_what_the_schedule_runs(given: dict) -> None:
    assert _selection(given["test-suite"], sha=SHA, tier="full") == _selection(given["test-suite"], event="schedule")
    assert _selection(given["test-marker"], sha=SHA, tier="full") == _selection(given["test-marker"], event="schedule")


def test_a_commit_ignores_the_manual_overrides_whatever_the_tier(given: dict) -> None:
    assert _selection(given["test-suite"], sha=SHA, tier="full", suite="caas/regression") == "caas"
    assert _selection(given["test-marker"], sha=SHA, tier="full", marker="serial") == FULL_MARKER
    assert _selection(given["test-suite"], sha=SHA, suite="caas/regression") == "caas/sanity"
    assert _selection(given["test-suite"], sha=SHA, tier="other") == "caas/sanity"


def test_the_schedule_and_manual_tier_selection(given: dict) -> None:
    assert _selection(given["test-suite"], event="schedule") == "caas"
    assert _selection(given["test-suite"], event="workflow_dispatch", suite="caas/regression") == "caas/regression"
    assert _selection(given["test-suite"], event="workflow_dispatch") == "caas/sanity"
    assert _selection(given["test-marker"], event="workflow_dispatch", suite="caas/sanity") == "sanity"
    assert _selection(given["test-marker"], event="workflow_dispatch", suite="caas/regression") == "regression"
    assert _selection(given["test-marker"], event="workflow_dispatch", suite="caas", marker="metering") == "metering"


def test_the_wrapper_takes_the_tier_as_an_optional_input_that_defaults_to_the_sanity_tests() -> None:
    tier = _load(".github/workflows/e2e-caas-full-install.yml")[True]["workflow_call"]["inputs"]["tier"]
    assert tier["required"] is False and tier["default"] == "" and tier["type"] == "string"


@pytest.mark.parametrize("service", SERVICES)
def test_the_full_box_is_a_variant_of_its_service_on_the_same_line(service: str) -> None:
    jobs = _load(".github/e2e-policy.yml")["jobs"]
    default, full = jobs[f"e2e-{service}"], jobs[f"e2e-{service}-full"]
    assert full["variant_of"] == default["suite"] == service
    assert full["tier"] == "full" and full["manual_only"] is True
    assert full["check"] == default["check"], "the same line, so the E2E check counts it like the suite"
    assert "manual_only" not in default


def test_the_policy_loads_with_the_shared_rules_and_the_decision_never_chooses_the_variant() -> None:
    osac_ci = pytest.importorskip("osac_ci.policy")
    e2e_start = pytest.importorskip("osac_ci.e2e_start")
    policy = osac_ci.load_policy(ROOT / ".github/e2e-policy.yml")
    suites = {s.name: s for s in e2e_start.suites_of(policy)}
    for service in SERVICES:
        assert suites[f"{service}-full"].variant_of == service and suites[f"{service}-full"].tier == "full"
    assert [s.name for s in e2e_start.automatic(suites.values())] == ["vmaas", "caas", "bmaas"]


@pytest.mark.parametrize("service", SERVICES)
def test_each_service_passes_the_tier_the_decision_gives(service: str) -> None:
    jobs = _load(".github/workflows/e2e-pr.yml")["jobs"]
    assert set(jobs) == {"decide", "track", "finish"}, "every suite runs as a run of its own (e2e-pr-<suite>.yml)"
    wrapper = _load(f".github/workflows/e2e-pr-{service}.yml")
    tier = wrapper[True]["workflow_dispatch"]["inputs"]["tier"]
    assert tier["required"] is False and tier["default"] == "" and tier["type"] == "string"
    assert wrapper["jobs"][service]["with"]["tier"] == "${{ inputs.tier }}"


@pytest.mark.parametrize(("service", "sanity_suite"), [("vmaas", "vmaas"), ("caas", "caas/sanity"), ("bmaas", "bmaas")])
def test_service_commit_and_schedule_tiers(service: str, sanity_suite: str) -> None:
    given = _load(f".github/workflows/e2e-{service}-full-install.yml")["jobs"][f"e2e-{service}-full-install"]["with"]
    assert _selection(given["test-suite"], sha=SHA) == sanity_suite
    assert _selection(given["test-marker"], sha=SHA) == "sanity"
    assert _selection(given["test-suite"], sha=SHA, tier="full") == service
    assert _selection(given["test-marker"], sha=SHA, tier="full") == SERVICES[service]
    assert _selection(given["test-suite"], event="schedule") == service
    assert _selection(given["test-marker"], event="schedule") == SERVICES[service]


@pytest.mark.parametrize("service", SERVICES)
def test_manual_service_tiers_use_matching_markers(service: str) -> None:
    given = _load(f".github/workflows/e2e-{service}-full-install.yml")["jobs"][f"e2e-{service}-full-install"]["with"]
    assert _selection(given["test-marker"], event="workflow_dispatch", suite=f"{service}/sanity") == "sanity"
    assert _selection(given["test-marker"], event="workflow_dispatch", suite=f"{service}/regression") == "regression"
    assert _selection(given["test-marker"], event="workflow_dispatch", suite=service) == SERVICES[service]
    assert _selection(given["test-marker"], event="workflow_dispatch", suite=service, marker="metering") == "metering"


def test_vmaas_service_root_keeps_root_level_sanity_modules_in_presubmit() -> None:
    for name in ("test_disabled_services.py", "test_self_subject_access_review.py"):
        path = ROOT / "tests/e2e/vmaas" / name
        assert "pytest.mark.sanity" in path.read_text()


def test_full_variants_have_no_merge_queue_jobs() -> None:
    queue = _load(".github/workflows/e2e-queue.yml")["jobs"]
    assert not any("tier" in (job.get("with") or {}) for job in queue.values()), "the queue gets the sanity tests alone"
    assert not any(f"{service}-full" in queue for service in SERVICES), "variants have no merge queue jobs"
