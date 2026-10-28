from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from ansible.module_utils.common.arg_spec import ArgumentSpecValidator
from jinja2 import Environment, StrictUndefined


AAP_ROOT = Path(__file__).resolve().parents[2]
ARGUMENT_SPECS = (
    AAP_ROOT
    / "collections/ansible_collections/osac/templates/roles/netris/meta/argument_specs.yaml"
)


def move_argument_spec():
    return yaml.safe_load(ARGUMENT_SPECS.read_text(encoding="utf-8"))["argument_specs"][
        "move_network_attachment"
    ]["options"]


@pytest.mark.parametrize("offboard", [False, True])
@pytest.mark.parametrize("include_subnet_ref", [False, True])
def test_netris_accepts_shared_move_attachment_payload(offboard, include_subnet_ref):
    tasks = yaml.safe_load(
        (AAP_ROOT / "_move_one_attachment.yaml").read_text(encoding="utf-8")
    )
    move = next(task for task in tasks if task["name"] == "Move port with lock held")
    payload_template = move["block"][0]["vars"]["network_attachment"]
    render = Environment(undefined=StrictUndefined)
    render.filters["ternary"] = lambda condition, yes, no: yes if condition else no
    context = {
        "host_name": "test-host",
        "item": {
            "interface": "eth9",
            "subnet_ref": "11111111-2222-4333-8444-555555555555",
            "tenant_vnet": "tenant-vnet",
        },
        "provisioning_vnet": "bm-provisioning",
        "offboard": offboard,
    }
    payload = {
        key: render.from_string(value).render(**context)
        for key, value in payload_template.items()
    }
    if not include_subnet_ref:
        payload.pop("subnet_ref")

    result = ArgumentSpecValidator(move_argument_spec()).validate(
        {"network_attachment": payload}, validate_role_argument_spec=True
    )

    assert not result.error_messages, result.error_messages


def test_netris_move_attachment_still_rejects_unknown_fields():
    result = ArgumentSpecValidator(move_argument_spec()).validate(
        {
            "network_attachment": {
                "host_name": "test-host",
                "logical_interface_name": "eth9",
                "unexpected_field": "invalid",
            }
        },
        validate_role_argument_spec=True,
    )

    assert result.error_messages
    assert "unexpected_field" in " ".join(result.error_messages)
