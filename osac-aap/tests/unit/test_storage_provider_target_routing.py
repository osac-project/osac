"""Run the real dispatcher and LVMS tasks with recording Kubernetes modules."""

import json
import os
import subprocess
import sys

import pytest

from .storage_runner import FIXTURES, OSAC_AAP, run_tasks


def run_dispatches(tmp_path, targets, remote_env="", provider="lvms", module_options=None):
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
    for index, target in enumerate(targets):
        tasks.append(
            {
                "name": "Dispatch tenant StorageClasses",
                "ansible.builtin.include_role": {"name": "osac.service.storage_provider"},
                "vars": {
                    "storage_provider_action": "ensure_storage_class",
                    "storage_provider_tiers": [
                        {"name": f"fast-{index}", "protocol": "block", "provider": provider, "backend_id": f"{provider}-backend-1"}
                    ],
                    **target,
                },
            }
        )
    result, calls = run_tasks(
        tmp_path, tasks, {"tenant_name": "routing-test"},
        environment={"OSAC_REMOTE_CLUSTER_KUBECONFIG": remote_env},
    )
    assert result.returncode == 0, result.stdout + result.stderr
    return calls


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
    assert [auth["kubeconfig"] for auth in resolve_auth(calls)] == ["/fixture/environment-default"] * 2


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
        [sys.executable, str(FIXTURES / "resolve_auth.py"), str(OSAC_AAP / "vendor"), json.dumps(calls)],
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
