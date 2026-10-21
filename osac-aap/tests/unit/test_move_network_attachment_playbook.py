import json
from pathlib import Path

import yaml
from jinja2 import Environment


PLAYBOOK = Path(__file__).resolve().parents[2] / "playbook_osac_move_network_attachment.yml"
AGENTLESS_NET_MOVE_TASKS = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/roles/agentless_net/tasks/_move_network_attachment_impl.yaml"
)


def test_move_playbook_resolves_subnets_with_controller_credentials():
    play = yaml.safe_load(PLAYBOOK.read_text(encoding="utf-8"))[0]
    lookup_task = next(
        task
        for task in play["tasks"]
        if task.get("name") == "Resolve tenant network segment name for each attachment"
    )
    lookup = lookup_task["kubernetes.core.k8s_info"]

    assert lookup["api_version"] == "osac.openshift.io/v1alpha1"
    assert lookup["kind"] == "Subnet"
    assert lookup["namespace"] == "{{ resource.metadata.namespace }}"
    assert lookup["label_selectors"] == [
        "osac.openshift.io/subnet-uuid={{ item.subnetRef }}"
    ]
    assert "host" not in lookup
    assert "api_key" not in lookup
    assert "ca_cert" not in lookup
    assert lookup_task["no_log"] is True


def test_agentless_net_delegated_switch_tasks_read_port_mapping_from_controller_hostvars():
    tasks = yaml.safe_load(AGENTLESS_NET_MOVE_TASKS.read_text(encoding="utf-8"))
    mapping = "hostvars['localhost']['_agentless_net_attachment_port']"
    delegated_tasks = {
        task["name"]: task
        for task in tasks
        if task["name"]
        in {
            "Inspect the mapped Cumulus access port before moving it",
            "Configure the mapped physical port with the shared AgentlessNet port role",
            "Verify the mapped Cumulus access port converged to its destination VLAN",
        }
    }

    assert len(delegated_tasks) == 3
    inspect = delegated_tasks["Inspect the mapped Cumulus access port before moving it"]
    assert mapping in inspect["delegate_to"]
    assert mapping in inspect["ansible.builtin.raw"]
    assert mapping in inspect["vars"]["ansible_host"]

    configure = delegated_tasks[
        "Configure the mapped physical port with the shared AgentlessNet port role"
    ]
    include_role = configure["ansible.builtin.include_role"]
    assert mapping in include_role["apply"]["delegate_to"]
    assert mapping in configure["vars"]["ansible_host"]
    assert mapping in configure["vars"]["port_name"]

    verify = delegated_tasks[
        "Verify the mapped Cumulus access port converged to its destination VLAN"
    ]
    assert mapping in verify["delegate_to"]
    assert mapping in verify["ansible.builtin.raw"]
    assert mapping in verify["vars"]["ansible_host"]


def test_access_port_assert_reads_ifname_from_bridge_json_mapping():
    tasks = yaml.safe_load(AGENTLESS_NET_MOVE_TASKS.read_text(encoding="utf-8"))
    verify = next(
        task
        for task in tasks
        if task.get("name") == "Verify the moved port is an untagged PVID access port"
    )
    conditions = verify["ansible.builtin.assert"]["that"]

    assert any(".get('ifname')" in condition for condition in conditions)
    assert all("attr('ifname')" not in condition for condition in conditions)

    render = Environment()
    render.filters["from_json"] = json.loads
    result = render.compile_expression(conditions[1])(
        _agentless_net_attachment_vlan_after={
            "stdout": json.dumps([{"ifname": "swp2", "vlans": []}])
        },
        _agentless_net_attachment_port={"port": "swp2"},
    )

    assert result is True
