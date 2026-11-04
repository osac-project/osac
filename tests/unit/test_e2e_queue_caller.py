"""The merge queue workflow has one job that looks after the entry (osac-ci's e2e-queue-result): it writes the required
E2E check and stops what still runs when something required fails."""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github" / "workflows"
PIN = re.compile(r"osac-project/osac-ci/\.github/workflows/([a-z0-9-]+\.yml)@([0-9a-f]{40})\s+# (v\d+\.\d+\.\d+)")


def jobs() -> dict:
    return yaml.safe_load((WORKFLOWS / "e2e-queue.yml").read_text())["jobs"]


def test_one_job_looks_after_the_entry_and_the_old_finish_job_is_gone() -> None:
    queue = jobs()
    assert "finish" not in queue
    result = queue["result"]
    assert result["needs"] == "plan" and result["if"] == "always()"
    assert result["permissions"] == {"actions": "write", "contents": "read"}
    assert result["with"] == {
        "sha": "${{ github.event.merge_group.head_sha }}",
        "branch": "${{ github.event.merge_group.base_ref }}",
        "plan-result": "${{ needs.plan.result }}",
        "suites": "${{ needs.plan.outputs.suites }}",
    }
    assert result["secrets"] == {"OSAC_CI_APP_PRIVATE_KEY": "${{ secrets.OSAC_CI_APP_PRIVATE_KEY }}"}
    assert "e2e-queue-result.yml@" in result["uses"]


def test_no_suite_waits_for_the_job_that_watches_them() -> None:
    for name, job in jobs().items():
        if name in ("plan", "result"):
            continue
        assert "result" not in (job.get("needs") if isinstance(job.get("needs"), list) else [job.get("needs")])


def test_every_suite_of_the_plan_has_a_job_and_the_job_watches_the_suites_the_plan_chose() -> None:
    queue = jobs()
    # `entry` derives the pull request number for the captures; every other job is a suite.
    suites = {name for name in queue if name not in ("plan", "result", "entry")}
    policy = yaml.safe_load((ROOT / ".github" / "e2e-policy.yml").read_text())["jobs"]
    automatic = {j.get("suite") or name for name, j in policy.items() if not j.get("manual_only")}
    assert suites == automatic, "a suite the policy chooses has a job here and the other way round"


def test_every_shared_workflow_is_pinned_to_the_same_release() -> None:
    pins = {}
    for path in sorted(WORKFLOWS.glob("*.yml")):
        for name, sha, tag in PIN.findall(path.read_text(encoding="utf-8")):
            pins[(path.name, name)] = (sha, tag)
    assert pins, "the callers pin osac-ci"
    assert len(set(pins.values())) == 1, f"every caller is on one release: {sorted(set(pins.values()))}"
    assert ("e2e-queue.yml", "e2e-queue-result.yml") in pins and ("e2e-queue.yml", "e2e-queue-plan.yml") in pins


def test_the_queue_derives_the_pull_request_number_from_the_queue_branch(tmp_path: Path) -> None:
    """A merge_group event carries no pull request number: GitHub exposes it only in the temporary branch of the entry,
    "gh-readonly-queue/<base>/pr-<N>-<sha>". The suites get the number so a queue attempt is captured as pull request N.
    """
    entry = jobs()["entry"]
    assert entry["outputs"] == {"pr-number": "${{ steps.derive.outputs.number }}"}
    step = entry["steps"][0]
    assert step["env"] == {"HEAD_REF": "${{ github.event.merge_group.head_ref }}"}

    def derive(head_ref: str) -> str:
        output = tmp_path / "output"
        output.write_text("")
        subprocess.run(
            ["bash", "-c", step["run"]],
            env={"PATH": os.environ["PATH"], "HEAD_REF": head_ref, "GITHUB_OUTPUT": str(output)},
            check=True,
            capture_output=True,
        )
        return dict(line.split("=", 1) for line in output.read_text().splitlines())["number"]

    assert derive("gh-readonly-queue/main/pr-42-abc123") == "42"
    # A base branch that contains "/" must not shift the match: the suffix is anchored, not one path segment.
    assert derive("gh-readonly-queue/release/4.20/pr-7-deadbeef") == "7"
    # `merge_group.head_ref` can carry the refs/heads/ prefix; both forms must work.
    assert derive("refs/heads/gh-readonly-queue/main/pr-42-abc123") == "42"
    assert derive("refs/heads/main") == ""


def test_every_suite_carries_the_pull_request_number_and_runs_one_capture() -> None:
    """The queue is the only caller that turns ci-observe on for every run: each attempt leaves one capture, named after
    the pull request, which the Pull requests dashboard links to. A caller may pass only a secret the workflow declares.
    """
    queue = jobs()
    for name in ("vmaas", "caas", "bmaas"):
        job = queue[name]
        assert job["needs"] == ["plan", "entry"]
        assert "always()" in job["if"], "an entry runner failure must not skip the suite"
        assert "needs.plan.result == 'success'" in job["if"]
        assert "needs.entry.result == 'success'" not in job["if"]
        assert job["with"]["pr-number"] == "${{ needs.entry.outputs.pr-number }}"
        assert job["with"]["ci-observe"] is True
        assert job["secrets"] == {"CI_OBSERVE_OTLP_PASSWORD": "${{ secrets.CI_OBSERVE_OTLP_PASSWORD }}"}

        path = WORKFLOWS / job["uses"].removeprefix("./.github/workflows/")
        called = yaml.safe_load(path.read_text())
        workflow_call = called.get("on") or called[True]
        assert "CI_OBSERVE_OTLP_PASSWORD" in workflow_call["workflow_call"]["secrets"], (
            f"{path.name} must declare the secret the queue passes it"
        )
