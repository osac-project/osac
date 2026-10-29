"""Every workflow file is one YAML document GitHub can read, and every suite that runs as a run of its own follows the
contract the decision relies on (see .github/CI.md): a file that GitHub cannot parse is registered under its path,
cannot be started, and fails on every push."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github" / "workflows"
FILES = sorted([*WORKFLOWS.glob("*.yml"), *WORKFLOWS.glob("*.yaml")])
POLICY = yaml.safe_load((ROOT / ".github" / "e2e-policy.yml").read_text(encoding="utf-8"))["jobs"]
DISPATCHED = {job["suite"]: job["workflow"] for job in POLICY.values() if job.get("workflow")}


def _rejecting_duplicate_keys(base: type[yaml.Loader] | type[yaml.SafeLoader] | type[yaml.BaseLoader]) -> type:
    """``base`` that raises on a mapping with the same key twice (PyYAML keeps the last one without a word, and GitHub
    refuses the file), so the test does not depend on the yamllint hook for it."""

    class Strict(base):  # type: ignore[valid-type, misc]
        def construct_mapping(self, node: yaml.MappingNode, deep: bool = False) -> dict:
            seen: set[object] = set()
            for key_node, _ in node.value:
                key = self.construct_object(key_node, deep=True)
                if key in seen:
                    raise yaml.constructor.ConstructorError(None, None, f"duplicate key {key!r}", key_node.start_mark)
                seen.add(key)
            return super().construct_mapping(node, deep)  # type: ignore[no-any-return]

    return Strict


StrictSafeLoader = _rejecting_duplicate_keys(yaml.SafeLoader)
StrictBaseLoader = _rejecting_duplicate_keys(
    yaml.BaseLoader
)  # keeps `on` as the string "on" (SafeLoader reads it as True)


def load(path: Path) -> dict:
    return yaml.load(path.read_text(encoding="utf-8"), Loader=StrictSafeLoader)


def problems(text: str) -> list[str]:
    """What is wrong with a workflow file as a whole: not one document, a duplicate key, no `on` or no `jobs`."""
    try:
        documents = list(yaml.load_all(text, Loader=StrictBaseLoader))
    except yaml.YAMLError as exc:
        return [f"cannot be read: {exc}"]
    if len(documents) != 1:
        return [f"holds {len(documents)} YAML documents (a stray line before `---`?)"]
    top = documents[0]
    if not isinstance(top, dict):
        return ["is not a mapping"]
    return [f"has no top-level `{key}`" for key in ("on", "jobs") if key not in top]


@pytest.mark.parametrize("path", FILES, ids=lambda p: p.name)
def test_a_workflow_file_is_one_document_with_triggers_and_jobs(path: Path) -> None:
    assert problems(path.read_text(encoding="utf-8")) == []


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        ("id: stray\n---\non: push\njobs: {}\n", "holds 2 YAML documents"),
        ("on: push\njobs: {}\njobs: {}\n", "duplicate key 'jobs'"),
        ("on: push\njobs:\n  a: {}\n  a: {}\n", "duplicate key 'a'"),
        ("true: push\njobs: {}\n", "has no top-level `on`"),
        ("on: push\n", "has no top-level `jobs`"),
    ],
)
def test_the_check_catches_what_github_refuses(text: str, expected: str) -> None:
    (found,) = problems(text)
    assert expected in found


@pytest.mark.parametrize(("suite", "file"), sorted(DISPATCHED.items()))
def test_a_suite_that_runs_as_a_run_of_its_own_follows_the_contract_of_the_decision(suite: str, file: str) -> None:
    wrapper = load(WORKFLOWS / file)
    assert wrapper["run-name"] == f"E2E suite · {suite} · #${{{{ inputs.pr-number }}}} · ${{{{ inputs.sha }}}}"
    inputs = wrapper[True]["workflow_dispatch"]["inputs"]
    assert {"repo", "sha", "pr-number", "fork-pr-author", "fork-pr-author-association"} <= set(inputs)
    assert wrapper["jobs"]["verify"]["uses"].startswith("osac-project/osac-ci/.github/workflows/suite-inputs.yml@")
    assert wrapper["jobs"][suite]["needs"] == "verify", "the suite's job is named after the suite and needs verify"
    for given in ("fork-pr-author", "fork-pr-author-association"):
        assert "needs.verify.outputs" in wrapper["jobs"][suite]["with"][given], (
            "read from the pull request, not the input"
        )
    listed = load(WORKFLOWS / "e2e-pr.yml")[True]["workflow_run"]["workflows"]
    assert wrapper["name"] in listed, "e2e-pr.yml must listen to the end of the suite's run"
