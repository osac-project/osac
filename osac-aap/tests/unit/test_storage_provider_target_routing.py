"""Run the real dispatcher and LVMS tasks with recording Kubernetes modules."""

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

OSAC_AAP = Path(__file__).resolve().parents[2]
RECORDER = """
import json
import os
from ansible.module_utils.basic import AnsibleModule

module = AnsibleModule(argument_spec={
    "kubeconfig": {"type": "str"},
    "context": {"type": "str"},
    "host": {"type": "str"},
    "validate_certs": {"type": "bool", "default": True},
    "api_version": {"type": "str"},
    "kind": {"type": "str"},
    "label_selectors": {"type": "list", "elements": "str"},
    "state": {"type": "str"},
    "definition": {"type": "dict"},
})
with open(os.environ["OSAC_ROUTING_CAPTURE"], "a") as capture:
    capture.write(json.dumps(module.params) + "\\n")
module.exit_json(changed=False, resources=[])
"""


# Stateful Kubernetes module doubles for the native CSI target-routing test.
NATIVE_CSI_RECORDER = """
import json
import os
from pathlib import Path
from ansible.module_utils.basic import AnsibleModule

module = AnsibleModule(argument_spec={
    "api_version": {"type": "str"},
    "kind": {"type": "str"},
    "name": {"type": "str"},
    "namespace": {"type": "str"},
    "kubeconfig": {"type": "str"},
    "validate_certs": {"type": "bool", "default": True},
    "state": {"type": "str"},
    "definition": {"type": "dict"},
    "apply": {"type": "bool"},
    "server_side_apply": {"type": "dict"},
    "label_selectors": {"type": "list", "elements": "str"},
})
params = module.params
with open(os.environ["OSAC_ROUTING_CAPTURE"], "a") as capture:
    capture.write(json.dumps({"module": Path(__file__).stem, **params}) + "\\n")

state_path = Path(os.environ["OSAC_ROUTING_STATE"])
state = json.loads(state_path.read_text())
definition = params.get("definition") or {}
resource = definition.get("kind") or params.get("kind")
name = (definition.get("metadata") or {}).get("name") or params.get("name")
namespace = (definition.get("metadata") or {}).get("namespace") or params.get("namespace")
key = json.dumps((resource, name, namespace))

if Path(__file__).stem == "k8s":
    if params.get("state") == "present":
        state[key] = definition
        state_path.write_text(json.dumps(state))
        module.exit_json(changed=True, result=definition)
    if params.get("state") == "absent":
        state.pop(key, None)
        state_path.write_text(json.dumps(state))
        module.exit_json(changed=True)
    module.exit_json(changed=False)

if resource == "CSIDriver":
    stored = state.get(json.dumps((resource, name, None)))
    resources = [stored] if stored else []
elif resource == "Deployment":
    resources = [{"metadata": {"name": name}, "status": {"availableReplicas": 1}}]
elif resource == "DaemonSet":
    resources = [{
        "metadata": {"name": name},
        "status": {"desiredNumberScheduled": 1, "numberReady": 1},
    }]
else:
    resources = [{"metadata": {"name": name}}]
module.exit_json(changed=False, resources=resources)
"""


def run_native_csi_dispatch(tmp_path: Path) -> list[dict[str, object]]:
    """Run the real native CSI role with recorded Kubernetes module calls."""
    modules = tmp_path / "ansible_collections/kubernetes/core/plugins/modules"
    modules.mkdir(parents=True)
    for name in ("k8s", "k8s_info"):
        (modules / f"{name}.py").write_text(NATIVE_CSI_RECORDER)

    override = tmp_path / "ansible_collections/osac/test_overrides/roles/csi_driver_install_hooks/tasks"
    override.mkdir(parents=True)
    (override.parents[2] / "galaxy.yml").write_text("namespace: osac\nname: test_overrides\nversion: 0.1.0\n")
    (override / "install.yaml").write_text(
        """---
- name: Register native CSI driver in the target cluster fixture
  kubernetes.core.k8s:
    state: present
    definition:
      apiVersion: storage.k8s.io/v1
      kind: CSIDriver
      metadata:
        name: "{{ item }}"
  loop: "{{ native_csi_driver_names }}"
"""
    )

    playbook = tmp_path / "native-csi-routing.json"
    playbook.write_text(
        json.dumps(
            [
                {
                    "name": "Verify native CSI operations use the selected target",
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": {
                        "ansible_connection": "local",
                        "ansible_python_interpreter": sys.executable,
                        "tenant_name": "routing-test",
                        "tenant_namespace": "osac-tenants",
                    },
                    "tasks": [
                        {
                            "name": "Ensure native CSI driver",
                            "ansible.builtin.include_role": {"name": "osac.service.storage_provider"},
                            "vars": {
                                "storage_provider_action": "ensure_csi_driver",
                                "storage_provider_tiers": [
                                    {
                                        "name": "fast-block",
                                        "protocol": "block",
                                        "provider": "netapp",
                                        "backend_id": "netapp-backend",
                                    }
                                ],
                                "storage_provider_target_kubeconfig": "/fixture/workload.kubeconfig",
                                "storage_provider_target_kubeconfig_insecure": False,
                                "storage_provider_netapp_trident_install_override": {
                                    "name": "osac.test_overrides.csi_driver_install_hooks",
                                    "tasks_from": "install.yaml",
                                },
                            },
                        }
                    ],
                }
            ]
        )
    )

    capture = tmp_path / "native-csi-calls.jsonl"
    state = tmp_path / "native-csi-state.json"
    state.write_text("{}")
    environment = {
        **os.environ,
        "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
        "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path}:{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
        "ANSIBLE_FORCE_COLOR": "false",
        "ANSIBLE_STDOUT_CALLBACK": "default",
        "OSAC_AAP_ENABLE_TEST_INSTALL_OVERRIDES": "true",
        "OSAC_ROUTING_CAPTURE": str(capture),
        "OSAC_ROUTING_STATE": str(state),
        "POD_NAMESPACE": "aap-jobs",
        "POD_NAME": "worker-123",
        "POD_UID": "worker-uid-123",
    }
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(playbook)],
        cwd=OSAC_AAP,
        env=environment,
        capture_output=True,
        text=True,
        timeout=90,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    return [json.loads(line) for line in capture.read_text().splitlines()]


