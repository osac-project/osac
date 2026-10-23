import os
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

_OUTPUT_KEYS = ("l2_vni", "l3_vni", "fabric_reserved_range")


def _create_subnet_tasks() -> list[dict]:
    path = (
        Path(__file__).resolve().parents[2]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "templates"
        / "roles"
        / "netris"
        / "tasks"
        / "create_subnet.yaml"
    )
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    flattened = []
    for task in tasks:
        flattened.append(task)
        flattened.extend(_flatten_tasks(task.get("block", [])))
    return flattened


def test_vnet_read_by_id_uses_fact_from_create_or_find_step() -> None:
    tasks = _create_subnet_tasks()
    task_names = [task.get("name") for task in tasks]
    create_index = task_names.index("Create V-Net in Netris controller")
    read_index = task_names.index("Read V-Net details before EVPN gateway reconciliation")

    assert create_index < read_index
    create_task = tasks[create_index]
    assert create_task["ansible.builtin.include_role"]["name"] == "netris.controller.vnet"
    assert create_task["ansible.builtin.include_role"]["tasks_from"] == "create"

    read_task = tasks[read_index]
    assert read_task["ansible.builtin.include_role"]["name"] == "netris.controller.vnet"
    assert read_task["ansible.builtin.include_role"]["tasks_from"] == "read_by_id"
    assert "vnet_id" not in read_task.get("vars", {})


def test_existing_vnet_gateway_is_updated_without_dropping_other_fields() -> None:
    tasks = _create_subnet_tasks()
    names = [task.get("name") for task in tasks]
    before_index = names.index("Read V-Net details before EVPN gateway reconciliation")
    match_index = names.index("Check whether existing V-Net matches EVPN gateway contract")
    capture_index = names.index("Capture the existing gateway before EVPN reconciliation")
    gateway_index = names.index("Build V-Net gateway update preserving existing fields")
    body_index = names.index("Build V-Net update preserving existing configuration")
    update_index = names.index("Update existing V-Net gateway and DHCP range")
    after_index = names.index("Read V-Net details after EVPN gateway reconciliation")
    validation_index = names.index("Validate Netris fabric outputs")

    assert (
        before_index
        < match_index
        < capture_index
        < gateway_index
        < body_index
        < update_index
        < after_index
        < validation_index
    )

    match_task = tasks[match_index]
    assert match_task["ansible.builtin.set_fact"]["_evpn_gateway_contract_matches"]
    assert "netris_vnet_detail.gateways" in match_task["ansible.builtin.set_fact"]["_evpn_gateway_contract_matches"]
    assert "get('dhcp')" in match_task["ansible.builtin.set_fact"]["_evpn_gateway_contract_matches"]
    assert "get('dhcpStartIp'" in match_task["ansible.builtin.set_fact"]["_evpn_gateway_contract_matches"]
    assert match_task["when"] == "is_cudn_evpn"

    body_task = tasks[body_index]
    update_body = body_task["ansible.builtin.set_fact"]["_vnet_update_body"]
    assert "netris_vnet_detail | combine" in update_body
    assert "'gateways'" in update_body

    update_task = tasks[update_index]
    assert update_task["ansible.builtin.include_role"]["name"] == "netris.controller.vnet"
    assert update_task["ansible.builtin.include_role"]["tasks_from"] == "update"
    assert update_task["vars"]["vnet_body"] == "{{ _vnet_update_body }}"
    assert update_task["when"] == "is_cudn_evpn and not _evpn_gateway_contract_matches"

    after_task = tasks[after_index]
    assert after_task["ansible.builtin.include_role"]["tasks_from"] == "read_by_id"
    assert after_task["when"] == "is_cudn_evpn and not _evpn_gateway_contract_matches"


