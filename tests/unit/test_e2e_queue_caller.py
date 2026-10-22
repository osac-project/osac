"""The merge queue workflow has one job that looks after the entry (osac-ci's e2e-queue-result): it writes the required
E2E check and stops what still runs when something required fails."""

from __future__ import annotations

import re
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
    suites = {name for name in queue if name not in ("plan", "result")}
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
