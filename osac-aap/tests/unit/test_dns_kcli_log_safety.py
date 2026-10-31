"""Regression tests for No-Sensitive-Data-In-Logs on the kcli DNS backend.

The ``dns.kcli.dns`` role must not log record names, zones, values, or
other caller-controlled strings in task names that appear in Ansible
output.

These tests parse the shipped task YAML directly, following the pattern
established by ``test_dns_noop_log_safety.py``.
"""

from pathlib import Path

import pytest
import yaml


AAP_ROOT = Path(__file__).parents[2]
KCLI_ROLE = (
    AAP_ROOT
    / "collections"
    / "ansible_collections"
    / "dns"
    / "kcli"
    / "roles"
    / "dns"
)
CREATE_TASKS = KCLI_ROLE / "tasks" / "create.yaml"
DELETE_TASKS = KCLI_ROLE / "tasks" / "delete.yaml"

SENSITIVE_VARS = [
    "dns_record_name",
    "dns_record_value",
    "dns_record_type",
    "dns_zone",
    "dns_record_ttl",
]


def _load_tasks(path: Path) -> list[dict]:
    """Load the task list from a YAML file."""
    return yaml.safe_load(path.read_text())


@pytest.fixture(params=["create", "delete"])
def task_file(request):
    """Yield (label, path) for each kcli entry point."""
    if request.param == "create":
        return ("create", CREATE_TASKS)
    return ("delete", DELETE_TASKS)


def test_task_names_do_not_contain_record_variables(task_file):
    """Task names must not interpolate record-specific variables."""
    label, path = task_file
    tasks = _load_tasks(path)
    for task in tasks:
        name = task.get("name", "")
        for var in SENSITIVE_VARS:
            assert var not in name, (
                f"kcli {label} task name contains {var!r}: {name!r}"
            )


def test_shell_commands_use_no_log(task_file):
    """Tasks that run commands must set no_log: true."""
    label, path = task_file
    tasks = _load_tasks(path)
    for task in tasks:
        if "ansible.builtin.shell" in task or "ansible.builtin.command" in task:
            assert task.get("no_log") is True, (
                f"kcli {label} task missing no_log: {task.get('name')!r}"
            )
