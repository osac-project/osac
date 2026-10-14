from pathlib import Path

import yaml


CONTROLLER_VARS = (
    Path(__file__).parents[2]
    / "collections/ansible_collections/osac/config_as_code/roles/aap/vars/controller.yml"
)


def test_netris_templates_disable_instance_group_fallback():
    controller = yaml.safe_load(CONTROLLER_VARS.read_text())
    templates = controller["controller_templates"]
    netris_templates = [
        template
        for template in templates
        if any(
            group.endswith(("cluster-fulfillment-ig", "networking-operations-ig"))
            for group in template.get("instance_groups", [])
        )
    ]

    assert netris_templates
    assert all(
        template.get("prevent_instance_group_fallback") is True
        for template in netris_templates
    )
