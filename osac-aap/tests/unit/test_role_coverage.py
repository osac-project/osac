"""The task coverage tool: the callback in a real ansible-playbook run, the
static parse of the role YAML, the merge, the gaps and the exclusions."""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
TOOL = ROOT / "tests/coverage"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"

spec = importlib.util.spec_from_file_location("role_coverage", TOOL / "role_coverage.py")
rc = importlib.util.module_from_spec(spec)
sys.modules["role_coverage"] = rc
spec.loader.exec_module(rc)

MAIN = """---
- name: Outer
  when: go_on
  block:
    - name: Include the failure
      when: fail_now
      ansible.builtin.include_tasks: _fail.yaml
    - name: Never fires in the first run
      ansible.builtin.fail:
        msg: boom
      when:
        - fail_now
        - other
    - name: Ignored failure
      ansible.builtin.fail:
        msg: ignored
      ignore_errors: true
    - name: Loop with one skipped item
      ansible.builtin.debug:
        msg: "{{ item }}"
      loop: [1, 2]
      when: item == 1
    - name: Empty loop
      ansible.builtin.debug:
        msg: never
      loop: []
  rescue:
    - name: Report
      ansible.builtin.include_tasks: _rescue.yaml
"""
FAIL = """---
- name: Fail the play
  ansible.builtin.fail:
    msg: classified
"""
RESCUE = """---
- name: Report the rescue
  ansible.builtin.debug:
    msg: rescued
"""
PLAYBOOK = """---
- name: Try
  hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - name: Run the role
      ansible.builtin.include_role:
        name: osac.templates.netris
        tasks_from: main.yaml
"""


@pytest.fixture()
def collection(tmp_path: Path) -> Path:
    tasks = tmp_path / "ansible_collections/osac/templates/roles/netris/tasks"
    tasks.mkdir(parents=True)
    (tasks.parent.parent.parent / "galaxy.yml").write_text("namespace: osac\nname: templates\nversion: 1.0.0\n")
    (tasks / "main.yaml").write_text(MAIN)
    (tasks / "_fail.yaml").write_text(FAIL)
    (tasks / "_rescue.yaml").write_text(RESCUE)
    (tmp_path / "play.yml").write_text(PLAYBOOK)
    return tmp_path


def play(root: Path, runs: Path, **extra: bool) -> None:
    env = {
        **os.environ,
        "ANSIBLE_CALLBACK_PLUGINS": str(TOOL / "callback_plugins"),
        "ANSIBLE_CALLBACKS_ENABLED": "osac_task_coverage",
        "OSAC_TASK_COVERAGE_DIR": str(runs),
        "ANSIBLE_COLLECTIONS_PATH": str(root),
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
    }
    env.pop("ANSIBLE_CONFIG", None)
    extra_vars = json.dumps({"go_on": True, "fail_now": False, "other": True, **extra})
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", str(root / "play.yml"), "-e", extra_vars],
        cwd=root, env=env, capture_output=True, text=True, timeout=120,
    )
    assert proc.returncode == 0, proc.stdout + proc.stderr


def scope_of(root: Path) -> rc.Scope:
    return rc.load_scope("netris", root / "ansible_collections/osac/templates/roles/netris",
                         root / "ansible_collections")


def test_the_static_parse_finds_tasks_conditions_and_rescues(collection: Path) -> None:
    scope = scope_of(collection)
    assert sorted(t.name for t in scope.tasks) == sorted([
        "Include the failure", "Never fires in the first run", "Ignored failure", "Loop with one skipped item",
        "Empty loop", "Report", "Fail the play", "Report the rescue",
    ])
    assert len(scope.rescues) == 1
    assert sorted(c.text for c in scope.clauses) == sorted(["go_on", "fail_now", "fail_now", "other", "item == 1"])
    failing = next(t for t in scope.tasks if t.name == "Never fires in the first run")
    assert [c.text for c in failing.chain] == ["go_on", "fail_now", "other"]
    assert failing.is_fail_path
    assert next(t for t in scope.tasks if t.name == "Include the failure").is_fail_path
    assert not next(t for t in scope.tasks if t.name == "Report the rescue").is_fail_path


def test_the_callback_records_outcomes_conditions_and_rescues(collection: Path, tmp_path: Path) -> None:
    runs = tmp_path / "runs"
    play(collection, runs)
    documents = list(runs.glob("run-*.json"))
    assert len(documents) == 1
    tasks = json.loads(documents[0].read_text())["tasks"]
    by_name = {entry["name"].split(" : ")[-1]: entry for entry in tasks.values()}
    assert by_name["Include the failure"]["skipped"] == 1
    assert by_name["Include the failure"]["false_conditions"] == {"fail_now": 1}
    ignored = by_name["Ignored failure"]
    assert (ignored["failed"], ignored["ignored"], ignored["rescued"]) == (1, 1, 0)
    assert by_name["Loop with one skipped item"]["ok"] == 1
    assert by_name["Loop with one skipped item"]["false_conditions"] == {"item == 1": 1}
    assert by_name["Empty loop"]["skipped"] == 1 and by_name["Empty loop"]["false_conditions"] == {}


