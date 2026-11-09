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
    output.write(json.dumps({"module": MODULE_NAME, **params}) + "\\n")
if state.get("forbidden") == kind:
    module.fail_json(msg="Forbidden fixture response")
if params["definition"] is not None:
    module.exit_json(changed=True, result=params["definition"])
resources = state.get(kind, [])
if params["name"]:
    resources = [item for item in resources if item.get("metadata", {}).get("name") == params["name"]]
if params["namespace"]:
    resources = [item for item in resources if item.get("metadata", {}).get("namespace") == params["namespace"]]
module.exit_json(changed=False, api_found=kind not in state.get("missing_apis", []), resources=resources)
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


def policy_resource(spec: dict) -> dict:
    """An AddOnDeploymentConfig fixture entry the double can find by name and namespace."""
    return {"metadata": {"name": "hypershift-addon-deploy-config", "namespace": "multicluster-engine"}, "spec": spec}


def policy() -> dict:
    return {
        "AddOnDeploymentConfig": [
            policy_resource({"customizedVariables": [{"name": "autoImportDisabled", "value": "true"}]})
        ],
        "Deployment": [disabled_agent()],
        "ManagedCluster": [],
        "Namespace": [{"metadata": {"name": "order-a"}}],
        "KlusterletAddonConfig": [],
    }


