"""Unit tests for the fabric_bgp_config service role.

Tests cover:
- FRRConfiguration template rendering with valid inputs.
- NNCP template rendering (dedicated NIC, no bridge, no VLAN).
- Fail-closed validation: empty remoteVTEPRanges, missing localVTEP.
- Typed API compliance: no raw FRR config, no IPv6.
- Route policy: outbound exactly local VTEP /32; inbound ge/le=32.
- advertiseVNIs is never set (Disabled by omission).
- allowAsIn is "origin".
- ebgpMultiHop and gracefulRestart are booleans.
"""

from pathlib import Path

import yaml
from jinja2 import Environment, StrictUndefined


def _role_path() -> Path:
    return (
        Path(__file__).resolve().parents[2]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "service"
        / "roles"
        / "fabric_bgp_config"
    )


def _render_frr_configuration(overrides: dict | None = None) -> dict:
    template_path = _role_path() / "templates" / "frr_configuration.yaml.j2"
    env = Environment(undefined=StrictUndefined)
    # Register the ansible.utils.ipaddr filter stub for template tests.
    env.filters["ansible.utils.ipaddr"] = _ipaddr_filter
    template = env.from_string(template_path.read_text(encoding="utf-8"))
    context = {
        "worker_node_name": "worker-0",
        "worker_local_asn": 64512,
        "worker_peer_address": "10.1.0.2",
        "worker_peer_asn": 65000,
        "worker_local_address": "10.1.0.1/30",
        "worker_local_vtep": "10.1.0.1/32",
        "worker_remote_vtep_ranges": ["10.2.0.0/16"],
        "worker_ebgp_multihop": True,
        "worker_graceful_restart": True,
        "fabric_bgp_frr_namespace": "metallb-system",
    }
    if overrides:
        context.update(overrides)
    rendered = template.render(**context)
    return yaml.safe_load(rendered)


def _render_nncp(overrides: dict | None = None) -> dict:
    template_path = _role_path() / "templates" / "nncp.yaml.j2"
    env = Environment(undefined=StrictUndefined)
    env.filters["ansible.utils.ipaddr"] = _ipaddr_filter
    template = env.from_string(template_path.read_text(encoding="utf-8"))
    context = {
        "worker_node_name": "worker-0",
        "worker_interface": "ens5f0",
        "worker_local_address": "10.1.0.1/30",
    }
    if overrides:
        context.update(overrides)
    rendered = template.render(**context)
    return yaml.safe_load(rendered)


def _ipaddr_filter(value: str, query: str = "") -> str:
    """Minimal stub for ansible.utils.ipaddr filter."""
    if query == "address":
        return value.split("/")[0] if "/" in value else value
    if query == "prefix":
        return value.split("/")[1] if "/" in value else ""
    return value


def _raw_create_tasks() -> list[dict]:
    tasks_path = _role_path() / "tasks" / "create.yaml"
    return yaml.safe_load(tasks_path.read_text(encoding="utf-8"))


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    flattened = []
    for task in tasks:
        flattened.append(task)
        flattened.extend(_flatten_tasks(task.get("block", [])))
        flattened.extend(_flatten_tasks(task.get("always", [])))
    return flattened


def _create_tasks() -> list[dict]:
    return _flatten_tasks(_raw_create_tasks())


# ===================================================================
# FRRConfiguration template tests
# ===================================================================


def test_frr_configuration_api_version_and_kind() -> None:
    frr = _render_frr_configuration()
    assert frr["apiVersion"] == "frrk8s.metallb.io/v1beta1"
    assert frr["kind"] == "FRRConfiguration"


def test_frr_configuration_naming_and_labels() -> None:
    frr = _render_frr_configuration()
    assert frr["metadata"]["name"] == "fabric-bgp-worker-0"
    assert frr["metadata"]["namespace"] == "metallb-system"
    assert frr["metadata"]["labels"]["osac.openshift.io/managed-by"] == "fabric-bgp-config"
    assert frr["metadata"]["labels"]["osac.openshift.io/worker"] == "worker-0"


def test_frr_configuration_node_selector() -> None:
    frr = _render_frr_configuration()
    selector = frr["spec"]["nodeSelector"]["matchLabels"]
    assert selector["kubernetes.io/hostname"] == "worker-0"


def test_frr_configuration_router_asn_and_prefixes() -> None:
    frr = _render_frr_configuration()
    router = frr["spec"]["bgp"]["routers"][0]
    assert router["asn"] == 64512
    assert router["prefixes"] == ["10.1.0.1/32"]


