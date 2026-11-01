"""Unit tests of the cudn_evpn Kubernetes Manager's pure logic.

The task inputs follow the operator (osac-operator/internal/networktask
input.go and the networking controllers) and the Fabric Manager guide's
Appendix A, with the network segment as the launch key network_segment.
"""

from __future__ import annotations

import copy
import importlib.util
import ipaddress
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/cudn_evpn"
FILTER = ROOT / "collections/ansible_collections/osac/templates/plugins/filter/cudn_evpn.py"

_spec = importlib.util.spec_from_file_location("cudn_evpn_filter", FILTER)
assert _spec and _spec.loader
ce = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ce)

TENANT = "5d6a0f1e-2b3c-4d5e-8f90-a1b2c3d4e5f6"
VN = "3f1c9a2e-7b4d-4e8a-9c6f-0d2e4b6a8c10"
SUBNET = "8a2b4c6d-1e3f-4a5b-9c7d-2e4f6a8b0c12"
SUBNET2 = "1b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d"
SG = "7b6a5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d"
SG2 = "6a5b4c3d-2e1f-4a0b-9c8d-7e6f5a4b3c2e"
VM = "9c8b7a6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
VM2 = "0f1e2d3c-4b5a-4968-8776-655443322110"
MANAGER = {"name": "cudn_evpn", "type": "kubernetes", "ansible_role": "osac.templates.cudn_evpn"}
IPAM = {
    "gateway_ipv4": "10.0.1.1",
    "overlay_ipv4_cidrs": ["10.0.1.0/25", "10.0.1.128/26"],
    "physical_ipv4_cidrs": ["10.0.1.192/26"],
    "lb_vip_ipv4_cidrs": [],
}
SEGMENT = {"type": "evpn", "l2_vni": 10100}


def snapshot(kind: str, api_id: str, spec: dict | None = None, name: str | None = None) -> dict:
    return {
        "apiVersion": "osac.openshift.io/v1alpha1",
        "kind": kind,
        "id": api_id,
        "metadata": {"name": name or f"{kind.lower()}-{api_id[:8]}", "namespace": "osac-networking",
                     "uid": "0e9d8c7b-6a5f-4e3d-8c2b-1a0f9e8d7c6b", "generation": 1},
        "spec": spec if spec is not None else {},
    }


VN_SNAP = snapshot("VirtualNetwork", VN, {"ipv4Cidr": "10.0.0.0/16"}, "vn-a")
VN_SNAP["status"] = {"l3Vni": 20001}


def job(task: str, **overrides) -> dict:
    """The osac_job_vars of one Kubernetes task, as the operator builds them."""
    base: dict = {"manager": MANAGER, "task": task, "tenant": TENANT}
    if task.endswith("virtual_network"):
        base["resource"] = VN_SNAP
    elif task.endswith("subnet"):
        base.update(resource=snapshot("Subnet", SUBNET, {"ipv4Cidr": "10.0.1.0/24"}, "subnet-a"),
                    virtual_network=VN_SNAP, ipam=IPAM, network_segment=SEGMENT)
    elif task.endswith("security_group"):
        base.update(resource=snapshot("SecurityGroup", SG, {
            "ingressRules": [
                {"protocol": "tcp", "portFrom": 22, "portTo": 22, "sourceCidr": "0.0.0.0/0"},
                {"protocol": "udp", "portFrom": 5000, "portTo": 5100, "sourceCidr": "10.0.1.0/24"},
                {"protocol": "all", "sourceCidr": "10.0.0.0/16"},
            ],
            "egressRules": [{"protocol": "all", "destinationCidr": "0.0.0.0/0"}],
        }, "web"), virtual_network=VN_SNAP)
    else:
        base.update(
            resource=snapshot("NetworkAttachment", VM, {"target": {"kind": "ComputeInstance", "id": VM}, "subnet": SUBNET}),
            target={"kind": "ComputeInstance", "id": VM},
            network_attachment={"subnet": SUBNET, "interface": {"type": "overlay"}, "security_group_ids": [SG]},
            virtual_network=VN_SNAP,
            subnet=snapshot("Subnet", SUBNET, {"ipv4Cidr": "10.0.1.0/24"}, "subnet-a"),
            ipam=IPAM,
            network_segment=SEGMENT,
        )
    base.update(overrides)
    return copy.deepcopy(base)


def plan(task: str, **overrides) -> dict:
    result = ce.cudn_evpn_plan(job(task, **overrides))
    assert result["error"] is None, result["error"]
    return result


def error(task: str, **overrides) -> dict:
    result = ce.cudn_evpn_plan(job(task, **overrides))
    assert result["error"] is not None
    return result["error"]