def run_tasks(
    tmp_path: Path,
    state: dict,
    *,
    opt_in: bool = False,
    tenant: str = "tenant-a",
    extra_vars: dict | None = None,
    verbose: bool = False,
) -> tuple[subprocess.CompletedProcess, list[dict]]:
    """Run the real ACM task files in a play against recording Kubernetes modules."""
    modules = tmp_path / "collections/ansible_collections/kubernetes/core/plugins/modules"
    modules.mkdir(parents=True)
    for name in ("k8s_info", "k8s"):
        (modules / f"{name}.py").write_text("MODULE_NAME = " + repr(name) + "\n" + MODULE)
    fixture, calls = tmp_path / "fixture.json", tmp_path / "calls.jsonl"
    fixture.write_text(json.dumps(state))
    variables = {
        "hosted_cluster_name": "order-a",
        "hosted_cluster_namespace": "osac",
        "cluster_order_label": "osac.openshift.io/clusterorder",
        "hosted_cluster_enable_acm_registration": opt_in,
        "cluster_order": {"metadata": {"name": "order-a", "annotations": {TENANT: tenant}}},
    } | (extra_vars or {})
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
    command = [str(Path(sys.executable).with_name("ansible-playbook")), "-i", "localhost,", "-c", "local"]
    if verbose:
        command.append("-vvv")
    command.append(str(play_path))
    result = subprocess.run(
        command,
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
    """Return the definitions the play wrote to the API, in call order."""
    return [call["definition"] for call in calls if call["module"] == "k8s" and call["definition"] is not None]


def mutations(calls: list[dict]) -> list[dict]:
    """Return every kubernetes.core.k8s mutation, including ones without a definition."""
    return [call for call in calls if call["module"] == "k8s"]


def test_create_flow_checks_policy_before_creating_resources() -> None:
    """The import-policy gate is the first step of cluster creation."""
    tasks = yaml.safe_load((ROLE / "tasks/create_hosted_cluster.yaml").read_text())
    assert tasks[0].get("ansible.builtin.include_tasks") == "validate_acm_import_policy.yaml"
    registration = next(
        task for task in tasks if task.get("ansible.builtin.include_tasks") == "register_managed_cluster.yaml"
    )
    assert registration["when"] == "hosted_cluster_enable_acm_registration | bool"


def test_regular_cluster_never_writes_shared_policy_or_registration(tmp_path: Path) -> None:
    """A non-opted-in cluster writes neither hub policy nor registration."""
    result, calls = run_tasks(tmp_path, policy())
    assert result.returncode == 0, result.stdout + result.stderr
    assert not mutations(calls)


@pytest.mark.parametrize(
    "bad_policy",
    [
        [],
        [policy_resource({})],
        [policy_resource({"customizedVariables": [{"name": "autoImportDisabled", "value": "false"}]})],
    ],
)
def test_missing_or_enabled_policy_fails_before_registration(tmp_path: Path, bad_policy: list) -> None:
    """An absent, empty, or enabled policy stops the run before registration."""
    state = policy()
    state["AddOnDeploymentConfig"] = bad_policy
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "autoImportDisabled" in result.stdout
    assert not mutations(calls)


@pytest.mark.parametrize("field", ["enabled", "stale-generation", "old-replicas"])
def test_effective_agent_must_be_disabled_and_fully_rolled_out(tmp_path: Path, field: str) -> None:
    """Old replicas or an unapplied rollout are not proof of a disabled importer."""
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
    assert not mutations(calls)


def test_disabled_default_without_installed_agent_is_safe(tmp_path: Path) -> None:
    """No installed agent means the upstream default is already disabled."""
    state = policy()
    state["Deployment"] = []
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes(calls) == []


def test_opt_in_creates_hosted_registration_with_tenant_ownership(tmp_path: Path) -> None:
    """An opted-in cluster gets a tenant-owned ManagedCluster and addon config."""
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
    """Without the ACM addon API a registration stops after the ManagedCluster."""
    state = policy()
    state["missing_apis"] = ["KlusterletAddonConfig"]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert [item["kind"] for item in writes(calls)] == ["ManagedCluster"]


def test_agent_rollout_check_logs_no_agent_environment(tmp_path: Path) -> None:
    """A failing rollout check must not log agent container environment values."""
    state = policy()
    agent = disabled_agent()
    agent["status"]["availableReplicas"] = 0
    agent["spec"]["template"]["spec"]["containers"][0]["env"].append(
        {"name": "HYPERSHIFT_AGENT_KUBECONFIG", "value": "agent-secret-value"}
    )
    state["Deployment"] = [agent]
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode != 0
    assert "rolled out" in result.stdout
    assert "agent-secret-value" not in result.stdout + result.stderr
    assert not mutations(calls)


def test_rollout_state_from_an_earlier_invocation_is_discarded(tmp_path: Path) -> None:
    """A rollout recorded by an earlier invocation cannot validate an empty agent list."""
    state = policy()
    state["Deployment"] = []
    stale_rollout = [{"namespace": "agent-ns", "name": "hypershift-addon-agent", "auto_import_disabled": False}]
    result, calls = run_tasks(tmp_path, state, extra_vars={"hosted_cluster_acm_agent_rollout": stale_rollout})
    assert result.returncode == 0, result.stdout + result.stderr
    assert not mutations(calls)


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_foreign_registration_is_not_modified(tmp_path: Path, kind: str) -> None:
    """A registration owned by another ClusterOrder is rejected, never adopted."""
    state = policy()
    foreign = {"name": "order-a", "namespace": "order-a", "annotations": {TENANT: "other", OWNER: "ClusterOrder/other"}}
    state[kind] = [{"metadata": foreign}]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "ownership" in result.stdout.lower()
    assert not mutations(calls)


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_ownership_check_logs_no_foreign_resource_content(tmp_path: Path, kind: str) -> None:
    """A rejected registration can carry credentials; only ownership data may be logged."""
    state = policy()
    state[kind] = [
        {
            "metadata": {
                "name": "order-a",
                "namespace": "order-a",
                "annotations": {TENANT: "other", OWNER: "ClusterOrder/other"},
            },
            "spec": {"pullSecret": {"name": "foreign-pull-secret-name"}},
            "status": {"conditions": [{"message": "foreign-kubeconfig-token"}]},
        }
    ]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "ownership" in result.stdout.lower()
    output = result.stdout + result.stderr
    assert "foreign-pull-secret-name" not in output
    assert "foreign-kubeconfig-token" not in output
    assert not mutations(calls)


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_ownership_check_verbose_output_logs_no_foreign_annotations(tmp_path: Path, kind: str) -> None:
    """Verbose output may not dump foreign metadata; only ownership fields are reportable."""
    state = policy()
    state[kind] = [
        {
            "metadata": {
                "name": "order-a",
                "namespace": "order-a",
                "annotations": {
                    TENANT: "other",
                    OWNER: "ClusterOrder/other",
                    "kubectl.kubernetes.io/last-applied-configuration": '{"token": "foreign-annotation-secret"}',
                },
            }
        }
    ]
    result, calls = run_tasks(tmp_path, state, opt_in=True, verbose=True)
    assert result.returncode != 0
    assert "ownership" in result.stdout.lower()
    assert "foreign-annotation-secret" not in result.stdout + result.stderr
    assert not mutations(calls)


def test_registration_requires_tenant(tmp_path: Path) -> None:
    """Without a ClusterOrder tenant annotation nothing may be registered."""
    result, calls = run_tasks(tmp_path, policy(), opt_in=True, tenant="")
    assert result.returncode != 0
    assert not mutations(calls)


def test_policy_read_permission_error_is_not_absence(tmp_path: Path) -> None:
    """An RBAC failure must not read as a disabled policy."""
    state = policy()
    state["forbidden"] = "AddOnDeploymentConfig"
    result, calls = run_tasks(tmp_path, state)
    assert result.returncode != 0
    assert "Forbidden" in result.stdout
    assert not mutations(calls)
