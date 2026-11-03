#!/usr/bin/env python3
# Copyright (c) 2026 Red Hat, Inc.
# SPDX-License-Identifier: Apache-2.0
"""Merge task coverage runs and report the gaps of the network manager roles.

Input: the directory the callback osac_task_coverage writes to, one
run-*.json per ansible-playbook process, from the integration targets and from
the unit tests that run real task files, plus the evidence files asserted-*.json
that tests write after they asserted an osac_error.

The denominators come from the role YAML, not from the runs, so a task no test
reaches still counts:

  tasks       every task; it ran when it ended ok, changed or failed
  when        every `when` condition of a task or block, one per list item;
              both sides must be seen. A false side is the condition Ansible
              reported as false; a true side is a task that got past it.
  rescues     every `rescue` section; entered when one of its tasks started
  fail paths  every include of _fail.yaml/_stop.yaml, every ansible.builtin.fail
              and assert. An include fired when it ran, a fail or assert when
              it failed. A path is asserted when a test recorded the
              classification and the message of the osac_error of a run in
              which the path fired.

Every gap has a stable id. A gap listed in the exclusions file (with its
reason) is reported but does not fail the check; an exclusion that matches no
gap fails it. The exit status is 1 when a gap is left.

Python coverage comes from a pytest-cov JSON report (--python-coverage).
netris.controller roles are reported, not gated.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
COLLECTIONS = HERE.parent.parent / "collections" / "ansible_collections"
GATED_ROLES = {"netris": "osac/templates/roles/netris", "cudn_evpn": "osac/templates/roles/cudn_evpn"}
REPORTED_ROLES = "netris/controller/roles"
PYTHON_FILES = (
    "plugins/filter/netris_contract.py",
    "plugins/filter/cudn_evpn.py",
    "plugins/modules/netris_external_ip.py",
    "plugins/module_utils/netris_external_ip.py",
)
FAIL_ACTIONS = {"ansible.builtin.fail", "ansible.builtin.assert", "fail", "assert"}
INCLUDE_ACTIONS = {"ansible.builtin.include_tasks", "include_tasks"}
FAIL_INCLUDE = re.compile(r"(^|/)(_fail|_stop)\.yaml$")
TASK_KEYWORDS = {
    "name", "when", "vars", "register", "loop", "loop_control", "tags", "no_log", "ignore_errors",
    "changed_when", "failed_when", "until", "retries", "delay", "environment", "args", "async",
    "poll", "delegate_to", "run_once", "become", "check_mode", "notify", "listen", "with_items",
}


def normalize(condition: str) -> str:
    return " ".join(condition.split())


@dataclass
class Clause:
    text: str
    owner: str  # stable id of the task or block that holds it
    line: int
    file: str
    index: int
    multiple: bool
    seen_true: bool = False
    seen_false: bool = False

    @property
    def label(self) -> str:
        return f"{self.owner}#{self.index}" if self.multiple else self.owner


@dataclass
class Task:
    file: str
    line: int
    name: str
    action: str
    stable: str
    chain: list[Clause]
    include: str = ""
    ran: bool = False
    outcome: dict = field(default_factory=dict)

    @property
    def key(self) -> str:
        return f"{self.file}:{self.line}"

    @property
    def is_fail_path(self) -> bool:
        return self.action in FAIL_ACTIONS or bool(self.action in INCLUDE_ACTIONS and FAIL_INCLUDE.search(self.include))


@dataclass
class Rescue:
    stable: str
    file: str
    line: int
    tasks: list[Task]


class RoleFile:
    """Parse one task file into tasks, `when` conditions and rescue sections."""

    def __init__(self, relative: str, path: Path) -> None:
        self.relative = relative
        self.tasks: list[Task] = []
        self.clauses: list[Clause] = []
        self.rescues: list[Rescue] = []
        self._names: dict[str, int] = {}
        node = yaml.compose(path.read_text(), Loader=yaml.SafeLoader)
        if node is not None:
            self._walk(node, [])

    def _stable(self, name: str, prefix: str = "") -> str:
        base = f"{self.relative}: {prefix}{name}"
        self._names[base] = self._names.get(base, 0) + 1
        return base if self._names[base] == 1 else f"{base} @{self._names[base]}"

    def _clauses(self, mapping: dict, owner: str, line: int) -> list[Clause]:
        node = mapping.get("when")
        if node is None:
            return []
        items = [node] if isinstance(node, yaml.ScalarNode) else list(node.value)
        made = [Clause(normalize(str(item.value)), owner, line, self.relative, i, len(items) > 1)
                for i, item in enumerate(items)]
        self.clauses.extend(made)
        return made

    def _walk(self, sequence: yaml.SequenceNode, chain: list[Clause]) -> list[Task]:
        found: list[Task] = []
        for item in sequence.value:
            mapping = {key.value: value for key, value in item.value}
            line = item.start_mark.line + 1
            name = mapping["name"].value if "name" in mapping else f"line {line}"
            if "block" in mapping:
                stable = self._stable(name, "block ")
                inner = chain + self._clauses(mapping, stable, line)
                found += self._walk(mapping["block"], inner)
                if "rescue" in mapping:
                    rescued = self._walk(mapping["rescue"], inner)
                    found += rescued
                    self.rescues.append(Rescue(stable, self.relative, mapping["rescue"].start_mark.line + 1, rescued))
                if "always" in mapping:
                    found += self._walk(mapping["always"], inner)
                continue
            stable = self._stable(name)
            actions = [key for key in mapping if key not in TASK_KEYWORDS]
            action = actions[0] if actions else ""
            task = Task(self.relative, line, name, action, stable, chain + self._clauses(mapping, stable, line))
            if action in INCLUDE_ACTIONS and isinstance(mapping[action], yaml.ScalarNode):
                task.include = mapping[action].value
            self.tasks.append(task)
            found.append(task)
        return found


@dataclass
class Scope:
    name: str
    files: list[RoleFile]

    @property
    def tasks(self) -> list[Task]:
        return [t for f in self.files for t in f.tasks]

    @property
    def clauses(self) -> list[Clause]:
        return [c for f in self.files for c in f.clauses]

    @property
    def rescues(self) -> list[Rescue]:
        return [r for f in self.files for r in f.rescues]


def load_scope(name: str, role_dir: Path, collections: Path) -> Scope:
    files = []
    for path in sorted((role_dir / "tasks").rglob("*.y*ml")):
        files.append(RoleFile(path.relative_to(collections).as_posix(), path))
    return Scope(name, files)


def merge_runs(directory: Path) -> tuple[list[dict], dict[str, dict]]:
    runs = [json.loads(path.read_text()) for path in sorted(directory.glob("run-*.json"))]
    merged: dict[str, dict] = {}
    for run in runs:
        for key, entry in run["tasks"].items():
            target = merged.setdefault(key, {"false_conditions": {}})
            for outcome in ("ok", "changed", "skipped", "failed", "rescued", "ignored", "when_errors"):
                target[outcome] = target.get(outcome, 0) + entry.get(outcome, 0)
            for condition, count in entry["false_conditions"].items():
                text = normalize(condition)
                target["false_conditions"][text] = target["false_conditions"].get(text, 0) + count
    return runs, merged


def apply_runs(scope: Scope, merged: dict[str, dict]) -> None:
    for task in scope.tasks:
        entry = merged.get(task.key)
        if entry is None:
            continue
        task.outcome = entry
        task.ran = entry.get("ok", 0) + entry.get("failed", 0) > 0
        if task.ran:
            for clause in task.chain:
                clause.seen_true = True
        for condition in entry["false_conditions"]:
            for clause in task.chain:
                if clause.text == condition:
                    clause.seen_false = True
                    break
                clause.seen_true = True


def fired(task: Task) -> bool:
    if task.action in INCLUDE_ACTIONS:
        return task.outcome.get("ok", 0) > 0
    return task.outcome.get("failed", 0) > 0


def asserted_paths(directory: Path, runs: list[dict]) -> dict[str, set[bool]]:
    """Fail paths that fired in a run whose osac_error a test asserted."""
    evidence = set()
    for path in directory.glob("asserted-*.json"):
        doc = json.loads(path.read_text())
        if doc.get("message_checked"):
            evidence.add((doc["message"], bool(doc["retryable"])))
    paths: dict[str, set[bool]] = {}
    for run in runs:
        error = run.get("osac_error")
        if not error:
            continue
        item = (error.get("message"), bool(error.get("retryable")))
        if item in evidence:
            for key in run.get("fired", []):
                paths.setdefault(key, set()).add(item[1])
    return paths


def scope_gaps(scope: Scope, asserted: dict[str, set[bool]]) -> list[tuple[str, str]]:
    """Return (id, where) of every gap of a role."""
    gaps: list[tuple[str, str]] = []
    for task in scope.tasks:
        if not task.ran:
            gaps.append((f"task-never-ran {task.stable}", task.key))
        if task.outcome.get("when_errors"):
            gaps.append((f"when-error {task.stable}", task.key))
    for clause in scope.clauses:
        where = f"{clause.file}:{clause.line}"
        if not clause.seen_true:
            gaps.append((f"when-never-true {clause.label}", where))
        if not clause.seen_false:
            gaps.append((f"when-never-false {clause.label}", where))
    for rescue in scope.rescues:
        if not any(t.ran or t.outcome.get("skipped") for t in rescue.tasks):
            gaps.append((f"rescue-never-entered {rescue.stable}", f"{rescue.file}:{rescue.line}"))
    for task in scope.tasks:
        if task.is_fail_path:
            if not fired(task):
                gaps.append((f"fail-path-never-fired {task.stable}", task.key))
            elif task.key not in asserted:
                gaps.append((f"fail-path-not-asserted {task.stable}", task.key))
    return gaps


def scope_numbers(scope: Scope, asserted: dict[str, set[bool]]) -> dict:
    fail_paths = [t for t in scope.tasks if t.is_fail_path]
    clauses = scope.clauses
    return {
        "tasks": [sum(t.ran for t in scope.tasks), len(scope.tasks)],
        "when_both_sides": [sum(c.seen_true and c.seen_false for c in clauses), len(clauses)],
        "rescues": [sum(any(t.ran or t.outcome.get("skipped") for t in r.tasks) for r in scope.rescues), len(scope.rescues)],
        "fail_paths_fired": [sum(fired(t) for t in fail_paths), len(fail_paths)],
        "fail_paths_asserted": [sum(t.key in asserted for t in fail_paths), len(fail_paths)],
    }


def python_gaps(report: dict | None, templates: str) -> tuple[list[tuple[str, str]], dict]:
    gaps: list[tuple[str, str]] = []
    numbers: dict = {}
    if report is None:
        return gaps, numbers
    by_suffix = {path.replace("\\", "/"): data for path, data in report.get("files", {}).items()}
    for suffix in PYTHON_FILES:
        data = next((d for p, d in by_suffix.items() if p.endswith(f"{templates}/{suffix}")), None)
        if data is None:
            gaps.append((f"python-not-measured {suffix}", suffix))
            continue
        summary = data["summary"]
        numbers[suffix] = {
            "lines": [summary["covered_lines"], summary["num_statements"]],
            "branches": [summary.get("covered_branches", 0), summary.get("num_branches", 0)],
        }
        for line in data.get("missing_lines", []):
            gaps.append((f"python-line {suffix}:{line}", f"{suffix}:{line}"))
        for start, end in data.get("missing_branches", []):
            gaps.append((f"python-branch {suffix}:{start}->{end}", f"{suffix}:{start}"))
    return gaps, numbers


def fraction(pair: list[int]) -> str:
    done, total = pair
    return f"{done}/{total}" + (f" ({100 * done / total:.1f}%)" if total else "")


def print_numbers(name: str, numbers: dict) -> None:
    print(f"{name}:")
    for key, label in (("tasks", "tasks ran"), ("when_both_sides", "when both sides"), ("rescues", "rescues entered"),
                       ("fail_paths_fired", "fail paths fired"), ("fail_paths_asserted", "fail paths asserted")):
        print(f"  {label:<22}{fraction(numbers[key])}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument("--dir", required=True, type=Path, help="directory of the run-*.json files")
    parser.add_argument("--exclusions", type=Path, default=HERE / "exclusions.yaml")
    parser.add_argument("--python-coverage", type=Path, help="pytest-cov JSON report of the plugins in scope")
    parser.add_argument("--json", type=Path, help="also write the numbers and gaps to this file")
    parser.add_argument("--collections", type=Path, default=COLLECTIONS)
    parser.add_argument("--role", action="append", metavar="NAME=PATH", help="gate this role instead of the defaults")
    parser.add_argument("--no-gate", action="store_true", help="print the report and exit 0")
    args = parser.parse_args(argv)

    runs, merged = merge_runs(args.dir)
    if not runs:
        print(f"no task coverage runs in {args.dir}", file=sys.stderr)
        return 2
    asserted = asserted_paths(args.dir, runs)
    print(f"{len(runs)} runs merged from {args.dir}\n")

    gaps: list[tuple[str, str]] = []
    summary: dict = {"roles": {}, "controller": {}}
    roles = dict(item.split("=", 1) for item in args.role) if args.role else GATED_ROLES
    for name, relative in roles.items():
        scope = load_scope(name, args.collections / relative, args.collections)
        apply_runs(scope, merged)
        numbers = scope_numbers(scope, asserted)
        summary["roles"][name] = numbers
        print_numbers(name, numbers)
        gaps += scope_gaps(scope, asserted)

    python = json.loads(args.python_coverage.read_text()) if args.python_coverage else None
    py_gaps, py_numbers = python_gaps(python, "osac/templates")
    summary["python"] = py_numbers
    gaps += py_gaps
    for suffix, numbers in py_numbers.items():
        print(f"python {suffix}: lines {fraction(numbers['lines'])}, branches {fraction(numbers['branches'])}")

    controller = args.collections / REPORTED_ROLES
    if controller.is_dir():
        print("\nnetris.controller (reported, not gated): tasks ran, when both sides")
        for role_dir in sorted(controller.iterdir()):
            if not (role_dir / "tasks").is_dir():
                continue
            scope = load_scope(role_dir.name, role_dir, args.collections)
            apply_runs(scope, merged)
            numbers = scope_numbers(scope, asserted)
            summary["controller"][role_dir.name] = numbers
            if numbers["tasks"][0]:
                print(f"  {role_dir.name:<18}{fraction(numbers['tasks']):<20}{fraction(numbers['when_both_sides'])}")

    exclusions = {}
    if args.exclusions.exists():
        for entry in yaml.safe_load(args.exclusions.read_text()) or []:
            exclusions[entry["gap"]] = entry["reason"]
    open_gaps = [gap for gap in gaps if gap[0] not in exclusions]
    known = {gap[0] for gap in gaps}
    stale = sorted(gap for gap in exclusions if gap not in known)
    excluded = [gap for gap in gaps if gap[0] in exclusions]

    if excluded:
        print(f"\nExcluded ({len(excluded)}):")
        for gap, where in excluded:
            print(f"  {gap}\n      {where}: {exclusions[gap]}")
    if open_gaps:
        print(f"\nGaps ({len(open_gaps)}):")
        for kind in ("task-never-ran", "when-error", "when-never-true", "when-never-false", "rescue-never-entered",
                     "fail-path-never-fired", "fail-path-not-asserted", "python-not-measured", "python-line", "python-branch"):
            group = [g for g in open_gaps if g[0].startswith(kind + " ")]
            if group:
                print(f" {kind} ({len(group)}):")
                for gap, where in group:
                    print(f"  {where}  {gap[len(kind) + 1:]}")
    if stale:
        print(f"\nStale exclusions that match no gap ({len(stale)}):")
        for gap in stale:
            print(f"  {gap}")
    summary["gaps"] = [gap for gap, _ in open_gaps]
    summary["excluded"] = {gap: exclusions[gap] for gap, _ in excluded}
    summary["stale_exclusions"] = stale
    if args.json:
        args.json.write_text(json.dumps(summary, indent=1, sort_keys=True))
    if not open_gaps and not stale:
        print("\nNo gaps.")
        return 0
    return 0 if args.no_gate else 1


if __name__ == "__main__":
    sys.exit(main())
