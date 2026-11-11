"""Verify opt-in ACM registration for CaaS HostedClusters (OSAC-6313).

These tests cover metadata rendering and template wiring only. Labels do
not disable the HyperShift import controller: test_hosted_cluster_acm_workflow
covers the disabled-policy prerequisite and explicit registration tasks.
AI MaaS opts in because hub registration is the WorkloadCluster import
prerequisite (OSAC-6488).
"""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
ROLE_TASKS = COLLECTIONS / "service/roles/hosted_cluster/tasks"
ROLE_DEFAULTS = COLLECTIONS / "service/roles/hosted_cluster/defaults/main.yml"
OCP_SMALL_INSTALL = COLLECTIONS / "templates/roles/ocp_small/tasks/install.yaml"
OCP_CI_SMALL_INSTALL = COLLECTIONS / "templates/roles/ocp_ci_small/tasks/install.yaml"
AI_MAAS_INSTALL = COLLECTIONS / "templates/roles/ocp_4_20_ai_maas/tasks/install.yaml"
AI_MAAS_DEFAULTS = COLLECTIONS / "templates/roles/ocp_4_20_ai_maas/defaults/main.yaml"
AI_MAAS_OSAC = COLLECTIONS / "templates/roles/ocp_4_20_ai_maas/meta/osac.yaml"

CLUSTERSET_LABEL = "cluster.open-cluster-management.io/clusterset"
CLUSTER_ORDER_LABEL = "osac.openshift.io/clusterorder"


def _load(path: Path) -> list[dict] | dict:
    return yaml.safe_load(path.read_text())


def _task(path: Path, name: str) -> dict:
    return next(task for task in _load(path) if task["name"] == name)


def _render_labels(enable_acm_registration: bool) -> dict:
    """Render the HostedCluster labels chain with real Ansible filters."""
    context = {"hosted_cluster_enable_acm_registration": enable_acm_registration}
    templar = Templar(loader=DataLoader(), variables=context)
    clusterset = templar.template(_load(ROLE_DEFAULTS)["hosted_cluster_acm_clusterset_label"])

    build = _task(ROLE_TASKS / "create_hosted_cluster.yaml", "Build HostedCluster definition")
    expression = build["ansible.builtin.set_fact"]["hosted_cluster_definition"]["metadata"]["labels"]
    labels_context = {
        "hosted_cluster_acm_clusterset_label": clusterset,
        "hosted_cluster_default_cluster_order_label": {CLUSTER_ORDER_LABEL: "order-abc"},
    }
    return Templar(loader=DataLoader(), variables=labels_context).template(expression)


def test_regular_clusters_omit_clusterset_label() -> None:
    labels = _render_labels(False)
    assert CLUSTERSET_LABEL not in labels
    assert labels == {CLUSTER_ORDER_LABEL: "order-abc"}


def test_optin_clusters_include_clusterset_label() -> None:
    labels = _render_labels(True)
    assert labels[CLUSTERSET_LABEL] == "default"
    assert labels[CLUSTER_ORDER_LABEL] == "order-abc"


def test_role_defaults_to_excluded() -> None:
    assert _load(ROLE_DEFAULTS)["hosted_cluster_enable_acm_registration"] is False


def test_managed_cluster_delete_remains_unconditional() -> None:
    """Cleanup must also cover clusters registered before the flag existed."""
    delete = _task(ROLE_TASKS / "delete_hosted_cluster.yaml", "Delete ManagedCluster resource")
    assert "when" not in delete


def test_ocp_small_passes_flag_to_hosted_cluster() -> None:
    step = _task(OCP_SMALL_INSTALL, "Step - Create hosted cluster")
    assert step["vars"]["hosted_cluster_enable_acm_registration"] == ("{{ ocp_small_enable_acm_registration | bool }}")


def test_ocp_ci_small_passes_flag_to_hosted_cluster() -> None:
    step = _task(OCP_CI_SMALL_INSTALL, "Step - Create hosted cluster")
    assert step["vars"]["hosted_cluster_enable_acm_registration"] == (
        "{{ ocp_ci_small_enable_acm_registration | bool }}"
    )


def test_ai_maas_opts_in() -> None:
    include = _task(AI_MAAS_INSTALL, "Create cluster with RHOAI + MaaS post-install")
    assert include["vars"]["ocp_small_enable_acm_registration"] == (
        "{{ ocp_4_20_ai_maas_enable_acm_registration | bool }}"
    )
    assert _load(AI_MAAS_DEFAULTS)["ocp_4_20_ai_maas_enable_acm_registration"] is True


def test_ai_maas_flag_is_not_fulfillment_settable() -> None:
    """Hub registration stays a provider-plane decision, not a form parameter."""
    parameters = _load(AI_MAAS_OSAC)["parameters"]
    assert all("acm" not in parameter["name"] for parameter in parameters)
