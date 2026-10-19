"""Guard the Metal3-to-Assisted-Agent provisioning handoff."""

from pathlib import Path

import yaml


COMPONENT_ROOT = Path(__file__).resolve().parents[2]
CREATE_METAL3_TASKS = (
    COMPONENT_ROOT
    / "collections/ansible_collections/osac/templates/roles/bm_host_provisioning/tasks/create_metal3.yaml"
)
DEFAULT_NETWORK_DATA_TASKS = (
    COMPONENT_ROOT
    / "collections/ansible_collections/osac/templates/roles/bm_host_provisioning/tasks/default_network_data.yaml"
)
BUILD_BMH_PATCH_TASKS = (
    COMPONENT_ROOT
    / "collections/ansible_collections/osac/templates/roles/bm_host_provisioning/tasks/build_bmh_patch.yaml"
)


def test_assisted_agent_wait_is_gated_to_ignition_and_matches_bmh_mac() -> None:
    tasks = yaml.safe_load(CREATE_METAL3_TASKS.read_text())
    provision_block = next(
        task["block"] for task in tasks if task.get("name") == "Provision BMH"
    )
    build_user_data = next(
        task for task in provision_block if task.get("name") == "Build userData content"
    )
    detect = next(
        task for task in build_user_data["block"]
        if task.get("name") == "Detect Assisted Installer ignition user data"
    )
    wait = next(
        task for task in provision_block if task.get("name") == "Wait for the host's Assisted Agent to register"
    )

    assert detect["block"][0]["ansible.builtin.set_fact"]["wait_for_assisted_agent"] == (
        "{{ (user_data_secret_result.resources[0].data.userdata | b64decode | from_json).ignition is defined }}"
    )
    assert detect["rescue"][0]["ansible.builtin.set_fact"]["wait_for_assisted_agent"] is False
    assert wait["when"] == "wait_for_assisted_agent | default(false) | bool"
    assert wait["kubernetes.core.k8s_info"]["kind"] == "Agent"
    assert wait["kubernetes.core.k8s_info"]["namespace"] == "{{ bare_metal_instance.metadata.namespace }}"
    assert "selectattr('macAddress', 'equalto', bmh_info.spec.bootMACAddress)" in wait["until"]
    assert wait["retries"] == 180
    assert wait["delay"] == 10