@pytest.mark.parametrize(
    ("gateway", "expects_nested_dhcp"),
    [
        pytest.param(
            {
                "id": 21,
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcpLeaseCount": 61,
                "dhcp": {
                    "start": "192.0.2.2",
                    "end": "192.0.2.62",
                    "optionSet": {"id": 31},
                },
            },
            True,
            id="nested-dhcp-response",
        ),
        pytest.param(
            {
                "id": 21,
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcpStartIp": "192.0.2.2",
                "dhcpEndIp": "192.0.2.62",
            },
            False,
            id="legacy-flat-dhcp-response",
        ),
    ],
)
def test_vnet_update_body_preserves_existing_fields_and_replaces_gateway(
    tmp_path: Path, gateway: dict, expects_nested_dhcp: bool
) -> None:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"
    ansible_home = tmp_path / "ansible-home"
    ansible_local_temp = tmp_path / "ansible-local"
    ansible_home.mkdir()
    ansible_local_temp.mkdir()
    existing_vnet = {
        "id": 7,
        "name": "existing-vnet",
        "vxlanID": 10007,
        "vpc": {"id": 9},
        "ports": [{"id": 91}],
        "gateways": [gateway],
    }
    update_task_names = {
        "Capture the existing gateway before EVPN reconciliation",
        "Build V-Net gateway update preserving existing fields",
        "Build V-Net update preserving existing configuration",
    }
    update_tasks = [task for task in _create_subnet_tasks() if task.get("name") in update_task_names]
    assert [task["name"] for task in update_tasks] == [
        "Capture the existing gateway before EVPN reconciliation",
        "Build V-Net gateway update preserving existing fields",
        "Build V-Net update preserving existing configuration",
    ]
    playbook = tmp_path / "update_vnet_body.yaml"
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": {
                        "netris_vnet_detail": existing_vnet,
                        "_gateway_cidr": "192.0.2.1/24",
                        "_dhcp_start": "192.0.2.3",
                        "_dhcp_end": "192.0.2.127",
                        "is_cudn_evpn": True,
                        "_evpn_gateway_contract_matches": False,
                        "_expects_nested_dhcp": expects_nested_dhcp,
                    },
                    "tasks": [
                        *update_tasks,
                        {
                            "name": "Assert full V-Net update contract",
                            "ansible.builtin.assert": {
                                "that": [
                                    "_vnet_update_body.name == 'existing-vnet'",
                                    "_vnet_update_body.vxlanID | int == 10007",
                                    "_vnet_update_body.vpc.id | int == 9",
                                    "_vnet_update_body.ports == [{'id': 91}]",
                                    "_vnet_update_body.gateways | length == 1",
                                    "_vnet_update_body.gateways[0].prefix == '192.0.2.1/24'",
                                    "_vnet_update_body.gateways[0].dhcpEnabled | bool",
                                    "_vnet_update_body.gateways[0].id | int == 21",
                                ]
                            },
                        },
                        {
                            "name": "Assert nested DHCP is updated without losing metadata",
                            "ansible.builtin.assert": {
                                "that": [
                                    "_vnet_update_body.gateways[0].dhcp.start == '192.0.2.3'",
                                    "_vnet_update_body.gateways[0].dhcp.end == '192.0.2.127'",
                                    "_vnet_update_body.gateways[0].dhcp.optionSet.id | int == 31",
                                    "_vnet_update_body.gateways[0].dhcpLeaseCount | int == 61",
                                    "'dhcpStartIp' not in _vnet_update_body.gateways[0]",
                                ]
                            },
                            "when": "_expects_nested_dhcp",
                        },
                        {
                            "name": "Assert flat DHCP is updated for legacy responses",
                            "ansible.builtin.assert": {
                                "that": [
                                    "_vnet_update_body.gateways[0].dhcpStartIp == '192.0.2.3'",
                                    "_vnet_update_body.gateways[0].dhcpEndIp == '192.0.2.127'",
                                    "'dhcp' not in _vnet_update_body.gateways[0]",
                                ]
                            },
                            "when": "not _expects_nested_dhcp",
                        },
                    ],
                }
            ],
            sort_keys=False,
        ),
        encoding="utf-8",
    )

    result = subprocess.run(
        [ansible_playbook, "-i", "localhost,", "-c", "local", str(playbook)],
        capture_output=True,
        text=True,
        check=False,
        env={**os.environ, "ANSIBLE_HOME": str(ansible_home), "ANSIBLE_LOCAL_TEMP": str(ansible_local_temp)},
    )

    assert result.returncode == 0, result.stdout + result.stderr


