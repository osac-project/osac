"""Verify the generic and CaaS external-access entry-point contracts."""

from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
EXTERNAL_ACCESS = COLLECTIONS / "osac" / "service" / "roles" / "external_access"
OCP_CI_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_ci_small"
OCP_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_small"
CI_EXTERNAL_ACCESS = COLLECTIONS / "ci" / "steps" / "roles" / "external_access"


def load_tasks(path: Path) -> list[dict[str, object]]:
    return yaml.safe_load(path.read_text())


def task_text(path: Path) -> str:
    return path.read_text()


def test_publish_and_clear_are_annotation_only_entry_points() -> None:
    publish = load_tasks(EXTERNAL_ACCESS / "tasks" / "publish.yaml")
    clear = load_tasks(EXTERNAL_ACCESS / "tasks" / "clear.yaml")

    assert len(publish) == 1
    publish_definition = publish[0]["kubernetes.core.k8s"]["definition"]
    publish_annotations = publish_definition["metadata"]["annotations"]
    assert publish_definition["kind"] == "ClusterOrder"
    assert publish_annotations["osac.openshift.io/api-endpoint"]
    assert publish_annotations["osac.openshift.io/ingress-endpoint"]
    assert "ansible.builtin.include_role" not in publish[0]
    assert "include_role" not in publish[0]

    assert len(clear) == 1
    clear_definition = clear[0]["kubernetes.core.k8s"]["definition"]
    clear_annotations = clear_definition["metadata"]["annotations"]
    assert clear_definition["kind"] == "ClusterOrder"
    assert clear_annotations["osac.openshift.io/api-endpoint"] is None
    assert clear_annotations["osac.openshift.io/ingress-endpoint"] is None
    assert "ansible.builtin.include_role" not in clear[0]
    assert "include_role" not in clear[0]


def test_generic_create_and_delete_dispatch_to_network_collection() -> None:
    create = task_text(EXTERNAL_ACCESS / "tasks" / "create.yaml")
    delete = task_text(EXTERNAL_ACCESS / "tasks" / "delete.yaml")

    assert "{{ network_steps_collection }}.external_access" in create
    assert "tasks_from: create.yaml" in create
    assert "include_tasks: publish.yaml" in create
    assert "{{ network_steps_collection }}.external_access" in delete
    assert "tasks_from: delete.yaml" in delete
    assert "include_tasks: clear.yaml" in delete


def test_shared_main_dispatches_by_external_access_state() -> None:
    tasks = load_tasks(EXTERNAL_ACCESS / "tasks" / "main.yaml")
    assert tasks[0]["ansible.builtin.include_tasks"] == "create.yaml"
    assert tasks[0]["when"] == "external_access_state == 'present'"
    assert tasks[1]["ansible.builtin.include_tasks"] == "delete.yaml"
    assert tasks[1]["when"] == "external_access_state == 'absent'"


def test_caas_entry_points_own_dns_lifecycle_without_network_dispatch() -> None:
    create_path = EXTERNAL_ACCESS / "tasks" / "caas_create.yaml"
    delete_path = EXTERNAL_ACCESS / "tasks" / "caas_delete.yaml"
    dns_create = load_tasks(EXTERNAL_ACCESS / "tasks" / "caas_dns_create.yaml")
    dns_delete = load_tasks(EXTERNAL_ACCESS / "tasks" / "caas_dns_delete.yaml")
    create = load_tasks(create_path)
    delete = load_tasks(delete_path)

    assert create[0]["ansible.builtin.include_tasks"] == "caas_dns_create.yaml"
    assert delete[0]["ansible.builtin.include_tasks"] == "caas_dns_delete.yaml"
    assert any("Create DNS records" in task.get("name", "") for task in dns_create)
    assert any("Resolve authoritative nameserver" in task.get("name", "") for task in dns_create)
    assert any("Wait for DNS records" in task.get("name", "") for task in dns_create)
    assert any("Delete DNS records" in task.get("name", "") for task in dns_delete)
    assert "network_steps_collection" not in create_path.read_text()
    assert "network_steps_collection" not in delete_path.read_text()
    assert create[1]["ansible.builtin.include_tasks"] == "publish.yaml"
    assert delete[1]["ansible.builtin.include_tasks"] == "clear.yaml"
    assert not any("retrieve_kubeconfig" in str(task) for task in create)
    assert not any("wait_for_network_cluster_operator" in str(task) for task in create)

    agent_lookup = next(task for task in dns_create if task.get("name") == "Get agents assigned to this cluster")
    assert agent_lookup["kubernetes.core.k8s_info"]["label_selectors"] == [
        "{{ cluster_order_label }}={{ external_access_name }}"
    ]