def test_assisted_ignition_boots_the_infraenv_discovery_iso() -> None:
    tasks = yaml.safe_load(CREATE_METAL3_TASKS.read_text())
    patch_tasks = yaml.safe_load(BUILD_BMH_PATCH_TASKS.read_text())

    default_image = next(
        task for task in tasks if task.get("name") == "Set the default Metal3 image"
    )
    already_provisioned = next(
        task for task in tasks
        if task.get("name") == "Check if BMH is already provisioned with the correct image"
    )
    provision_block = next(task["block"] for task in tasks if task.get("name") == "Provision BMH")
    build_user_data = next(
        task for task in provision_block if task.get("name") == "Build userData content"
    )
    user_data_secret = next(
        task for task in build_user_data["block"]
        if task.get("name") == "Create userData Secret in BMH namespace"
    )
    resolve_iso = next(
        task for task in provision_block
        if task.get("name") == "Resolve the Assisted Installer discovery ISO"
    )
    owner = next(task for task in resolve_iso["block"] if task.get("name") == "Extract the owning ClusterOrder")
    owner_name = next(task for task in resolve_iso["block"] if task.get("name") == "Extract the owning ClusterOrder name")
    cluster_order = next(task for task in resolve_iso["block"] if task.get("name") == "Get the owning ClusterOrder")
    tenant_guard = next(
        task for task in resolve_iso["block"]
        if task.get("name") == "Fail when the Assisted worker tenant does not own the ClusterOrder"
    )
    infraenv = next(task for task in resolve_iso["block"] if task.get("name") == "Get the ClusterOrder InfraEnv")
    select_iso = next(
        task for task in resolve_iso["block"]
        if task.get("name") == "Select the Assisted discovery ISO as a live-iso image"
    )
    image_format = next(
        task for task in patch_tasks if task.get("name") == "Add image format to BMH patch"
    )
    checksum = next(
        task for task in patch_tasks if task.get("name") == "Add checksum to BMH patch for non-OCI images"
    )

    assert default_image["ansible.builtin.set_fact"] == {
        "bmh_image_url": "{{ template_params.imageURL }}",
        "bmh_image_format": "",
    }
    already_provisioned_expression = already_provisioned["ansible.builtin.set_fact"]["already_provisioned"]
    assert "bmh_image_url" in already_provisioned_expression
    assert "bmh_image_format" in already_provisioned_expression
    assert user_data_secret["when"] == "not (wait_for_assisted_agent | default(false) | bool)"
    assert owner["ansible.builtin.set_fact"]["cluster_order_owner"] == (
        "{{ bare_metal_instance.metadata.annotations['osac.openshift.io/owner-reference'] | default('') }}"
    )
    assert owner_name["ansible.builtin.set_fact"]["cluster_order_name"] == (
        "{{ cluster_order_owner.split('/')[1] | default('') }}"
    )
    assert owner is not owner_name
    assert cluster_order["kubernetes.core.k8s_info"]["kind"] == "ClusterOrder"
    assert "osac.openshift.io/tenant" in tenant_guard["when"]
    assert infraenv["kubernetes.core.k8s_info"]["kind"] == "InfraEnv"
    assert infraenv["kubernetes.core.k8s_info"]["name"] == "{{ cluster_order_name }}-infraenv"
    assert select_iso["ansible.builtin.set_fact"] == {
        "bmh_image_url": "{{ infraenv_result.resources[0].status.isoDownloadURL | default('') }}",
        "bmh_image_format": "live-iso",
    }
    image_format_expression = image_format["ansible.builtin.set_fact"]["bmh_patch"]
    assert "'format'" in image_format_expression
    assert "bmh_image_format" in image_format_expression
    assert "bmh_image_format | default('') != 'live-iso'" in checksum["when"]


def test_metal3_network_data_defaults_to_discovered_interface_dhcp() -> None:
    create_tasks = yaml.safe_load(CREATE_METAL3_TASKS.read_text())
    default_tasks = yaml.safe_load(DEFAULT_NETWORK_DATA_TASKS.read_text())
    patch_tasks = yaml.safe_load(BUILD_BMH_PATCH_TASKS.read_text())
    provision_block = next(
        task["block"] for task in create_tasks if task.get("name") == "Provision BMH"
    )

    extract = next(
        task for task in create_tasks
        if task.get("name") == "Extract SSH public key, userDataSecret, and networkData from template parameters"
    )
    default = next(
        task for task in create_tasks
        if task.get("name") == "Default networkData to DHCP on discovered Metal3 interfaces"
    )
    secret = next(
        task for task in provision_block if task.get("name") == "Create networkData Secret"
    )
    patch = next(
        task for task in patch_tasks if task.get("name") == "Add networkData to BMH patch"
    )

    assert extract["ansible.builtin.set_fact"]["bmh_network_data"] == (
        "{{ template_params.networkData | default('') }}"
    )
    assert default["ansible.builtin.include_tasks"] == "default_network_data.yaml"
    assert secret["when"] == "bmh_network_data | length > 0"
    assert secret["kubernetes.core.k8s"]["definition"]["stringData"]["networkData"] == (
        "{{ bmh_network_data }}"
    )
    assert patch["when"] == "bmh_network_data | default('') | length > 0"
    assert "bmh_info.status.hardware.nic" in default_tasks[0]["when"]
    assert "bmh_info.spec.bootMACAddress | default('')" in default_tasks[0]["when"]
    network_data = default_tasks[0]["ansible.builtin.set_fact"]["bmh_network_data"]
    assert "ipv4_dhcp" in network_data
    assert "selectattr('mac', 'defined')" in network_data
    assert "ethernet_mac_address': nic.mac" in network_data
    assert "set interface_id = 'interface-' ~ loop.index" in network_data
