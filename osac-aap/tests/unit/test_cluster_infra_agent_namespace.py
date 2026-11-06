"""Verify the CaaS endpoint contract no longer reads Agent inventory.

The CI CaaS ingress target used to be the first non-loopback address of an
Agent, which is not a stable or cluster-owned ingress endpoint. The endpoint is
now a reserved VIP configured on the guest router, so the CaaS external-access
flow must not read or mutate Agents at all.
"""

from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
CI_INSTALL = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/install.yaml"
CI_DELETE = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/delete.yaml"
INFRA_CREATE = COLLECTIONS / "service/roles/cluster_infra/tasks/create.yaml"
INFRA_DELETE = COLLECTIONS / "service/roles/cluster_infra/tasks/delete.yaml"
CAAS_TASKS = COLLECTIONS / "service/roles/external_access/tasks"

CAAS_FLOW = (
    "caas_create.yaml",
    "caas_api_dns_create.yaml",
    "caas_ingress_create.yaml",
    "caas_dns_names.yaml",
    "caas_dns_publish.yaml",
    "caas_dns_delete.yaml",
    "caas_vip_reserve.yaml",
    "caas_vip_release.yaml",
)


def _load(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text())


def _step_vars(path: Path, step_name: str) -> dict[str, str]:
    step = next(task for task in _load(path) if task["name"] == step_name)
    return step["vars"]


def test_caas_steps_still_scope_external_access_to_the_control_plane_namespace() -> None:
    install_vars = _step_vars(CI_INSTALL, "Step - Configure external access")
    delete_vars = _step_vars(CI_DELETE, "Step - Remove external access")

    for role_vars in (install_vars, delete_vars):
        assert role_vars["external_access_namespace"] == "{{ cluster_working_namespace }}"
        assert role_vars["external_access_name"] == "{{ cluster_order.metadata.name }}"
        assert "external_access_agent_namespace" not in role_vars

    install_defaults = _load(CI_INSTALL)[0]["ansible.builtin.set_fact"]
    assert "install_step_wait_for_agents_default" not in install_defaults
    assert "Step - Wait for operator-attached Agents" not in CI_INSTALL.read_text()
    assert "delete_step_cluster_infra_default" not in CI_DELETE.read_text()
    assert "Delete remaining agents" not in INFRA_DELETE.read_text()


def test_caas_external_access_no_longer_reads_agent_inventory() -> None:
    for name in CAAS_FLOW:
        text = (CAAS_TASKS / name).read_text()

        assert "kind: Agent" not in text, name
        assert "agent-install.openshift.io" not in text, name
        assert "external_access_agent_namespace" not in text, name
        assert "inventory.interfaces" not in text, name
        assert "ipV4Addresses" not in text, name


def test_caas_external_access_never_mutates_agents() -> None:
    for name in CAAS_FLOW:
        text = (CAAS_TASKS / name).read_text()

        assert "k8s_json_patch" not in text, name
        assert "spec.approved" not in text, name
        if "state: absent" in text:
            # Only the VIP claim release may remove anything.
            assert "osac.service.ingress_vip_claim" in text, name


def test_cluster_infra_remains_a_provider_dispatcher() -> None:
    create = _load(INFRA_CREATE)
    delete = _load(INFRA_DELETE)

    assert create[0]["ansible.builtin.include_role"]["name"] == "{{ network_steps_collection }}.cluster_infra"
    assert create[0]["ansible.builtin.include_role"]["tasks_from"] == "create.yaml"
    assert delete[0]["ansible.builtin.include_role"]["name"] == "{{ network_steps_collection }}.cluster_infra"
    assert delete[0]["ansible.builtin.include_role"]["tasks_from"] == "delete.yaml"
