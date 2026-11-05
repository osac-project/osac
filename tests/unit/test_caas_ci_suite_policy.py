"""CI selection contracts; set OSAC_TEST_INFRA_DIR to check the companion repo."""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
FULL_MARKER = "sanity or regression or requires_caas or reference_common"


def _workflow(root: Path, filename: str) -> dict:
    return yaml.safe_load((root / ".github/workflows" / filename).read_text())


def _selection(
    expression: str,
    event: str,
    suite: str = "",
    marker: str = "",
    pr: str = "",
    *,
    sha: str | None = None,
    tier: str = "",
) -> str:
    # These selectors use only string comparisons and &&/|| short-circuiting.
    # Substitute contexts from the real YAML rather than reimplementing policy.
    values = {
        "github.event_name": event,
        "inputs.test-suite": suite,
        "inputs.test-marker": marker,
        "inputs.pr-number": pr,
        "inputs.sha": sha if sha is not None else ("a" * 40 if event in {"pull_request", "merge_group"} else ""),
        "inputs.tier": tier,
        "github.event.pull_request.labels.*.name": ["e2e-serial", "e2e-regression"],
    }
    code = expression.removeprefix("${{").removesuffix("}}").strip()
    for name, value in values.items():
        code = code.replace(name, repr(value))
    code = code.replace("&&", " and ").replace("||", " or ")
    code = " ".join(code.split())
    return eval(code, {"__builtins__": {}, "contains": lambda items, item: item in items})


