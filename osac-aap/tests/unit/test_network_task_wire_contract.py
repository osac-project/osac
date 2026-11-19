"""Operator <-> AAP wire contract, from the shared golden files.

tests/contract/network-task holds the task input the operator launches for each
networking task family (inputs/<task>.<manager>.json: the osac_job_vars of the
AAP job) and the results a manager returns (results/<task>.<manager>.json: the
job artifacts). The operator test
(osac-operator/internal/controller/networking_wire_golden_test.go) produces
every input golden from the real controllers and feeds every result golden to
the real result parsers. This file runs the same input files, unchanged,
through the managers' own input handling and compares what they return with
the result goldens, so a renamed or reshaped field fails on both sides.

Fabric inputs run through the generic playbook against the in-memory Netris
double; Kubernetes inputs run through the cudn_evpn planner (the Kubernetes
objects themselves are covered by the cudn_evpn Kind target); the NATGateway
dependency snapshot is validated by the agentless_net state module.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_netris_attachment as netris_attachment  # noqa: E402
from test_cudn_evpn_role import ce  # noqa: E402
import test_netris_vn_subnet as vs  # noqa: E402
from test_netris_role import ROOT  # noqa: E402
from test_netris_vn_subnet import err, netris, ok, run  # noqa: E402,F401

MODULE_UTILS = ROOT / "collections/ansible_collections/osac/templates/plugins/module_utils"
sys.path.insert(0, str(MODULE_UTILS))
sys.path.insert(0, str(MODULE_UTILS.parents[4]))
import agentless_net_state  # noqa: E402

GOLDEN = ROOT.parent / "tests/contract/network-task"


def golden_input(task: str, manager: str, variant: str | None = None) -> dict:
    name = f"{task}.{variant}.{manager}.json" if variant else f"{task}.{manager}.json"
    return json.loads((GOLDEN / "inputs" / name).read_text())


def golden_result(task: str, manager: str, variant: str | None = None) -> dict:
    """The osac_result of a result golden (the golden is the job artifacts)."""
    name = f"{task}.{variant}.{manager}.json" if variant else f"{task}.{manager}.json"
    artifacts = json.loads((GOLDEN / "results" / name).read_text())
    assert set(artifacts) == {"osac_result"}
    return artifacts["osac_result"]


def same(actual, expected) -> None:
    """Strict JSON equality: 10001 and 10001.0, or 1 and True, are different."""
    assert json.dumps(actual, sort_keys=True) == json.dumps(expected, sort_keys=True)


def fabric(tmp_path: Path, server, task: str, variant: str | None = None) -> dict:
    """Run one Fabric input golden through the playbook and return its osac_result."""
    job = golden_input(task, "fabric", variant)
    assert job["task"] == task and job["manager"]["type"] == "fabric"
    if variant == "physical":
        return ok(*netris_attachment.run(tmp_path, server, job))
    return ok(*run(tmp_path, server, job))


def prepare_physical_attachment(tmp_path: Path, netris, job: dict) -> None:
    virtual_network = job["virtual_network"]
    subnet = job["subnet"]
    vn_job = vs.vn_job("create_virtual_network", cidr=virtual_network["spec"]["ipv4Cidr"])
    vn_job["resource"]["id"] = virtual_network["id"]
    netris_attachment.ok(*netris_attachment.run(tmp_path, netris, vn_job))

    subnet_input = netris_attachment.subnet_job("create_subnet", ipam=job["ipam"])
    subnet_input["resource"]["id"] = subnet["id"]
    subnet_input["resource"]["spec"] = subnet["spec"]
    subnet_input["virtual_network"]["id"] = virtual_network["id"]
    subnet_input["virtual_network"]["spec"] = virtual_network["spec"]
    subnet_input["virtual_network"]["status"] = virtual_network["status"]
    netris_attachment.ok(*netris_attachment.run(tmp_path, netris, subnet_input))
    netris_attachment.seed_host(netris)
    netris.calls.clear()


def test_every_golden_is_used_by_a_test() -> None:
    inputs = sorted(p.name for p in (GOLDEN / "inputs").glob("*.json"))
    assert inputs == [
        "apply_network_attachment.fabric.json",
        "apply_network_attachment.kubernetes.json",
        "apply_network_attachment.physical.fabric.json",
        "attach_external_ip.fabric.json",
        "create_external_ip.fabric.json",
        "create_external_ip_pool.fabric.json",
        "create_nat_gateway.fabric.json",
        "create_subnet.fabric.json",
        "create_subnet.kubernetes.json",
        "create_virtual_network.fabric.json",
        "create_virtual_network.kubernetes.json",
        "delete_network_attachment.physical.fabric.json",
        "query_dhcp_lease.physical.fabric.json",
        "reserve_cluster_ingress_ip.fabric.json",
    ]
    results = sorted(p.name for p in (GOLDEN / "results").glob("*.json"))
    assert results == [
        "apply_network_attachment.kubernetes.json",
        "create_external_ip.fabric.json",
        "create_subnet.fabric.json",
        "create_virtual_network.fabric.json",
        "query_dhcp_lease.physical.fabric.json",
        "reserve_cluster_ingress_ip.fabric.json",
    ]


def test_fabric_goldens_produce_the_result_goldens(tmp_path, netris) -> None:
    """The Fabric journey in the operator's order: each result equals its golden
    and each task without a result golden returns no output."""
    same(fabric(tmp_path, netris, "create_virtual_network"), golden_result("create_virtual_network", "fabric"))
    same(fabric(tmp_path, netris, "create_subnet"), golden_result("create_subnet", "fabric"))
    assert fabric(tmp_path, netris, "create_external_ip_pool") == {}
    same(fabric(tmp_path, netris, "create_external_ip"), golden_result("create_external_ip", "fabric"))
    assert fabric(tmp_path, netris, "apply_network_attachment") == {}
    assert fabric(tmp_path, netris, "attach_external_ip") == {}
    assert fabric(tmp_path, netris, "create_nat_gateway") == {}
    same(
        fabric(tmp_path, netris, "reserve_cluster_ingress_ip"),
        golden_result("reserve_cluster_ingress_ip", "fabric"),
    )


def test_physical_network_attachment_goldens_produce_the_dhcp_lease_result(tmp_path, netris) -> None:
    job = golden_input("apply_network_attachment", "fabric", "physical")
    prepare_physical_attachment(tmp_path, netris, job)
    netris.state["servers"][0]["name"] = "rack1-node3"
    netris.state["server_ports"]["50"][1]["port"] = "data-0"
    netris_attachment.lease_host(
        netris, mac="aa:bb:cc:dd:ee:ff", address="10.20.1.130", name="rack1-node3"
    )

    assert fabric(tmp_path, netris, "apply_network_attachment", "physical") == {}
    assert netris_attachment.ports_of(netris, netris_attachment.PROVISIONING) == []
    assert netris_attachment.ports_of(netris, f"osac-sn-{netris_attachment.SUBNET}") == [62]
    result = fabric(tmp_path, netris, "query_dhcp_lease", "physical")
    same(result, golden_result("query_dhcp_lease", "fabric", "physical"))
    assert fabric(tmp_path, netris, "delete_network_attachment", "physical") == {}
    assert netris_attachment.ports_of(netris, netris_attachment.PROVISIONING) == [62]
    assert netris_attachment.ports_of(netris, f"osac-sn-{netris_attachment.SUBNET}") == []


def test_the_kubernetes_goldens_are_valid_cudn_evpn_inputs_with_the_accepted_vnis() -> None:
    vn = ce.cudn_evpn_plan(golden_input("create_virtual_network", "kubernetes"))
    assert vn["error"] is None
    job = golden_input("create_virtual_network", "kubernetes")
    vn_id = job["resource"]["id"]
    assert vn["vn_id"] == vn_id
    assert vn["namespace"] == f"osac-vn-{vn_id}"
    assert vn["tenant"] == job["tenant"]
    labels = vn["baseline_manifest"]["metadata"]["labels"]
    assert labels["osac.openshift.io/virtualnetwork-id"] == vn_id
    assert labels["osac.openshift.io/tenant"] == job["tenant"]
    assert vn["baseline_manifest"]["metadata"]["namespace"] == vn["namespace"]
    assert vn["namespace_manifest"]["metadata"]["name"] == vn["namespace"]
    subnet = ce.cudn_evpn_plan(golden_input("create_subnet", "kubernetes"))
    assert subnet["error"] is None
    # The VNIs the operator accepted from the Fabric results reach the CUDN.
    evpn = subnet["cudn_manifest"]["spec"]["network"]["evpn"]
    assert evpn["ipVRF"]["vni"] == golden_result("create_virtual_network", "fabric")["l3_vni"]
    assert evpn["macVRF"]["vni"] == golden_result("create_subnet", "fabric")["network_segment"]["l2_vni"]
    assert subnet["cudn_manifest"]["spec"]["network"]["layer2"]["subnets"] == ["10.0.1.0/24"]


def test_the_cudn_evpn_apply_result_equals_its_golden() -> None:
    plan = ce.cudn_evpn_plan(golden_input("apply_network_attachment", "kubernetes"))
    assert plan["error"] is None
    same(plan["result"], golden_result("apply_network_attachment", "kubernetes"))


@pytest.mark.parametrize("manager", ["fabric", "kubernetes"])
def test_goldens_carry_the_launch_envelope(manager: str) -> None:
    for path in (GOLDEN / "inputs").glob(f"*.{manager}.json"):
        job = json.loads(path.read_text())
        assert set(job["manager"]) == {"name", "type", "ansible_role"}, path.name
        assert job["manager"]["type"] == manager and job["task"] == path.name.split(".")[0]


def test_the_nat_gateway_context_golden_is_a_valid_agentless_net_snapshot() -> None:
    job = golden_input("create_nat_gateway", "fabric")
    context = agentless_net_state._nat_gateway_context(job["nat_gateway_context"], job["tenant"])
    assert context["external_ip"]["address"] == job["external_ip"]["address"]
    assert context["virtual_network"]["fulfillment_id"] == job["virtual_network"]["id"]
    assert context["virtual_network"]["kubernetes_uid"] == job["virtual_network"]["metadata"]["uid"]