def test_frr_configuration_neighbor_basics() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["address"] == "10.1.0.2"
    assert neighbor["asn"] == 65000
    assert neighbor["sourceaddress"] == "10.1.0.1"
    assert neighbor["ebgpMultiHop"] is True
    assert neighbor["enableGracefulRestart"] is True


def test_frr_configuration_address_families_unicast_and_evpn() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["addressFamilies"] == ["unicast", "evpn"]


def test_frr_configuration_no_ipv6_address_family() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    for family in neighbor["addressFamilies"]:
        assert "ipv6" not in family.lower()


def test_frr_configuration_allowas_in_origin() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["allowAsIn"] == "origin"


def test_frr_configuration_outbound_exactly_local_vtep_32() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    advertise = neighbor["toAdvertise"]["allowed"]
    assert advertise["mode"] == "filtered"
    assert advertise["prefixes"] == ["10.1.0.1/32"]


def test_frr_configuration_inbound_only_32_within_approved_ranges() -> None:
    frr = _render_frr_configuration()
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    receive = neighbor["toReceive"]["allowed"]
    assert receive["mode"] == "filtered"
    assert len(receive["prefixes"]) == 1
    prefix_sel = receive["prefixes"][0]
    assert prefix_sel["prefix"] == "10.2.0.0/16"
    assert prefix_sel["ge"] == 32
    assert prefix_sel["le"] == 32


def test_frr_configuration_multiple_remote_vtep_ranges() -> None:
    frr = _render_frr_configuration(
        {"worker_remote_vtep_ranges": ["10.2.0.0/16", "10.200.255.0/30"]}
    )
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    receive = neighbor["toReceive"]["allowed"]
    assert len(receive["prefixes"]) == 2
    for entry in receive["prefixes"]:
        assert entry["ge"] == 32
        assert entry["le"] == 32
    assert receive["prefixes"][0]["prefix"] == "10.2.0.0/16"
    assert receive["prefixes"][1]["prefix"] == "10.200.255.0/30"


def test_frr_configuration_no_raw_config() -> None:
    frr = _render_frr_configuration()
    assert "raw" not in frr.get("spec", {})


def test_frr_configuration_no_advertise_vnis() -> None:
    """advertiseVNIs must not appear — this workflow creates no VNIs."""
    frr = _render_frr_configuration()
    router = frr["spec"]["bgp"]["routers"][0]
    assert "evpn" not in router


def test_frr_configuration_no_vlan_no_macvni_no_ipvni() -> None:
    frr = _render_frr_configuration()
    rendered = yaml.dump(frr)
    assert "vlan" not in rendered.lower()
    assert "macvni" not in rendered.lower()
    assert "ipvni" not in rendered.lower()


def test_frr_configuration_ebgp_multihop_disabled() -> None:
    frr = _render_frr_configuration({"worker_ebgp_multihop": False})
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["ebgpMultiHop"] is False


