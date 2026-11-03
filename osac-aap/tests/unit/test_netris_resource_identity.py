"""Keep Netris create, update, and delete operations scoped to resource identity."""

from pathlib import Path

import yaml
from jinja2 import Environment


ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = ROOT / "collections/ansible_collections"
TEMPLATE_NETRIS = COLLECTIONS / "osac/templates/roles/netris/tasks"
IPAM_ROLE = COLLECTIONS / "netris/controller/roles/ipam/tasks"
VNET_ROLE = COLLECTIONS / "netris/controller/roles/vnet/tasks"
VPC_ROLE = COLLECTIONS / "netris/controller/roles/vpc/tasks"


def _tasks(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _block(path: Path) -> list[dict]:
    """The tasks of the main block of a contract task file."""
    return yaml.safe_load(path.read_text(encoding="utf-8"))[0]["block"]


def _named_task(tasks: list[dict], name: str) -> dict:
    return next(task for task in tasks if task.get("name") == name)


def test_ipam_create_and_delete_match_the_complete_requested_identity() -> None:
    create = _tasks(IPAM_ROLE / "create_subnet.yaml")
    find = _named_task(create, "Find IPAM subnets by name and CIDR")
    expression = find["ansible.builtin.set_fact"]["_existing_ipam_subnets"]
    assert "selectattr('name', 'equalto', ipam_name)" in expression
    assert "selectattr('prefix', 'defined')" in expression
    assert "selectattr('prefix', 'equalto', ipam_cidr)" in expression
    fallback = _named_task(create, "Keep same-name IPAM subnet candidates when identity filtering found no match")
    assert fallback["when"] == [
        "_existing_ipam_subnets | length == 0",
        "_ipam_subnets_by_name | length > 0",
    ]
    assert "_existing_ipam_subnets" in _named_task(create, "Filter existing IPAM subnets by VPC")[
        "ansible.builtin.set_fact"
    ]["_existing_ipam_subnets"]
    assert "ipam_vpc_id | int" in _named_task(create, "Filter existing IPAM subnets by VPC")[
        "ansible.builtin.set_fact"
    ]["_existing_ipam_subnets"]
    assert "ipam_tenant_id | int" in _named_task(create, "Filter existing IPAM subnets by tenant")[
        "ansible.builtin.set_fact"
    ]["_existing_ipam_subnets"]
    assert _named_task(create, "Fail if the IPAM subnet identity is ambiguous")["ansible.builtin.assert"][
        "that"
    ] == ["_existing_ipam_subnets | length <= 1"]

    delete = _tasks(IPAM_ROLE / "delete_subnet.yaml")
    for name in (
        "Filter IPAM subnets by CIDR",
        "Filter IPAM subnets by VPC",
        "Filter IPAM subnets by tenant",
        "Filter child IPAM subnets by parent",
    ):
        assert "_existing_ipam_subnets" in _named_task(delete, name)["ansible.builtin.set_fact"]
    assert _named_task(delete, "Fail if the IPAM deletion identity is ambiguous")["ansible.builtin.assert"][
        "that"
    ] == ["_existing_ipam_subnets | length <= 1"]
    delete_request = _named_task(delete, "Delete IPAM subnet")["netris.controller.protected_uri"]
    assert delete_request["url"].endswith("/api/v2/ipam/subnet/{{ _existing_ipam_subnet.id }}")


def test_netris_subnet_flow_supplies_full_identity_to_ipam_roles() -> None:
    create = _block(TEMPLATE_NETRIS / "create_subnet.yaml")
    parent_ipam_create = _named_task(create, "Create the IPAM subnet")
    assert parent_ipam_create["vars"]["ipam_vpc_id"] == "{{ netris_vpc.id }}"
    assert parent_ipam_create["vars"]["ipam_tenant_id"] == "{{ netris_tenant_id | int }}"

    delete = _block(TEMPLATE_NETRIS / "delete_subnet.yaml")
    ipam_delete = _named_task(delete, "Delete the IPAM subnet")
    assert ipam_delete["ansible.builtin.include_role"]["tasks_from"] == "delete_subnet"
    assert ipam_delete["vars"]["ipam_tenant_id"] == "{{ netris_tenant_id | int }}"
    assert "'vpc.id': _netris_vpc_id" in ipam_delete["vars"]["ipam_expected_fields"]


def test_vnet_create_delete_and_gateway_update_are_identity_scoped() -> None:
    create = _tasks(VNET_ROLE / "create.yaml")
    find_by_name = _named_task(create, "Find V-Nets by name")["ansible.builtin.set_fact"]["_vnets_by_name"]
    assert "selectattr('name', 'equalto', vnet_name)" in find_by_name
    find = _named_task(create, "Find V-Nets by name and VPC")["ansible.builtin.set_fact"]["_existing_vnets"]
    assert "selectattr('vpc.id', 'defined')" in find
    assert "selectattr('vpc.id', 'equalto', vnet_vpc_id | int)" in find
    fallback = _named_task(create, "Keep same-name V-Net candidates when identity filtering found no match")
    assert fallback["when"] == [
        "_existing_vnets | default([]) | length == 0",
        "_vnets_by_name | length > 0",
    ]
    assert "vnet_tenant_id | int" in _named_task(create, "Filter V-Nets by tenant")["ansible.builtin.set_fact"][
        "_existing_vnets"
    ]
    assert _named_task(create, "Fail if the V-Net identity is ambiguous")["ansible.builtin.assert"]["that"] == [
        "_existing_vnets | length <= 1"
    ]

    delete = _tasks(VNET_ROLE / "delete.yaml")
    for name in ("Filter V-Nets by VPC", "Filter V-Nets by tenant"):
        assert "_existing_vnets" in _named_task(delete, name)["ansible.builtin.set_fact"]
    assert _named_task(delete, "Fail if the V-Net deletion identity is ambiguous")["ansible.builtin.assert"][
        "that"
    ] == ["_existing_vnets | length <= 1"]
    delete_request = _named_task(delete, "Delete V-Net")["netris.controller.protected_uri"]
    assert delete_request["url"].endswith("/api/v2/vnet/{{ _existing_vnet.id }}")

    subnet_create = _block(TEMPLATE_NETRIS / "create_subnet.yaml")
    for name in ("Create the V-Net with a gateway", "Create the V-Net without a gateway"):
        role_call = _named_task(subnet_create, name)
        assert role_call["vars"]["vnet_vpc_id"] == "{{ netris_vpc.id }}"
        assert role_call["vars"]["vnet_tenant_id"] == "{{ netris_tenant_id | int }}"
    # The create role compares vpc, tenant and site of an existing V-Net before
    # it is reused, so the update below only touches an owned V-Net.
    names = [task.get("name") for task in subnet_create]
    assert names.index("Create the V-Net with a gateway") < names.index("Update the gateway and DHCP of an existing V-Net")

    subnet_delete = _block(TEMPLATE_NETRIS / "delete_subnet.yaml")
    vnet_delete = _named_task(subnet_delete, "Delete the V-Net")
    assert vnet_delete["vars"]["vnet_tenant_id"] == "{{ netris_tenant_id | int }}"
    assert "'vpc.id': _netris_vpc_id" in vnet_delete["vars"]["vnet_expected_fields"]


def test_parent_vpc_lookup_matches_id_and_tenant_and_rejects_ambiguity() -> None:
    read = _tasks(VPC_ROLE / "read.yaml")
    filter_by_id = _named_task(read, "Filter VPCs by ID")
    assert "vpc_lookup_id | int" in filter_by_id["ansible.builtin.set_fact"][
        "_netris_vpc_matches"
    ]
    assert filter_by_id["when"] == "vpc_lookup_id is defined"
    filter_by_id_enabled = Environment().compile_expression(filter_by_id["when"])
    assert filter_by_id_enabled(vpc_id=99) is False
    assert filter_by_id_enabled(vpc_id=99, vpc_lookup_id=42) is True
    filter_by_id_expression = Environment().compile_expression(
        filter_by_id["ansible.builtin.set_fact"]["_netris_vpc_matches"]
        .removeprefix("{{")
        .removesuffix("}}")
        .strip()
    )
    vpcs = [{"id": 42, "name": "requested"}, {"id": 99, "name": "other"}]
    assert filter_by_id_expression(_netris_vpc_matches=vpcs, vpc_id=99, vpc_lookup_id=42) == [vpcs[0]]
    assert "vpc_tenant_id | int" in _named_task(read, "Filter VPCs by admin tenant")["ansible.builtin.set_fact"][
        "_netris_vpc_matches"
    ]
    assert _named_task(read, "Fail if the VPC lookup is ambiguous")["ansible.builtin.assert"]["that"] == [
        "_netris_vpc_matches | length <= 1"
    ]
    read_args = yaml.safe_load((VPC_ROLE.parent / "meta" / "argument_specs.yaml").read_text(encoding="utf-8"))[
        "argument_specs"
    ]["read"]["options"]
    assert "vpc_lookup_id" in read_args
    assert "vpc_id" not in read_args

    subnet_create = _block(TEMPLATE_NETRIS / "create_subnet.yaml")
    role_call = _named_task(subnet_create, "Look up the VPC of the VirtualNetwork")
    assert role_call["vars"]["vpc_name"] == "{{ netris_names.vpc }}"
    assert role_call["vars"]["vpc_tenant_id"] == "{{ netris_tenant_id | int }}"
