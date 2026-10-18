"""CI selection contracts; set OSAC_TEST_INFRA_DIR to check the companion repo."""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
FULL_MARKER = "sanity or regression or requires_caas or reference_common"
SHA = "a" * 40  # osac's wrapper is called for a pull request or a merge queue entry with the commit to test


def _workflow(root: Path, filename: str) -> dict:
    return yaml.safe_load((root / ".github/workflows" / filename).read_text())


def _selection(expression: str, event: str, suite: str = "", marker: str = "", pr: str = "", sha: str = "") -> str:
    # These selectors use only string comparisons and &&/|| short-circuiting.
    # Substitute contexts from the real YAML rather than reimplementing policy.
    values = {
        "github.event_name": event,
        "inputs.test-suite": suite,
        "inputs.test-marker": marker,
        "inputs.pr-number": pr,
        "inputs.sha": sha,
        "github.event.pull_request.labels.*.name": ["e2e-serial", "e2e-regression"],
    }
    code = expression.removeprefix("${{").removesuffix("}}").strip()
    for name, value in values.items():
        code = code.replace(name, repr(value))
    code = code.replace("&&", " and ").replace("||", " or ")
    code = " ".join(code.split())
    return eval(code, {"__builtins__": {}, "contains": lambda items, item: item in items})


@pytest.fixture
def caller() -> dict:
    """The caller of the test-infra repository (needs OSAC_TEST_INFRA_DIR); osac's own wrapper is tested below."""
    return _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")


@pytest.fixture
def osac_inputs() -> dict:
    """The inputs osac's wrapper passes to the reusable workflow (what the CaaS suite runs)."""
    return _workflow(ROOT, "e2e-caas-full-install.yml")["jobs"]["e2e-caas-full-install"]["with"]


def _infra_root() -> Path:
    path = os.environ.get("OSAC_TEST_INFRA_DIR")
    if not path:
        pytest.skip("Set OSAC_TEST_INFRA_DIR to validate companion test-infra checkout")
    return Path(path)


@pytest.mark.parametrize(
    ("event", "suite", "marker"),
    [
        ("pull_request", "caas/sanity", "sanity"),
        ("merge_group", "caas/sanity", "sanity"),
        ("schedule", "caas", FULL_MARKER),
        ("workflow_dispatch", "caas/sanity", FULL_MARKER),
    ],
)
def test_caller_trigger_policy(caller: dict, event: str, suite: str, marker: str) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], event) == suite
    assert _selection(inputs.get("test-marker", "''"), event) == marker


@pytest.mark.parametrize("event", ["pull_request", "merge_group"])
def test_pr_events_ignore_overrides(caller: dict, event: str) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], event, "caas/regression") == "caas/sanity"
    assert _selection(inputs["test-marker"], event, marker="regression") == "sanity"


def test_dispatch_defaults(caller: dict) -> None:
    # PyYAML's YAML 1.1 loader treats the unquoted Actions key `on` as True.
    triggers = caller[True]
    assert triggers["workflow_dispatch"]["inputs"]["test-suite"]["default"] == "caas/sanity"


def test_manual_overrides(caller: dict) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression") == "caas/regression"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression") == "regression"


def test_pr_associated_dispatch_ignores_suite_overrides() -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    inputs = workflow["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression", pr="123") == "caas/sanity"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression", pr="123") == "sanity"


@pytest.mark.parametrize("suite", ["caas/sanity", "caas/regression", "caas"])
@pytest.mark.parametrize("marker", ["", FULL_MARKER])
def test_runner_reference_selection(tmp_path: Path, suite: str, marker: str) -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install.yml")
    step = next(step for step in workflow["jobs"]["e2e"]["steps"] if step.get("id") == "test")
    # Execute just the real path-selection block, before filters/container setup.
    script = step["run"].split('TEST_ARGS="tests/e2e/"', 1)[1].split("# Keep compound", 1)[0]
    result = subprocess.run(
        ["bash", "-euc", 'TEST_ARGS="tests/e2e/"' + script + '\nprintf "%s" "$TEST_ARGS"'],
        env={**os.environ, "EFFECTIVE_TEST_SUITE": suite, "TEST_MARKER": marker},
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=True,
    )
    expected = f"tests/e2e/{suite}/"
    if suite != "caas/sanity" and marker:
        expected += " tests/e2e/references/"
    assert result.stdout == expected


