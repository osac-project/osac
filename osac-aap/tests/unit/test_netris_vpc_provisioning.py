import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

ROLE_ROOT = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "netris"
)
VPC_ROLE_ROOT = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "roles"
    / "vpc"
)


def _virtual_network_tasks() -> list[dict]:
    return yaml.safe_load((ROLE_ROOT / "tasks" / "create_virtual_network.yaml").read_text(encoding="utf-8"))


def _subnet_tasks() -> list[dict]:
    return yaml.safe_load((ROLE_ROOT / "tasks" / "create_subnet.yaml").read_text(encoding="utf-8"))


def test_virtual_network_provisioning_uses_idempotent_vpc_create_or_find() -> None:
    virtual_network_tasks = _virtual_network_tasks()
    create = next(task for task in virtual_network_tasks if task.get("name") == "Create VPC in Netris controller")
    assert create["ansible.builtin.include_role"]["name"] == "netris.controller.vpc"
    assert create["ansible.builtin.include_role"]["tasks_from"] == "create"
    assert create["vars"]["vpc_name"] == "{{ vn_name }}"

    vpc_create_tasks = yaml.safe_load((VPC_ROLE_ROOT / "tasks" / "create.yaml").read_text(encoding="utf-8"))
    existing = next(
        task for task in vpc_create_tasks if task.get("name") == "Set VPC facts from existing VPC (skip creation)"
    )
    created = next(task for task in vpc_create_tasks if task.get("name") == "Set VPC facts from create response")
    assert "vpc_id" in existing["ansible.builtin.set_fact"]
    assert "vpc_id" in created["ansible.builtin.set_fact"]


def test_virtual_network_exports_vpc_id_as_aap_job_artifact() -> None:
    tasks = _virtual_network_tasks()
    task_names = [task.get("name") for task in tasks]
    vpc_create_index = task_names.index("Create VPC in Netris controller")
    vpc_validation_index = task_names.index("Validate Netris VPC ID")
    ipam_create_index = task_names.index("Create IPAM allocation for VirtualNetwork CIDR")
    export_index = task_names.index("Export VirtualNetwork VPC ID")

    assert vpc_create_index < vpc_validation_index < ipam_create_index < export_index
    assert export_index == len(tasks) - 1
    assert tasks[export_index]["ansible.builtin.set_stats"] == {
        "data": {"vpc_id": "{{ vpc_id | int }}"},
        "per_host": False,
        "aggregate": False,
    }


def test_vpc_id_artifact_replaces_previous_value_and_is_an_integer(tmp_path: Path) -> None:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"

    export_task = next(task for task in _virtual_network_tasks() if task.get("name") == "Export VirtualNetwork VPC ID")
    first_export = {**export_task, "vars": {"vpc_id": 7}}
    final_export = {**export_task, "vars": {"vpc_id": "19"}, "register": "export_result"}
    playbook = tmp_path / "export_vpc_id.yaml"
    ansible_config = tmp_path / "ansible.cfg"
    ansible_home = tmp_path / "ansible-home"
    ansible_local_temp = tmp_path / "ansible-local"
    ansible_home.mkdir()
    ansible_local_temp.mkdir()
    ansible_config.write_text(
        "[defaults]\nshow_custom_stats = True\njinja2_native = True\n",
        encoding="utf-8",
    )
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "tasks": [
                        first_export,
                        final_export,
                        {
                            "name": "Check the exported VPC ID",
                            "ansible.builtin.assert": {
                                "that": [
                                    "export_result.ansible_stats.data.vpc_id == 19",
                                    "export_result.ansible_stats.data.vpc_id is integer",
                                ]
                            },
                        },
                    ],
                }
            ],
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    result = subprocess.run(
        [ansible_playbook, "-v", "-i", "localhost,", "-c", "local", str(playbook)],
        capture_output=True,
        text=True,
        check=False,
        env={
            **os.environ,
            "ANSIBLE_CONFIG": str(ansible_config),
            "ANSIBLE_HOME": str(ansible_home),
            "ANSIBLE_LOCAL_TEMP": str(ansible_local_temp),
        },
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert re.search(r'RUN:\s*\{\s*"vpc_id": 19\s*\}', result.stdout), result.stdout


def _run_vpc_id_validation(
    tmp_path: Path, vpc_id: object = None, *, include_vpc_id: bool = True
) -> subprocess.CompletedProcess[str]:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"

    validation_task = next(
        task for task in _virtual_network_tasks() if task.get("name") == "Validate Netris VPC ID"
    )
    variables: dict[str, object] = {"vn_name": "test-vnet"}
    if include_vpc_id:
        variables["vpc_id"] = vpc_id

    playbook = tmp_path / "validate_vpc_id.yaml"
    ansible_home = tmp_path / "ansible-home"
    ansible_local_temp = tmp_path / "ansible-local"
    ansible_home.mkdir()
    ansible_local_temp.mkdir()
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": variables,
                    "tasks": [validation_task],
                }
            ],
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    return subprocess.run(
        [ansible_playbook, "-v", "-i", "localhost,", "-c", "local", str(playbook)],
        capture_output=True,
        text=True,
        check=False,
        env={**os.environ, "ANSIBLE_HOME": str(ansible_home), "ANSIBLE_LOCAL_TEMP": str(ansible_local_temp)},
    )