def _fabric_output_tasks() -> list[dict]:
    names = {
        "Initialize the published fabric range from the Subnet CIDR",
        "Validate Subnet IPv4 CIDR for a disjoint fabric pool",
        "Calculate the Netris fabric pool prefix",
        "Calculate the Netris fabric pool CIDR",
        "Set gateway IP for the existing Netris Subnet contract",
        "Compute DHCP range for the existing Netris Subnet contract",
        "Set gateway IP using the full EVPN Subnet CIDR",
        "Compute DHCP range inside the reserved EVPN fabric pool",
        "Check whether existing V-Net matches EVPN gateway contract",
        "Validate Netris fabric outputs",
    }
    return [task for task in _flatten_tasks(_create_subnet_tasks()) if task.get("name") in names]


def _run_fabric_output_validation(
    tmp_path: Path, *, vnet: object, vpc: object, subnet_ipv4_cidr: str = "192.0.2.0/24", is_cudn_evpn: bool = True
) -> subprocess.CompletedProcess[str]:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"

    playbook = tmp_path / "validate_fabric_outputs.yaml"
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
                        "netris_vnet_detail": vnet,
                        "_parent_vpc": vpc,
                        "subnet_name": "test-subnet",
                        "subnet_ipv4_cidr": subnet_ipv4_cidr,
                        "is_cudn_evpn": is_cudn_evpn,
                    },
                    "tasks": [
                        *_fabric_output_tasks(),
                        {
                            "name": "Capture the reserved fabric address contract",
                            "ansible.builtin.set_fact": {
                                "fabric_contract": {
                                    "reserved_range": "{{ _fabric_reserved_range }}",
                                    "gateway_cidr": "{{ _gateway_cidr }}",
                                    "dhcp_start": "{{ _dhcp_start }}",
                                    "dhcp_end": "{{ _dhcp_end }}",
                                }
                            },
                        },
                        {
                            "name": "Show the reserved fabric address contract",
                            "ansible.builtin.debug": {"var": "fabric_contract"},
                        },
                        {"name": "Confirm outputs passed validation", "ansible.builtin.set_fact": {"validated": True}},
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


def test_fabric_outputs_are_validated_before_configmap_publication() -> None:
    tasks = _create_subnet_tasks()
    names = [task.get("name") for task in tasks]
    validation_index = names.index("Validate Netris fabric outputs")
    publication_index = names.index("Publish fabric outputs for the k8s manager")
    assert validation_index < publication_index

    configmap = tasks[publication_index]["kubernetes.core.k8s"]["definition"]["data"]
    assert tuple(configmap) == _OUTPUT_KEYS
    assert configmap["l2_vni"] == "{{ netris_vnet_detail.vxlanID | string | trim }}"
    assert configmap["l3_vni"] == "{{ subnet_l3_vni | string }}"
    assert configmap["fabric_reserved_range"] == "{{ _fabric_reserved_range | string | trim }}"

    create_vnet = tasks[names.index("Create V-Net in Netris controller")]["vars"]
    assert create_vnet["vnet_gateway"] == "{{ _gateway_cidr | default(omit) }}"
    assert create_vnet["vnet_dhcp_start_ip"] == "{{ _dhcp_start | default(omit) }}"
    assert create_vnet["vnet_dhcp_end_ip"] == "{{ _dhcp_end | default(omit) }}"


@pytest.mark.parametrize(
    ("subnet_cidr", "fabric_range", "gateway_cidr", "dhcp_start", "dhcp_end"),
    [
        ("192.0.2.0/25", "192.0.2.0/26", "192.0.2.1/25", "192.0.2.3", "192.0.2.63"),
        ("192.0.2.0/29", "192.0.2.0/30", "192.0.2.1/29", "192.0.2.3", "192.0.2.3"),
    ],
    ids=["/25-subnet", "/29-minimum"],
)
def test_evpn_gateway_keeps_full_subnet_and_splits_dhcp_pool(
    tmp_path: Path,
    subnet_cidr: str,
    fabric_range: str,
    gateway_cidr: str,
    dhcp_start: str,
    dhcp_end: str,
) -> None:
    result = _run_fabric_output_validation(
        tmp_path,
        subnet_ipv4_cidr=subnet_cidr,
        vpc={"vni": 20007},
        vnet={
            "vxlanID": 10007,
            "gateways": [
                {
                    "prefix": gateway_cidr,
                    "dhcpEnabled": True,
                    "dhcpStartIp": dhcp_start,
                    "dhcpEndIp": dhcp_end,
                }
            ],
        },
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert f'"reserved_range": "{fabric_range}"' in result.stdout
    assert f'"gateway_cidr": "{gateway_cidr}"' in result.stdout
    assert f'"dhcp_start": "{dhcp_start}"' in result.stdout
    assert f'"dhcp_end": "{dhcp_end}"' in result.stdout


@pytest.mark.parametrize(
    ("gateway", "expected_match"),
    [
        (
            {
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcp": {"start": "192.0.2.3", "end": "192.0.2.127"},
            },
            True,
        ),
        (
            {
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcpStartIp": "192.0.2.3",
                "dhcpEndIp": "192.0.2.127",
            },
            True,
        ),
        (
            {
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcp": {"start": "192.0.2.2", "end": "192.0.2.126"},
            },
            False,
        ),
        (
            {
                "prefix": "192.0.2.1/24",
                "dhcpEnabled": True,
                "dhcpStartIp": "192.0.2.2",
                "dhcpEndIp": "192.0.2.126",
            },
            False,
        ),
        (
            {
                "prefix": "192.0.2.1/26",
                "dhcpEnabled": True,
                "dhcp": {"start": "192.0.2.3", "end": "192.0.2.62"},
            },
            False,
        ),
    ],
    ids=["nested-current", "flat-current", "nested-old", "flat-old", "old-prefix"],
)
def test_existing_netris_gateway_contract_is_idempotent_for_nested_and_flat_dhcp(
    tmp_path: Path, gateway: dict, expected_match: bool
) -> None:
    ansible_playbook = shutil.which("ansible-playbook")
    assert ansible_playbook is not None, "ansible-playbook must be installed in the AAP dev environment"
    match_task = next(
        task
        for task in _create_subnet_tasks()
        if task.get("name") == "Check whether existing V-Net matches EVPN gateway contract"
    )
    playbook = tmp_path / "match_existing_gateway.yaml"
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": {
                        "netris_vnet_detail": {"gateways": [gateway]},
                        "_gateway_cidr": "192.0.2.1/24",
                        "_dhcp_start": "192.0.2.3",
                        "_dhcp_end": "192.0.2.127",
                        "is_cudn_evpn": True,
                    },
                    "tasks": [
                        match_task,
                        {
                            "name": "Show whether the existing V-Net already matches",
                            "ansible.builtin.debug": {"var": "_evpn_gateway_contract_matches"},
                        },
                    ],
                }
            ],
            sort_keys=False,
        ),
        encoding="utf-8",
    )
    ansible_home = tmp_path / "ansible-home"
    ansible_local_temp = tmp_path / "ansible-local"
    ansible_home.mkdir()
    ansible_local_temp.mkdir()

    result = subprocess.run(
        [ansible_playbook, "-v", "-i", "localhost,", "-c", "local", str(playbook)],
        capture_output=True,
        text=True,
        check=False,
        env={**os.environ, "ANSIBLE_HOME": str(ansible_home), "ANSIBLE_LOCAL_TEMP": str(ansible_local_temp)},
    )

    assert result.returncode == 0, result.stdout + result.stderr
    expected = "true" if expected_match else "false"
    assert f'"_evpn_gateway_contract_matches": {expected}' in result.stdout


