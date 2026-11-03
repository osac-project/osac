"""VirtualNetwork, Subnet and SecurityGroup tasks of the Netris Fabric Manager.

The pure rules (V-Net plan, VNI check, gateway update) are tested on the filter
module. The task files run through the generic networking playbook in a child
Ansible process against the stateful Netris double in netris_fake.py.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from netris_fake import Netris  # noqa: E402
from test_netris_role import GOOD_ENV, MANAGER, SUBNET, VN, nc  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"
CIDR = "10.20.0.0/16"
SUBNET_CIDR = "10.20.1.0/24"
IPAM = {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": ["10.20.1.8/29", "10.20.1.16/28"]}


# ---------------------------------------------------------------------------
# Pure rules
# ---------------------------------------------------------------------------


def plan_job(network_type=None, ipam=IPAM, cidr=SUBNET_CIDR) -> dict:
    job = {"resource": {"spec": {"ipv4Cidr": cidr}}, "ipam": ipam}
    if network_type:
        job["network_type"] = network_type
    return job


def test_evpn_default_mode_keeps_the_gateway_and_no_dhcp() -> None:
    plan = nc.netris_vnet_plan(plan_job("evpn"), "gateway_ipv4")
    assert plan == {"error": "", "gateway": "10.20.1.1/24", "dhcp_start": "", "dhcp_end": ""}


def test_evpn_none_mode_has_no_gateway() -> None:
    plan = nc.netris_vnet_plan(plan_job("evpn"), "none")
    assert plan == {"error": "", "gateway": "", "dhcp_start": "", "dhcp_end": ""}


def test_evpn_physical_first_mode() -> None:
    assert nc.netris_vnet_plan(plan_job("evpn"), "physical_first")["gateway"] == "10.20.1.9/24"
    empty = nc.netris_vnet_plan(plan_job("evpn", {**IPAM, "physical_ipv4_cidrs": []}), "physical_first")
    assert "physical_ipv4_cidrs" in empty["error"]


def test_physical_first_refuses_a_network_or_broadcast_address() -> None:
    edge = {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": ["10.20.1.0/32"]}
    assert nc.netris_vnet_plan(plan_job("evpn", edge), "physical_first")["error"]
    edge = {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": ["10.20.1.255/32"]}
    assert nc.netris_vnet_plan(plan_job("evpn", edge), "physical_first")["error"]


def test_unknown_gateway_mode_is_an_error() -> None:
    assert "shared_anycast" in nc.netris_vnet_plan(plan_job("evpn"), "shared_anycast")["error"]


def test_without_network_type_the_gateway_and_dhcp_follow_the_ipam_result() -> None:
    plan = nc.netris_vnet_plan(plan_job(), "gateway_ipv4")
    assert plan == {"error": "", "gateway": "10.20.1.1/24", "dhcp_start": "10.20.1.8", "dhcp_end": "10.20.1.31"}


def test_the_mode_does_not_change_a_subnet_without_network_type() -> None:
    assert nc.netris_vnet_plan(plan_job(), "none") == nc.netris_vnet_plan(plan_job(), "gateway_ipv4")


def test_dhcp_start_is_after_the_gateway() -> None:
    plan = nc.netris_vnet_plan(plan_job(ipam={"gateway_ipv4": "10.20.1.9", "physical_ipv4_cidrs": ["10.20.1.8/29"]}), "none")
    assert plan["dhcp_start"] == "10.20.1.10"
    assert plan["dhcp_end"] == "10.20.1.15"


def test_no_physical_ranges_means_no_dhcp() -> None:
    plan = nc.netris_vnet_plan(plan_job(ipam={"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": []}), "none")
    assert plan["gateway"] == "10.20.1.1/24" and plan["dhcp_start"] == "" and plan["error"] == ""


@pytest.mark.parametrize(
    "bad",
    [
        {"ipam": {}},
        {"ipam": {"gateway_ipv4": "10.99.0.1", "physical_ipv4_cidrs": []}},
        {"ipam": {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": ["10.99.0.0/29"]}},
        {"ipam": {"gateway_ipv4": "nope", "physical_ipv4_cidrs": []}},
        {"cidr": "10.20.1.5/24"},
        {"cidr": ""},
        {"ipam": {"gateway_ipv4": "10.20.1.0", "physical_ipv4_cidrs": []}},
        {"ipam": {"gateway_ipv4": "10.20.1.255", "physical_ipv4_cidrs": []}},
    ],
)
def test_a_bad_subnet_input_is_an_error(bad: dict) -> None:
    assert nc.netris_vnet_plan(plan_job(**bad), "gateway_ipv4")["error"]


@pytest.mark.parametrize("value", [10001, "10001", 16777215])
def test_vni_check_accepts_a_good_value(value) -> None:
    assert nc.netris_vni_check(value, "VPC") == {"vni": int(value), "error": "", "retryable": False}


@pytest.mark.parametrize("value", [None, "", 0, "0"])
def test_a_missing_vni_is_retryable(value) -> None:
    out = nc.netris_vni_check(value, "VPC")
    assert out["retryable"] is True and "has not assigned the VNI yet" in out["error"]


@pytest.mark.parametrize("value", [16777216, -3, "abc", True, 1.5])
def test_a_bad_vni_is_final(value) -> None:
    out = nc.netris_vni_check(value, "V-Net")
    assert out["retryable"] is False and out["error"]


def test_vn_cidr_error() -> None:
    assert nc.netris_vn_cidr_error(CIDR) == ""
    assert nc.netris_vn_cidr_error("") == ""
    assert nc.netris_vn_cidr_error("10.20.0.1/16")
    assert nc.netris_vn_cidr_error("fd00::/64")


def test_gateway_update_matches_and_replaces_keeping_fields() -> None:
    plan = {"gateway": "10.20.1.1/24", "dhcp_start": "", "dhcp_end": ""}
    same = {"id": 1, "gateways": [{"id": 9, "prefix": "10.20.1.1/24", "dhcpEnabled": False}]}
    assert nc.netris_vnet_gateway_update(same, plan)["matches"] is True
    old = {"id": 1, "name": "x", "gateways": [{"id": 9, "prefix": "10.20.1.1/24", "dhcpEnabled": True,
                                               "dhcp": {"start": "10.20.1.2", "end": "10.20.1.9", "optionSet": {"id": 3}}}]}
    out = nc.netris_vnet_gateway_update(old, plan)
    assert out["matches"] is False
    assert out["body"]["name"] == "x"
    assert out["body"]["gateways"] == [{"id": 9, "prefix": "10.20.1.1/24", "dhcpEnabled": False,
                                        "dhcp": {"start": "10.20.1.2", "end": "10.20.1.9", "optionSet": {"id": 3}}}]
    dhcp = {"gateway": "10.20.1.1/24", "dhcp_start": "10.20.1.8", "dhcp_end": "10.20.1.31"}
    flat = {"gateways": [{"prefix": "10.20.1.1/24", "dhcpEnabled": True, "dhcpStartIp": "10.20.1.2", "dhcpEndIp": "10.20.1.5"}]}
    out = nc.netris_vnet_gateway_update(flat, dhcp)
    assert out["matches"] is False
    assert out["body"]["gateways"][0]["dhcpStartIp"] == "10.20.1.8" and out["body"]["gateways"][0]["dhcpEnabled"] is True
    nested = {"gateways": [{"prefix": "10.20.1.1/24", "dhcpEnabled": True, "dhcp": {"start": "10.20.1.8", "end": "10.20.1.31"}}]}
    assert nc.netris_vnet_gateway_update(nested, dhcp)["matches"] is True
    assert nc.netris_vnet_gateway_update({"gateways": []}, {"gateway": "", "dhcp_start": "", "dhcp_end": ""})["matches"] is True
    assert nc.netris_vnet_gateway_update(same, {"gateway": "", "dhcp_start": "", "dhcp_end": ""})["body"]["gateways"] == []


def test_result_rejects_bools_and_a_segment_without_l2_vni() -> None:
    with pytest.raises(ValueError):
        nc.netris_contract_result({"l3_vni": True})
    with pytest.raises(ValueError, match="l2_vni"):
        nc.netris_contract_result({"network_segment": {"type": "evpn"}})


# ---------------------------------------------------------------------------
# Task files through the generic playbook
# ---------------------------------------------------------------------------


def snap(kind: str, api_id: str, spec: dict | None = None, status: dict | None = None) -> dict:
    return {"kind": kind, "id": api_id, "metadata": {"name": "ignored"}, "spec": spec or {}, "status": status or {}}


def vn_job(task: str, network_type: str | None = None, cidr: str = CIDR) -> dict:
    job = {"manager": MANAGER, "task": task, "resource": snap("VirtualNetwork", VN, {"ipv4Cidr": cidr})}
    if network_type:
        job["network_type"] = network_type
    return job


def subnet_job(task: str, network_type: str | None = None, l3_vni: int | None = 10001, ipam: dict | None = None) -> dict:
    job = {
        "manager": MANAGER, "task": task,
        "resource": snap("Subnet", SUBNET, {"ipv4Cidr": SUBNET_CIDR}),
        "virtual_network": snap("VirtualNetwork", VN, {"ipv4Cidr": CIDR}, {"l3Vni": l3_vni} if l3_vni else {}),
        "ipam": IPAM if ipam is None else ipam,
    }
    if network_type:
        job["network_type"] = network_type
    return job


@pytest.fixture()
def netris():
    server = Netris()
    yield server
    server.shutdown()
    server.server_close()


def run(tmp_path: Path, server: Netris, job: dict) -> tuple[int, dict]:
    extra = tmp_path / "extravars.json"
    extra.write_text(json.dumps({"osac_job_vars": job}))
    environ = {k: v for k, v in os.environ.items() if not k.startswith("NETRIS_")}
    environ.update(GOOD_ENV)
    environ.update({
        "NETRIS_CONTROLLER_URL": server.url,
        "NETRIS_VALIDATE_CERTS": "false",
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
    })
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", "playbook_osac_network_task.yml", "-e", f"@{extra}"],
        cwd=ROOT, env=environ, capture_output=True, text=True, timeout=300,
    )
    assert "{" in proc.stdout, proc.stdout + proc.stderr
    doc = json.loads(proc.stdout[proc.stdout.index("{"):])
    return proc.returncode, doc.get("global_custom_stats") or {}


def ok(rc: int, stats: dict) -> dict:
    assert rc == 0 and "osac_error" not in stats, stats
    return stats.get("osac_result") or {}


def err(rc: int, stats: dict, retryable: bool) -> str:
    assert rc != 0 and "osac_result" not in stats
    assert stats["osac_error"]["retryable"] is retryable, stats
    assert "pw-not-a-secret" not in stats["osac_error"]["message"]
    return stats["osac_error"]["message"]


def test_create_vn_with_network_type_returns_backend_id_and_l3_vni(tmp_path, netris) -> None:
    result = ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    assert result == {"backend_id": "101", "l3_vni": 10001}
    vpc = netris.state["vpcs"][0]
    assert vpc["name"] == f"osac-vn-{VN}" and vpc["adminTenant"] == {"id": 2}
    alloc = netris.state["allocations"][0]
    assert alloc["name"] == f"osac-vn-{VN}" and alloc["prefix"] == CIDR and alloc["vpc"] == {"id": 101}


def test_create_vn_without_network_type_returns_only_backend_id(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network"))) == {"backend_id": "101"}


def test_create_vn_rerun_makes_no_write_and_returns_the_same_result(tmp_path, netris) -> None:
    first = ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn"))) == first
    assert netris.writes() == []


def test_create_vn_without_a_vni_is_retryable(tmp_path, netris) -> None:
    netris.state["assign_vni"] = False
    message = err(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")), retryable=True)
    assert "Netris has not assigned the VNI yet" in message


def test_create_vn_without_a_vni_needs_none_without_network_type(tmp_path, netris) -> None:
    netris.state["assign_vni"] = False
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network"))) == {"backend_id": "101"}


def test_create_vn_vni_out_of_range_is_final(tmp_path, netris) -> None:
    netris.state["next_vni"] = 16777216
    err(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")), retryable=False)


def test_create_vn_cidr_rejected_by_netris_is_final(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/ipam/allocation"] = (400, {"message": "prefix overlaps"})
    message = err(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")), retryable=False)
    assert "CIDR" in message and CIDR in message


def test_create_vn_unknown_network_type_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, vn_job("create_virtual_network", "vlan")), retryable=False)
    assert netris.writes() == []


def test_create_vn_bad_cidr_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, vn_job("create_virtual_network", cidr="10.20.0.1/16")), retryable=False)
    assert netris.writes() == []


def test_create_vn_provider_failure_is_retryable(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/vpc"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")), retryable=True)


def test_delete_vn_removes_allocation_then_vpc_and_publishes_nothing(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, vn_job("delete_virtual_network", "evpn"))) == {}
    assert [m + " " + p.rsplit("/", 2)[-2] for m, p in netris.writes()] == [
        "DELETE allocation", "DELETE vpc"]
    assert netris.state["vpcs"] == [] and netris.state["allocations"] == [] and netris.state["nats"] == []


def test_delete_vn_of_an_absent_network_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, vn_job("delete_virtual_network"))) == {}
    assert netris.writes() == []


def prepare_vn(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()


def test_create_subnet_with_network_type_returns_the_segment(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    result = ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    assert result == {"network_segment": {"type": "evpn", "l2_vni": 10101}}
    ipam = netris.state["ipam_subnets"][0]
    assert ipam["name"] == f"osac-sn-{SUBNET}" and ipam["prefix"] == SUBNET_CIDR and ipam["purpose"] == "common"
    vnet = netris.state["vnets"][0]
    assert vnet["name"] == f"osac-sn-{SUBNET}" and vnet["vpc"] == {"id": 101}
    assert vnet["gateways"] == [{"prefix": "10.20.1.1/24"}]


def test_create_subnet_rerun_makes_no_write(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    first = ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn"))) == first
    assert netris.writes() == []


def test_create_subnet_without_network_type_enables_dhcp_and_publishes_nothing(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    assert ok(*run(tmp_path, netris, subnet_job("create_subnet", l3_vni=None))) == {}
    gateway = netris.state["vnets"][0]["gateways"][0]
    assert gateway["prefix"] == "10.20.1.1/24" and gateway["dhcpEnabled"] is True
    assert (gateway["dhcpStartIp"], gateway["dhcpEndIp"]) == ("10.20.1.8", "10.20.1.31")


def test_create_subnet_without_physical_ranges_has_no_dhcp(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ipam = {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": []}
    ok(*run(tmp_path, netris, subnet_job("create_subnet", ipam=ipam)))
    assert netris.state["vnets"][0]["gateways"] == [{"prefix": "10.20.1.1/24"}]


def test_create_subnet_updates_a_drifted_gateway(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.state["vnets"][0]["gateways"] = [{"id": 7, "prefix": "10.20.1.9/24", "dhcpEnabled": True}]
    netris.calls.clear()
    ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    assert [m for m, _ in netris.writes()] == ["PUT"]
    gateway = netris.state["vnets"][0]["gateways"][0]
    # the gateway is corrected; the DHCP of a network-typed Subnet is left as it is
    assert gateway["id"] == 7 and gateway["prefix"] == "10.20.1.1/24" and gateway["dhcpEnabled"] is True


def test_create_subnet_l3_vni_mismatch_is_final(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    message = err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn", l3_vni=777)), retryable=False)
    assert "10001" in message and "777" in message
    assert netris.writes() == []


def test_create_subnet_without_the_vpc_is_retryable(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=True)
    assert netris.writes() == []


def test_create_subnet_without_a_vxlan_id_is_retryable(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["assign_vni"] = False
    message = err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=True)
    assert "has not assigned the VNI yet" in message


def test_create_subnet_requires_the_parent_virtual_network(tmp_path, netris) -> None:
    job = subnet_job("create_subnet", "evpn")
    del job["virtual_network"]
    assert "virtual_network is required" in err(*run(tmp_path, netris, job), retryable=False)


def test_create_subnet_bad_gateway_is_final(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    job = subnet_job("create_subnet", ipam={"gateway_ipv4": "10.99.0.1", "physical_ipv4_cidrs": []})
    err(*run(tmp_path, netris, job), retryable=False)
    assert netris.writes() == []


def test_delete_subnet_removes_vnet_then_ipam_subnet(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet", "evpn"))) == {}
    assert [p.rsplit("/", 2)[-2] for _, p in netris.writes()] == ["vnet", "subnet"]
    assert netris.state["vnets"] == [] and netris.state["ipam_subnets"] == []


def test_delete_subnet_that_is_absent_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert netris.writes() == []


def test_delete_subnet_needs_only_the_resource(tmp_path, netris) -> None:
    job = subnet_job("delete_subnet")
    del job["virtual_network"], job["ipam"]
    assert ok(*run(tmp_path, netris, job)) == {}


@pytest.mark.parametrize("task", ["create_security_group", "delete_security_group"])
def test_security_group_tasks_are_no_ops(tmp_path, netris, task) -> None:
    job = {"manager": MANAGER, "task": task, "resource": snap("SecurityGroup", "5d6a0f1e-2b3c-4d5e-8f90-a1b2c3d4e5f6")}
    assert ok(*run(tmp_path, netris, job)) == {}
    assert netris.calls == []


# ---------------------------------------------------------------------------
# Tenant isolation: objects of another tenant are never reused, changed or deleted
# ---------------------------------------------------------------------------

FOREIGN = {"id": 999}


def test_create_vn_refuses_a_vpc_of_another_tenant(tmp_path, netris) -> None:
    netris.state["vpcs"].append({"id": 55, "name": f"osac-vn-{VN}", "adminTenant": FOREIGN, "vni": 5})
    err(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")), retryable=False)
    assert netris.writes() == []


def test_create_subnet_refuses_a_vnet_of_another_tenant(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append({"id": 77, "name": f"osac-sn-{SUBNET}", "vpc": {"id": 101}, "tenant": FOREIGN,
                                  "sites": [{"id": 1}], "vxlanID": 4242})
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert netris.writes() == []


def test_create_subnet_refuses_a_vnet_in_another_vpc(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append({"id": 77, "name": f"osac-sn-{SUBNET}", "vpc": {"id": 555}, "tenant": {"id": 2},
                                  "sites": [{"id": 1}], "vxlanID": 4242})
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert netris.writes() == []


def test_create_subnet_refuses_an_ipam_subnet_of_another_tenant(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["ipam_subnets"].append({"id": 88, "name": f"osac-sn-{SUBNET}", "prefix": SUBNET_CIDR, "purpose": "common",
                                         "vpc": {"id": 101}, "tenant": FOREIGN, "sites": [{"id": 1}]})
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert netris.writes() == []


def test_delete_vn_never_deletes_a_vpc_of_another_tenant(tmp_path, netris) -> None:
    netris.state["vpcs"].append({"id": 55, "name": f"osac-vn-{VN}", "adminTenant": FOREIGN, "vni": 5})
    # The tenant-scoped lookup does not see the foreign VPC, so the delete is a
    # success that touches nothing.
    assert ok(*run(tmp_path, netris, vn_job("delete_virtual_network"))) == {}
    assert netris.writes() == []
    assert len(netris.state["vpcs"]) == 1


@pytest.mark.parametrize("foreign", [{"tenant": FOREIGN, "vpc": {"id": 101}}, {"tenant": {"id": 2}, "vpc": {"id": 555}}])
def test_delete_subnet_leaves_a_foreign_vnet_and_deletes_its_own_ipam_subnet(tmp_path, netris, foreign) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append({"id": 77, "name": f"osac-sn-{SUBNET}", "sites": [{"id": 1}], **foreign})
    netris.state["ipam_subnets"].append({"id": 88, "name": f"osac-sn-{SUBNET}", "prefix": SUBNET_CIDR, "purpose": "common",
                                         "vpc": {"id": 101}, "tenant": {"id": 2}, "sites": [{"id": 1}]})
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert [(m, p.rsplit("/", 2)[-2]) for m, p in netris.writes()] == [("DELETE", "subnet")]
    assert [v["id"] for v in netris.state["vnets"]] == [77]
    assert netris.state["ipam_subnets"] == []


def test_a_mixed_immutable_drift_message_is_final(tmp_path) -> None:
    # The exact heading of netris.controller.format_immutable_mismatch_message
    # when drift and unverifiable fields are both present.
    sys.path.insert(0, str(ROOT / "collections/ansible_collections/netris/controller/plugins/filter"))
    import idempotency

    message = idempotency.format_immutable_mismatch_message(
        "V-Net", "x", [{"field": "tenant.id", "expected": "2", "actual": "9"},
                       {"field": "vpc.id", "expected": "1", "actual": "", "unverifiable": True}])
    assert "immutable drift and fields that could not be verified" in message
    play = [{"hosts": "localhost", "gather_facts": False, "tasks": [{"name": "run", "block": [
        {"name": "Fail like the controller role", "ansible.builtin.fail": {"msg": message}}], "rescue": [
        {"name": "rescue", "ansible.builtin.include_role": {"name": "osac.templates.netris", "tasks_from": "_rescue"}}]}]}]
    import yaml
    (tmp_path / "play.yaml").write_text(yaml.safe_dump(play))
    (tmp_path / "x.json").write_text(json.dumps({"osac_job_vars": {"task": "create_subnet"}}))
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(tmp_path / "play.yaml"), "-e", f"@{tmp_path / 'x.json'}"],
        cwd=ROOT, capture_output=True, text=True, timeout=300,
        env={**os.environ, "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json", "ANSIBLE_SHOW_CUSTOM_STATS": "true",
             "ANSIBLE_PYTHON_INTERPRETER": sys.executable, "ANSIBLE_LOCALHOST_WARNING": "False"},
    )
    stats = json.loads(proc.stdout[proc.stdout.index("{"):]).get("global_custom_stats") or {}
    assert proc.returncode != 0 and stats["osac_error"]["retryable"] is False


# A list entry can omit the owner of a V-Net: the task reads it by ID and fails
# closed when the owner stays unknown.


def foreign_vnet(**extra) -> dict:
    return {"id": 77, "name": f"osac-sn-{SUBNET}", "sites": [{"id": 1}], **extra}


def our_ipam_subnet() -> dict:
    return {"id": 88, "name": f"osac-sn-{SUBNET}", "prefix": SUBNET_CIDR, "purpose": "common",
            "vpc": {"id": 101}, "tenant": {"id": 2}, "sites": [{"id": 1}]}


def test_create_subnet_reads_the_detail_of_a_vnet_without_owner_and_refuses_a_foreign_one(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet(vpc={"id": 101}, tenant=FOREIGN))
    netris.state["list_omit"]["77"] = ["vpc", "tenant"]
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert netris.writes() == []


def test_create_subnet_proceeds_when_the_detail_shows_our_vnet(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    first = ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.state["list_omit"]["401"] = ["vpc", "tenant"]
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn"))) == first
    assert netris.writes() == []


def test_create_subnet_fails_closed_when_the_owner_stays_unknown(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet())
    message = err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert f"osac-sn-{SUBNET}" in message
    assert netris.writes() == []


def test_delete_subnet_leaves_a_vnet_whose_detail_is_foreign(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet(vpc={"id": 101}, tenant=FOREIGN))
    netris.state["list_omit"]["77"] = ["vpc", "tenant"]
    netris.state["ipam_subnets"].append(our_ipam_subnet())
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert [(m, p.rsplit("/", 2)[-2]) for m, p in netris.writes()] == [("DELETE", "subnet")]
    assert [v["id"] for v in netris.state["vnets"]] == [77]


def test_delete_subnet_deletes_a_vnet_whose_detail_is_ours(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.state["list_omit"]["401"] = ["vpc", "tenant"]
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert netris.state["vnets"] == [] and netris.state["ipam_subnets"] == []


def test_delete_subnet_keeps_an_unproven_vnet_but_deletes_its_own_ipam_subnet(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet())
    netris.state["ipam_subnets"].append(our_ipam_subnet())
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert [(m, p.rsplit("/", 2)[-2]) for m, p in netris.writes()] == [("DELETE", "subnet")]
    assert [v["id"] for v in netris.state["vnets"]] == [77]


def test_delete_subnet_fails_final_when_nothing_can_be_proven(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet())
    err(*run(tmp_path, netris, subnet_job("delete_subnet")), retryable=False)
    assert netris.writes() == []
    assert [v["id"] for v in netris.state["vnets"]] == [77]


def test_create_subnet_retries_a_304_on_the_vnet_detail(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet(vpc={"id": 101}, tenant=FOREIGN))
    netris.state["list_omit"]["77"] = ["vpc", "tenant"]
    netris.state["get_304"]["/api/v2/vnet/77"] = 1
    err(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")), retryable=False)
    assert netris.writes() == []
    assert [p for m, p, _ in netris.calls if m == "GET" and p == "/api/v2/vnet/77"] == ["/api/v2/vnet/77"] * 2


def test_delete_subnet_retries_a_304_on_the_vnet_detail(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    netris.state["list_omit"]["401"] = ["vpc", "tenant"]
    netris.state["get_304"]["/api/v2/vnet/401"] = 1
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert netris.state["vnets"] == [] and netris.state["ipam_subnets"] == []


def test_delete_subnet_counts_a_vnet_that_vanished_after_the_list_as_absent(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet())
    netris.state["ipam_subnets"].append(our_ipam_subnet())
    netris.state["vanish_after_list"] = 77
    assert ok(*run(tmp_path, netris, subnet_job("delete_subnet"))) == {}
    assert [(m, p.rsplit("/", 2)[-2]) for m, p in netris.writes()] == [("DELETE", "subnet")]


def test_delete_subnet_fails_retryable_when_the_vnet_detail_cannot_be_read(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["vnets"].append(foreign_vnet())
    netris.state["responses"]["GET /api/v2/vnet/77"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, subnet_job("delete_subnet")), retryable=True)
    assert netris.writes() == []