# ---------------------------------------------------------------------------
# Role files and dispatch
# ---------------------------------------------------------------------------


def test_role_has_exactly_the_eight_kubernetes_task_files() -> None:
    files = {p.stem for p in (ROLE / "tasks").glob("*.yaml") if not p.stem.startswith("_")}
    assert files == set(ce.TASKS)
    assert len(ce.TASKS) == 8


def test_every_entry_point_takes_only_osac_job_vars() -> None:
    specs = yaml.safe_load((ROLE / "meta/argument_specs.yaml").read_text())["argument_specs"]
    assert set(specs) == set(ce.TASKS)
    for spec in specs.values():
        assert spec["options"] == {
            "osac_job_vars": {"type": "dict", "required": True, "description": "Network manager contract task input"}
        }


def test_role_metadata_registers_the_ipv4_k8s_manager() -> None:
    meta = yaml.safe_load((ROLE / "meta/osac.yaml").read_text())
    assert meta["template_type"] == "network"
    assert meta["k8s_manager"] == "cudn_evpn"
    assert meta["capabilities"] == {"supports_ipv4": True, "supports_ipv6": False, "supports_dual_stack": False}


def _tasks(path: Path) -> list:
    """Every task of a task file, nested blocks included."""
    out = []
    for task in yaml.safe_load(path.read_text()) or []:
        out.append(task)
        for key in ("block", "rescue", "always"):
            out.extend(_tasks_of(task.get(key) or []))
    return out


def _tasks_of(tasks: list) -> list:
    out = []
    for task in tasks:
        out.append(task)
        for key in ("block", "rescue", "always"):
            out.extend(_tasks_of(task.get(key) or []))
    return out


@pytest.mark.parametrize("task", ce.TASKS)
def test_every_task_rescues_into_osac_error(task: str) -> None:
    top = yaml.safe_load((ROLE / f"tasks/{task}.yaml").read_text())
    assert len(top) == 1 and "block" in top[0]
    assert top[0]["rescue"] == [
        {"name": "Report the failure to OSAC", "ansible.builtin.include_tasks": "_fail.yaml"}
    ]


@pytest.mark.parametrize("task", ce.TASKS)
def test_only_apply_and_subnet_create_publish_results(task: str) -> None:
    stats = [t for t in _tasks(ROLE / f"tasks/{task}.yaml") if "ansible.builtin.set_stats" in t]
    if task == "apply_network_attachment":
        assert len(stats) == 1
        assert stats[0]["ansible.builtin.set_stats"]["data"] == {"osac_result": "{{ cudn_evpn.result }}"}
    elif task == "create_subnet":
        assert len(stats) == 1
        assert set(stats[0]["ansible.builtin.set_stats"]["data"]["osac_result"]) == {
            "compute_instance_supported", "compute_instance_unsupported_reason",
        }
    else:
        assert stats == []


def test_role_takes_no_locks_and_reads_no_osac_crs() -> None:
    text = "\n".join(p.read_text() for p in (ROLE / "tasks").glob("*.yaml"))
    for forbidden in ("Lease", "vnet_lock", "osac.openshift.io/v1alpha1", "remote_cluster_kubeconfig",
                      "VirtualMachine"):
        assert forbidden not in text, forbidden


def test_errors_read_only_the_api_status_and_reason_of_a_failure() -> None:
    import re

    text = "\n".join(
        line for p in (ROLE / "tasks").glob("*.yaml") for line in p.read_text().splitlines()
        if not line.lstrip().startswith("#")
    )
    used = set(re.findall(r"ansible_failed_result(\.\w+)?", text))
    assert used == {".status", ".reason"}


def test_failure_handler_publishes_exactly_message_and_retryable() -> None:
    tasks = yaml.safe_load((ROLE / "tasks/_fail.yaml").read_text())
    report = tasks[0]["ansible.builtin.set_fact"]["_cudn_evpn_report"]
    assert set(report) == {"message", "retryable"}
    assert tasks[1]["ansible.builtin.set_stats"] == {
        "aggregate": False, "per_host": False, "data": {"osac_error": "{{ _cudn_evpn_report }}"}
    }
    assert "ansible.builtin.fail" in tasks[2]


# ---------------------------------------------------------------------------
# Namespace and baseline
# ---------------------------------------------------------------------------