def test_frr_configuration_graceful_restart_disabled() -> None:
    frr = _render_frr_configuration({"worker_graceful_restart": False})
    neighbor = frr["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["enableGracefulRestart"] is False


# ===================================================================
# NNCP template tests
# ===================================================================


def test_nncp_api_version_and_kind() -> None:
    nncp = _render_nncp()
    assert nncp["apiVersion"] == "nmstate.io/v1"
    assert nncp["kind"] == "NodeNetworkConfigurationPolicy"


def test_nncp_naming_and_labels() -> None:
    nncp = _render_nncp()
    assert nncp["metadata"]["name"] == "fabric-bgp-worker-0"
    assert nncp["metadata"]["labels"]["osac.openshift.io/managed-by"] == "fabric-bgp-config"


def test_nncp_dedicated_ethernet_no_bridge_no_vlan() -> None:
    nncp = _render_nncp()
    iface = nncp["spec"]["desiredState"]["interfaces"][0]
    assert iface["name"] == "ens5f0"
    assert iface["type"] == "ethernet"
    assert iface["state"] == "up"
    rendered = yaml.dump(nncp)
    assert "bridge" not in rendered.lower()
    assert "vlan" not in rendered.lower()
    assert "br-ex" not in rendered


def test_nncp_ipv4_static_address() -> None:
    nncp = _render_nncp()
    ipv4 = nncp["spec"]["desiredState"]["interfaces"][0]["ipv4"]
    assert ipv4["enabled"] is True
    assert ipv4["dhcp"] is False
    assert ipv4["address"][0]["ip"] == "10.1.0.1"
    assert ipv4["address"][0]["prefix-length"] == 30


def test_nncp_ipv6_disabled() -> None:
    nncp = _render_nncp()
    ipv6 = nncp["spec"]["desiredState"]["interfaces"][0]["ipv6"]
    assert ipv6["enabled"] is False


def test_nncp_node_selector() -> None:
    nncp = _render_nncp()
    selector = nncp["spec"]["nodeSelector"]
    assert selector["kubernetes.io/hostname"] == "worker-0"


# ===================================================================
# Task ordering and validation tests
# ===================================================================


def test_input_validation_precedes_kubernetes_writes() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    validation_index = task_names.index(
        "Validate fabric BGP worker list is nonempty"
    )
    k8s_write_indices = [
        i for i, task in enumerate(tasks) if "kubernetes.core.k8s" in task
    ]
    assert k8s_write_indices
    assert validation_index < min(k8s_write_indices)


def test_each_worker_entry_is_validated() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    assert "Validate each worker entry" in task_names
    validation = next(
        task
        for task in tasks
        if task.get("name") == "Validate each worker entry"
    )
    conditions = validation["ansible.builtin.assert"]["that"]
    assert any("nodeName" in c for c in conditions)
    assert any("interface" in c for c in conditions)
    assert any("localAddress" in c for c in conditions)
    assert any("peerAddress" in c for c in conditions)
    assert any("localASN" in c for c in conditions)
    assert any("peerASN" in c for c in conditions)
    assert any("localVTEP" in c for c in conditions)
    assert any("remoteVTEPRanges" in c for c in conditions)
    assert "No Kubernetes resources were changed" in validation[
        "ansible.builtin.assert"
    ]["fail_msg"]


def test_remote_vtep_ranges_validated_as_cidrs() -> None:
    tasks = _create_tasks()
    task = next(
        task
        for task in tasks
        if task.get("name") == "Validate remote VTEP ranges are valid CIDRs"
    )
    assert "No Kubernetes resources were changed" in task[
        "ansible.builtin.assert"
    ]["fail_msg"]


def test_frr_crd_capability_check_before_apply() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    crd_check_index = task_names.index(
        "Check FRRConfiguration CRD availability"
    )
    crd_validate_index = task_names.index(
        "Validate FRRConfiguration CRD is installed"
    )
    apply_frr_index = task_names.index(
        "Apply FRRConfiguration for each worker"
    )
    assert crd_check_index < crd_validate_index < apply_frr_index

    validate = tasks[crd_validate_index]["ansible.builtin.assert"]
    assert "No Kubernetes resources were changed" in validate["fail_msg"]


def test_nncp_crd_capability_check_before_apply() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    crd_check_index = task_names.index(
        "Check NodeNetworkConfigurationPolicy CRD availability"
    )
    apply_nncp_index = task_names.index(
        "Apply NMState policy for each worker"
    )
    assert crd_check_index < apply_nncp_index


def test_nncp_applied_before_frr_configuration() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    nncp_index = task_names.index("Apply NMState policy for each worker")
    frr_index = task_names.index("Apply FRRConfiguration for each worker")
    assert nncp_index < frr_index


def test_delete_tasks_exist() -> None:
    delete_path = _role_path() / "tasks" / "delete.yaml"
    tasks = yaml.safe_load(delete_path.read_text(encoding="utf-8"))
    task_names = [task.get("name") for task in tasks]
    assert "Delete FRRConfiguration for each worker" in task_names
    assert "Delete NMState policy for each worker" in task_names

    frr_delete = next(
        task
        for task in tasks
        if task.get("name") == "Delete FRRConfiguration for each worker"
    )
    assert frr_delete["kubernetes.core.k8s"]["state"] == "absent"

    nncp_delete = next(
        task
        for task in tasks
        if task.get("name") == "Delete NMState policy for each worker"
    )
    assert nncp_delete["kubernetes.core.k8s"]["state"] == "absent"


def test_argument_specs_define_required_parameters() -> None:
    argspec_path = _role_path() / "meta" / "argument_specs.yaml"
    argspec = yaml.safe_load(argspec_path.read_text(encoding="utf-8"))

    create_opts = argspec["argument_specs"]["create"]["options"]
    assert create_opts["fabric_bgp_workers"]["required"] is True
    assert create_opts["fabric_bgp_workers"]["type"] == "list"

    delete_opts = argspec["argument_specs"]["delete"]["options"]
    assert delete_opts["fabric_bgp_workers"]["required"] is True