@pytest.fixture(params=["osac", "test-infra"])
def caller(request: pytest.FixtureRequest) -> dict:
    if request.param == "osac":
        return _workflow(ROOT, "e2e-caas-full-install.yml")
    return _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")


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
        env={
            **os.environ,
            "TEST_SUITE": "caas/sanity",
            "TEST_MARKER": "sanity",
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
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    expression = workflow["jobs"]["e2e-caas-full-install"]["with"]["test-suite"]
    assert not re.search(r"labels.*(e2e-serial|e2e-regression)", expression)


PR_SHA = "a" * 40
REGRESSION = "tests/e2e/caas/regression/test_cluster.py"
SANITY = "tests/e2e/caas/sanity/test_cluster.py"
UNUSUAL_REGRESSION = 'tests/e2e/caas/regression/$(touch injected); "quoted"\nfile.py'
FILE_CHANGES = [
    {"filename": REGRESSION, "status": "added"},
    {"filename": REGRESSION, "status": "modified"},
    {"filename": REGRESSION, "status": "removed"},
    {"filename": REGRESSION, "previous_filename": SANITY, "status": "renamed"},
    {"filename": SANITY, "previous_filename": REGRESSION, "status": "renamed"},
    {"filename": "docs/moved.md", "previous_filename": REGRESSION, "status": "renamed"},
    {"filename": REGRESSION, "previous_filename": REGRESSION + ".old", "status": "renamed"},
]


@pytest.mark.parametrize("matched", ["true", "false"])
@pytest.mark.parametrize("requested", ["", "sanity", "full"])
def test_filtered_tier_reaches_caas(matched: str, requested: str) -> None:
    expression = _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["selection"]["outputs"]["tier"]
    expression = expression.replace("steps.comparison.outputs.changed", repr(matched))
    tier = _selection(expression, "workflow_dispatch", tier=requested)
    full = matched == "true" or requested == "full"
    assert tier == ("full" if full else "")
    inputs = _workflow(ROOT, "e2e-caas-full-install.yml")["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", sha=PR_SHA, tier=tier) == (
        "caas" if full else "caas/sanity"
    )
    assert _selection(inputs["test-marker"], "workflow_dispatch", sha=PR_SHA, tier=tier) == (
        FULL_MARKER if full else "sanity"
    )


@pytest.mark.parametrize(
    "response", ["valid", "stale", "missing", "api-error", "invalid-sha", "null-base", "invalid-json"]
)
def test_pr_base_read_fails_closed(tmp_path: Path, response: str) -> None:
    step = next(
        step for step in _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["selection"]["steps"] if step.get("id") == "base"
    )
    pull = {"head": {"sha": "b" * 40 if response == "stale" else PR_SHA}, "base": {"sha": "c" * 40}}
    if response == "invalid-sha":
        pull["base"]["sha"] = "not-a-sha"
    if response == "null-base":
        pull["base"] = None
    (tmp_path / "response").write_text(
        "{" if response == "invalid-json" else json.dumps(pull if response != "missing" else {})
    )
    mock = tmp_path / "gh"
    mock.write_text("#!/bin/sh\n" + ("exit 1\n" if response == "api-error" else 'cat "$RESPONSE"\n'))
    mock.chmod(0o755)
    output = tmp_path / "output"
    result = subprocess.run(
        ["bash", "-c", step["run"]],
        env={
            **os.environ,
            "PATH": f"{tmp_path}:{os.environ['PATH']}",
            "RESPONSE": str(tmp_path / "response"),
            "GH_REPO": "osac-project/osac",
            "PR_NUMBER": "2152",
            "PR_SHA": PR_SHA,
            "GITHUB_OUTPUT": str(output),
        },
        capture_output=True,
        text=True,
        check=False,
    )
    assert (result.returncode == 0) == (response == "valid")
    assert (output.read_text() if output.exists() else "") == (f"sha={'c' * 40}\n" if response == "valid" else "")


@pytest.mark.parametrize(
    "changed",
    [
        *FILE_CHANGES,
        {"filename": SANITY, "status": "added"},
        {"filename": "tests/e2e/caas/regression-other/test.py", "status": "added"},
        {"filename": SANITY, "status": "base-only"},
        {"filename": "docs/readme.md", "status": "added"},
        {"filename": UNUSUAL_REGRESSION, "status": "added"},
        {"filename": "tests/e2e/caas/regression/dir\nname/test.py", "status": "added"},
        {"filename": UNUSUAL_REGRESSION, "other_files": [SANITY], "status": "added"},
        {"filename": SANITY, "status": "git-error"},
        {"filename": SANITY, "status": "diff-error"},
        {"filename": SANITY, "status": "output-error"},
    ],
)
def test_git_directory_selection_on_pr_changes(tmp_path: Path, changed: dict) -> None:
    steps = _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["selection"]["steps"]

    def git(*args: str) -> str:
        return subprocess.check_output(
            ["git", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@example.com", *args],
            cwd=tmp_path,
            text=True,
            stderr=subprocess.PIPE,
        ).strip()

    git("init", "-q")
    git("remote", "add", "origin", str(tmp_path))
    filename = tmp_path / changed["filename"]
    previous = tmp_path / changed.get("previous_filename", changed["filename"])
    if changed["status"] in {"modified", "removed", "renamed"}:
        previous.parent.mkdir(parents=True, exist_ok=True)
        previous.write_text("before")
    git("add", ".")
    git("commit", "-qm", "base", "--allow-empty")
    base = git("rev-parse", "HEAD")
    if changed["status"] in {"removed", "renamed"}:
        previous.unlink()
    if changed["status"] != "removed":
        filename.parent.mkdir(parents=True, exist_ok=True)
        filename.write_text("after")
    for other in changed.get("other_files", []):
        other_file = tmp_path / other
        other_file.parent.mkdir(parents=True, exist_ok=True)
        other_file.write_text("another PR change")
    git("add", ".")
    git("commit", "-qm", "PR change")
    head = git("rev-parse", "HEAD")
    git("checkout", "--detach", base)
    if changed["status"] == "base-only":
        upstream = tmp_path / REGRESSION
        upstream.parent.mkdir(parents=True, exist_ok=True)
        upstream.write_text("upstream only")
        git("add", ".")
        git("commit", "-qm", "upstream regression change")
        base = git("rev-parse", "HEAD")
    output = tmp_path / "output"
    runner_temp = tmp_path / "runner-temp"
    if changed["status"] != "output-error":
        runner_temp.mkdir()
    env = {**os.environ, "PR_BASE": base, "PR_SHA": head, "GITHUB_OUTPUT": str(output), "RUNNER_TEMP": str(runner_temp)}
    comparison = next(step for step in steps if step.get("id") == "comparison")
    if changed["status"] == "git-error":
        git("remote", "remove", "origin")
        env["PR_SHA"] = "f" * 40
    if changed["status"] == "diff-error":
        mock_bin = tmp_path / "bin"
        mock_bin.mkdir()
        mock = mock_bin / "git"
        mock.write_text('#!/bin/sh\nif [ "$1" = diff ]; then exit 128; fi\nexec "$REAL_GIT" "$@"\n')
        mock.chmod(0o755)
        env["REAL_GIT"] = shutil.which("git")
        env["PATH"] = f"{mock_bin}:{os.environ['PATH']}"
    result = subprocess.run(["bash", "-c", comparison["run"]], cwd=tmp_path, env=env, check=False, capture_output=True)
    if changed["status"] in {"git-error", "diff-error", "output-error"}:
        assert result.returncode != 0
        assert not output.exists()
        return
    assert result.returncode == 0, result.stdout + result.stderr
    expected = any(
        path.startswith("tests/e2e/caas/regression/")
        for path in (changed["filename"], changed.get("previous_filename", ""))
    )
    assert output.read_text() == f"changed={str(expected).lower()}\n"
    expression = _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["selection"]["outputs"]["tier"]
    expression = expression.replace("steps.comparison.outputs.changed", repr(str(expected).lower()))
    for requested in ("", "sanity", "full"):
        assert _selection(expression, "workflow_dispatch", tier=requested) == (
            "full" if expected or requested == "full" else ""
        )
    assert git("rev-parse", "HEAD") == base, "path detection must not check out PR code"
    assert not (tmp_path / "injected").exists()


def test_pr_tier_resolution_preserves_trust_boundaries() -> None:
    workflow = _workflow(ROOT, "e2e-pr-caas.yml")
    jobs = workflow["jobs"]
    assert workflow["name"] == "E2E CaaS PR suite"
    assert workflow["run-name"] == "E2E suite · caas · #${{ inputs.pr-number }} · ${{ inputs.sha }}"
    assert jobs["verify"]["uses"] == (
        "osac-project/osac-ci/.github/workflows/suite-inputs.yml@5bfcd1ff220362171967451de7aaabb9e027d855"
    )
    assert jobs["selection"]["needs"] == "verify"
    assert jobs["selection"]["permissions"] == {"contents": "read", "pull-requests": "read"}
    assert "secrets" not in jobs["selection"]
    checkout, base, comparison = jobs["selection"]["steps"]
    assert checkout["uses"] == "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
    assert checkout["with"] == {"ref": "${{ github.workflow_sha }}", "fetch-depth": 0, "persist-credentials": False}
    assert base["env"]["PR_SHA"] == "${{ inputs.sha }}"
    assert comparison["env"] == {"PR_BASE": "${{ steps.base.outputs.sha }}", "PR_SHA": "${{ inputs.sha }}"}
    assert all("if" not in step and not step.get("continue-on-error") for step in (checkout, base, comparison))
    assert "if" not in jobs["selection"]
    assert jobs["verify"]["with"] == {
        "repo": "${{ inputs.repo }}",
        "sha": "${{ inputs.sha }}",
        "pr-number": "${{ inputs.pr-number }}",
    }
    assert jobs["caas"]["needs"] == ["verify", "selection"]
    assert jobs["caas"]["with"]["tier"] == "${{ needs.selection.outputs.tier }}"
    assert jobs["caas"]["with"]["fork-pr-author"] == "${{ needs.verify.outputs.fork-author }}"
    assert jobs["caas"]["with"]["fork-pr-author-association"] == "${{ needs.verify.outputs.fork-association }}"
    assert jobs["caas"]["with"]["sha"] == "${{ inputs.sha }}"
    assert "if" not in jobs["caas"]
    assert "if" not in jobs["verify"]
    assert not jobs["verify"].get("continue-on-error")
    assert not jobs["selection"].get("continue-on-error")
    assert not jobs["caas"].get("continue-on-error")
    assert set(jobs["selection"]["outputs"]) == {"tier"}
    assert "labels" not in jobs
    queue = _workflow(ROOT, "e2e-queue.yml")
    assert "tier" not in queue["jobs"]["caas"]["with"]


def test_native_selection_preserves_unconditional_telemetry() -> None:
    caas = _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["caas"]
    assert "ci-observe" not in caas["with"]
    assert caas["secrets"] == {"CI_OBSERVE_OTLP_PASSWORD": "${{ secrets.CI_OBSERVE_OTLP_PASSWORD }}"}
    wrapper = _workflow(ROOT, "e2e-caas-full-install.yml")
    triggers = wrapper.get("on") or wrapper[True]
    for event in ("workflow_call", "workflow_dispatch"):
        assert "ci-observe" not in triggers[event]["inputs"]
    assert triggers["workflow_call"]["secrets"]["CI_OBSERVE_OTLP_PASSWORD"]["required"] is False
    runner = wrapper["jobs"]["e2e-caas-full-install"]
    assert runner["with"]["ci-observe"] is True
    assert runner["secrets"]["ci-observe-otlp-password"] == "${{ secrets.CI_OBSERVE_OTLP_PASSWORD }}"


def test_parent_decision_reaches_caas_for_regression_changes() -> None:
    companion = os.environ.get("OSAC_CI_DIR")
    if not companion:
        pytest.skip("Set OSAC_CI_DIR to validate the pinned parent suite decision")
    pin = _workflow(ROOT, "e2e-pr-caas.yml")["jobs"]["verify"]["uses"].rsplit("@", 1)[1]
    revision = subprocess.run(
        ["git", "-C", companion, "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    ).stdout.strip()
    assert revision == pin, "OSAC_CI_DIR must be checked out at the workflow's osac-ci pin"
    script = """
import json
import sys
from dataclasses import replace
from pathlib import Path
from osac_ci.e2e_start import Inputs, Review, decide, suites_for_files, suites_of
from osac_ci.policy import load_policy

root, changes = json.load(sys.stdin)
policy = load_policy(Path(root) / '.github/e2e-policy.yml')
head = 'a' * 40
for changed in changes:
    paths = tuple(changed[key] for key in ('filename', 'previous_filename') if key in changed)
    paths += tuple(changed.get('other_files', []))
    assert 'caas' in suites_for_files(policy, paths), paths
    inputs = Inputs(
        number=123, head_sha=head, author='author', state='open', draft=False,
        reviews=(Review('reviewer', 'APPROVED', 'MEMBER', head, permission='write'),),
        check_runs=(), changed_files=paths, changed_files_known=True, cheap=(),
        suites=suites_of(policy), filters=policy.path_filters.filters,
    )
    decision = decide(inputs)
    assert decision.starts and 'caas' in decision.suites, decision
    assert ('caas', 'e2e-pr-caas.yml') in decision.workflows, decision
    assert ('caas', 'E2E / CaaS Full Install') in decision.suite_checks, decision
    assert not decide(replace(inputs, reviews=())).starts
    assert not decide(replace(inputs, draft=True)).starts
    assert not decide(replace(inputs, reviews=(replace(inputs.reviews[0], commit_id='b' * 40),))).starts
    assert decide(replace(inputs, fork=True)).starts
    bot = Review('coderabbitai[bot]', 'APPROVED', 'NONE', head, permission='write', user_type='Bot')
    assert not decide(replace(inputs, fork=True, reviews=(bot,))).starts
print('Pinned parent: regression and mixed newline/sanity cases reach CaaS; approval/draft/fork guards preserved')
"""
    result = subprocess.run(
        ["uv", "run", "--project", companion, "--no-dev", "python", "-c", script],
        input=json.dumps([str(ROOT), [*FILE_CHANGES, {"filename": UNUSUAL_REGRESSION, "other_files": [SANITY]}]]),
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