def test_legacy_ci_external_access_reuses_caas_dns_tasks() -> None:
    create = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "create.yaml")
    delete = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "delete.yaml")

    assert create[0]["ansible.builtin.include_role"]["name"] == "osac.service.external_access"
    assert create[0]["ansible.builtin.include_role"]["tasks_from"] == "caas_dns_create.yaml"
    assert delete[0]["ansible.builtin.include_role"]["name"] == "osac.service.external_access"
    assert delete[0]["ansible.builtin.include_role"]["tasks_from"] == "caas_dns_delete.yaml"


def test_caas_template_uses_service_entry_points() -> None:
    install = load_tasks(OCP_CI_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_CI_SMALL / "tasks" / "delete.yaml")
    install_default = install[0]["ansible.builtin.set_fact"]["install_step_external_access_default"]
    delete_default = delete[0]["ansible.builtin.set_fact"]["delete_step_external_access_default"]

    assert install_default == {"name": "osac.service.external_access", "tasks_from": "caas_create.yaml"}
    assert delete_default == {"name": "osac.service.external_access", "tasks_from": "caas_delete.yaml"}

    install_defaults = install[0]["ansible.builtin.set_fact"]
    assert install_defaults["skip_cluster_operators_wait"] is True
    hosted_cluster = next(task for task in install if task.get("name") == "Step - Create hosted cluster")
    assert hosted_cluster["vars"]["hosted_cluster_settings"]["ovn_kubernetes_mtu"] == 1400
    external_access = next(task for task in install if task.get("name") == "Step - Configure external access")
    assert "external_access_ingress_internal_network" not in external_access["vars"]
    assert "install_step_cluster_infra_default" not in install_defaults
    assert "install_step_wait_for_agents_default" not in install_defaults
    assert "delete_step_cluster_infra_default" not in delete[0]["ansible.builtin.set_fact"]
    wait_for_operators = next(task for task in install if task.get("name") == "Step - Wait for cluster operators")
    assert wait_for_operators["when"] == "not skip_cluster_operators_wait"


def test_caas_templates_do_not_use_provider_step_collection_dispatch() -> None:
    for path in (OCP_CI_SMALL / "tasks" / "install.yaml", OCP_CI_SMALL / "tasks" / "delete.yaml"):
        assert "network_steps_collection" not in path.read_text()


def test_external_access_argument_spec_covers_generic_and_caas_inputs() -> None:
    spec = yaml.safe_load((EXTERNAL_ACCESS / "meta" / "argument_specs.yaml").read_text())
    options = spec["argument_specs"]["main"]["options"]

    for name in (
        "network_steps_collection",
        "external_access_agent_namespace",
        "external_access_resource_wait_retries",
        "external_access_resource_wait_delay",
        "external_access_dns_ttl",
        "external_access_supported_base_domains",
        "cluster_order_label",
        "dns_class",
        "dns_server",
        "external_access_dns_wait_use_authoritative",
    ):
        assert options[name]["required"] is False


def test_ocp_small_uses_generic_external_access_dispatch() -> None:
    install = load_tasks(OCP_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_SMALL / "tasks" / "delete.yaml")
    install_default = install[0]["ansible.builtin.set_fact"]["install_step_external_access_default"]
    delete_default = delete[0]["ansible.builtin.set_fact"]["delete_step_external_access_default"]

    assert install_default == {"name": "osac.service.external_access", "tasks_from": "main.yaml"}
    assert delete_default == {"name": "osac.service.external_access", "tasks_from": "main.yaml"}