@pytest.mark.parametrize("vpc_id", [1, "19", "0019"])
def test_positive_integer_vpc_ids_pass_validation(tmp_path: Path, vpc_id: object) -> None:
    result = _run_vpc_id_validation(tmp_path, vpc_id)

    assert result.returncode == 0, result.stdout + result.stderr


@pytest.mark.parametrize("vpc_id", [None, "", "  ", "not-a-number", 0, "0", -1, "-1", 1.5, "1.5"])
def test_invalid_vpc_ids_fail_validation(tmp_path: Path, vpc_id: object) -> None:
    result = _run_vpc_id_validation(tmp_path, vpc_id)

    assert result.returncode != 0
    assert "Netris did not return a VPC ID" in result.stdout


def test_missing_vpc_id_fails_validation(tmp_path: Path) -> None:
    result = _run_vpc_id_validation(tmp_path, include_vpc_id=False)

    assert result.returncode != 0
    assert "Netris did not return a VPC ID" in result.stdout


def test_configmap_publishes_validated_l3_vni_and_current_reserved_range() -> None:
    tasks = _subnet_tasks()
    task_names = [task.get("name") for task in tasks]
    validation_index = task_names.index("Validate Netris VPC L3 VNI")
    export_index = task_names.index("Export Subnet L3 VNI")
    publication_index = task_names.index("Publish fabric outputs for the k8s manager")
    assert validation_index < export_index < publication_index

    data = tasks[publication_index]["kubernetes.core.k8s"]["definition"]["data"]
    assert isinstance(data, str)
    assert "'l2_vni': netris_vnet_detail.vxlanID | string | trim" in data
    assert "'l3_vni': subnet_l3_vni | string" in data
    assert "'fabric_reserved_range': _fabric_reserved_range | string | trim" in data


def _vni_validation_tasks() -> list[dict]:
    return [
        task for task in _subnet_tasks() if task.get("name") in {"Validate Netris VPC L3 VNI", "Export Subnet L3 VNI"}
    ]


def _run_vni_validation(tmp_path: Path, vni: object) -> subprocess.CompletedProcess[str]:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"

    playbook = tmp_path / "validate_vni.yaml"
    ansible_home = tmp_path / "ansible-home"
    ansible_local_temp = tmp_path / "ansible-local"
    ansible_home.mkdir()
    ansible_local_temp.mkdir()
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": {
                        "_parent_vpc": {"vni": vni},
                        "subnet_virtual_network_name": "test-vnet",
                        "expected_vni": int(vni) if str(vni).isdigit() else 0,
                    },
                    "tasks": [
                        *_vni_validation_tasks(),
                        {
                            "name": "Require exported VNI to match the Netris value",
                            "ansible.builtin.assert": {"that": ["subnet_l3_vni | int == expected_vni"]},
                        },
                    ],
                }
            ],
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    return subprocess.run(
        [ansible_playbook, "-v", "-i", "localhost,", "-c", "local", str(playbook)],
        capture_output=True,
        text=True,
        check=False,
        env={**os.environ, "ANSIBLE_HOME": str(ansible_home), "ANSIBLE_LOCAL_TEMP": str(ansible_local_temp)},
    )


@pytest.mark.parametrize("vni", [None, "", "  ", "not-a-number", "0", "16777216"])
def test_invalid_l3_vni_fails_before_export(tmp_path: Path, vni: object) -> None:
    result = _run_vni_validation(tmp_path, vni)

    assert result.returncode != 0
    assert "L3 VNI" in result.stdout
    assert "subnet_l3_vni" not in result.stdout


@pytest.mark.parametrize("vni", [100, "16777215"])
def test_valid_l3_vni_is_exported(tmp_path: Path, vni: object) -> None:
    result = _run_vni_validation(tmp_path, vni)

    assert result.returncode == 0, result.stdout + result.stderr
