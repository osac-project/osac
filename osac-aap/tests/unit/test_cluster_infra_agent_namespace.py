"""Verify the CaaS DNS Agent endpoint contract."""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
CI_INSTALL = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/install.yaml"
CI_DELETE = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/delete.yaml"
INFRA_CREATE = COLLECTIONS / "service/roles/cluster_infra/tasks/create.yaml"
INFRA_DELETE = COLLECTIONS / "service/roles/cluster_infra/tasks/delete.yaml"
CAAS_DNS = COLLECTIONS / "service/roles/external_access/tasks/caas_dns_create.yaml"


def _load(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text())


def _step_vars(path: Path, step_name: str) -> dict[str, str]:
    step = next(task for task in _load(path) if task["name"] == step_name)
    return step["vars"]


def _render(expression: str, context: dict) -> str:
    return str(Templar(loader=DataLoader(), variables=context).template(expression))


def test_caas_dns_uses_cluster_order_agent_namespace() -> None:
    context = {
        "cluster_working_namespace": "osac-order-gqfhb-control-plane",
        "cluster_order_label": "osac.openshift.io/clusterorder",
        "cluster_order": {"metadata": {"name": "order-gqfhb", "namespace": "osac-order-gqfhb"}},
    }
    external_access_create_vars = _step_vars(CI_INSTALL, "Step - Configure external access")
    external_access_delete_vars = _step_vars(CI_DELETE, "Step - Remove external access")

    for role_vars in (external_access_create_vars, external_access_delete_vars):
        assert _render(role_vars["external_access_namespace"], context) == "osac-order-gqfhb-control-plane"
        assert _render(role_vars["external_access_agent_namespace"], context) == "osac-order-gqfhb"

    install_defaults = _load(CI_INSTALL)[0]["ansible.builtin.set_fact"]
    assert "install_step_wait_for_agents_default" not in install_defaults
    assert "Step - Wait for operator-attached Agents" not in CI_INSTALL.read_text()
    assert "delete_step_cluster_infra_default" not in CI_DELETE.read_text()
    assert "Delete remaining agents" not in INFRA_DELETE.read_text()

    dns_tasks = {task["name"]: task for task in _load(CAAS_DNS)}
    agent_lookup = dns_tasks["Get agents assigned to this cluster"]["kubernetes.core.k8s_info"]
    assert agent_lookup["namespace"] == "{{ external_access_agent_namespace }}"
    assert agent_lookup["label_selectors"] == [
        "{{ cluster_order_label }}={{ external_access_name }}"
    ]
    assert "external_access_ci_agents.resources[0]" in str(
        dns_tasks["Set ingress IP from first agent"]
    )


def test_caas_dns_waits_for_inventory_ip_without_agent_mutation() -> None:
    tasks = {task["name"]: task for task in _load(CAAS_DNS)}
    agent_lookup = tasks["Get agents assigned to this cluster"]
    until = str(agent_lookup["until"])

    assert "status.inventory.interfaces" in until
    assert "ipV4Addresses" in until
    assert "kubernetes.core.k8s_json_patch" not in str(tasks)
    assert "spec.approved" not in str(tasks)
    assert "state: absent" not in str(tasks)
    assert agent_lookup["no_log"] is True


def test_cluster_infra_remains_a_provider_dispatcher() -> None:
    create = _load(INFRA_CREATE)
    delete = _load(INFRA_DELETE)

    assert create[0]["ansible.builtin.include_role"]["name"] == "{{ network_steps_collection }}.cluster_infra"
    assert create[0]["ansible.builtin.include_role"]["tasks_from"] == "create.yaml"
    assert delete[0]["ansible.builtin.include_role"]["name"] == "{{ network_steps_collection }}.cluster_infra"
    assert delete[0]["ansible.builtin.include_role"]["tasks_from"] == "delete.yaml"
