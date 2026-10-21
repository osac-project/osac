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


def test_config_as_code_group_reads_mutable_netris_settings_from_secret():
    controller = yaml.safe_load(CONTROLLER_VARS.read_text())
    config_as_code_group = next(
        group
        for group in controller["controller_instance_groups"]
        if group["name"] == "{{ aap_prefix }}-config-as-code-ig"
    )
    pod_spec = yaml.safe_load(config_as_code_group["pod_spec_override"])
    worker = pod_spec["spec"]["containers"][0]
    env = {item["name"]: item.get("value") for item in worker["env"]}
    secret_refs = {
        source["secretRef"]["name"]
        for source in worker["envFrom"]
        if "secretRef" in source
    }

    assert "config-as-code-ig" in secret_refs
    assert "NETRIS_CONTROLLER_URL" not in env
    assert "NETRIS_VALIDATE_CERTS" not in env
    assert env["NETRIS_CA_PATH"] == "{{ netris_ca_path }}"