def test_namespace_has_the_primary_network_and_owner_labels_at_creation() -> None:
    ns = plan("create_virtual_network")["namespace_manifest"]
    assert ns["metadata"]["name"] == f"osac-vn-{VN}"
    assert ns["metadata"]["labels"] == {
        "k8s.ovn.org/primary-user-defined-network": "",
        "osac.openshift.io/tenant": TENANT,
        "osac.openshift.io/virtualnetwork-id": VN,
        "osac.openshift.io/owner-kind": "virtualnetwork",
        "osac.openshift.io/owner-id": VN,
        "osac.openshift.io/k8s-manager": "cudn_evpn",
    }
    assert ns["metadata"]["annotations"] == {
        "osac.openshift.io/tenant": TENANT,
        "osac.openshift.io/owner-reference": "VirtualNetwork/vn-a",
    }


def test_baseline_denies_both_ways_except_dhcp() -> None:
    baseline = plan("create_virtual_network")["baseline_manifest"]
    assert baseline["metadata"]["namespace"] == f"osac-vn-{VN}"
    assert baseline["spec"] == {
        "podSelector": {},
        "policyTypes": ["Ingress", "Egress"],
        "ingress": [{"ports": [{"protocol": "UDP", "port": 68}]}],
        "egress": [{"ports": [{"protocol": "UDP", "port": 67}]}],
    }


def test_virtual_network_tasks_need_no_vni() -> None:
    p = plan("create_virtual_network")
    assert "cudn_manifest" not in p
    assert plan("delete_virtual_network")["namespace"] == f"osac-vn-{VN}"


# ---------------------------------------------------------------------------
# Hub network
# ---------------------------------------------------------------------------


def test_cudn_is_an_evpn_layer2_primary_network_keyed_by_the_virtual_network() -> None:
    cudn = plan("create_subnet")["cudn_manifest"]
    assert cudn["apiVersion"] == "k8s.ovn.org/v1"
    assert cudn["metadata"]["name"] == f"osac-vn-{VN}"
    assert cudn["metadata"]["labels"]["evpn"] == "true"
    assert cudn["metadata"]["labels"]["osac.openshift.io/owner-kind"] == "subnet"
    assert cudn["metadata"]["labels"]["osac.openshift.io/owner-id"] == SUBNET
    assert cudn["metadata"]["annotations"]["osac.openshift.io/owner-subnet"] == SUBNET
    assert cudn["spec"]["namespaceSelector"] == {
        "matchLabels": {"osac.openshift.io/virtualnetwork-id": VN, "osac.openshift.io/tenant": TENANT}
    }
    assert cudn["spec"]["network"] == {
        "topology": "Layer2",
        "transport": "EVPN",
        "layer2": {
            "role": "Primary",
            "mtu": 1500,
            "subnets": ["10.0.1.0/24"],
            "defaultGatewayIPs": ["10.0.1.1"],
            "infrastructureSubnets": ["10.0.1.1/32", "10.0.1.2/32"],
            "reservedSubnets": ["10.0.1.192/26"],
            "ipam": {"mode": "Enabled", "lifecycle": "Persistent"},
        },
        "evpn": {
            "vtep": "tenant-vtep",
            "macVRF": {"vni": 10100, "routeTarget": "0:10100"},
            "ipVRF": {"vni": 20001, "routeTarget": "0:20001"},
        },
    }


def test_namespace_selector_matches_the_namespace_labels() -> None:
    selector = plan("create_subnet")["cudn_manifest"]["spec"]["namespaceSelector"]["matchLabels"]
    labels = plan("create_virtual_network")["namespace_manifest"]["metadata"]["labels"]
    assert all(labels[k] == v for k, v in selector.items())


@pytest.mark.parametrize(
    ("subnet", "ipam", "reserved"),
    [
        # plan 0.5 / 0.25 / 0.25
        ("10.0.1.0/24", {"gateway_ipv4": "10.0.1.1", "overlay_ipv4_cidrs": ["10.0.1.0/25"],
                         "physical_ipv4_cidrs": ["10.0.1.128/26"], "lb_vip_ipv4_cidrs": ["10.0.1.192/26"]},
         ["10.0.1.128/25"]),
        # plan 0.75 / 0.25 / 0 (VMaaS only)
        ("10.0.1.0/24", IPAM, ["10.0.1.192/26"]),
        # plan 1 / 0 / 0: nothing outside the overlay
        ("10.0.1.0/24", {"gateway_ipv4": "10.0.1.1", "overlay_ipv4_cidrs": ["10.0.1.0/24"],
                         "physical_ipv4_cidrs": [], "lb_vip_ipv4_cidrs": []}, []),
        # plan 0.5 / 0.5 / 0 on a /20
        ("172.16.0.0/20", {"gateway_ipv4": "172.16.0.1", "overlay_ipv4_cidrs": ["172.16.0.0/21"],
                           "physical_ipv4_cidrs": ["172.16.8.0/21"], "lb_vip_ipv4_cidrs": []},
         ["172.16.8.0/21"]),
    ],
)
def test_reserved_subnets_are_the_subnet_minus_the_overlay(subnet: str, ipam: dict, reserved: list) -> None:
    layer2 = ce.layer2_addressing(subnet, ipam)
    assert layer2["reservedSubnets"] == reserved
    gateway = ipaddress.ip_address(ipam["gateway_ipv4"])
    management = ipaddress.ip_network(subnet).network_address + 2
    assert layer2["infrastructureSubnets"] == [f"{gateway}/32", f"{management}/32"]
    assert layer2["defaultGatewayIPs"] == [ipam["gateway_ipv4"]]


