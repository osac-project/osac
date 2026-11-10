"""Run the real dispatcher and LVMS tasks with recording Kubernetes modules."""

import json
import os
from pathlib import Path
import subprocess
import sys

import pytest


OSAC_AAP = Path(__file__).resolve().parents[2]
RECORDER = '''
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
'''


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
                        {"name": "fast", "protocol": "block", "provider": provider}
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