@pytest.mark.parametrize(
    ("vnet", "vpc", "subnet_ipv4_cidr"),
    [
        (None, {"vni": 20007}, "192.0.2.0/24"),
        ({"vxlanID": None}, {"vni": 20007}, "192.0.2.0/24"),
        ({"vxlanID": ""}, {"vni": 20007}, "192.0.2.0/24"),
        ({"vxlanID": 0}, {"vni": 20007}, "192.0.2.0/24"),
        ({"vxlanID": "16777216"}, {"vni": 20007}, "192.0.2.0/24"),
        ({"vxlanID": 10007}, None, "192.0.2.0/24"),
        ({"vxlanID": 10007}, {"vni": None}, "192.0.2.0/24"),
        ({"vxlanID": 10007}, {"vni": "16777216"}, "192.0.2.0/24"),
        (
            {
                "vxlanID": 10007,
                "gateways": [
                    {
                        "prefix": "192.0.2.1/26",
                        "dhcpEnabled": True,
                        "dhcpStartIp": "192.0.2.3",
                        "dhcpEndIp": "192.0.2.62",
                    }
                ],
            },
            {"vni": 20007},
            "192.0.2.0/24",
        ),
        (
            {
                "vxlanID": 10007,
                "gateways": [
                    {
                        "prefix": "198.51.100.1/24",
                        "dhcpEnabled": True,
                        "dhcpStartIp": "198.51.100.3",
                        "dhcpEndIp": "198.51.100.62",
                    }
                ],
            },
            {"vni": 20007},
            "192.0.2.0/24",
        ),
        (
            {
                "vxlanID": 10007,
                "gateways": [
                    {
                        "prefix": "192.0.2.1/24",
                        "dhcpEnabled": True,
                        "dhcpStartIp": "192.0.2.2",
                        "dhcpEndIp": "192.0.2.126",
                    }
                ],
            },
            {"vni": 20007},
            "192.0.2.0/24",
        ),
    ],
)
def test_invalid_fabric_output_fails_before_publication(
    tmp_path: Path, vnet: object, vpc: object, subnet_ipv4_cidr: str
) -> None:
    result = _run_fabric_output_validation(tmp_path, vnet=vnet, vpc=vpc, subnet_ipv4_cidr=subnet_ipv4_cidr)

    assert result.returncode != 0
    assert "Refusing to publish incomplete fabric outputs" in result.stdout
    assert "Confirm outputs passed validation" not in result.stdout