def test_infrastructure_addresses_are_inside_the_overlay_range() -> None:
    layer2 = ce.layer2_addressing("10.0.1.0/24", IPAM)
    overlay = [ipaddress.ip_network(c) for c in IPAM["overlay_ipv4_cidrs"]]
    for infra in layer2["infrastructureSubnets"]:
        net = ipaddress.ip_network(infra)
        assert any(net.subnet_of(o) for o in overlay)
    assert layer2["defaultGatewayIPs"] == ["10.0.1.1"]
    assert layer2["reservedSubnets"] == ["10.0.1.192/26"]


@pytest.mark.parametrize("missing", ["ingressRules", "egressRules"])
def test_an_omitted_direction_means_no_rules(missing: str) -> None:
    spec = {"ingressRules": [{"protocol": "all", "sourceCidr": "10.0.0.0/8"}],
            "egressRules": [{"protocol": "all", "destinationCidr": "0.0.0.0/0"}]}
    del spec[missing]
    rules = ce.policy_rules(spec)
    field = "ingress" if missing == "ingressRules" else "egress"
    assert rules[field] == []


def test_a_direction_that_is_not_a_list_is_still_rejected() -> None:
    with pytest.raises(ce._InputError):
        ce.policy_rules({"ingressRules": {}, "egressRules": []})


def test_reserved_and_infrastructure_never_overlap_and_cover_the_subnet() -> None:
    # A gateway outside the overlay range is cut out of reservedSubnets.
    ipam = {"gateway_ipv4": "10.0.1.129", "overlay_ipv4_cidrs": ["10.0.1.0/25"]}
    layer2 = ce.layer2_addressing("10.0.1.0/24", ipam)
    reserved = [ipaddress.ip_network(n) for n in layer2["reservedSubnets"]]
    infra = [ipaddress.ip_network(n) for n in layer2["infrastructureSubnets"]]
    overlay = [ipaddress.ip_network(n) for n in ipam["overlay_ipv4_cidrs"]]
    assert not any(r.overlaps(i) for r in reserved for i in infra)
    # network + 2 is inside the overlay range, so only the gateway adds addresses.
    outside = [i for i in infra if not any(i.subnet_of(o) for o in overlay)]
    total = sum(n.num_addresses for n in reserved + outside + overlay)
    assert total == 256


def test_cudn_without_reserved_subnets_omits_the_field() -> None:
    ipam = {"gateway_ipv4": "10.0.1.1", "overlay_ipv4_cidrs": ["10.0.1.0/24"],
            "physical_ipv4_cidrs": [], "lb_vip_ipv4_cidrs": []}
    layer2 = plan("create_subnet", ipam=ipam)["cudn_manifest"]["spec"]["network"]["layer2"]
    assert "reservedSubnets" not in layer2


def test_more_than_25_reserved_subnets_is_a_final_implementation_limit() -> None:
    # 32 overlay /28 islands in a /22 leave 32 separate gaps to reserve.
    overlay = [f"10.0.{i // 16}.{(i % 16) * 16}/28" for i in range(0, 64, 2)]
    ipam = {"gateway_ipv4": "10.0.0.1", "overlay_ipv4_cidrs": overlay,
            "physical_ipv4_cidrs": [], "lb_vip_ipv4_cidrs": []}
    resource = snapshot("Subnet", SUBNET, {"ipv4Cidr": "10.0.0.0/22"})
    err = error("create_subnet", ipam=ipam, resource=resource)
    assert err["retryable"] is False
    assert "implementation limit" in err["message"] and "25" in err["message"]


def test_l2_vni_comes_from_the_segment_and_l3_vni_from_the_virtual_network() -> None:
    assert ce.segment_vnis({"network_segment": SEGMENT, "virtual_network": VN_SNAP}) == (10100, 20001)


