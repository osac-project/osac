"""Contract error-publication tests for the Netris task entry points."""
from __future__ import annotations

from pathlib import Path

import yaml

TASKS = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/roles/netris/tasks"
)


def _tasks(name: str) -> list[dict]:
    return yaml.safe_load((TASKS / f"{name}.yaml").read_text(encoding="utf-8"))


def _rescue_includes(tasks):
    for task in tasks:
        for rescued in task.get("rescue") or []:
            if rescued.get("ansible.builtin.include_tasks") == "_contract_rescue.yaml":
                yield rescued


def test_nat_gateway_contract_create_publishes_a_sanitized_error() -> None:
    rescue = list(_rescue_includes(_tasks("create_nat_gateway")))

    assert rescue, "the contract NATGateway block must publish osac_error on failure"
    assert rescue[0]["vars"]["_osac_error_label"] == "NATGateway create"


def test_dhcp_query_publishes_a_retryable_error() -> None:
    rescue = list(_rescue_includes(_tasks("query_dhcp_lease")))

    assert rescue, "the DHCP query must publish osac_error on failure"
    assert rescue[0]["vars"]["_osac_error_retryable_default"] is True


# Task files that serve both the contract envelope and the legacy playbooks in
# one block. Their rescue re-raises a plain message, so the underlying result
# must be preserved for the playbook-level legacy reporter.
SHARED_TASK_FILES = (
    "attach_external_ip",
    "create_external_ip_pool",
    "create_fabric_domain",
    "create_nat_gateway",
    "create_subnet",
    "create_virtual_network",
    "delete_external_ip_pool",
    "delete_fabric_domain",
    "delete_subnet",
    "delete_virtual_network",
    "move_network_attachment",
)


def test_shared_task_rescues_preserve_the_provider_failure() -> None:
    for name in SHARED_TASK_FILES:
        includes = [
            rescued.get("ansible.builtin.include_tasks")
            for task in _tasks(name)
            for rescued in task.get("rescue") or []
        ]
        assert "_preserve_failure.yaml" in includes, (
            f"{name} re-raises a plain message; without _preserve_failure.yaml the "
            "legacy reporter loses the HTTP status and provider body"
        )


def test_legacy_reporter_prefers_the_preserved_failure() -> None:
    reporter = _tasks("_report_failure")[0]["ansible.builtin.fail"]["msg"]

    assert "_netris_failure_origin_task" in reporter
    assert "_netris_failure_origin_result" in reporter


def _walk(tasks):
    for task in tasks or []:
        if not isinstance(task, dict):
            continue
        yield task
        for key in ("block", "rescue", "always"):
            yield from _walk(task.get(key))


def _includes(tasks):
    for task in _walk(tasks):
        for key, value in task.items():
            if key.endswith("include_tasks") and isinstance(value, str):
                yield value


def _publishes(name: str, depth: int = 2) -> bool:
    tasks = _tasks(name)
    if any(
        value in ("_fail_task.yaml", "_contract_rescue.yaml")
        for task in _walk(tasks)
        for key, value in task.items()
        if key.endswith("include_tasks")
    ):
        return True
    return depth > 0 and any(
        _publishes(include.removesuffix(".yaml"), depth - 1)
        for include in _includes(tasks)
        if include.startswith("_")
    )


def test_every_routed_task_publishes_a_sanitized_error() -> None:
    spec = yaml.safe_load((TASKS.parent / "meta" / "argument_specs.yaml").read_text())
    missing = [name for name in spec["argument_specs"] if not _publishes(name)]

    assert not missing, (
        "these contract task entry points never publish osac_error, so a "
        f"transient provider failure is treated as final: {missing}"
    )
