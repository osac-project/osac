"""Verify the CI and legacy-template external-access entry-point contracts."""

from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
EXTERNAL_ACCESS = COLLECTIONS / "osac" / "service" / "roles" / "external_access"
OCP_CI_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_ci_small"
OCP_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_small"
CI_EXTERNAL_ACCESS = COLLECTIONS / "ci" / "steps" / "roles" / "external_access"
NICO_EXTERNAL_ACCESS = COLLECTIONS / "nico" / "steps" / "roles" / "external_access"


def load_tasks(path: Path) -> list[dict[str, object]]:
    return yaml.safe_load(path.read_text())


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
    for template in (OCP_CI_SMALL, OCP_SMALL):
        paths = (template / "tasks" / "install.yaml", template / "tasks" / "delete.yaml")
        for path in paths:
            assert "network_steps_collection" not in path.read_text()
    configuration = AAP_ROOT / "group_vars" / "all" / "configuration.yaml"
    assert "NETWORK_STEPS_COLLECTION" not in configuration.read_text()


def test_external_access_argument_spec_covers_caas_inputs() -> None:
    spec = yaml.safe_load((EXTERNAL_ACCESS / "meta" / "argument_specs.yaml").read_text())
    options = spec["argument_specs"]["main"]["options"]
    assert "network_steps_collection" not in options
    assert "external_access_state" not in options
    assert "cluster_working_namespace" not in options

    for name in (
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

def test_ocp_small_leaves_external_access_to_clusterorder_and_nico_override_is_preserved() -> None:
    install = load_tasks(OCP_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_SMALL / "tasks" / "delete.yaml")
    install_default = install[0]["ansible.builtin.set_fact"][
        "install_step_external_access_default"
    ]
    delete_default = delete[0]["ansible.builtin.set_fact"][
        "delete_step_external_access_default"
    ]

    assert install_default == {
        "name": "osac.templates.ocp_small",
        "tasks_from": "noop.yaml",
    }
    assert delete_default == {
        "name": "osac.templates.ocp_small",
        "tasks_from": "noop.yaml",
    }

    defaults = install[0]["ansible.builtin.set_fact"]
    assert defaults["install_step_wait_for_nodes_default"] == {
        "name": "osac.service.wait_for",
        "tasks_from": "wait_for_nodes.yaml",
    }
    assert defaults["install_step_wait_for_cluster_operators_default"] == {
        "name": "osac.service.wait_for",
        "tasks_from": "wait_for_cluster_operators.yaml",
    }

    install_access = next(
        task for task in install if task.get("name") == "Step - Configure external access"
    )
    assert "external_access_ingress_internal_network" not in install_access["vars"]

    nico_template = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_4_20_small_nico"
    nico_install = load_tasks(nico_template / "tasks" / "install.yaml")
    nico_delete = load_tasks(nico_template / "tasks" / "delete.yaml")
    nico_defaults = yaml.safe_load((nico_template / "defaults" / "main.yaml").read_text())
    assert nico_defaults["nico_ocp_release_version"] == "4.20.0"
    assert nico_install[0]["vars"]["ocp_release_version"] == "{{ nico_ocp_release_version }}"
    assert "ocp_release_image" not in nico_install[0]["vars"]
    assert nico_delete[0]["vars"]["ocp_release_version"] == "{{ nico_ocp_release_version }}"
    assert "ocp_release_image" not in nico_delete[0]["vars"]
    assert nico_install[0]["vars"]["install_step_external_access_override"] == {
        "name": "osac.templates.ocp_4_20_small_nico",
        "tasks_from": "nico_external_access_create.yaml",
    }
    assert nico_delete[0]["vars"]["delete_step_external_access_override"] == {
        "name": "osac.templates.ocp_4_20_small_nico",
        "tasks_from": "nico_external_access_delete.yaml",
    }
    assert "install_step_wait_for_nodes_override" not in nico_install[0]["vars"]
    assert "install_step_wait_for_cluster_operators_override" not in nico_install[0]["vars"]
    nico_create = load_tasks(nico_template / "tasks" / "nico_external_access_create.yaml")
    nico_remove = load_tasks(nico_template / "tasks" / "nico_external_access_delete.yaml")
    assert nico_create[0]["ansible.builtin.include_role"]["name"] == "nico.steps.external_access"
    assert nico_create[1]["ansible.builtin.include_role"]["tasks_from"] == "publish.yaml"
    assert nico_remove[0]["ansible.builtin.include_role"]["name"] == "nico.steps.external_access"
    assert nico_remove[1]["ansible.builtin.include_role"]["tasks_from"] == "clear.yaml"


def test_nico_external_access_does_not_retry_an_exhausted_ip_range() -> None:
    allocate = load_tasks(NICO_EXTERNAL_ACCESS / "tasks" / "allocate_ip.yml")
    rescue = allocate[0]["rescue"]

    detect = next(
        task for task in rescue if task.get("name") == "Detect exhaustion reported by the IP filter"
    )
    stop = next(
        task
        for task in rescue
        if task.get("name") == "Stop retries when the ingress range has no free address"
    )

    assert "No available IPs in " in detect["ansible.builtin.set_fact"]["nico_ip_allocation_exhausted"]
    assert stop["when"] == "nico_ip_allocation_exhausted | default(false) | bool"
    assert "no free" in stop["ansible.builtin.fail"]["msg"]


def test_caas_does_not_publish_a_provider_specific_external_access_task_pair() -> None:
    for task_name in ("main.yaml", "create.yaml", "delete.yaml", "resolve_fabric_manager.yaml"):
        assert not (EXTERNAL_ACCESS / "tasks" / task_name).exists()

    for manager_name in ("netris", "agentless_net"):
        manager_tasks = COLLECTIONS / "osac" / "templates" / "roles" / manager_name / "tasks"
        for task_name in ("create_external_access.yaml", "delete_external_access.yaml"):
            assert not (manager_tasks / task_name).exists()
