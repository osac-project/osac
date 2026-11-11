"""Run the real ACM task files in Ansible against recording Kubernetes modules.

No API server, MCE controller or guest is real: these are isolated role-contract
checks, not evidence that ACM has joined an actual hosted cluster.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

AAP = Path(__file__).resolve().parents[2]
ROLE = AAP / "collections/ansible_collections/osac/service/roles/hosted_cluster"
TENANT = "osac.openshift.io/tenant"
OWNER = "osac.openshift.io/owner-reference"
ANNOTATIONS = {TENANT: "tenant-a", OWNER: "ClusterOrder/order-a"}

MODULE = """from ansible.module_utils.basic import AnsibleModule
import json
import os
from pathlib import Path

module = AnsibleModule(argument_spec={
    "api_version": {"type": "str"}, "kind": {"type": "str"},
    "name": {"type": "str"}, "namespace": {"type": "str"},
    "label_selectors": {"type": "list", "elements": "str"},
    "state": {"type": "str"}, "definition": {"type": "dict"},
    "merge_type": {"type": "list", "elements": "str"},
})
params = module.params
state = json.loads(Path(os.environ["ACM_FIXTURE"]).read_text())
kind = params["kind"] or (params["definition"] or {}).get("kind")
with Path(os.environ["ACM_CALLS"]).open("a") as output:
    output.write(json.dumps(params) + "\\n")
if state.get("forbidden") == kind:
    module.fail_json(msg="Forbidden fixture response")
if params["definition"] is not None:
    module.exit_json(changed=True, result=params["definition"])
