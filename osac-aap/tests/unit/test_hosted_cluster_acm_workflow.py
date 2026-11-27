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
    "delete_options": {"type": "dict"},
})
params = module.params
state = json.loads(Path(os.environ["ACM_FIXTURE"]).read_text())
kind = params["kind"] or (params["definition"] or {}).get("kind")
with Path(os.environ["ACM_CALLS"]).open("a") as output:
    output.write(json.dumps({"module": MODULE_NAME, **params}) + "\\n")
if params["state"] == "absent":
    module.exit_json(changed=True, result={})
if params["definition"] is not None:
    module.exit_json(changed=True, result=params["definition"])
resources = state.get(kind, [])
if params["name"]:
    resources = [item for item in resources if item.get("metadata", {}).get("name") == params["name"]]
if params["namespace"]:
    resources = [item for item in resources if item.get("metadata", {}).get("namespace") == params["namespace"]]
module.exit_json(changed=False, api_found=kind not in state.get("missing_apis", []), resources=resources)
"""


def registration_fixture() -> dict:
    return {"Namespace": [{"metadata": {"name": "order-a"}}]}


def run_tasks(
    tmp_path: Path,
    state: dict,
    *,
    opt_in: bool = False,
    create_cluster: bool = False,
    delete_cluster: bool = False,
    tenant: str = "tenant-a",
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
        "cluster_order": {"metadata": {"name": "order-a", "namespace": "osac", "annotations": {TENANT: tenant}}},
    }
    play = [
        {
            "hosts": "localhost",
            "gather_facts": False,
            "vars": variables,
            "tasks": [
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
    if create_cluster:
        play[0]["tasks"] = [
            {
                "name": "Create HostedCluster through the production role",
                "ansible.builtin.include_role": {
                    "name": "osac.service.hosted_cluster",
                    "tasks_from": "create_hosted_cluster.yaml",
                },
                "vars": {
                    "ocp_release_image": "quay.io/example/control-plane:4.20",
                    "hosted_cluster_settings": {"pull_secret": "{}", "ssh_public_key": "fixture-key"},
                },
            }
        ]
    if delete_cluster:
        play[0]["tasks"] = [
            {
                "name": "Delete the HostedCluster through the production role",
                "ansible.builtin.include_role": {
                    "name": "osac.service.hosted_cluster",
                    "tasks_from": "delete_hosted_cluster.yaml",
                },
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


def registration_deletes(calls: list[dict]) -> list[dict]:
    """Return the ManagedCluster deletions, in call order."""
    deletes = [
        call
        for call in mutations(calls)
        if call["state"] == "absent" and (call["kind"] or (call["definition"] or {}).get("kind")) == "ManagedCluster"
    ]
    return [call for call in deletes if call["api_version"] == "cluster.open-cluster-management.io/v1"]


def test_regular_create_requires_no_mce_or_acm_api(tmp_path: Path) -> None:
    """The regular create path can run in Kind with no MCE addon or registration APIs."""
    absent_apis = {"AddOnDeploymentConfig", "ManagedCluster", "KlusterletAddonConfig"}
    result, calls = run_tasks(tmp_path, {"missing_apis": sorted(absent_apis)}, create_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "HostedCluster" in {item["kind"] for item in writes(calls)}
    called_kinds = {call["kind"] or (call["definition"] or {}).get("kind") for call in calls}
    assert called_kinds.isdisjoint(absent_apis | {"Deployment"})


def test_opt_in_creates_hosted_registration_with_tenant_ownership(tmp_path: Path) -> None:
    """An opted-in cluster gets a tenant-owned ManagedCluster and addon config."""
    result, calls = run_tasks(tmp_path, registration_fixture(), opt_in=True, create_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    resources = {item["kind"]: item for item in writes(calls)}
    assert {"HostedCluster", "ManagedCluster", "KlusterletAddonConfig"} <= resources.keys()
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
    state = registration_fixture()
    state["missing_apis"] = ["KlusterletAddonConfig"]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert [item["kind"] for item in writes(calls)] == ["ManagedCluster"]


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_foreign_registration_is_not_modified(tmp_path: Path, kind: str) -> None:
    """A registration owned by another ClusterOrder is rejected, never adopted."""
    state = registration_fixture()
    foreign = {"name": "order-a", "namespace": "order-a", "annotations": {TENANT: "other", OWNER: "ClusterOrder/other"}}
    state[kind] = [{"metadata": foreign}]
    result, calls = run_tasks(tmp_path, state, opt_in=True)
    assert result.returncode != 0
    assert "ownership" in result.stdout.lower()
    assert not mutations(calls)


@pytest.mark.parametrize("kind", ["ManagedCluster", "KlusterletAddonConfig"])
def test_ownership_check_logs_no_foreign_resource_content(tmp_path: Path, kind: str) -> None:
    """A rejected registration can carry credentials; only ownership data may be logged."""
    state = registration_fixture()
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
    state = registration_fixture()
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
    result, calls = run_tasks(tmp_path, registration_fixture(), opt_in=True, tenant="")
    assert result.returncode != 0
    assert not mutations(calls)


# The hub's own HyperShift auto-import writes these markers without OSAC
# ownership (hypershift-addon-operator); pre-flag clusters carry only them.
HOSTED_IMPORT = {
    "import.open-cluster-management.io/hosting-cluster-name": "local-cluster",
    "import.open-cluster-management.io/klusterlet-deploy-mode": "Hosted",
    "open-cluster-management/created-via": "hypershift",
}


def hub_registration(*, annotations: dict | None = None, **metadata: object) -> dict:
    """Return a ManagedCluster fixture; annotations default to this order's ownership."""
    fields = {"name": "order-a", "uid": "uid-1", "annotations": ANNOTATIONS}
    fields.update(metadata)
    if annotations is not None:
        fields["annotations"] = annotations
    return {"metadata": fields}


