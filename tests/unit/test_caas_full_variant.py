"""The CaaS suite has a variant: a second box that runs sanity and regression together, counted as the CaaS suite."""

from __future__ import annotations

import ast
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40  # the commit a pull request or a merge queue entry is tested on
FULL_MARKER = "sanity or regression or requires_caas or reference_common"


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


def test_the_schedule_and_a_manual_run_do_not_change(given: dict) -> None:
    assert _selection(given["test-suite"], event="schedule") == "caas"
    assert _selection(given["test-suite"], event="workflow_dispatch", suite="caas/regression") == "caas/regression"
    assert _selection(given["test-suite"], event="workflow_dispatch") == "caas/sanity"
    assert _selection(given["test-marker"], event="workflow_dispatch") == FULL_MARKER


def test_the_wrapper_takes_the_tier_as_an_optional_input_that_defaults_to_the_sanity_tests() -> None:
    tier = _load(".github/workflows/e2e-caas-full-install.yml")[True]["workflow_call"]["inputs"]["tier"]
    assert tier["required"] is False and tier["default"] == "" and tier["type"] == "string"


def test_the_full_box_is_a_variant_of_the_caas_suite_on_its_line_started_by_hand_only() -> None:
    jobs = _load(".github/e2e-policy.yml")["jobs"]
    caas, full = jobs["e2e-caas"], jobs["e2e-caas-full"]
    assert full["variant_of"] == caas["suite"] == "caas" and full["tier"] == "full" and full["manual_only"] is True
    assert full["check"] == caas["check"], "the same line, so the E2E check counts it like the suite"
    assert "manual_only" not in caas


def test_the_policy_loads_with_the_shared_rules_and_the_decision_never_chooses_the_variant() -> None:
    osac_ci = pytest.importorskip("osac_ci.policy")
    e2e_start = pytest.importorskip("osac_ci.e2e_start")
    policy = osac_ci.load_policy(ROOT / ".github/e2e-policy.yml")
    suites = {s.name: s for s in e2e_start.suites_of(policy)}
    assert suites["caas-full"].variant_of == "caas" and suites["caas-full"].tier == "full"
    assert [s.name for s in e2e_start.automatic(suites.values())] == ["vmaas", "caas", "bmaas"]


def test_the_caas_suite_passes_the_tier_the_decision_gives_and_nothing_else_does() -> None:
    jobs = _load(".github/workflows/e2e-pr.yml")["jobs"]
    assert set(jobs) == {"decide", "track", "finish"}, "every suite runs as a run of its own (e2e-pr-<suite>.yml)"
    caas = _load(".github/workflows/e2e-pr-caas.yml")
    tier = caas[True]["workflow_dispatch"]["inputs"]["tier"]
    assert tier["required"] is False and tier["default"] == "" and tier["type"] == "string"
    assert caas["jobs"]["caas"]["with"]["tier"] == "${{ inputs.tier }}"
    for other in ("vmaas", "bmaas", "netris"):
        wrapper = _load(f".github/workflows/e2e-pr-{other}.yml")
        assert "tier" not in wrapper[True]["workflow_dispatch"]["inputs"], "only the caas suite has a variant"
        assert "tier" not in wrapper["jobs"][other]["with"]
    queue = _load(".github/workflows/e2e-queue.yml")["jobs"]
    assert not any("tier" in (job.get("with") or {}) for job in queue.values()), "the queue gets the sanity tests alone"
    assert "caas-full" not in queue, "a variant has no job in the queue either"