module.exit_json(changed=False, api_found=kind not in state.get("missing_apis", []), resources=state.get(kind, []))
"""


def disabled_agent() -> dict:
    return {
        "metadata": {"name": "hypershift-addon-agent", "namespace": "agent-ns", "generation": 2},
        "spec": {
            "replicas": 1,
            "template": {
                "spec": {
                    "containers": [
                        {"name": "hypershift-addon-agent", "env": [{"name": "DISABLE_AUTO_IMPORT", "value": "true"}]}
                    ]
                }
            },
        },
        "status": {"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1},
    }


def policy() -> dict:
    return {
        "AddOnDeploymentConfig": [{"spec": {"customizedVariables": [{"name": "autoImportDisabled", "value": "true"}]}}],
        "Deployment": [disabled_agent()],
        "ManagedCluster": [],
        "Namespace": [{"metadata": {"name": "order-a"}}],
        "KlusterletAddonConfig": [],
    }


def run_tasks(
    tmp_path: Path, state: dict, *, opt_in: bool = False, tenant: str = "tenant-a"
) -> tuple[subprocess.CompletedProcess, list[dict]]:
    modules = tmp_path / "collections/ansible_collections/kubernetes/core/plugins/modules"
    modules.mkdir(parents=True)
    for name in ("k8s_info", "k8s"):
        (modules / f"{name}.py").write_text(MODULE)
    fixture, calls = tmp_path / "fixture.json", tmp_path / "calls.jsonl"
    fixture.write_text(json.dumps(state))
    variables = {
        "hosted_cluster_name": "order-a",
        "hosted_cluster_namespace": "osac",
        "cluster_order_label": "osac.openshift.io/clusterorder",
        "hosted_cluster_enable_acm_registration": opt_in,
        "cluster_order": {"metadata": {"name": "order-a", "annotations": {TENANT: tenant}}},
    }
    play = [
        {
            "hosts": "localhost",
            "gather_facts": False,
            "vars": variables,
            "tasks": [
                {
                    "name": "Validate import prerequisite",
                    "ansible.builtin.include_role": {
                        "name": "osac.service.hosted_cluster",
                        "tasks_from": "validate_acm_import_policy.yaml",
                    },
                },
                {
                    "name": "Register only opted-in clusters",
                    "ansible.builtin.include_role": {
                        "name": "osac.service.hosted_cluster",
                        "tasks_from": "register_managed_cluster.yaml",
                    },
                    "when": "hosted_cluster_enable_acm_registration | bool",
                },
            ],
        }
    ]
    play_path = tmp_path / "play.yml"
    play_path.write_text(yaml.safe_dump(play))
    result = subprocess.run(
        [str(Path(sys.executable).with_name("ansible-playbook")), "-i", "localhost,", "-c", "local", str(play_path)],
        cwd=AAP,
        env={
            **os.environ,
            "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path / 'collections'}:{AAP / 'collections'}",
            "ACM_FIXTURE": str(fixture),
            "ACM_CALLS": str(calls),
            "ANSIBLE_NOCOLOR": "1",
        },
        capture_output=True,
        text=True,
        timeout=40,
    )
    return result, [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []


def writes(calls: list[dict]) -> list[dict]:
    return [call["definition"] for call in calls if call["definition"] is not None]


def test_create_flow_checks_policy_before_creating_resources() -> None:
    tasks = yaml.safe_load((ROLE / "tasks/create_hosted_cluster.yaml").read_text())
    assert tasks[0].get("ansible.builtin.include_tasks") == "validate_acm_import_policy.yaml"
    registration = next(
        task for task in tasks if task.get("ansible.builtin.include_tasks") == "register_managed_cluster.yaml"
    )
    assert registration["when"] == "hosted_cluster_enable_acm_registration | bool"


def test_regular_cluster_never_writes_shared_policy_or_registration(tmp_path: Path) -> None:
    result, calls = run_tasks(tmp_path, policy())
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes(calls) == []


@pytest.mark.parametrize(
    "bad_policy",
    [[], [{"spec": {}}], [{"spec": {"customizedVariables": [{"name": "autoImportDisabled", "value": "false"}]}}]],
)
def test_missing_or_enabled_policy_fails_before_registration(tmp_path: Path, bad_policy: list) -> None:
    state = policy()
    state["AddOnDeploymentConfig"] = bad_policy
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "autoImportDisabled" in result.stdout
    assert writes(calls) == []


@pytest.mark.parametrize("field", ["enabled", "stale-generation", "old-replicas"])
def test_effective_agent_must_be_disabled_and_fully_rolled_out(tmp_path: Path, field: str) -> None:
    state = policy()
    agent = state["Deployment"][0]
    if field == "enabled":
        agent["spec"]["template"]["spec"]["containers"][0]["env"] = []
    elif field == "stale-generation":
        agent["status"]["observedGeneration"] = 1
    else:
        agent["status"]["replicas"] = 2
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode != 0
    assert "DISABLE_AUTO_IMPORT" in result.stdout
    assert writes(calls) == []


def test_disabled_default_without_installed_agent_is_safe(tmp_path: Path) -> None:
    state = policy()
    state["Deployment"] = []
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes(calls) == []


def test_opt_in_creates_hosted_registration_with_tenant_ownership(tmp_path: Path) -> None:
    result, calls = run_tasks(tmp_path, policy(), opt_in=True)
    assert result.returncode == 0, result.stdout + result.stderr
    resources = {item["kind"]: item for item in writes(calls)}
    assert set(resources) == {"ManagedCluster", "KlusterletAddonConfig"}
    mc = resources["ManagedCluster"]
    assert mc["metadata"]["name"] == "order-a"
    assert mc["metadata"]["annotations"] == {
        **ANNOTATIONS,
        "import.open-cluster-management.io/hosting-cluster-name": "local-cluster",
        "import.open-cluster-management.io/klusterlet-deploy-mode": "Hosted",
        "open-cluster-management/created-via": "hypershift",
    }
    assert mc["spec"] == {"hubAcceptsClient": True, "leaseDurationSeconds": 60}
    kac = resources["KlusterletAddonConfig"]
    assert kac["metadata"]["namespace"] == "order-a"
    assert kac["metadata"]["annotations"] == ANNOTATIONS
    assert kac["spec"]["clusterName"] == "order-a"
    assert kac["spec"]["applicationManager"]["enabled"] is True


def test_mce_only_has_no_acm_addon_configuration(tmp_path: Path) -> None:
    state = policy()
    state["missing_apis"] = ["KlusterletAddonConfig"]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert [item["kind"] for item in writes(calls)] == ["ManagedCluster"]


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_foreign_registration_is_not_modified(tmp_path: Path, kind: str) -> None:
    state = policy()
    state[kind] = [{"metadata": {"annotations": {TENANT: "other", OWNER: "ClusterOrder/other"}}}]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "ownership" in result.stdout.lower()
    assert not any(item["kind"] == kind for item in writes(calls))


def test_registration_requires_tenant(tmp_path: Path) -> None:
    result, calls = run_tasks(tmp_path, policy(), opt_in=True, tenant="")
    assert result.returncode != 0
    assert writes(calls) == []


def test_policy_read_permission_error_is_not_absence(tmp_path: Path) -> None:
    state = policy()
    state["forbidden"] = "AddOnDeploymentConfig"
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode != 0
    assert "Forbidden" in result.stdout
    assert writes(calls) == []