def run_dispatches(tmp_path, targets, remote_env="", provider="lvms", module_options=None):
    modules = tmp_path / "ansible_collections/kubernetes/core/plugins/modules"
    modules.mkdir(parents=True)
    for name in ("k8s", "k8s_info"):
        (modules / f"{name}.py").write_text(RECORDER)
    role = tmp_path / "ansible_collections/osac/templates/roles/routing_fixture_storage/tasks"
    role.mkdir(parents=True)
    (role.parents[1] / "lvms_storage").symlink_to(
        OSAC_AAP / "collections/ansible_collections/osac/templates/roles/lvms_storage",
        target_is_directory=True,
    )
    (role / "ensure_storage_class.yaml").write_text(json.dumps([
        {"name": "Read through dispatcher defaults", "kubernetes.core.k8s_info": {
            "api_version": "storage.k8s.io/v1", "kind": "StorageClass", **(module_options or {}),
        }},
        {"name": "Write through dispatcher defaults", "kubernetes.core.k8s": {
            "state": "present", "definition": {"kind": "StorageClass"}, **(module_options or {}),
        }},
    ]))
    tasks = []
    for target in targets:
        tasks.append(
            {
                "name": "Dispatch tenant StorageClasses",
                "ansible.builtin.include_role": {"name": "osac.service.storage_provider"},
                "vars": {
                    "storage_provider_action": "ensure_storage_class",
                    "storage_provider_tiers": [
                        {
                            "name": "fast",
                            "protocol": "block",
                            "provider": provider,
                            "backend_id": f"{provider}-backend-1",
                        }
                    ],
                    **target,
                },
            }
        )
    playbook = tmp_path / "routing.json"
    playbook.write_text(
        json.dumps(
            [{
                "name": "Exercise sequential provider dispatch",
                "hosts": "localhost",
                "gather_facts": False,
                "vars": {
                    "ansible_connection": "local",
                    "ansible_python_interpreter": sys.executable,
                    "tenant_name": "routing-test",
                    "tenant_namespace": "osac-tenants",
                },
                "tasks": tasks,
            }]
        )
    )
    capture = tmp_path / "calls.jsonl"
    environment = {
        **os.environ,
        "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
        "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path}:{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
        "ANSIBLE_FORCE_COLOR": "false",
        "ANSIBLE_STDOUT_CALLBACK": "default",
        "OSAC_ROUTING_CAPTURE": str(capture),
        "OSAC_REMOTE_CLUSTER_KUBECONFIG": remote_env,
    }
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(playbook)],
        cwd=OSAC_AAP,
        env=environment,
        capture_output=True,
        text=True,
        timeout=90,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    return [json.loads(line) for line in capture.read_text().splitlines()]


@pytest.mark.parametrize("insecure", [False, True])
def test_parent_include_parameters_reach_lvms(tmp_path, insecure):
    calls = run_dispatches(tmp_path, [{
        "storage_provider_target_kubeconfig": "/fixture/explicit",
        "storage_provider_target_kubeconfig_insecure": insecure,
    }], remote_env="/fixture/unrelated-env")
    assert len(calls) == 2
    assert all(call["kubeconfig"] == "/fixture/explicit" for call in calls)
    assert all(call["validate_certs"] is not insecure for call in calls)
    auths = resolve_auth(calls)
    assert all(auth["kubeconfig"] == "/fixture/explicit" for auth in auths)
    assert all(auth["verify_ssl"] is not insecure for auth in auths)


