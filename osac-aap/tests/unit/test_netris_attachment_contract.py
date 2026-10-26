"""Contract tests for the Netris attachment apply and delete entry points."""
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


def _move_vars(tasks):
    for task in _walk(tasks):
        if task.get("ansible.builtin.include_tasks") == "_move_port.yaml":
            return (task.get("vars") or {}).get("network_attachment")
    return None


def test_apply_moves_a_physical_port_from_provisioning_to_the_subnet() -> None:
    move = _move_vars(_tasks("apply_network_attachment"))

    assert move["from_vnet_name"] == "{{ provisioning_vnet }}"
    assert move["to_vnet_name"] == "{{ subnet.metadata.name }}"
    assert move["host_name"] == "{{ _osac_target.host_name }}"
    assert move["logical_interface_name"] == "{{ _osac_interface.name }}"


def test_delete_returns_a_physical_port_to_the_provisioning_network() -> None:
    move = _move_vars(_tasks("delete_network_attachment"))

    assert move["from_vnet_name"] == "{{ subnet.metadata.name }}"
    assert move["to_vnet_name"] == "{{ provisioning_vnet }}"


def test_overlay_attachments_change_nothing_on_the_fabric() -> None:
    for name in ("apply_network_attachment", "delete_network_attachment"):
        tasks = _tasks(name)
        overlay = [t for t in _walk(tasks) if "overlay" in str(t.get("when"))]
        assert overlay, f"{name} has no overlay no-op"
        physical = [t for t in tasks if "physical" in str(t.get("when")) and t.get("block")]
        assert physical, f"{name} does not gate the port move on a physical interface"


def test_legacy_move_entry_point_keeps_using_the_shared_port_helper() -> None:
    tasks = _tasks("move_network_attachment")

    assert any(
        task.get("ansible.builtin.include_tasks") == "_move_port.yaml" for task in tasks
    )
