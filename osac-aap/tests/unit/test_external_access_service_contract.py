"""Verify the CI and legacy-template external-access entry-point contracts."""

import json
import os
import subprocess
import sys
from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
EXTERNAL_ACCESS = COLLECTIONS / "osac" / "service" / "roles" / "external_access"
OCP_CI_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_ci_small"
OCP_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_small"
CI_EXTERNAL_ACCESS = COLLECTIONS / "ci" / "steps" / "roles" / "external_access"
NICO_EXTERNAL_ACCESS = COLLECTIONS / "nico" / "steps" / "roles" / "external_access"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"


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
    dns_create = load_tasks(EXTERNAL_ACCESS / "tasks" / "caas_dns_publish.yaml")
    dns_delete = load_tasks(EXTERNAL_ACCESS / "tasks" / "caas_dns_delete.yaml")
    create = load_tasks(create_path)
    delete = load_tasks(delete_path)

    assert "ansible.builtin.assert" in create[0]
    assert "ansible.builtin.set_fact" in create[1]
    assert create[2]["ansible.builtin.include_tasks"] == "caas_api_dns_create.yaml"
    assert create[3]["ansible.builtin.include_tasks"] == "caas_ingress_create.yaml"
    assert delete[0]["ansible.builtin.include_tasks"] == "caas_dns_delete.yaml"
    assert any("Create DNS records" in task.get("name", "") for task in dns_create)
    assert any("Resolve authoritative nameserver" in task.get("name", "") for task in dns_create)
    assert any("Wait for DNS records" in task.get("name", "") for task in dns_create)
    assert any("Delete DNS records" in task.get("name", "") for task in dns_delete)
    assert "network_steps_collection" not in create_path.read_text()
    assert "network_steps_collection" not in delete_path.read_text()
    assert create[4]["ansible.builtin.include_tasks"] == "publish.yaml"
    assert delete[1]["ansible.builtin.include_tasks"] == "clear.yaml"
    assert not any("retrieve_kubeconfig" in str(task) for task in create)
    assert not any("wait_for_network_cluster_operator" in str(task) for task in create)

    assert delete[2]["ansible.builtin.include_tasks"] == "caas_vip_release.yaml"
    assert not (EXTERNAL_ACCESS / "tasks" / "caas_dns_create.yaml").exists()


def test_legacy_ci_external_access_reuses_caas_lifecycle() -> None:
    create = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "create.yaml")
    delete = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "delete.yaml")

    assert create[0]["ansible.builtin.include_role"]["name"] == "osac.service.external_access"
    assert create[0]["ansible.builtin.include_role"]["tasks_from"] == "caas_create.yaml"
    assert delete[0]["ansible.builtin.include_role"]["name"] == "osac.service.external_access"
    assert delete[0]["ansible.builtin.include_role"]["tasks_from"] == "caas_delete.yaml"


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
        "external_access_metallb_ingress_range",
        "external_access_vip_registry_namespace",
        "external_access_vip_registry_name",
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


def test_nico_external_access_fails_without_retry_when_range_is_exhausted(
    tmp_path: Path,
) -> None:
    """Run the production allocator with fake Kubernetes modules and a full pool."""
    fake_collection_root = tmp_path / "fake_collections"
    fake_modules = (
        fake_collection_root
        / "ansible_collections/kubernetes/core/plugins/modules"
    )
    fake_modules.mkdir(parents=True)
    state_path = tmp_path / "registry.json"
    initial_state = {
        "registry": {"192.0.2.1": "existing-1", "192.0.2.2": "existing-2"},
        "operations": [],
    }
    state_path.write_text(json.dumps(initial_state), encoding="utf-8")

    (fake_modules / "k8s_info.py").write_text(
        """
from ansible.module_utils.basic import AnsibleModule
import json
import os
from pathlib import Path

module = AnsibleModule(argument_spec={
    "api_version": {"type": "str"},
    "kind": {"type": "str", "required": True},
    "namespace": {"type": "str"},
    "name": {"type": "str"},
    "kubeconfig": {"type": "raw"},
})
state_path = Path(os.environ["NICO_TEST_STATE_FILE"])
state = json.loads(state_path.read_text(encoding="utf-8"))
state["operations"].append({"module": "k8s_info", "name": module.params["name"]})
state_path.write_text(json.dumps(state), encoding="utf-8")
module.exit_json(changed=False, resources=[{"data": state["registry"]}])
""",
        encoding="utf-8",
    )
    (fake_modules / "k8s.py").write_text(
        """
from ansible.module_utils.basic import AnsibleModule
import json
import os
from pathlib import Path

module = AnsibleModule(argument_spec={
    "state": {"type": "str"},
    "definition": {"type": "dict", "required": True},
    "kubeconfig": {"type": "raw"},
})
state_path = Path(os.environ["NICO_TEST_STATE_FILE"])
state = json.loads(state_path.read_text(encoding="utf-8"))
state["operations"].append({"module": "k8s", "name": module.params["definition"]["metadata"]["name"]})
state_path.write_text(json.dumps(state), encoding="utf-8")
module.exit_json(changed=True)
""",
        encoding="utf-8",
    )

    playbook = tmp_path / "allocate_exhausted.yml"
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "name": "Exhausted NICO ingress allocation",
                    "hosts": "localhost",
                    "connection": "local",
                    "gather_facts": False,
                    "vars": {
                        "cluster_name": "new-cluster",
                        "ingress_cidr_range": "192.0.2.0/30",
                        "nico_ip_registry_namespace": "test",
                        "nico_ip_allocation_retries": 1,
                        "nico_ip_allocation_delay": 0,
                    },
                    "tasks": [
                        {
                            "name": "Run the production allocation task",
                            "ansible.builtin.include_tasks": str(
                                NICO_EXTERNAL_ACCESS / "tasks" / "allocate_ip.yml"
                            ),
                        }
                    ],
                }
            ]
        ),
        encoding="utf-8",
    )

    env = os.environ.copy()
    env.update(
        {
            "ANSIBLE_CONFIG": str(AAP_ROOT / "ansible.cfg"),
            "ANSIBLE_COLLECTIONS_PATH": os.pathsep.join(
                [
                    str(fake_collection_root),
                    str(AAP_ROOT / "collections"),
                    str(AAP_ROOT / "vendor"),
                    "/usr/share/ansible/collections",
                ]
            ),
            "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
            "ANSIBLE_LOCALHOST_WARNING": "False",
            "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
            "NICO_TEST_STATE_FILE": str(state_path),
        }
    )
    result = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", str(playbook)],
        cwd=AAP_ROOT,
        env=env,
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )

    output = " ".join(result.stdout.split())
    assert result.returncode != 0, result.stdout + result.stderr
    assert "no free host addresses remain" in output, output

    final_state = json.loads(state_path.read_text(encoding="utf-8"))
    assert final_state["registry"] == initial_state["registry"]
    assert final_state["operations"] == [
        {"module": "k8s_info", "name": "nico-ip-registry"}
    ]


def test_caas_does_not_publish_a_provider_specific_external_access_task_pair() -> None:
    for task_name in ("main.yaml", "create.yaml", "delete.yaml", "resolve_fabric_manager.yaml"):
        assert not (EXTERNAL_ACCESS / "tasks" / task_name).exists()

    for manager_name in ("netris", "agentless_net"):
        manager_tasks = COLLECTIONS / "osac" / "templates" / "roles" / manager_name / "tasks"
        for task_name in ("create_external_access.yaml", "delete_external_access.yaml"):
            assert not (manager_tasks / task_name).exists()