def test_a_failure_caught_by_a_rescue_is_marked_and_a_final_one_is_not(collection: Path, tmp_path: Path) -> None:
    runs = tmp_path / "runs"
    play(collection, runs, fail_now=True)
    document = json.loads(next(runs.glob("run-*.json")).read_text())
    by_name = {entry["name"].split(" : ")[-1]: entry for entry in document["tasks"].values()}
    fail = by_name["Fail the play"]
    assert (fail["failed"], fail["rescued"]) == (1, 1)
    assert by_name["Report the rescue"]["ok"] == 1
    assert any(key.endswith("_fail.yaml:2") for key in document["fired"])
    assert any(key.endswith("main.yaml:5") for key in document["fired"])


def report(root: Path, runs: Path, tmp_path: Path, exclusions: list | None = None) -> tuple[int, dict]:
    """Run the gate on the temporary role only."""
    out = tmp_path / "report.json"
    excluded = tmp_path / "exclusions.yaml"
    excluded.write_text(json.dumps(exclusions or []))
    code = rc.main(["--dir", str(runs), "--json", str(out), "--exclusions", str(excluded),
                    "--collections", str(root / "ansible_collections"), "--role", "netris=osac/templates/roles/netris"])
    return code, json.loads(out.read_text())


def test_both_sides_and_every_path_leave_no_gap_with_the_exclusions(collection: Path, tmp_path: Path) -> None:
    runs = tmp_path / "runs"
    play(collection, runs)
    play(collection, runs, fail_now=True, other=False)
    play(collection, runs, go_on=False)
    code, summary = report(collection, runs, tmp_path)
    assert code == 1
    assert "when-never-false osac/templates/roles/netris/tasks/main.yaml: Empty loop" not in summary["gaps"]
    # An empty loop never ran, and the second fail task can only fire with
    # both conditions true, which no run did.
    assert "task-never-ran osac/templates/roles/netris/tasks/main.yaml: Empty loop" in summary["gaps"]
    assert "fail-path-never-fired osac/templates/roles/netris/tasks/main.yaml: Never fires in the first run" in summary["gaps"]
    assert "rescue-never-entered osac/templates/roles/netris/tasks/main.yaml: block Outer" not in summary["gaps"]
    numbers = summary["roles"]["netris"]
    assert numbers["rescues"] == [1, 1]
    code, summary = report(collection, runs, tmp_path, [{"gap": gap, "reason": "test"} for gap in summary["gaps"]])
    assert code == 0
    assert summary["gaps"] == []


def test_a_stale_exclusion_fails_the_gate(collection: Path, tmp_path: Path) -> None:
    runs = tmp_path / "runs"
    play(collection, runs)
    _, summary = report(collection, runs, tmp_path)
    exclusions = [{"gap": gap, "reason": "test"} for gap in summary["gaps"]] + [{"gap": "task-never-ran gone", "reason": "old"}]
    code, summary = report(collection, runs, tmp_path, exclusions)
    assert code == 1
    assert summary["stale_exclusions"] == ["task-never-ran gone"]


def test_a_fail_path_counts_as_asserted_only_with_evidence_for_its_message(collection: Path, tmp_path: Path) -> None:
    run = {"osac_error": {"message": "m", "retryable": True}, "fired": ["k"], "tasks": {}}
    (tmp_path / "asserted-1.json").write_text(json.dumps({"message": "m", "retryable": True, "message_checked": False}))
    assert rc.asserted_paths(tmp_path, [run]) == {}
    (tmp_path / "asserted-2.json").write_text(json.dumps({"message": "m", "retryable": False, "message_checked": True}))
    assert rc.asserted_paths(tmp_path, [run]) == {}
    (tmp_path / "asserted-3.json").write_text(json.dumps({"message": "m", "retryable": True, "message_checked": True}))
    assert rc.asserted_paths(tmp_path, [run]) == {"k": {True}}


def test_python_gaps_name_lines_branches_and_unmeasured_files() -> None:
    suffix = rc.PYTHON_FILES[0]
    report_doc = {"files": {f"x/osac/templates/{suffix}": {
        "summary": {"covered_lines": 9, "num_statements": 10, "covered_branches": 3, "num_branches": 4},
        "missing_lines": [7], "missing_branches": [[3, 5]],
    }}}
    gaps, numbers = rc.python_gaps(report_doc, "osac/templates")
    ids = [gap for gap, _ in gaps]
    assert f"python-line {suffix}:7" in ids and f"python-branch {suffix}:3->5" in ids
    assert f"python-not-measured {rc.PYTHON_FILES[1]}" in ids
    assert numbers[suffix] == {"lines": [9, 10], "branches": [3, 4]}


def test_no_runs_is_an_error_and_not_a_clean_report(tmp_path: Path) -> None:
    assert rc.main(["--dir", str(tmp_path)]) == 2
