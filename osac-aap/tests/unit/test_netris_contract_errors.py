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
    assert "_netris_failure_origin_detail" in reporter
    assert "ansible_failed_result" not in reporter


def test_preserve_failure_keeps_only_a_sanitized_summary() -> None:
    facts = _tasks("_preserve_failure")[0]["ansible.builtin.set_fact"]

    assert "netris_safe_error_detail" in facts["_netris_failure_origin_detail"]
    assert "_netris_failure_origin_result" not in facts


def test_contract_rescue_publishes_the_sanitized_detail() -> None:
    tasks = _tasks("_contract_rescue")

    build = next(task for task in tasks if task.get("ansible.builtin.set_fact"))
    facts = build["ansible.builtin.set_fact"]
    assert "netris_safe_error_detail" in facts["_osac_rescue_message"]
    assert "netris_retryable_error" in facts["_osac_rescue_retryable"]


def test_no_rescue_reraises_an_include_scoped_variable() -> None:
    pattern = 'msg: "{{ _osac_error_message }}"'
    offenders = [
        path.name
        for path in TASKS.glob("*.yaml")
        if pattern in path.read_text(encoding="utf-8")
    ]

    assert not offenders, (
        "include vars do not survive the include; these rescues fail with an "
        f"undefined variable instead of the real error: {offenders}"
    )


def test_missing_connection_settings_publish_the_setting_marker() -> None:
    auth = (
        TASKS.parents[4] / "netris/controller/roles/auth/tasks/main.yaml"
    ).read_text(encoding="utf-8")

    assert "Netris setting error" in auth
    for setting in ("NETRIS_CONTROLLER_URL", "NETRIS_USERNAME", "NETRIS_PASSWORD"):
        assert setting in auth


def _walk_with_ancestors(tasks, ancestors=()):
    for task in tasks or []:
        if not isinstance(task, dict):
            continue
        yield task, ancestors
        for key in ("block", "rescue", "always"):
            yield from _walk_with_ancestors(task.get(key) or [], ancestors + (task,))


def test_dhcp_provider_reads_are_inside_the_rescue() -> None:
    reads = [
        (task, ancestors)
        for task, ancestors in _walk_with_ancestors(_tasks("query_dhcp_lease"))
        if (task.get("ansible.builtin.include_role") or {}).get(
            "name"
        ) == "netris.controller.ipam"
    ]

    assert len(reads) == 2, "expected the subnet and host IPAM reads"
    for task, ancestors in reads:
        rescued = any(
            any(
                included.get("ansible.builtin.include_tasks")
                == "_contract_rescue.yaml"
                for included in (ancestor.get("rescue") or [])
            )
            for ancestor in ancestors
        )
        assert rescued, f"{task.get('name')} runs outside the DHCP rescue boundary"


def test_dhcp_counts_only_active_dhcp_leases() -> None:
    raw = (TASKS / "query_dhcp_lease.yaml").read_text(encoding="utf-8")

    assert "netris_active_dhcp_addresses" in raw
    assert "_dq_matched_hosts" not in raw


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
