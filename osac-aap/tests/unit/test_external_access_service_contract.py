"""Verify the generic and CaaS external-access entry-point contracts."""

from pathlib import Path

import yaml


AAP_ROOT = Path(__file__).parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
EXTERNAL_ACCESS = COLLECTIONS / "osac" / "service" / "roles" / "external_access"
OCP_CI_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_ci_small"
OCP_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_small"


def load_tasks(path):
    return yaml.safe_load(path.read_text())


def task_text(path):
    return path.read_text()


def test_publish_and_clear_are_annotation_only_entry_points():
    publish = load_tasks(EXTERNAL_ACCESS / "tasks" / "publish.yaml")
    clear = load_tasks(EXTERNAL_ACCESS / "tasks" / "clear.yaml")

    assert len(publish) == 1
    assert "kubernetes.core.k8s" in str(publish[0])
    assert "api-endpoint" in str(publish[0])
    assert "ingress-endpoint" in str(publish[0])
    assert "include_role" not in str(publish[0])

    assert len(clear) == 1
    assert "kubernetes.core.k8s" in str(clear[0])
    assert "api-endpoint" in str(clear[0])
    assert "ingress-endpoint" in str(clear[0])
    assert "include_role" not in str(clear[0])


def test_generic_create_and_delete_dispatch_to_network_collection():
    create = task_text(EXTERNAL_ACCESS / "tasks" / "create.yaml")
    delete = task_text(EXTERNAL_ACCESS / "tasks" / "delete.yaml")

    assert "{{ network_steps_collection }}.external_access" in create
    assert "tasks_from: create.yaml" in create
    assert "include_tasks: publish.yaml" in create
    assert "{{ network_steps_collection }}.external_access" in delete
    assert "tasks_from: delete.yaml" in delete
    assert "include_tasks: clear.yaml" in delete


def test_shared_main_dispatches_by_external_access_state():
    tasks = load_tasks(EXTERNAL_ACCESS / "tasks" / "main.yaml")
    assert tasks[0]["ansible.builtin.include_tasks"] == "create.yaml"
    assert tasks[0]["when"] == "external_access_state == 'present'"
    assert tasks[1]["ansible.builtin.include_tasks"] == "delete.yaml"
    assert tasks[1]["when"] == "external_access_state == 'absent'"


def test_caas_entry_points_own_dns_lifecycle_without_network_dispatch():
    create_path = EXTERNAL_ACCESS / "tasks" / "caas_create.yaml"
    delete_path = EXTERNAL_ACCESS / "tasks" / "caas_delete.yaml"
    create = load_tasks(create_path)
    delete = load_tasks(delete_path)

    assert any("Create DNS records" in task.get("name", "") for task in create)
    assert any("Resolve authoritative nameserver" in task.get("name", "") for task in create)
    assert any("Wait for DNS records" in task.get("name", "") for task in create)
    assert any("Delete DNS records" in task.get("name", "") for task in delete)
    assert "network_steps_collection" not in create_path.read_text()
    assert "network_steps_collection" not in delete_path.read_text()
    assert "include_tasks: publish.yaml" in create_path.read_text()
    assert "include_tasks: clear.yaml" in delete_path.read_text()
    assert not any("retrieve_kubeconfig" in str(task) for task in create)
    assert not any("wait_for_network_cluster_operator" in str(task) for task in create)


def test_caas_template_uses_service_entry_points():
    install = load_tasks(OCP_CI_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_CI_SMALL / "tasks" / "delete.yaml")
    install_default = install[0]["ansible.builtin.set_fact"]["install_step_external_access_default"]
    delete_default = delete[0]["ansible.builtin.set_fact"]["delete_step_external_access_default"]

    assert install_default == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_create.yaml",
    }
    assert delete_default == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_delete.yaml",
    }


def test_ocp_small_uses_generic_external_access_dispatch():
    install = load_tasks(OCP_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_SMALL / "tasks" / "delete.yaml")
    install_default = install[0]["ansible.builtin.set_fact"]["install_step_external_access_default"]
    delete_default = delete[0]["ansible.builtin.set_fact"]["delete_step_external_access_default"]

    assert install_default == {"name": "osac.service.external_access", "tasks_from": "main.yaml"}
    assert delete_default == {"name": "osac.service.external_access", "tasks_from": "main.yaml"}
