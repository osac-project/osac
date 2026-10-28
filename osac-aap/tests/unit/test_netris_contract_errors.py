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
    "create_virtual_network",
    "delete_external_ip_pool",
    "delete_fabric_domain",
    "delete_virtual_network",
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