@pytest.mark.parametrize(
    "segment",
    [
        None,
        {"type": "vlan", "l2_vni": 10100},
        {"type": "evpn", "l2_vni": "10100"},
        {"type": "evpn", "l2_vni": True},
        {"type": "evpn", "l2_vni": 0},
        {"type": "evpn", "l2_vni": 16777216},
        {"type": "evpn", "l2_vni": 10100, "l3_vni": 20001},
        {"type": "evpn", "l2_vni": 10100, "extra": 1},
    ],
)
def test_malformed_network_segment_is_a_final_input_error(segment) -> None:
    err = error("create_subnet", network_segment=segment)
    assert err["retryable"] is False
    assert "invalid task input" in err["message"]


@pytest.mark.parametrize(
    "status",
    [None, {}, {"l3Vni": "20001"}, {"l3Vni": 0}, {"l3Vni": 16777216}, {"l3Vni": 10100}],
)
def test_a_missing_or_malformed_l3_vni_is_a_final_input_error(status) -> None:
    vn = dict(VN_SNAP)
    if status is None:
        vn.pop("status")
    else:
        vn["status"] = status
    err = error("create_subnet", virtual_network=vn)
    assert err["retryable"] is False
    assert "invalid task input" in err["message"]


def test_large_vnis_render_without_a_hub_as_rule() -> None:
    cudn = plan(
        "create_subnet",
        network_segment={"type": "evpn", "l2_vni": 16000000},
        virtual_network={**VN_SNAP, "status": {"l3Vni": 16777215}},
    )["cudn_manifest"]
    evpn = cudn["spec"]["network"]["evpn"]
    assert evpn["macVRF"]["routeTarget"] == "0:16000000"
    assert evpn["ipVRF"]["routeTarget"] == "0:16777215"


@pytest.mark.parametrize(
    "ipam",
    [
        {**IPAM, "gateway_ipv4": "10.0.2.1"},
        {**IPAM, "overlay_ipv4_cidrs": []},
        {**IPAM, "overlay_ipv4_cidrs": ["10.0.1.0/23"]},
        {**IPAM, "overlay_ipv4_cidrs": ["10.0.1.1/25"]},
        {k: v for k, v in IPAM.items() if k != "physical_ipv4_cidrs"},
    ],
)
def test_malformed_ipam_is_a_final_input_error(ipam: dict) -> None:
    assert error("create_subnet", ipam=ipam)["retryable"] is False


def test_delete_subnet_needs_no_layout_or_segment() -> None:
    p = plan("delete_subnet", ipam=None, network_segment=None)
    assert p["cudn_name"] == f"osac-vn-{VN}" and p["subnet_id"] == SUBNET


# ---------------------------------------------------------------------------
# Owner claim
# ---------------------------------------------------------------------------


def live_cudn(owner: str = SUBNET, **changes) -> dict:
    cudn = copy.deepcopy(plan("create_subnet")["cudn_manifest"])
    cudn["metadata"]["annotations"]["osac.openshift.io/owner-subnet"] = owner
    cudn["metadata"].update(uid="u1", resourceVersion="7")
    for key, value in changes.items():
        cudn["metadata"][key] = value
    return cudn


def test_the_owner_subnet_reconciles_and_sees_no_drift() -> None:
    result = ce.cudn_evpn_cudn_assess(live_cudn(), plan("create_subnet"))
    assert result == {"state": "owner", "owner": SUBNET, "drift": [], "metadata_patch": None, "selector_patch": None}


def test_another_subnet_leaves_the_network_unchanged() -> None:
    result = ce.cudn_evpn_cudn_assess(live_cudn(owner=SUBNET2), plan("create_subnet"))
    assert result["state"] == "other" and result["owner"] == SUBNET2
    assert result["metadata_patch"] is None


def test_a_terminating_network_holds_the_claim() -> None:
    cudn = live_cudn(owner=SUBNET2, deletionTimestamp="2026-10-29T08:00:00Z")
    assert ce.cudn_evpn_cudn_assess(cudn, plan("create_subnet"))["state"] == "terminating"


def test_a_network_of_another_virtual_network_or_tenant_is_foreign() -> None:
    cudn = live_cudn()
    cudn["metadata"]["labels"]["osac.openshift.io/tenant"] = "someone-else"
    assert ce.cudn_evpn_cudn_assess(cudn, plan("create_subnet"))["state"] == "foreign"


def test_owner_drift_names_the_network_field() -> None:
    cudn = live_cudn()
    cudn["spec"]["network"]["evpn"]["macVRF"]["vni"] = 999
    result = ce.cudn_evpn_cudn_assess(cudn, plan("create_subnet"))
    assert result["drift"] == ["spec.network.evpn.macVRF.vni"]


