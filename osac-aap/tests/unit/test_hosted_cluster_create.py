"""Guard CaaS creation Agent waits and hosted-cluster lock cleanup."""

from pathlib import Path

import yaml


COMPONENT_ROOT = Path(__file__).resolve().parents[2]
CREATE_PLAYBOOK = COMPONENT_ROOT / "playbook_osac_create_hosted_cluster.yml"
CI_INFRA_TASKS = (
    COMPONENT_ROOT
    / "collections/ansible_collections/osac/templates/roles/ocp_ci_small/tasks/ci_cluster_infra_create.yaml"
)


def test_ci_agent_wait_handles_missing_resources_and_covers_provisioning() -> None:
    tasks = yaml.safe_load(CI_INFRA_TASKS.read_text())
    wait = next(task for task in tasks if task["name"] == "Wait for this order's agents in the Agent namespace")
    selection = next(task for task in tasks if task["name"] == "Select unapproved agents")

    assert "(ci_agents.resources | default([])) | length" in wait["until"]
    assert wait["retries"] >= 270
    assert wait["delay"] == 10
    assert "(ci_agents.resources | default([]))" in selection["ansible.builtin.set_fact"]["ci_unapproved_agents"]


def test_hosted_cluster_lock_is_released_after_success_or_failure() -> None:
    play = yaml.safe_load(CREATE_PLAYBOOK.read_text())[0]
    create_task = play["tasks"][0]
    acquire = create_task["block"][0]
    mark_acquired = create_task["block"][1]
    release = create_task["always"][0]

    assert acquire["name"] == "Acquire cluster lock"
    assert mark_acquired["name"] == "Mark cluster lock acquired"
    assert mark_acquired["ansible.builtin.set_fact"] == {"cluster_order_lock_acquired": True}
    assert release["name"] == "Release cluster lock"
    assert release["vars"]["lease_state"] == "absent"
    assert release["when"] == "cluster_order_lock_acquired | default(false)"
