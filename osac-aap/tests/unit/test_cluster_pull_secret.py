"""Exercise reference-only CaaS settings without any Kubernetes Secret access."""

import base64
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

OSAC_AAP = Path(__file__).resolve().parents[2]
SERVICE = OSAC_AAP / "collections/ansible_collections/osac/service"
TEMPLATES = OSAC_AAP / "collections/ansible_collections/osac/templates"
FIXTURE_MARKER = "OSAC7034_SYNTHETIC_REJECTED_INPUT"
ACCEPTED_FIXTURE_MARKER = "OSAC7034_SYNTHETIC_ACCEPTED_PARAMETER"


@pytest.mark.parametrize("failure", [None, "missing-reference", "inline", "parameters"])
@pytest.mark.parametrize("verbosity", [0, 2])
def test_reference_only_settings_and_extraction(tmp_path: Path, failure: str | None, verbosity: int) -> None:
    """Run the real settings/extraction consumers; no Kubernetes module is provided."""
    order = {
        "metadata": {"name": "fixture", "namespace": "hub", "uid": "fixture-uid"},
        "spec": {
            "templateID": "ocp_small",
            "nodeRequests": [],
            "pullSecretRef": {"name": "owned-source"},
            "sshPublicKey": "fixture-public-key",
            "templateParameters": json.dumps(
                {"unrelated": "keep", "synthetic_sensitive_parameter": ACCEPTED_FIXTURE_MARKER}
            ),
        },
    }
    if failure == "missing-reference":
        del order["spec"]["pullSecretRef"]
    elif failure == "inline":
        order["spec"]["pullSecret"] = FIXTURE_MARKER
    elif failure == "parameters":
        order["spec"]["templateParameters"] = json.dumps({"pull_secret": FIXTURE_MARKER})

    tasks = [
        {"name": "Apply settings", "ansible.builtin.include_role": {"name": "osac.service.cluster_settings"}},
        {"name": "Extract inputs", "ansible.builtin.include_role": {"name": "osac.service.extract_template_info"}},
        {
            "name": "Assert reference-only runtime state",
            "ansible.builtin.assert": {
                "that": [
                    "cluster_settings_pull_secret_name == 'pullsecret-cluster-fixture'",
                    "template_parameters.unrelated == 'keep'",
                    "template_parameters.synthetic_sensitive_parameter == "
                    "(cluster_order.spec.templateParameters | from_json).synthetic_sensitive_parameter",
                    "template_parameters.ssh_public_key == 'fixture-public-key'",
                    "'pull_secret' not in template_parameters",
                    "cluster_settings_pull_secret is not defined",
                    "default_cluster_credentials is not defined",
                    "cluster_settings_referenced_secret is not defined",
                    "cluster_order == original_order",
                ]
            },
        },
    ]
    play = [
        {
            "name": "Reference-only runtime",
            "hosts": "localhost",
            "gather_facts": False,
            "vars": {
                "cluster_order": order,
                "original_order": order,
                "ansible_connection": "local",
                "ansible_python_interpreter": sys.executable,
            },
            "tasks": tasks,
        }
    ]
    playbook = tmp_path / "runtime.json"
    playbook.write_text(json.dumps(play))
    environment = os.environ.copy()
    environment.update(
        {
            "ANSIBLE_COLLECTIONS_PATH": f"{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
            "ANSIBLE_FORCE_COLOR": "false",
            "ANSIBLE_STDOUT_CALLBACK": "default",
        }
    )
    result = subprocess.run(
        ["ansible-playbook", *(["-vv"] if verbosity else []), "-i", "localhost,", str(playbook)],
        cwd=OSAC_AAP,
        env=environment,
        capture_output=True,
        text=True,
        timeout=45,
        check=False,
    )
    output = result.stdout + result.stderr
    assert (result.returncode == 0) == (failure is None), output
    assert FIXTURE_MARKER not in output
    assert base64.b64encode(FIXTURE_MARKER.encode()).decode() not in output
    assert ACCEPTED_FIXTURE_MARKER not in output
    assert base64.b64encode(ACCEPTED_FIXTURE_MARKER.encode()).decode() not in output


def test_hosted_cluster_uses_controller_projection_only() -> None:
    """The actual provider definition references a name; Ansible never copies credentials."""
    create = yaml.safe_load((SERVICE / "roles/hosted_cluster/tasks/create_hosted_cluster.yaml").read_text())
    definition = next(task for task in create if task["name"] == "Build HostedCluster definition")
    hosted = definition["ansible.builtin.set_fact"]["hosted_cluster_definition"]
    assert hosted["spec"]["pullSecret"]["name"] == "{{ hosted_cluster_settings.pull_secret_name }}"
    for name in ["create_hosted_cluster.yaml", "delete_hosted_cluster.yaml"]:
        text = (SERVICE / "roles/hosted_cluster/tasks" / name).read_text()
        assert ".dockerconfigjson" not in text
        assert "pullscret-cluster-" not in text
    settings = (SERVICE / "roles/cluster_settings/tasks/main.yml").read_text()
    assert "kubernetes.core." not in settings
    assert "b64decode" not in settings


@pytest.mark.parametrize("role", ["ocp_small", "ocp_ci_small"])
@pytest.mark.parametrize("phase", ["install", "delete"])
def test_template_adapters_pass_names_only(role: str, phase: str) -> None:
    """Cover both direct adapters; NICo and AI MaaS delegate to ocp_small."""
    tasks = yaml.safe_load((TEMPLATES / "roles" / role / "tasks" / f"{phase}.yaml").read_text())
    settings = next(
        task["vars"]["hosted_cluster_settings"] for task in tasks if "hosted_cluster_settings" in task.get("vars", {})
    )
    assert "pull_secret_name" in settings
    assert "pull_secret" not in settings
