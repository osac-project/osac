"""CI selection contracts; set OSAC_TEST_INFRA_DIR to check the companion repo."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
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
FILE_CHANGES = [
    {"filename": REGRESSION, "status": "added"},
    {"filename": REGRESSION, "status": "modified"},
    {"filename": REGRESSION, "status": "removed"},
    {"filename": REGRESSION, "previous_filename": SANITY, "status": "renamed"},
    {"filename": SANITY, "previous_filename": REGRESSION, "status": "renamed"},
    {"filename": "docs/moved.md", "previous_filename": REGRESSION, "status": "renamed"},
    {"filename": REGRESSION, "previous_filename": REGRESSION + ".old", "status": "renamed"},
]


def _resolve_pr_tier(tmp_path: Path, pages: list | str | None, tier: str = "", **changes: object) -> tuple:
    workflow = _workflow(ROOT, "e2e-pr-caas.yml")
    step = next(step for step in workflow["jobs"]["labels"]["steps"] if step.get("id") == "tier")
    file_count = sum(len(page) for page in pages) if isinstance(pages, list) else 1
    pull = {"head": {"sha": PR_SHA}, "changed_files": file_count}
    responses = [changes.get("before", pull), pages, changes.get("after", pull)]
    (tmp_path / "responses.json").write_text(json.dumps(responses))
    mock = tmp_path / "gh"
    mock.write_text(
        f"#!{sys.executable}\n"
        "import json, sys\n"
        "from pathlib import Path\n"
        "calls_path = Path('calls.json')\n"
        "calls = json.loads(calls_path.read_text()) if calls_path.exists() else []\n"
        "response = json.loads(Path('responses.json').read_text())[len(calls)]\n"
        "calls.append(sys.argv[1:])\n"
        "calls_path.write_text(json.dumps(calls))\n"
        "if response is None: sys.exit(1)\n"
        "print(response if isinstance(response, str) else json.dumps(response))\n"
    )
    mock.chmod(0o755)
    output = tmp_path / "output"
    result = subprocess.run(
        ["bash", "-euo", "pipefail", "-c", step["run"]],
        cwd=tmp_path,
        env={
            **os.environ,
            "PATH": f"{tmp_path}{os.pathsep}{os.environ['PATH']}",
            "GH_REPO": "osac-project/osac",
            "PR_NUMBER": "123",
            "PR_SHA": PR_SHA,
            "REQUESTED_TIER": tier,
            "GITHUB_OUTPUT": str(output),
        },
        capture_output=True,
        text=True,
        check=False,
    )
    return result, output.read_text() if output.exists() else "", json.loads((tmp_path / "calls.json").read_text())


@pytest.mark.parametrize("changed", FILE_CHANGES)
@pytest.mark.parametrize("requested", ["", "sanity", "full"])
def test_regression_changes_select_full(tmp_path: Path, changed: dict, requested: str) -> None:
    result, output, calls = _resolve_pr_tier(tmp_path, [[changed]], requested)
    assert result.returncode == 0, result.stderr
    assert output == "tier=full\n"
    assert calls[1] == ["api", "--paginate", "--slurp", "repos/osac-project/osac/pulls/123/files?per_page=100"]
    inputs = _workflow(ROOT, "e2e-caas-full-install.yml")["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", sha=PR_SHA, tier="full") == "caas"
    assert _selection(inputs["test-marker"], "workflow_dispatch", sha=PR_SHA, tier="full") == FULL_MARKER


@pytest.mark.parametrize("filename", [SANITY, "fulfillment-service/main.go", "tests/e2e/caas/regression-other/test.py"])
@pytest.mark.parametrize("requested", ["", "sanity", "full", "regression"])
def test_other_changes_preserve_requested_tier(tmp_path: Path, filename: str, requested: str) -> None:
    result, output, _ = _resolve_pr_tier(tmp_path, [[{"filename": filename}]], requested)
    assert result.returncode == 0, result.stderr
    expected = "full" if requested == "full" else ""
    assert output == f"tier={expected}\n"


def test_tier_selection_reads_all_pages(tmp_path: Path) -> None:
    first_page = [{"filename": f"docs/{number}.md"} for number in range(100)]
    result, output, _ = _resolve_pr_tier(tmp_path, [first_page, [{"filename": REGRESSION}]])
    assert result.returncode == 0, result.stderr
    assert output == "tier=full\n"


@pytest.mark.parametrize(
    ("pages", "changes"),
    [
        (None, {}),
        ("not json", {}),
        ([], {}),
        ([[{}]], {}),
        ([[{"filename": ""}]], {}),
        ([[{"filename": SANITY}], [{"filename": SANITY}]], {}),
        ([[{"filename": REGRESSION, "previous_filename": 1}]], {}),
        ([[{"filename": REGRESSION}]], {"before": None}),
        ([[{"filename": REGRESSION}]], {"after": None}),
        ([[{"filename": REGRESSION}]], {"before": {"head": {"sha": "b" * 40}, "changed_files": 1}}),
        ([[{"filename": REGRESSION}]], {"after": {"head": {"sha": "b" * 40}, "changed_files": 1}}),
        ([[{"filename": REGRESSION}]], {"before": {"head": {"sha": PR_SHA}, "changed_files": 3001}}),
        ([[{"filename": REGRESSION}]], {"after": {"head": {"sha": PR_SHA}, "changed_files": 2}}),
    ],
)
@pytest.mark.parametrize("requested", ["", "full"])
def test_incomplete_or_stale_metadata_fails_closed(
    tmp_path: Path, pages: list | str | None, changes: dict, requested: str
) -> None:
    result, output, _ = _resolve_pr_tier(tmp_path, pages, requested, **changes)
    assert result.returncode != 0
    assert output == ""


def test_pr_tier_inputs_and_filenames_are_data(tmp_path: Path) -> None:
    filename = 'tests/e2e/caas/regression/$(touch injected); "quoted"\nfile.py'
    result, output, _ = _resolve_pr_tier(tmp_path, [[{"filename": filename}]], "sanity\ninjected=value")
    assert result.returncode == 0, result.stderr
    assert output == "tier=full\n"
    assert not (tmp_path / "injected").exists()


def test_pr_tier_resolution_preserves_trust_boundaries() -> None:
    workflow = _workflow(ROOT, "e2e-pr-caas.yml")
    jobs = workflow["jobs"]
    assert workflow["name"] == "E2E CaaS PR suite"
    assert workflow["run-name"] == "E2E suite · caas · #${{ inputs.pr-number }} · ${{ inputs.sha }}"
    assert jobs["verify"]["uses"] == (
        "osac-project/osac-ci/.github/workflows/suite-inputs.yml@5bfcd1ff220362171967451de7aaabb9e027d855"
    )
    assert jobs["labels"]["needs"] == "verify"
    assert jobs["labels"]["permissions"] == {"pull-requests": "read"}
    assert "secrets" not in jobs["labels"]
    assert not any("uses" in step for step in jobs["labels"]["steps"])
    assert "if" not in jobs["labels"]
    assert jobs["verify"]["with"] == {
        "repo": "${{ inputs.repo }}",
        "sha": "${{ inputs.sha }}",
        "pr-number": "${{ inputs.pr-number }}",
    }
    assert jobs["caas"]["needs"] == ["verify", "labels"]
    assert jobs["caas"]["with"]["tier"] == "${{ needs.labels.outputs.tier }}"
    assert jobs["caas"]["with"]["fork-pr-author"] == "${{ needs.verify.outputs.fork-author }}"
    assert jobs["caas"]["with"]["fork-pr-author-association"] == "${{ needs.verify.outputs.fork-association }}"
    assert jobs["caas"]["with"]["sha"] == "${{ inputs.sha }}"
    assert "if" not in jobs["caas"]
    queue = _workflow(ROOT, "e2e-queue.yml")
    assert "tier" not in queue["jobs"]["caas"]["with"]


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
print('Pinned parent decision: 7 change cases reach CaaS; approval/draft/fork guards preserved')
"""
    result = subprocess.run(
        ["uv", "run", "--project", companion, "--no-dev", "python", "-c", script],
        input=json.dumps([str(ROOT), FILE_CHANGES]),
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
