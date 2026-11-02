# Copyright (c) 2026 Red Hat, Inc.
# SPDX-License-Identifier: Apache-2.0
"""Record what every role task of a run did, for tests/coverage/role_coverage.py.

Test tooling, not shipped in a collection. Enable it through the environment,
which child ansible-playbook processes inherit:

  ANSIBLE_CALLBACK_PLUGINS=<osac-aap>/tests/coverage/callback_plugins
  ANSIBLE_CALLBACKS_ENABLED=osac_task_coverage
  OSAC_TASK_COVERAGE_DIR=<directory>

Each process writes one JSON file <directory>/run-<pid>-<random>.json:

  {"schema": 1, "playbook": ..., "osac_error": {...} or null,
   "fired": ["<task key>", ...],
   "tasks": {"<task key>": {"name": ..., "action": ..., "ok": n, "changed": n,
                            "skipped": n, "failed": n, "rescued": n,
                            "ignored": n, "when_errors": n,
                            "false_conditions": {"<when>": n}}}}

A task key is <namespace>/<collection>/roles/<role>/tasks/<file>:<line>. Only
the roles in ROLE_PATTERN are recorded. `failed` counts every failure;
`rescued` and `ignored` are the failures a rescue caught or ignore_errors let
through. `false_conditions` holds the `when` text Ansible reported as false.
`when_errors` counts failures of the `when` itself, which are not a task run.
"""

from __future__ import annotations

import atexit
import json
import os
import re
import uuid
from pathlib import Path

from ansible.plugins.callback import CallbackBase

DOCUMENTATION = r"""
name: osac_task_coverage
type: notification
short_description: Record the outcome of the role tasks of a run
description:
  - Writes one JSON file per ansible-playbook process to OSAC_TASK_COVERAGE_DIR.
requirements:
  - enabled in ANSIBLE_CALLBACKS_ENABLED
"""

ROLE_PATTERN = re.compile(
    r"ansible_collections/("
    r"(?:osac/templates/roles/(?:netris|cudn_evpn)|netris/controller/roles/[^/]+)"
    r"/tasks/[^:]+:\d+)$"
)
FAIL_INCLUDES = re.compile(r"(^|/)(_fail|_stop)\.yaml$")
OUTCOMES = ("ok", "changed", "skipped", "failed", "rescued", "ignored", "when_errors")


def task_key(task) -> str | None:
    """The role-relative file:line of a task, or None when it is out of scope."""
    match = ROLE_PATTERN.search(task.get_path() or "")
    return match.group(1) if match else None


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "notification"
    CALLBACK_NAME = "osac_task_coverage"
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self) -> None:
        super().__init__()
        self._directory = os.environ.get("OSAC_TASK_COVERAGE_DIR", "")
        self._tasks: dict[str, dict] = {}
        self._fired: list[str] = []
        self._pending_failure: str | None = None
        self._playbook = ""
        self._osac_error: dict | None = None
        self._osac_result_keys: list[str] | None = None
        self._written = False
        if self._directory:
            atexit.register(self._write)

    # -- bookkeeping -------------------------------------------------------

    def _entry(self, task) -> dict | None:
        key = task_key(task)
        if key is None:
            return None
        entry = self._tasks.get(key)
        if entry is None:
            entry = {"name": task.get_name(), "action": task.action, "false_conditions": {}}
            entry.update(dict.fromkeys(OUTCOMES, 0))
            self._tasks[key] = entry
        return entry

    def _note_false_condition(self, entry: dict, result: dict) -> None:
        condition = result.get("false_condition")
        if isinstance(condition, str):
            entry["false_conditions"][condition] = entry["false_conditions"].get(condition, 0) + 1

    def _fire(self, task) -> None:
        key = task_key(task)
        if key is not None and key not in self._fired:
            self._fired.append(key)

    def _write(self) -> None:
        if self._written or not self._directory or not self._tasks:
            return
        self._written = True
        directory = Path(self._directory)
        directory.mkdir(parents=True, exist_ok=True)
        document = {
            "schema": 1,
            "playbook": self._playbook,
            "osac_error": self._osac_error,
            "osac_result_keys": self._osac_result_keys,
            "fired": self._fired,
            "tasks": self._tasks,
        }
        target = directory / f"run-{os.getpid()}-{uuid.uuid4().hex[:12]}.json"
        temporary = target.with_suffix(".tmp")
        temporary.write_text(json.dumps(document, sort_keys=True))
        temporary.replace(target)

    # -- Ansible events ----------------------------------------------------

    def v2_playbook_on_start(self, playbook) -> None:
        self._playbook = Path(getattr(playbook, "_file_name", "") or "").name

    def v2_playbook_on_task_start(self, task, is_conditional) -> None:
        # The play goes on after a failure only through a rescue: this
        # role has no `always`, and ignore_errors is counted on its own.
        if self._pending_failure is not None:
            self._tasks[self._pending_failure]["rescued"] += 1
            self._pending_failure = None

    def v2_runner_on_ok(self, result) -> None:
        entry = self._entry(result._task)
        if entry is None:
            return
        entry["ok"] += 1
        if result._result.get("changed"):
            entry["changed"] += 1
        if FAIL_INCLUDES.search(str(result._task.args.get("_raw_params", ""))):
            self._fire(result._task)

    def v2_runner_on_skipped(self, result) -> None:
        entry = self._entry(result._task)
        if entry is None:
            return
        entry["skipped"] += 1
        self._note_false_condition(entry, result._result)

    def v2_runner_item_on_skipped(self, result) -> None:
        entry = self._entry(result._task)
        if entry is not None:
            self._note_false_condition(entry, result._result)

    def v2_runner_on_failed(self, result, ignore_errors=False) -> None:
        entry = self._entry(result._task)
        if entry is None:
            return
        if str(result._result.get("msg", "")).startswith("The conditional check"):
            entry["when_errors"] += 1
        else:
            entry["failed"] += 1
        if ignore_errors:
            entry["ignored"] += 1
            return
        self._fire(result._task)
        self._pending_failure = task_key(result._task)

    def v2_runner_on_unreachable(self, result) -> None:
        self.v2_runner_on_failed(result)

    def v2_playbook_on_stats(self, stats) -> None:
        custom = getattr(stats, "custom", {}).get("_run", {})
        error = custom.get("osac_error")
        self._osac_error = error if isinstance(error, dict) else None
        published = custom.get("osac_result")
        self._osac_result_keys = sorted(published) if isinstance(published, dict) else None
        self._write()
