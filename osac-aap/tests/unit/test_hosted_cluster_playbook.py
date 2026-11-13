"""Regression tests for the hosted-cluster entry point."""

from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]
PLAYBOOK = AAP_ROOT / "playbook_osac_create_hosted_cluster.yml"


def test_ssh_keys_are_not_gated_on_removed_network_class_mapping() -> None:
    play = yaml.safe_load(PLAYBOOK.read_text(encoding="utf-8"))[0]
    ssh_key_setup = next(
        task
        for task in play["pre_tasks"]
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "osac.service.write_ssh_keys"
    )

    assert ssh_key_setup["when"] == [
        "lookup('env', 'SERVER_SSH_KEY') | length > 0"
    ]