def test_owner_restores_metadata_and_selector() -> None:
    cudn = live_cudn()
    del cudn["metadata"]["labels"]["evpn"]
    cudn["spec"]["namespaceSelector"] = {"matchLabels": {"foo": "bar"}}
    result = ce.cudn_evpn_cudn_assess(cudn, plan("create_subnet"))
    assert result["metadata_patch"] == {"metadata": {"labels": plan("create_subnet")["cudn_manifest"]["metadata"]["labels"]}}
    # The selector is replaced, not merged, so a stray label cannot stay in it.
    assert result["selector_patch"] == [
        {"op": "test", "path": "/metadata/uid", "value": "u1"},
        {"op": "test", "path": "/metadata/resourceVersion", "value": "7"},
        {"op": "replace", "path": "/spec/namespaceSelector",
         "value": {"matchLabels": {"osac.openshift.io/virtualnetwork-id": VN, "osac.openshift.io/tenant": TENANT}}},
    ]


def test_cudn_ready_needs_all_three_ovn_conditions() -> None:
    conditions = [{"type": t, "status": "True", "reason": "ok"} for t in ce.CUDN_CONDITIONS]
    assert ce.cudn_evpn_cudn_ready({"status": {"conditions": conditions}})["ready"] is True
    partial = conditions[:2] + [{"type": "NetworkAllocationSucceeded", "status": "False", "reason": "Busy"}]
    result = ce.cudn_evpn_cudn_ready({"status": {"conditions": partial}})
    assert result == {"ready": False, "pending": ["NetworkAllocationSucceeded=Busy"]}
    assert ce.cudn_evpn_cudn_ready({})["ready"] is False
    ready_only = [{"type": "Ready", "status": "True"}]
    assert ce.cudn_evpn_cudn_ready({"status": {"conditions": ready_only}})["ready"] is False


@pytest.mark.parametrize(
    ("info", "ready", "text"),
    [
        ({"api_found": False, "resources": []}, False, "not installed"),
        ({"resources": []}, False, "does not exist"),
        ({"resources": [{"status": {"conditions": [{"type": "Accepted", "status": "False", "reason": "Pending"}]}}]}, False, "Pending"),
        ({"resources": [{"status": {"conditions": [{"type": "Accepted", "status": "True", "reason": "Allocated"}]}}]}, True, ""),
    ],
)
def test_vtep_prerequisite(info: dict, ready: bool, text: str) -> None:
    state = ce.cudn_evpn_vtep_state(info)
    assert state["ready"] is ready and text in state["message"]


# ---------------------------------------------------------------------------
# Settings
# ---------------------------------------------------------------------------


# ---------------------------------------------------------------------------
# SecurityGroup policies
# ---------------------------------------------------------------------------


def test_rules_translate_to_network_policy() -> None:
    p = plan("create_security_group")
    policy = p["policy_manifest"]
    assert policy["metadata"]["name"] == f"osac-sg-{SG}"
    assert policy["metadata"]["namespace"] == f"osac-vn-{VN}"
    assert policy["metadata"]["labels"]["osac.openshift.io/owner-kind"] == "securitygroup"
    assert policy["metadata"]["labels"]["osac.openshift.io/owner-id"] == SG
    assert policy["spec"]["policyTypes"] == ["Ingress", "Egress"]
    assert policy["spec"]["ingress"] == [
        {"from": [{"ipBlock": {"cidr": "0.0.0.0/0"}}], "ports": [{"protocol": "TCP", "port": 22}]},
        {"from": [{"ipBlock": {"cidr": "10.0.1.0/24"}}], "ports": [{"protocol": "UDP", "port": 5000, "endPort": 5100}]},
        {"from": [{"ipBlock": {"cidr": "10.0.0.0/16"}}]},
    ]
    assert policy["spec"]["egress"] == [{"to": [{"ipBlock": {"cidr": "0.0.0.0/0"}}]}]


def test_an_unbound_group_selects_no_pod() -> None:
    selector = plan("create_security_group")["policy_manifest"]["spec"]["podSelector"]
    assert selector == {"matchExpressions": [
        {"key": "osac.openshift.io/compute-instance-id", "operator": "Exists"},
        {"key": "osac.openshift.io/compute-instance-id", "operator": "DoesNotExist"},
    ]}
    assert ce.bound_ids({"spec": {"podSelector": selector}}) == []


def test_empty_directions_allow_nothing_but_keep_both_policy_types() -> None:
    sg = snapshot("SecurityGroup", SG, {"ingressRules": [], "egressRules": []})
    policy = plan("create_security_group", resource=sg)["policy_manifest"]
    assert policy["spec"]["ingress"] == [] and policy["spec"]["egress"] == []
    assert policy["spec"]["policyTypes"] == ["Ingress", "Egress"]


