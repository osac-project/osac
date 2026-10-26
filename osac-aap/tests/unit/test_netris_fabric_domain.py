"""Contract tests for the Netris FabricDomain tasks."""
from __future__ import annotations

from pathlib import Path

import yaml

TASKS = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/roles/netris/tasks"
)


def _tasks(name: str) -> list[dict]:
    return yaml.safe_load((TASKS / f"{name}.yaml").read_text(encoding="utf-8"))


def _walk(tasks):
    for task in tasks:
        yield task
        for key in ("block", "rescue", "always"):
            yield from _walk(task.get(key) or [])


def test_create_reads_only_the_fabric_domain_snapshot_and_the_virtual_network() -> None:
    tasks = list(_walk(_tasks("create_fabric_domain")))

    assert not [task for task in tasks if any(key.startswith("kubernetes.core.") for key in task)]
    assert not [task for task in tasks if "spec.vpcId" in str(task.get("vars", {}))]

    assert_spec = next(
        task
        for task in tasks
        if "fabric_domain_spec.type" in str(task.get("ansible.builtin.assert", {}))
    )
    that = assert_spec["ansible.builtin.assert"]["that"]
    assert any("ethernet_ew" in item for item in that)
    assert any("servers" in item for item in that)


def test_create_resolves_the_vpc_by_virtual_network_name() -> None:
    tasks = _tasks("create_fabric_domain")
    lookups = [
        task
        for task in _walk(tasks)
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.vpc"
    ]

    assert lookups, "the contract path must resolve the parent VPC"
    assert lookups[0]["ansible.builtin.include_role"]["tasks_from"] == "read"
    assert lookups[0]["vars"]["vpc_name"] == "{{ _osac_virtual_network.metadata.name }}"
    assert "_osac_contract" in str(lookups[0].get("when"))


def test_create_emits_only_the_typed_backend_id() -> None:
    tasks = list(_walk(_tasks("create_fabric_domain")))
    result_task = next(
        task
        for task in tasks
        if "_osac_result_data" in str(task.get("ansible.builtin.set_fact", {}))
    )
    expression = str(result_task["ansible.builtin.set_fact"]["_osac_result_data"])
    assert "'backend_id'" in expression
    assert "server_cluster_vpc_id" not in expression

    emit = [task for task in tasks if task.get("ansible.builtin.include_tasks") == "_emit_result.yaml"]
    assert emit, "the typed result must be published through the shared helper"


def test_create_keeps_the_legacy_artifact_on_the_legacy_path() -> None:
    tasks = list(_walk(_tasks("create_fabric_domain")))
    stats = next(task for task in tasks if "ansible.builtin.set_stats" in task)
    assert "server_cluster_id" in stats["ansible.builtin.set_stats"]["data"]
    assert "not _osac_contract" in str(stats.get("when"))


def test_create_publishes_a_sanitized_error() -> None:
    raw = _tasks("create_fabric_domain")
    block = next(task for task in raw if task.get("rescue"))
    publish = next(
        task
        for task in block["rescue"]
        if task.get("ansible.builtin.include_tasks") == "_fail_task.yaml"
    )
    assert "ansible_failed_result.msg" in publish["vars"]["_osac_error_message"]
    assert publish["vars"]["_osac_error_retryable"]


def test_delete_scopes_the_lookup_to_the_virtual_network_vpc() -> None:
    tasks = list(_walk(_tasks("delete_fabric_domain")))
    include = next(
        task
        for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.server_cluster"
    )

    assert include["vars"]["server_cluster_name"] == "{{ fabric_domain_name }}"
    assert include["vars"]["server_cluster_backend_id"] == "{{ _fd_backend_id }}"
    assert include["vars"]["server_cluster_vpc_id"] == "{{ _fd_vpc_id | int }}"


def test_legacy_task_names_forward_to_the_fabric_domain_tasks() -> None:
    for legacy, target in [
        ("create_server_cluster", "create_fabric_domain.yaml"),
        ("delete_server_cluster", "delete_fabric_domain.yaml"),
    ]:
        tasks = _tasks(legacy)
        assert [task.get("ansible.builtin.include_tasks") for task in tasks] == [target]
