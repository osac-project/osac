"""Behavioral tests for Netris task-input normalization."""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
RULE = ROOT / "collections/ansible_collections/osac/templates/roles/netris"
HELPER = RULE / "tasks/_contract_input.yaml"

FACTS = (
    "{{ {'contract': _osac_contract | bool,"
    " 'task': _osac_task,"
    " 'tenant': _osac_tenant,"
    " 'resource': _osac_resource,"
    " 'ipam': _osac_ipam,"
    " 'network_type': _osac_network_type,"
    " 'network_segment': _osac_network_segment,"
    " 'virtual_network': _osac_virtual_network,"
    " 'subnet_alias': subnet | default(None),"
    " 'security_group_ids': _osac_security_group_ids} | to_json }}"
)

SUBNET = {
    "apiVersion": "osac.openshift.io/v1alpha1",
    "kind": "Subnet",
    "id": "subnet-api-id",
    "metadata": {"name": "subnet-a", "namespace": "tenant-a"},
    "spec": {"ipv4Cidr": "10.0.1.0/24"},
}
VIRTUAL_NETWORK = {
    "apiVersion": "osac.openshift.io/v1alpha1",
    "kind": "VirtualNetwork",
    "id": "vnet-api-id",
    "metadata": {"name": "vnet-a", "namespace": "tenant-a"},
    "spec": {"ipv4Cidr": "10.0.0.0/16"},
}
IPAM = {
    "gateway_ipv4": "10.0.1.1",
    "overlay_ipv4_cidrs": ["10.0.1.0/25"],
    "physical_ipv4_cidrs": ["10.0.1.128/26"],
    "lb_vip_ipv4_cidrs": ["10.0.1.192/26"],
}


def run_helper(tmp_path: Path, task_name: str, variables: dict) -> subprocess.CompletedProcess:
    playbook = tmp_path / "play.yml"
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": variables,
                    "tasks": [
                        {
                            "name": "Normalize task input",
                            "ansible.builtin.include_tasks": str(HELPER),
                            "vars": {"_osac_task_name": task_name},
                        },
                        {"name": "Report facts", "ansible.builtin.debug": {"msg": FACTS}},
                    ],
                }
            ]
        ),
        encoding="utf-8",
    )
    return subprocess.run(
        ["ansible-playbook", "-i", "localhost,", "-c", "local", str(playbook)],
        cwd=ROOT,
        env={
            **os.environ,
            "ANSIBLE_LOCAL_TEMP": str(tmp_path / "local"),
            "ANSIBLE_STDOUT_CALLBACK": "json",
        },
        capture_output=True,
        text=True,
        timeout=60,
    )


def facts(result: subprocess.CompletedProcess) -> dict:
    assert result.returncode == 0, result.stdout + result.stderr
    data = json.loads(result.stdout)
    for play in data["plays"]:
        for task in play["tasks"]:
            for host_result in task["hosts"].values():
                message = host_result.get("msg")
                if isinstance(message, str) and message.startswith("{"):
                    return json.loads(message)
    raise AssertionError("facts not reported: " + result.stdout[-2000:])


def contract_vars(**overrides) -> dict:
    job_vars = {
        "manager": {"name": "netris", "type": "fabric", "ansible_role": "osac.templates.netris"},
        "task": "create_subnet",
        "tenant": "tenant-api-id",
        "resource": SUBNET,
        "virtual_network": VIRTUAL_NETWORK,
        "ipam": IPAM,
        "network_type": "evpn",
    }
    job_vars.update(overrides.pop("job_vars", {}))
    return {"osac_job_vars": job_vars, **overrides}


def test_contract_input_passes_snapshots_through(tmp_path: Path) -> None:
    result = run_helper(tmp_path, "create_subnet", contract_vars())

    reported = facts(result)
    assert reported["contract"] is True
    assert reported["task"] == "create_subnet"
    assert reported["tenant"] == "tenant-api-id"
    assert reported["resource"]["id"] == "subnet-api-id"
    assert reported["virtual_network"]["id"] == "vnet-api-id"
    assert reported["ipam"] == IPAM
    assert reported["network_type"] == "evpn"


def test_contract_input_publishes_legacy_alias_for_untouched_tasks(tmp_path: Path) -> None:
    result = run_helper(tmp_path, "create_subnet", contract_vars())

    reported = facts(result)
    assert reported["subnet_alias"]["id"] == "subnet-api-id"


def test_legacy_input_is_detected_and_keeps_today_aliases(tmp_path: Path) -> None:
    subnet = {
        "metadata": {
            "name": "subnet-a",
            "annotations": {"osac.openshift.io/tenant": "tenant-from-annotation"},
        },
        "spec": {"ipv4Cidr": "10.0.1.0/24"},
    }
    result = run_helper(tmp_path, "create_subnet", {"subnet": subnet})

    reported = facts(result)
    assert reported["contract"] is False
    assert reported["task"] == "create_subnet"
    assert reported["tenant"] == "tenant-from-annotation"
    assert reported["subnet_alias"] == subnet


def test_legacy_input_does_not_invent_an_address_plan(tmp_path: Path) -> None:
    result = run_helper(tmp_path, "create_subnet", {"subnet": SUBNET})

    reported = facts(result)
    assert reported["ipam"] == {}
    assert reported["network_type"] == ""
    assert reported["network_segment"] == {}


def test_contract_input_rejects_a_malformed_envelope(tmp_path: Path) -> None:
    variables = contract_vars()
    del variables["osac_job_vars"]["task"]

    result = run_helper(tmp_path, "create_subnet", variables)

    assert result.returncode != 0
    assert "Malformed osac_job_vars envelope" in result.stdout + result.stderr


def test_contract_input_rejects_a_task_mismatch(tmp_path: Path) -> None:
    result = run_helper(
        tmp_path, "create_subnet", contract_vars(job_vars={"task": "delete_subnet"})
    )

    assert result.returncode != 0
    assert "Task mismatch" in result.stdout + result.stderr


def test_contract_input_rejects_a_missing_resource(tmp_path: Path) -> None:
    result = run_helper(tmp_path, "create_subnet", contract_vars(job_vars={"resource": {}}))

    assert result.returncode != 0
    assert "No resource snapshot" in result.stdout + result.stderr


def test_contract_input_rejects_a_non_fabric_manager(tmp_path: Path) -> None:
    result = run_helper(
        tmp_path,
        "create_subnet",
        contract_vars(job_vars={"manager": {
            "name": "cudn",
            "type": "kubernetes",
            "ansible_role": "osac.templates.cudn_evpn",
        }}),
    )

    assert result.returncode != 0
    assert "Fabric Manager" in result.stdout + result.stderr


def test_contract_input_requires_a_task_name(tmp_path: Path) -> None:
    playbook = tmp_path / "play.yml"
    playbook.write_text(
        yaml.safe_dump(
            [{"hosts": "localhost", "gather_facts": False,
              "tasks": [{"name": "Normalize", "ansible.builtin.include_tasks": str(HELPER)}]}]
        ),
        encoding="utf-8",
    )
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", "-c", "local", str(playbook)],
        cwd=ROOT,
        env={**os.environ, "ANSIBLE_LOCAL_TEMP": str(tmp_path / "local")},
        capture_output=True,
        text=True,
        timeout=60,
    )

    assert result.returncode != 0
    assert "task name" in (result.stdout + result.stderr).lower()