@pytest.mark.parametrize("direction", ["ingressRules", "egressRules"])
def test_icmp_is_a_final_implementation_limit(direction: str) -> None:
    peer = "sourceCidr" if direction == "ingressRules" else "destinationCidr"
    spec = {"ingressRules": [], "egressRules": [], direction: [{"protocol": "icmp", peer: "0.0.0.0/0"}]}
    err = error("create_security_group", resource=snapshot("SecurityGroup", SG, spec))
    assert err["retryable"] is False
    assert "icmp" in err["message"] and "NetworkPolicy cannot match ICMP" in err["message"]


@pytest.mark.parametrize(
    "rule",
    [
        {"protocol": "tcp", "sourceCidr": "0.0.0.0/0"},
        {"protocol": "tcp", "portFrom": 80, "portTo": 22, "sourceCidr": "0.0.0.0/0"},
        {"protocol": "all", "portFrom": 1, "portTo": 2, "sourceCidr": "0.0.0.0/0"},
        {"protocol": "sctp", "sourceCidr": "0.0.0.0/0"},
        {"protocol": "tcp", "portFrom": 22, "portTo": 22, "sourceCidr": "10.0.1.1/24"},
        {"protocol": "tcp", "portFrom": 22, "portTo": 22, "destinationCidr": "0.0.0.0/0"},
    ],
)
def test_malformed_rules_are_final_input_errors(rule: dict) -> None:
    sg = snapshot("SecurityGroup", SG, {"ingressRules": [rule], "egressRules": []})
    assert error("create_security_group", resource=sg)["retryable"] is False


def policy(bound: list, ingress: list | None = None) -> dict:
    return {
        "metadata": {"name": f"osac-sg-{SG}", "namespace": f"osac-vn-{VN}", "uid": "pol-uid", "resourceVersion": "42"},
        "spec": {
            "podSelector": ce.pod_selector(bound),
            "policyTypes": ["Ingress", "Egress"],
            "ingress": ingress if ingress is not None else [{"from": [{"ipBlock": {"cidr": "0.0.0.0/0"}}]}],
            "egress": [],
        },
    }


GUARD = [
    {"op": "test", "path": "/metadata/uid", "value": "pol-uid"},
    {"op": "test", "path": "/metadata/resourceVersion", "value": "42"},
]


def test_a_rule_update_replaces_rules_and_keeps_membership() -> None:
    rules = plan("create_security_group")["rules"]
    result = ce.cudn_evpn_rules_patch(policy([VM, VM2]), rules)
    assert result["changed"] is True
    assert result["patch"][:2] == GUARD
    assert [op["path"] for op in result["patch"][2:]] == ["/spec/policyTypes", "/spec/ingress", "/spec/egress"]
    assert not any(op["path"] == "/spec/podSelector" for op in result["patch"])


def test_a_rerun_with_the_same_rules_patches_nothing() -> None:
    rules = plan("create_security_group")["rules"]
    live = policy([VM], ingress=rules["ingress"])
    live["spec"]["egress"] = rules["egress"]
    assert ce.cudn_evpn_rules_patch(live, rules) == {"changed": False, "patch": []}


def test_a_stored_policy_without_empty_lists_matches_empty_rules() -> None:
    live = policy([])
    del live["spec"]["ingress"]
    del live["spec"]["egress"]
    assert ce.cudn_evpn_rules_patch(live, {"ingress": [], "egress": []})["changed"] is False


def test_binding_adds_the_vm_and_keeps_rules_and_other_vms() -> None:
    result = ce.cudn_evpn_membership_patch(policy([VM2]), [VM], [])
    assert result["bound"] == sorted([VM, VM2])
    assert result["patch"] == GUARD + [
        {"op": "replace", "path": "/spec/podSelector", "value": ce.pod_selector([VM, VM2])}
    ]


def test_binding_twice_patches_nothing() -> None:
    assert ce.cudn_evpn_membership_patch(policy([VM]), [VM], [])["changed"] is False


def test_unbinding_the_last_vm_restores_the_select_nothing_selector() -> None:
    result = ce.cudn_evpn_membership_patch(policy([VM]), [], [VM])
    assert result["bound"] == []
    assert result["patch"][-1]["value"] == ce.pod_selector([])


def test_unbinding_an_unbound_vm_patches_nothing() -> None:
    assert ce.cudn_evpn_membership_patch(policy([VM2]), [], [VM])["changed"] is False


# ---------------------------------------------------------------------------
# Attachments and results
# ---------------------------------------------------------------------------