@pytest.mark.parametrize("populated", [False, True])
def test_runner_tier_does_not_fall_back(tmp_path: Path, populated: bool) -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install.yml")
    step = next(
        step
        for step in workflow["jobs"]["e2e"]["steps"]
        if step.get("name", "").startswith("Resolve effective test suite")
    )
    suite_dir = tmp_path / "tests/e2e/caas/sanity"
    suite_dir.mkdir(parents=True)
    if populated:
        (suite_dir / "test_lifecycle.py").touch()
    output = tmp_path / "env"
    result = subprocess.run(
        ["bash", "-euo", "pipefail", "-c", step["run"]],
        env={**os.environ, "TEST_SUITE": "caas/sanity", "GITHUB_ENV": str(output)},
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=False,
    )
    if populated:
        assert result.returncode == 0, result.stderr
        assert output.read_text().strip() == "EFFECTIVE_TEST_SUITE=caas/sanity"
    else:
        assert result.returncode != 0, "Empty sanity tier must fail, not silently run regression"


def test_pr_selector_has_no_label_override() -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    expression = workflow["jobs"]["e2e-caas-full-install"]["with"]["test-suite"]
    assert not re.search(r"labels.*(e2e-serial|e2e-regression)", expression)


# osac's own wrapper (.github/workflows/e2e-caas-full-install.yml): called with a commit (inputs.sha) by e2e-pr.yml for
# a pull request and by e2e-queue.yml for a merge queue entry; run by the schedule; started by hand. No companion
# checkout is needed.


@pytest.mark.parametrize("event", ["workflow_run", "merge_group"])
def test_osac_called_for_a_pull_request_or_a_queue_entry_runs_sanity_only(osac_inputs: dict, event: str) -> None:
    # inside a called workflow github.event_name is the caller's event; the commit input is what says "called"
    assert _selection(osac_inputs["test-suite"], event, sha=SHA) == "caas/sanity"
    assert _selection(osac_inputs["test-marker"], event, sha=SHA) == "sanity"


@pytest.mark.parametrize("event", ["workflow_run", "merge_group"])
def test_osac_called_for_a_commit_ignores_overrides(osac_inputs: dict, event: str) -> None:
    assert _selection(osac_inputs["test-suite"], event, "caas/regression", sha=SHA) == "caas/sanity"
    assert _selection(osac_inputs["test-marker"], event, marker="regression", sha=SHA) == "sanity"


def test_osac_schedule_runs_the_full_suite_with_the_references(osac_inputs: dict) -> None:
    assert _selection(osac_inputs["test-suite"], "schedule") == "caas"
    assert _selection(osac_inputs["test-marker"], "schedule") == FULL_MARKER


def test_osac_manual_run_defaults_to_sanity_and_honors_overrides(osac_inputs: dict) -> None:
    assert _selection(osac_inputs["test-suite"], "workflow_dispatch") == "caas/sanity"
    assert _selection(osac_inputs["test-marker"], "workflow_dispatch") == FULL_MARKER
    assert _selection(osac_inputs["test-suite"], "workflow_dispatch", "caas/regression") == "caas/regression"
    assert _selection(osac_inputs["test-marker"], "workflow_dispatch", marker="regression") == "regression"


def test_osac_manual_run_input_defaults() -> None:
    triggers = _workflow(ROOT, "e2e-caas-full-install.yml")[True]  # PyYAML reads the unquoted key `on` as True
    assert triggers["workflow_dispatch"]["inputs"]["test-suite"]["default"] == "caas/sanity"
    assert "workflow_call" in triggers and "schedule" in triggers


def test_osac_wrapper_has_no_label_override() -> None:
    expression = _workflow(ROOT, "e2e-caas-full-install.yml")["jobs"]["e2e-caas-full-install"]["with"]["test-suite"]
    assert not re.search(r"labels.*(e2e-serial|e2e-regression)", expression)
