"""Regression tests for No-Sensitive-Data-In-Logs on the noop DNS backend.

The ``dns.noop.dns`` role must not log record names, zones, values, or
other caller-controlled strings that may contain internal hostnames or
customer identifiers (e.g. ``api-int.<cluster>.<base-domain>``).

These tests parse the shipped task YAML directly, following the pattern
established by ``test_external_access_dns_wait.py``.
"""

from pathlib import Path

import pytest
import yaml


AAP_ROOT = Path(__file__).parents[2]
NOOP_ROLE = (
    AAP_ROOT
    / "collections"
    / "ansible_collections"
    / "dns"
    / "noop"
    / "roles"
    / "dns"
)
CREATE_TASKS = NOOP_ROLE / "tasks" / "create.yaml"
DELETE_TASKS = NOOP_ROLE / "tasks" / "delete.yaml"

# Template variables that carry caller-controlled record data and must
# never appear in task names or log messages.
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
    """Yield (label, path) for each noop entry point."""
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
                f"noop {label} task name contains {var!r}: {name!r}"
            )


def test_debug_messages_do_not_contain_record_variables(task_file):
    """Debug messages must not interpolate record-specific variables."""
    label, path = task_file
    tasks = _load_tasks(path)
    for task in tasks:
        debug = task.get("ansible.builtin.debug", {})
        msg = debug.get("msg", "")
        for var in SENSITIVE_VARS:
            assert var not in msg, (
                f"noop {label} debug msg contains {var!r}"
            )


def test_noop_tasks_are_idempotent(task_file):
    """Noop tasks use only debug and must not modify state."""
    label, path = task_file
    tasks = _load_tasks(path)
    for task in tasks:
        # The only action module should be ansible.builtin.debug
        assert "ansible.builtin.debug" in task, (
            f"noop {label} task uses a module other than debug"
        )
