"""CI selection contracts; set OSAC_TEST_INFRA_DIR to check the companion repo."""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import pytest
import yaml

from _workflow_selector import evaluate_selector

ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40
FULL_MARKER = "sanity or regression or requires_caas or reference_common"


def _workflow(root: Path, filename: str) -> dict:
    """Load a workflow from the repository or companion checkout."""
    return yaml.safe_load((root / ".github/workflows" / filename).read_text())


def _selection(
    expression: str, event: str, suite: str = "", marker: str = "", pr: str = "", sha: str = "", tier: str = ""
) -> str:
    """Evaluate a workflow selector using the supplied GitHub contexts."""
    # These selectors use only string comparisons and &&/|| short-circuiting.
    # Substitute contexts from the real YAML rather than reimplementing policy.
    values = {
        "github.event_name": event,
        "inputs.test-suite": suite,
        "inputs.test-marker": marker,
        "inputs.pr-number": pr,
        "inputs.sha": sha,
        "inputs.tier": tier,
    }
    return evaluate_selector(expression, values)


@pytest.fixture(params=["osac", "test-infra"])
def caller(request: pytest.FixtureRequest) -> dict:
    """Load a caller alongside its explicit event-selection expectations."""
    if request.param == "osac":
        root = ROOT
        filename = "e2e-caas-full-install.yml"
        expectations = {
            "pr_sha": SHA,
            "dispatch_suite": "caas/sanity",
            "dispatch_marker": "sanity",
            "regression_marker": "regression",
        }
    else:
        root = _infra_root()
        filename = "e2e-caas-full-install-caller.yml"
        expectations = {
            "pr_sha": "",
            "dispatch_suite": "",
            "dispatch_marker": FULL_MARKER,
            "regression_marker": FULL_MARKER,
        }
    return {"workflow": _workflow(root, filename), **expectations}


def _infra_root() -> Path:
    """Return the companion checkout path or skip its dependent tests."""
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
        ("workflow_dispatch", "caas/sanity", "sanity"),
    ],
)
def test_caller_trigger_policy(caller: dict, event: str, suite: str, marker: str) -> None:
    """Verify each trigger selects its expected suite and marker."""
    inputs = caller["workflow"]["jobs"]["e2e-caas-full-install"]["with"]
    sha = caller["pr_sha"] if event in {"pull_request", "merge_group"} else ""
    manual_suite = caller["dispatch_suite"] if event == "workflow_dispatch" else ""
    expected_marker = caller["dispatch_marker"] if event == "workflow_dispatch" else marker
    assert _selection(inputs["test-suite"], event, suite=manual_suite, sha=sha) == suite
    assert _selection(inputs.get("test-marker", "''"), event, suite=manual_suite, sha=sha) == expected_marker


@pytest.mark.parametrize("event", ["pull_request", "merge_group"])
def test_pr_events_ignore_overrides(caller: dict, event: str) -> None:
    """Ensure PR and queue events always select the sanity suite."""
    inputs = caller["workflow"]["jobs"]["e2e-caas-full-install"]["with"]
    sha = caller["pr_sha"]
    assert _selection(inputs["test-suite"], event, "caas/regression", sha=sha) == "caas/sanity"
    assert _selection(inputs["test-marker"], event, marker="regression", sha=sha) == "sanity"


def test_dispatch_defaults(caller: dict) -> None:
    """Check the default manual suite configured by each caller."""
    # PyYAML's YAML 1.1 loader treats the unquoted Actions key `on` as True.
    triggers = caller["workflow"][True]
    assert triggers["workflow_dispatch"]["inputs"]["test-suite"]["default"] == "caas/sanity"


def test_manual_overrides(caller: dict) -> None:
    """Check manual suite and marker overrides are honored."""
    inputs = caller["workflow"]["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression") == "caas/regression"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", suite="caas/regression") == caller["regression_marker"]
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression") == "regression"


def test_pr_associated_dispatch_ignores_suite_overrides() -> None:
    """Keep PR-associated manual runs on the sanity tier."""
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    inputs = workflow["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression", pr="123") == "caas/sanity"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression", pr="123") == "sanity"


@pytest.mark.parametrize("suite", ["caas/sanity", "caas/regression", "caas"])
@pytest.mark.parametrize("marker", ["", FULL_MARKER])
def test_runner_reference_selection(tmp_path: Path, suite: str, marker: str) -> None:
    """Verify the test runner includes references for full marker runs."""
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
    """Fail an empty selected sanity tier instead of falling back."""
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
        env={
            **os.environ,
            "BASH_ENV": "/dev/null",
            "TEST_SUITE": "caas/sanity",
            "TEST_MARKER": "",
            "GITHUB_ENV": str(output),
            "GITHUB_OUTPUT": str(tmp_path / "output"),
        },
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
    """Prevent label-based selection of serial or regression coverage."""
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    expression = workflow["jobs"]["e2e-caas-full-install"]["with"]["test-suite"]
    assert not re.search(r"labels.*(e2e-serial|e2e-regression)", expression)
