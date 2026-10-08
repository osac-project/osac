"""Regression tests for Netris subnet provisioning task wiring."""

from pathlib import Path

import yaml


CREATE_SUBNET_TASKS = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/roles/netris/tasks/create_subnet.yaml"
)


def test_read_vnet_fabric_outputs_does_not_shadow_vnet_id() -> None:
    tasks = yaml.safe_load(CREATE_SUBNET_TASKS.read_text())
    read_task = next(task for task in tasks if task["name"] == "Read V-Net fabric outputs")

    # The preceding vnet role sets vnet_id as a fact. Rebinding it to itself
    # creates an Ansible recursive-variable error when the role is included.
    assert read_task["ansible.builtin.include_role"] == {
        "name": "netris.controller.vnet",
        "tasks_from": "read_by_id",
    }
    assert "vars" not in read_task
