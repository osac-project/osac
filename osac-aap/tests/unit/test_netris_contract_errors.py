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