def test_apply_returns_exactly_the_vm_namespace() -> None:
    p = plan("apply_network_attachment")
    assert p["result"] == {"vm_attachment": {"namespace": f"osac-vn-{VN}"}}
    assert p["target_id"] == VM and p["security_group_ids"] == [SG]


def test_apply_ignores_the_vm_address() -> None:
    attachment = {"subnet": SUBNET, "interface": {"type": "overlay", "ipv4_address": "10.0.1.17"}, "security_group_ids": [SG]}
    assert plan("apply_network_attachment", network_attachment=attachment)["result"]["vm_attachment"]["namespace"] == f"osac-vn-{VN}"


@pytest.mark.parametrize(
    "overrides",
    [
        {"target": {"kind": "BareMetalInstance", "id": VM}},
        {"target": {"kind": "ComputeInstance", "id": VM2}},
        {"network_attachment": {"subnet": SUBNET2, "interface": {"type": "overlay"}, "security_group_ids": [SG]}},
        {"network_attachment": {"subnet": SUBNET, "interface": {"type": "physical"}, "security_group_ids": [SG]}},
        {"network_attachment": {"subnet": SUBNET, "interface": {"type": "overlay"}, "security_group_ids": []}},
        {"network_attachment": {"subnet": SUBNET, "interface": {"type": "overlay"}, "security_group_ids": [SG, SG]}},
        {"network_attachment": {"subnet": SUBNET, "interface": {"type": "overlay"}, "security_group_ids": ["SG"]}},
    ],
)
def test_malformed_attachment_input_is_final(overrides: dict) -> None:
    assert error("apply_network_attachment", **overrides)["retryable"] is False


def test_attachment_delete_accepts_its_stored_input() -> None:
    attachment = {"subnet": SUBNET, "interface": {"type": "overlay", "ipv4_address": "10.0.1.17"}, "security_group_ids": [SG, SG2]}
    p = plan("delete_network_attachment", network_attachment=attachment)
    assert p["policy_names"] == [f"osac-sg-{SG}", f"osac-sg-{SG2}"]


# ---------------------------------------------------------------------------
# Common input
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "overrides",
    [
        {"tenant": ""},
        {"tenant": None},
        {"tenant": "not a label value"},
        {"manager": {**MANAGER, "type": "fabric"}},
        {"resource": snapshot("Subnet", VN)},
        {"resource": snapshot("VirtualNetwork", "3F1C9A2E-7B4D-4E8A-9C6F-0D2E4B6A8C10")},
        {"task": "query_dhcp_lease"},
    ],
)
def test_malformed_common_input_is_final(overrides: dict) -> None:
    err = ce.cudn_evpn_plan({**job("create_virtual_network"), **overrides})["error"]
    assert err is not None
    assert err["retryable"] is False
    assert set(err) == {"message", "retryable"}


def test_names_are_keyed_by_api_ids_only() -> None:
    renamed = dict(VN_SNAP, metadata={**VN_SNAP["metadata"], "name": "renamed", "uid": "other"})
    assert plan("create_virtual_network", resource=renamed)["namespace"] == plan("create_virtual_network")["namespace"]


def test_owned_checks_kind_id_and_tenant() -> None:
    ns = plan("create_virtual_network")["namespace_manifest"]
    assert ce.cudn_evpn_owned(ns, "virtualnetwork", VN, TENANT)
    assert not ce.cudn_evpn_owned(ns, "virtualnetwork", VN, "other")
    assert not ce.cudn_evpn_owned(ns, "subnet", VN, TENANT)
    assert not ce.cudn_evpn_owned({}, "virtualnetwork", VN, TENANT)


@pytest.mark.parametrize(
    ("task", "step", "manager"),
    [
        ("create_virtual_network", "Create the VirtualNetwork namespace", "cudn-evpn-{{ cudn_evpn.vn_id }}"),
        ("create_subnet", "Claim the hub network for this Subnet", "cudn-evpn-{{ cudn_evpn.subnet_id }}"),
        ("create_security_group", "Create the SecurityGroup policy", "cudn-evpn-{{ cudn_evpn.sg_id }}"),
    ],
)
def test_creates_never_merge_into_an_object_that_appeared(task: str, step: str, manager: str) -> None:
    """A create is a server-side apply without force under the owner's field manager.

    When an object of the same name appeared after the read, the owner fields
    it manages conflict (HTTP 409) instead of being overwritten, so the CUDN
    claim stays atomic.
    """
    task_def = next(t for t in _tasks(ROLE / f"tasks/{task}.yaml") if t.get("name") == step)
    module = task_def["kubernetes.core.k8s"]
    assert module["state"] == "present" and module["apply"] is True
    assert module["server_side_apply"] == {"field_manager": manager, "force_conflicts": False}