@pytest.mark.parametrize(
    "gateway",
    [
        {"prefix": "192.0.2.1/24", "dhcpEnabled": True, "dhcpStartIp": "192.0.2.3", "dhcpEndIp": "192.0.2.127"},
        {
            "prefix": "192.0.2.1/24",
            "dhcpEnabled": True,
            "dhcp": {"start": "192.0.2.3", "end": "192.0.2.127", "optionSet": {"id": 1}},
        },
    ],
    ids=["flat", "nested"],
)
def test_valid_fabric_outputs_pass_validation(tmp_path: Path, gateway: dict) -> None:
    result = _run_fabric_output_validation(
        tmp_path,
        vnet={
            "vxlanID": " 10007 ",
            "gateways": [gateway],
        },
        vpc={"vni": 20007},
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert "Confirm outputs passed validation" in result.stdout
    assert '"reserved_range": "192.0.2.0/25"' in result.stdout
    assert '"gateway_cidr": "192.0.2.1/24"' in result.stdout
    assert '"dhcp_start": "192.0.2.3"' in result.stdout
    assert '"dhcp_end": "192.0.2.127"' in result.stdout


@pytest.mark.parametrize(
    "gateway_changes",
    [
        {"dhcp": {"start": "192.0.2.2", "end": "192.0.2.127"}},
        {"dhcp": {"start": "192.0.2.2", "end": "192.0.2.63"}},
        {"prefix": "192.0.2.1/26"},
        {"dhcpEnabled": False},
        {"dhcp": {}},
        {
            "dhcp": {"start": "192.0.2.2", "end": "192.0.2.127"},
            "dhcpStartIp": "192.0.2.3",
            "dhcpEndIp": "192.0.2.127",
        },
    ],
    ids=["wrong-start", "wrong-end", "wrong-prefix", "dhcp-disabled", "missing-range", "conflicting-flat-range"],
)
def test_invalid_nested_gateway_fails_before_publication(tmp_path: Path, gateway_changes: dict) -> None:
    gateway = {
        "prefix": "192.0.2.1/24",
        "dhcpEnabled": True,
        "dhcp": {"start": "192.0.2.3", "end": "192.0.2.127"},
    }
    gateway.update(gateway_changes)
    result = _run_fabric_output_validation(
        tmp_path, vnet={"vxlanID": 10007, "gateways": [gateway]}, vpc={"vni": 20007}
    )
    assert result.returncode != 0
    assert "Refusing to publish incomplete fabric outputs" in result.stdout
    assert "Confirm outputs passed validation" not in result.stdout


def test_non_evpn_netris_keeps_the_existing_subnet_gateway_and_dhcp_pool(tmp_path: Path) -> None:
    result = _run_fabric_output_validation(
        tmp_path, vnet={"vxlanID": 10007}, vpc={"vni": 20007}, subnet_ipv4_cidr="192.0.2.0/29", is_cudn_evpn=False
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert '"reserved_range": "192.0.2.0/29"' in result.stdout
    assert '"gateway_cidr": "192.0.2.1/29"' in result.stdout
    assert '"dhcp_start": "192.0.2.2"' in result.stdout
    assert '"dhcp_end": "192.0.2.6"' in result.stdout


@pytest.mark.parametrize(
    ("subnet_cidr", "gateway_cidr", "fabric_range", "dhcp_start", "dhcp_end"),
    [
        ("192.0.2.0/28", "192.0.2.1/28", "192.0.2.0/29", "192.0.2.3", "192.0.2.7"),
        ("192.0.2.0/29", "192.0.2.1/29", "192.0.2.0/30", "192.0.2.3", "192.0.2.3"),
    ],
    ids=["/28", "/29-minimum"],
)
def test_smallest_cudn_subnet_leaves_space_for_gateway_ovn_and_dhcp(
    tmp_path: Path,
    subnet_cidr: str,
    gateway_cidr: str,
    fabric_range: str,
    dhcp_start: str,
    dhcp_end: str,
) -> None:
    result = _run_fabric_output_validation(
        tmp_path,
        vnet={
            "vxlanID": 10007,
            "gateways": [
                {
                    "prefix": gateway_cidr,
                    "dhcpEnabled": True,
                    "dhcpStartIp": dhcp_start,
                    "dhcpEndIp": dhcp_end,
                }
            ],
        },
        vpc={"vni": 20007},
        subnet_ipv4_cidr=subnet_cidr,
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert f'"reserved_range": "{fabric_range}"' in result.stdout
    assert f'"gateway_cidr": "{gateway_cidr}"' in result.stdout
    assert f'"dhcp_start": "{dhcp_start}"' in result.stdout
    assert f'"dhcp_end": "{dhcp_end}"' in result.stdout


@pytest.mark.parametrize("subnet_ipv4_cidr", ["", "not-a-cidr", "192.0.2.1/24", "192.0.2.0/30"])
def test_subnet_cidr_must_support_a_disjoint_fabric_pool(tmp_path: Path, subnet_ipv4_cidr: str) -> None:
    result = _run_fabric_output_validation(
        tmp_path,
        vnet={
            "vxlanID": 10007,
            "gateways": [
                {"prefix": "192.0.2.1/24", "dhcpEnabled": True, "dhcpStartIp": "192.0.2.3", "dhcpEndIp": "192.0.2.126"}
            ],
        },
        vpc={"vni": 20007},
        subnet_ipv4_cidr=subnet_ipv4_cidr,
    )

    assert result.returncode != 0
    assert "must have a canonical IPv4 CIDR with prefix length /29 or shorter" in result.stdout
    assert "Confirm outputs passed validation" not in result.stdout