def test_teardown_deletes_owned_registration_with_uid_precondition(tmp_path: Path) -> None:
    """Cleanup deletes the registration it owns, bound to the inspected object."""
    result, calls = run_tasks(tmp_path, {"ManagedCluster": [hub_registration()]}, delete_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    (deletion,) = registration_deletes(calls)
    assert deletion["name"] == "order-a"
    assert deletion["delete_options"] == {"preconditions": {"uid": "uid-1"}}


def test_teardown_deletes_pre_flag_auto_imported_registration(tmp_path: Path) -> None:
    """A registration created before the flag existed is still cleaned up."""
    state = {"ManagedCluster": [hub_registration(annotations=HOSTED_IMPORT)]}
    result, calls = run_tasks(tmp_path, state, delete_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    (deletion,) = registration_deletes(calls)
    assert deletion["delete_options"] == {"preconditions": {"uid": "uid-1"}}


@pytest.mark.parametrize(
    "annotations",
    [
        {},
        {"kubectl.kubernetes.io/last-applied-configuration": '{"token": "foreign-annotation-secret"}'},
        {TENANT: "other", OWNER: "ClusterOrder/other", **HOSTED_IMPORT},
    ],
    ids=["unattributed", "no-origin-evidence", "foreign-owner"],
)
def test_teardown_leaves_a_registration_it_does_not_own(tmp_path: Path, annotations: dict) -> None:
    """A foreign or unattributed registration is reported, never deleted."""
    state = {"ManagedCluster": [hub_registration(annotations=annotations)]}
    result, calls = run_tasks(tmp_path, state, delete_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not registration_deletes(calls)
    assert "Leaving ManagedCluster order-a in place" in result.stdout


def test_teardown_runs_without_the_acm_api(tmp_path: Path) -> None:
    """A hub without the ManagedCluster API still completes cluster deletion."""
    result, calls = run_tasks(tmp_path, {"missing_apis": ["ManagedCluster"]}, delete_cluster=True)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not registration_deletes(calls)


def test_teardown_logs_no_registration_content(tmp_path: Path) -> None:
    """Cleanup may report the name it leaves or deletes, never registration content."""
    annotations = {**ANNOTATIONS, "kubectl.kubernetes.io/last-applied-configuration": '{"token": "teardown-secret"}'}
    state = {"ManagedCluster": [hub_registration(annotations=annotations, labels={"admin": "teardown-label"})]}
    result, calls = run_tasks(tmp_path, state, delete_cluster=True, verbose=True)
    assert result.returncode == 0, result.stdout + result.stderr
    output = result.stdout + result.stderr
    assert "teardown-secret" not in output
    assert "teardown-label" not in output
    assert len(registration_deletes(calls)) == 1
