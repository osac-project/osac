"""Contract tests for the Netris role's argument specifications."""
from __future__ import annotations

from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]
SPECS = yaml.safe_load(
    (
        AAP_ROOT
        / "collections/ansible_collections/osac/templates/roles/netris/meta/argument_specs.yaml"
    ).read_text(encoding="utf-8")
)["argument_specs"]

RESOURCE_ARGUMENT = {
    "create_virtual_network": "virtual_network",
    "delete_virtual_network": "virtual_network",
    "create_subnet": "subnet",
    "delete_subnet": "subnet",
    "create_security_group": "security_group",
    "delete_security_group": "security_group",
    "create_external_ip": "external_ip",
    "delete_external_ip": "external_ip",
    "create_nat_gateway": "nat_gateway",
    "delete_nat_gateway": "nat_gateway",
    "move_network_attachment": "network_attachment",
}


def test_every_entry_point_declares_its_resource_argument() -> None:
    for task, option in RESOURCE_ARGUMENT.items():
        assert task in SPECS, f"{task} missing from argument_specs.yaml"
        options = SPECS[task]["options"]
        assert option in options, f"{task} does not declare {option}"
        assert options[option].get("required") is not True, (
            f"{task}.{option} must stay optional so a contract-only"
            " include_role validates before the normalizer runs"
        )


def test_the_contract_envelope_is_not_a_role_requirement() -> None:
    for task, spec in SPECS.items():
        assert "osac_job_vars" not in spec.get("options", {}), (
            f"{task} must not require osac_job_vars during the transition"
        )


def test_task_names_are_unique_and_non_empty() -> None:
    assert SPECS, "argument_specs.yaml defines no tasks"
    for task, spec in SPECS.items():
        assert task and isinstance(task, str)
        assert isinstance(spec.get("options", {}), dict)


def test_attachment_entry_point_accepts_the_shared_caller_keys() -> None:
    options = SPECS["move_network_attachment"]["options"]["network_attachment"]["options"]
    assert set(options) == {
        "host_name",
        "logical_interface_name",
        "subnet_ref",
        "from_vnet_name",
        "to_vnet_name",
    }
