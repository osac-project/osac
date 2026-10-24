"""Keep CaaS agent discovery and cleanup scoped to the ClusterOrder namespace."""

import re
from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
CI_INSTALL = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/install.yaml"
CI_DELETE = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/delete.yaml"
INFRA_CREATE = COLLECTIONS / "service/roles/cluster_infra/tasks/create.yaml"
INFRA_DELETE = COLLECTIONS / "service/roles/cluster_infra/tasks/delete.yaml"
INFRA_ARGS = COLLECTIONS / "service/roles/cluster_infra/meta/argument_specs.yaml"


def _load(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text())


def _cluster_infra_vars(path: Path, step_name: str) -> dict[str, str]:
    step = next(task for task in _load(path) if task["name"] == step_name)
    return step["vars"]


def _render(expression: str, context: dict) -> str:
    def resolve(match: re.Match[str]) -> str:
        value = context
        for key in match.group(1).strip().split("."):
            value = value[key]
        return str(value)

    return re.sub(r"{{\s*(.*?)\s*}}", resolve, expression)


def test_agent_discovery_and_cleanup_use_cluster_order_namespace() -> None:
    context = {
        "cluster_working_namespace": "osac-order-gqfhb-control-plane",
        "cluster_order": {"metadata": {"name": "order-gqfhb", "namespace": "osac-order-gqfhb"}},
    }
    create_vars = _cluster_infra_vars(CI_INSTALL, "Step - Create cluster infrastructure")
    delete_vars = _cluster_infra_vars(CI_DELETE, "Step - Destroy cluster infrastructure")
    external_access_create_vars = _cluster_infra_vars(CI_INSTALL, "Step - Configure external access")
    external_access_delete_vars = _cluster_infra_vars(CI_DELETE, "Step - Remove external access")

    for role_vars in (create_vars, delete_vars):
        assert _render(role_vars["cluster_infra_namespace"], context) == "osac-order-gqfhb-control-plane"
        assert _render(role_vars["cluster_infra_name"], context) == "order-gqfhb"
        assert _render(role_vars["cluster_infra_agent_namespace"], context) == "osac-order-gqfhb"

    for role_vars in (external_access_create_vars, external_access_delete_vars):
        assert _render(role_vars["external_access_namespace"], context) == "osac-order-gqfhb-control-plane"
        assert _render(role_vars["external_access_agent_namespace"], context) == "osac-order-gqfhb"

    create_tasks = {task["name"]: task for task in _load(INFRA_CREATE)}
    control_plane_wait = create_tasks["Wait for control plane namespace"]["kubernetes.core.k8s_info"]
    agents_wait = create_tasks["Wait for agents in ClusterOrder namespace"]["kubernetes.core.k8s_info"]
    assert (
        _render(
            control_plane_wait["name"],
            {"cluster_infra_namespace": "osac-order-gqfhb-control-plane", "cluster_infra_name": "order-gqfhb"},
        )
        == "osac-order-gqfhb-control-plane-order-gqfhb"
    )
    assert _render(agents_wait["namespace"], {"cluster_infra_agent_namespace": "osac-order-gqfhb"}) == (
        "osac-order-gqfhb"
    )

    delete_tasks = {task["name"]: task for task in _load(INFRA_DELETE)}
    cleanup_tasks = {task["name"]: task for task in delete_tasks["Clean up agents if namespace still exists"]["block"]}
    assert delete_tasks["Check if cluster namespace still exists"]["kubernetes.core.k8s_info"]["name"] == (
        "{{ cluster_infra_agent_namespace }}"
    )
    assert cleanup_tasks["Find agents in ClusterOrder namespace"]["kubernetes.core.k8s_info"]["namespace"] == (
        "{{ cluster_infra_agent_namespace }}"
    )
    assert cleanup_tasks["Delete remaining agents"]["kubernetes.core.k8s"]["namespace"] == (
        "{{ cluster_infra_agent_namespace }}"
    )

    args = yaml.safe_load(INFRA_ARGS.read_text())["argument_specs"]["main"]["options"]
    assert args["cluster_infra_agent_namespace"] == {"type": "str", "required": True}