@pytest.mark.parametrize("provider", ["lvms", "routing-fixture"])
def test_sequential_target_and_tls_state(tmp_path, provider):
    calls = run_dispatches(tmp_path, [
        {"storage_provider_target_kubeconfig": "/fixture/a",
         "storage_provider_target_kubeconfig_insecure": True},
        {},
        {"storage_provider_target_kubeconfig": ""},
        {"storage_provider_target_kubeconfig": "{{ remote_cluster_kubeconfig | default(omit) }}"},
        {"storage_provider_target_kubeconfig": None},
        {"storage_provider_target_kubeconfig": "/fixture/b"},
    ], remote_env="/fixture/unrelated-env", provider=provider)
    assert len(calls) == 12
    assert [(call["kubeconfig"], call["validate_certs"]) for call in calls] == [
        ("/fixture/a", False), ("/fixture/a", False),
        (None, True), (None, True),
        (None, True), (None, True),
        (None, True), (None, True),
        (None, True), (None, True),
        ("/fixture/b", True), ("/fixture/b", True),
    ]


@pytest.mark.parametrize("provider", ["lvms", "routing-fixture"])
def test_empty_target_preserves_environment_fallback(tmp_path, provider):
    calls = run_dispatches(tmp_path, [{"storage_provider_target_kubeconfig": ""}], provider=provider)
    result = subprocess.run(
        [sys.executable, "-c", """
import json
import sys
sys.path.insert(0, sys.argv[1])
from ansible_collections.kubernetes.core.plugins.module_utils.k8s.client import _create_auth_spec
print(json.dumps([_create_auth_spec(kubeconfig=call["kubeconfig"])["kubeconfig"]
                  for call in json.loads(sys.argv[2])]))
""", str(OSAC_AAP / "vendor"), json.dumps(calls)],
        env={**os.environ, "K8S_AUTH_KUBECONFIG": "/fixture/environment-default"},
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert json.loads(result.stdout) == ["/fixture/environment-default"] * 2


@pytest.mark.parametrize("options", [
    {"context": "explicit-context"},
    {"host": "https://explicit.invalid:6443"},
])
def test_empty_target_preserves_explicit_auth_options(tmp_path, options):
    calls = run_dispatches(
        tmp_path,
        [{"storage_provider_target_kubeconfig": ""}],
        provider="routing-fixture",
        module_options=options,
    )
    assert len(calls) == 2
    assert all(call[key] == value for call in calls for key, value in options.items())
    assert all(call["kubeconfig"] is None for call in calls)
    assert all(auth[key] == value for auth in resolve_auth(calls)
               for key, value in options.items())


def resolve_auth(calls):
    result = subprocess.run(
        [sys.executable, "-c", """
import json
import sys
sys.path.insert(0, sys.argv[1])
from ansible_collections.kubernetes.core.plugins.module_utils.k8s.client import _create_auth_spec
auths = [_create_auth_spec(**call) for call in json.loads(sys.argv[2])]
print(json.dumps([{key: auth.get(key) for key in ("kubeconfig", "context", "host", "verify_ssl")}
                  for auth in auths]))
""", str(OSAC_AAP / "vendor"), json.dumps(calls)],
        env={**os.environ, "K8S_AUTH_KUBECONFIG": "/fixture/environment-default",
             "K8S_AUTH_CONTEXT": "environment-context",
             "K8S_AUTH_HOST": "https://environment.invalid:6443"},
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    return json.loads(result.stdout)


@pytest.mark.parametrize("provider", ["lvms", "routing-fixture"])
def test_empty_target_preserves_environment_context_and_host(tmp_path, provider):
    calls = run_dispatches(tmp_path, [{"storage_provider_target_kubeconfig": ""}], provider=provider)
    assert len(calls) == 2
    assert resolve_auth(calls) == [{
        "kubeconfig": "/fixture/environment-default",
        "context": "environment-context",
        "host": "https://environment.invalid:6443",
        "verify_ssl": True,
    }] * 2


def test_native_csi_install_uses_workload_kubeconfig_and_keeps_lease_on_aap(tmp_path: Path) -> None:
    """Route driver resources to the workload while keeping the Lease with AAP."""
    calls = run_native_csi_dispatch(tmp_path)
    lease_calls = [call for call in calls if (call.get("definition") or {}).get("kind") == "Lease"]
    workload_calls = [call for call in calls if call not in lease_calls]

    assert workload_calls
    assert all(call["kubeconfig"] == "/fixture/workload.kubeconfig" for call in workload_calls)
    assert any(
        call.get("kind") == "CSIDriver" and call.get("name") == "csi.trident.netapp.io" for call in workload_calls
    )
    assert len(lease_calls) == 2
    assert all(call["definition"]["metadata"]["namespace"] == "aap-jobs" for call in lease_calls)
    assert all(call.get("kubeconfig") is None for call in lease_calls)
    acquire = next(call for call in lease_calls if call.get("state") == "present")
    owner = acquire["definition"]["metadata"]["ownerReferences"][0]
    assert owner["name"] == "worker-123"
    assert owner["uid"] == "worker-uid-123"
