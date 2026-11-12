"""Verify CI endpoint scoping and separation from Agent lifecycle."""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
CI_INSTALL = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/install.yaml"
CI_DELETE = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/delete.yaml"
CAAS_TASKS = COLLECTIONS / "service/roles/external_access/tasks"


def _load(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text())


def _step_vars(path: Path, step_name: str) -> dict[str, str]:
    step = next(task for task in _load(path) if task["name"] == step_name)
    return step["vars"]


def _render(expression: str, context: dict) -> str:
    return str(Templar(loader=DataLoader(), variables=context).template(expression))


def test_caas_external_access_uses_control_plane_namespace() -> None:
    context = {
        "cluster_working_namespace": "osac-order-gqfhb-control-plane",
        "cluster_order_label": "osac.openshift.io/clusterorder",
        "cluster_order": {"metadata": {"name": "order-gqfhb", "namespace": "osac-order-gqfhb"}},
    }
    external_access_create_vars = _step_vars(CI_INSTALL, "Step - Configure external access")
    external_access_delete_vars = _step_vars(CI_DELETE, "Step - Remove external access")

    for role_vars in (external_access_create_vars, external_access_delete_vars):
        assert _render(role_vars["external_access_namespace"], context) == "osac-order-gqfhb-control-plane"
        assert "external_access_agent_namespace" not in role_vars

    ingress = _load(CAAS_TASKS / "caas_ingress_create.yaml")
    credentials = next(task for task in ingress if "retrieve_kubeconfig" in str(task))
    assert _render(
        credentials["vars"]["retrieve_kubeconfig_namespace"],
        context | external_access_create_vars,
    ) == "osac-order-gqfhb-control-plane"

    release = _load(CAAS_TASKS / "caas_vip_release.yaml")
    lookups = {task["kubernetes.core.k8s_info"]["kind"]: task["kubernetes.core.k8s_info"]
               for task in release if "kubernetes.core.k8s_info" in task}
    assert _render(lookups["ClusterOrder"]["namespace"], context) == "osac-order-gqfhb"
    assert _render(
        lookups["HostedCluster"]["namespace"], context | external_access_delete_vars,
    ) == "osac-order-gqfhb-control-plane"

    install_defaults = _load(CI_INSTALL)[0]["ansible.builtin.set_fact"]
    assert "install_step_wait_for_agents_default" not in install_defaults
    assert "Step - Wait for operator-attached Agents" not in CI_INSTALL.read_text()
    assert "delete_step_cluster_infra_default" not in CI_DELETE.read_text()
    assert "cluster_infra" not in CI_DELETE.read_text()


def test_caas_external_access_does_not_read_or_mutate_agents() -> None:
    for path in CAAS_TASKS.glob("caas_*.yaml"):
        text = path.read_text()
        assert "kind: Agent" not in text, path.name
        assert "inventory.interfaces" not in text, path.name
        assert "external_access_agent_namespace" not in text, path.name
        assert "spec.approved" not in text, path.name
