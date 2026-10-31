"""NetworkAttachment tasks of the Netris Fabric Manager (OSAC-6766, T11.4).

Overlay attachments are a documented no-op. A physical attachment moves the
host's fabric ports from the provisioning V-Net to the V-Net of the Subnet
(EVPN Subnets included: DHCP is switched on over the physical range first),
and query_dhcp_lease reads the IPAM host of the interface MAC. The pure rules
are tested on the filter module; the task files run through the generic
networking playbook against the Netris double in netris_fake.py.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_netris_vn_subnet as vs  # noqa: E402
from netris_fake import Netris  # noqa: E402
from test_netris_role import GOOD_ENV, MANAGER, SUBNET, VN, nc  # noqa: E402
from test_netris_vn_subnet import (  # noqa: E402,F401
    ANSIBLE_PLAYBOOK, CIDR, IPAM, ROOT, SUBNET_CIDR, err, ok, snap, subnet_job,
)

ATT = "6c1d2e3f-4a5b-4c6d-8e7f-90a1b2c3d4e5"
BMI = "9e8d7c6b-5a49-4382-9160-5f4e3d2c1b0a"
MAC = "AA:BB:CC:DD:EE:01"
HOST = "host1"
PROVISIONING = "provisioning"


# ---------------------------------------------------------------------------
# Pure rules
# ---------------------------------------------------------------------------


def att_job(task="apply_network_attachment", itype="physical", network_type=None, ipam=None, mac=MAC,
            host=HOST, iface="eth1", subnet_ref=SUBNET) -> dict:
    interface = {"type": itype}
    target = {"kind": "BareMetalInstance", "id": BMI}
    if itype == "physical":
        interface.update({"name": iface, "mac_address": mac})
        if host is not None:
            target["host_name"] = host
    job = {
        "manager": MANAGER, "task": task,
        "resource": snap("NetworkAttachment", ATT, {}),
        "target": target,
        "network_attachment": {"subnet": subnet_ref, "interface": interface, "security_group_ids": []},
        "virtual_network": snap("VirtualNetwork", VN, {"ipv4Cidr": CIDR}, {"l3Vni": 10001} if network_type else {}),
        "subnet": snap("Subnet", SUBNET, {"ipv4Cidr": SUBNET_CIDR}),
        "ipam": IPAM if ipam is None else ipam,
    }
    if network_type:
        job["network_type"] = network_type
    return job


def test_attachment_plan_of_an_overlay_interface_is_a_no_op() -> None:
    assert nc.netris_attachment_plan(att_job(itype="overlay")) == {"error": "", "kind": "overlay"}


def test_attachment_plan_of_a_physical_interface() -> None:
    plan = nc.netris_attachment_plan(att_job())
    assert plan == {"error": "", "kind": "physical", "host_name": HOST, "port": "eth1", "mac": MAC.lower()}


@pytest.mark.parametrize(
    "bad",
    [
        {"host": None},
        {"host": ""},
        {"iface": ""},
        {"mac": "not-a-mac"},
        {"mac": "aa:bb:cc:dd:ee"},
        {"subnet_ref": "8a2b4c6d-1e3f-4a5b-9c7d-2e4f6a8b0c99"},
    ],
)
def test_attachment_plan_rejects_a_bad_physical_input(bad: dict) -> None:
    assert nc.netris_attachment_plan(att_job(**bad))["error"]


def test_attachment_plan_rejects_an_unknown_interface_type() -> None:
    assert "interface.type" in nc.netris_attachment_plan(att_job(itype="sriov"))["error"]
    assert nc.netris_attachment_plan({})["error"]


def vnet_detail(dhcp: bool = False, gateway: str = "10.20.1.1/24") -> dict:
    gw = {"id": 9, "prefix": gateway, "dhcpEnabled": dhcp}
    if dhcp:
        gw["dhcp"] = {"start": "10.20.1.8", "end": "10.20.1.31", "optionSet": {"id": 3}}
    return {"id": 401, "name": "x", "gateways": [gw] if gateway else []}


def test_dhcp_update_enables_dhcp_over_the_physical_range_and_keeps_the_gateway() -> None:
    out = nc.netris_vnet_dhcp_update(vnet_detail(), att_job(network_type="evpn"))
    assert (out["error"], out["matches"], out["start"], out["end"]) == ("", False, "10.20.1.8", "10.20.1.31")
    assert out["body"]["gateways"] == [{"id": 9, "prefix": "10.20.1.1/24", "dhcpEnabled": True,
                                        "dhcpStartIp": "10.20.1.8", "dhcpEndIp": "10.20.1.31"}]


def test_dhcp_update_matches_when_dhcp_already_serves_the_range() -> None:
    assert nc.netris_vnet_dhcp_update(vnet_detail(dhcp=True), att_job())["matches"] is True


def test_dhcp_update_keeps_a_physical_first_gateway() -> None:
    out = nc.netris_vnet_dhcp_update(vnet_detail(gateway="10.20.1.9/24"), att_job(network_type="evpn"))
    assert out["body"]["gateways"][0]["prefix"] == "10.20.1.9/24" and out["start"] == "10.20.1.10"


@pytest.mark.parametrize("ipam", [{"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": []}, {"gateway_ipv4": "10.20.1.1"}])
def test_dhcp_update_without_a_physical_range_is_an_error(ipam: dict) -> None:
    out = nc.netris_vnet_dhcp_update(vnet_detail(), att_job(network_type="evpn", ipam=ipam))
    assert out["error"] == "no fabric address range for bare-metal DHCP"


def test_dhcp_update_needs_a_gateway() -> None:
    assert "gateway" in nc.netris_vnet_dhcp_update(vnet_detail(gateway=""), att_job(network_type="evpn"))["error"]


def test_gateway_update_can_ignore_the_dhcp_of_the_v_net() -> None:
    plan = {"gateway": "10.20.1.1/24", "dhcp_start": "", "dhcp_end": ""}
    out = nc.netris_vnet_gateway_update(vnet_detail(dhcp=True), plan, True)
    assert out["matches"] is True
    assert out["body"]["gateways"][0]["dhcpEnabled"] is True
    drift = nc.netris_vnet_gateway_update(vnet_detail(dhcp=True, gateway="10.20.1.5/24"), plan, True)
    assert drift["matches"] is False
    assert drift["body"]["gateways"][0]["prefix"] == "10.20.1.1/24"
    assert drift["body"]["gateways"][0]["dhcpEnabled"] is True
    assert nc.netris_vnet_gateway_update(vnet_detail(dhcp=True), plan)["matches"] is False


def test_gateway_update_that_ignores_dhcp_creates_a_gateway_with_dhcp_off() -> None:
    plan = {"gateway": "10.20.1.1/24", "dhcp_start": "", "dhcp_end": ""}
    out = nc.netris_vnet_gateway_update({"id": 1, "gateways": []}, plan, True)
    assert out["matches"] is False
    assert out["body"]["gateways"] == [{"prefix": "10.20.1.1/24", "dhcpEnabled": False}]


def test_dhcp_lease_matches_the_mac_in_any_case_and_strips_the_prefix_length() -> None:
    hosts = [
        {"name": "a", "address": "10.20.1.10/32", "mac": [{"address": MAC.lower()}]},
        {"name": "b", "address": "10.20.1.11", "mac": [{"address": "aa:bb:cc:dd:ee:02"}]},
        {"name": "c", "address": "10.20.1.12", "mac": []},
        {"name": "d", "address": "10.20.1.13"},
    ]
    assert nc.netris_dhcp_lease(hosts, MAC) == {"error": "", "ip_address": "10.20.1.10"}


def test_dhcp_lease_without_or_with_several_matches_is_an_error() -> None:
    assert "no DHCP lease" in nc.netris_dhcp_lease([], MAC)["error"]
    two = [{"address": "10.20.1.10", "mac": [{"address": MAC.lower()}]},
           {"address": "10.20.1.11", "mac": [{"address": MAC.lower()}]}]
    assert "more than one" in nc.netris_dhcp_lease(two, MAC)["error"]
    assert nc.netris_dhcp_lease("nope", MAC)["error"]


def test_owned_pick_by_name_vpc_and_tenant() -> None:
    ours = {"id": 1, "name": "n", "vpc": {"id": 101}, "tenant": {"id": 2}}
    other = {"id": 2, "name": "n", "vpc": {"id": 102}, "tenant": {"id": 2}}
    foreign = {"id": 3, "name": "n", "vpc": {"id": 101}, "tenant": {"id": 9}}
    unknown = {"id": 4, "name": "n"}
    assert nc.netris_owned_pick([other, ours, foreign], "n", 101, 2) == {"error": "", "id": 1, "unverified": False}
    assert nc.netris_owned_pick([], "n", 101, 2) == {"error": "", "id": "", "unverified": False}
    assert nc.netris_owned_pick([{**ours, "name": "z"}], "n", 101, 2)["id"] == ""
    assert "another tenant or VPC" in nc.netris_owned_pick([foreign, other], "n", 101, 2)["error"]
    assert nc.netris_owned_pick([unknown], "n", 101, 2) == {"error": "", "id": 4, "unverified": True}
    assert "ambiguous" in nc.netris_owned_pick([ours, {**ours, "id": 5}], "n", 101, 2)["error"]
    assert "ambiguous" in nc.netris_owned_pick([unknown, {**unknown, "id": 5}], "n", 101, 2)["error"]
    assert nc.netris_owned_pick([foreign, unknown], "n", 101, 2)["id"] == 4


def test_owner_error_checks_the_read_object() -> None:
    assert nc.netris_owner_error({"vpc": {"id": 101}, "tenant": {"id": 2}}, 101, 2) == ""
    assert nc.netris_owner_error({"vpc": {"id": 101}, "tenant": {"id": 9}}, 101, 2)
    assert nc.netris_owner_error({"tenant": {"id": 2}}, 101, 2)
    assert nc.netris_owner_error(None, 101, 2)


# ---------------------------------------------------------------------------
# Task files through the generic playbook
# ---------------------------------------------------------------------------


@pytest.fixture()
def netris():
    server = Netris()
    yield server
    server.shutdown()
    server.server_close()


def run(tmp_path: Path, server, job: dict, env_drop: tuple = ()) -> tuple[int, dict]:
    """Like test_netris_vn_subnet.run, with no wait after a detach and optional unset settings."""
    extra = tmp_path / "extravars.json"
    extra.write_text(json.dumps({"osac_job_vars": job}))
    environ = {k: v for k, v in os.environ.items() if not k.startswith("NETRIS_")}
    environ.update(GOOD_ENV)
    environ.update({
        "NETRIS_CONTROLLER_URL": server.url,
        "NETRIS_VALIDATE_CERTS": "false",
        "NETRIS_PORT_SETTLE_SECONDS": "0",
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
    })
    for name in env_drop:
        environ.pop(name, None)
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", "playbook_osac_network_task.yml", "-e", f"@{extra}"],
        cwd=ROOT, env=environ, capture_output=True, text=True, timeout=300,
    )
    assert "{" in proc.stdout, proc.stdout + proc.stderr
    doc = json.loads(proc.stdout[proc.stdout.index("{"):])
    return proc.returncode, doc.get("global_custom_stats") or {}

SERVER_ID = 50


def seed_host(netris, on_provisioning: bool = True) -> None:
    """A registered host with two fabric ports and the provisioning V-Net."""
    netris.state["servers"].append({"id": SERVER_ID, "name": HOST})
    netris.state["server_ports"][str(SERVER_ID)] = [{"id": 61, "port": "eth0"}, {"id": 62, "port": "eth1"}]
    ports = [{"id": 61, "state": "active", "accessMode": True}, {"id": 62, "state": "active", "accessMode": True}]
    netris.state["vnets"].append({
        "id": 900, "name": PROVISIONING, "state": "active", "vpc": {"id": 3}, "tenant": {"id": 2},
        "gateways": [], "ports": ports if on_provisioning else [],
    })


def prepare(tmp_path, netris, network_type=None) -> None:
    """VirtualNetwork, Subnet (as the network type needs) and the host."""
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network", network_type)))
    ok(*run(tmp_path, netris, subnet_job("create_subnet", network_type, l3_vni=10001 if network_type else None)))
    seed_host(netris)
    netris.calls.clear()


def ports_of(netris, name: str) -> list[int]:
    vnet = next(v for v in netris.state["vnets"] if v["name"] == name)
    return sorted(p["id"] for p in vnet.get("ports") or [])


def tenant_vnet(netris) -> dict:
    return next(v for v in netris.state["vnets"] if v["name"] == f"osac-sn-{SUBNET}")


def test_overlay_apply_and_delete_are_no_ops(tmp_path, netris) -> None:
    for task in ("apply_network_attachment", "delete_network_attachment"):
        assert ok(*run(tmp_path, netris, att_job(task, itype="overlay", network_type="evpn"))) == {}
    assert netris.calls == []


def test_overlay_apply_needs_no_netris_settings(tmp_path, netris) -> None:
    rc, stats = run(tmp_path, netris, att_job(itype="overlay"), env_drop=["NETRIS_PASSWORD"])
    assert ok(rc, stats) == {}


def test_physical_apply_moves_the_port_to_the_subnet_v_net(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    assert ok(*run(tmp_path, netris, att_job())) == {}
    assert ports_of(netris, PROVISIONING) == []
    assert ports_of(netris, f"osac-sn-{SUBNET}") == [62]
    gateway = tenant_vnet(netris)["gateways"][0]
    assert gateway["dhcpEnabled"] is True and (gateway["dhcpStartIp"], gateway["dhcpEndIp"]) == ("10.20.1.8", "10.20.1.31")


def test_physical_apply_rerun_makes_no_write(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    ok(*run(tmp_path, netris, att_job()))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, att_job())) == {}
    assert netris.writes() == []


def test_physical_apply_on_an_evpn_subnet_enables_dhcp_before_the_move(tmp_path, netris) -> None:
    prepare(tmp_path, netris, "evpn")
    assert tenant_vnet(netris)["gateways"][0].get("dhcpEnabled", False) is False
    assert ok(*run(tmp_path, netris, att_job(network_type="evpn"))) == {}
    gateway = tenant_vnet(netris)["gateways"][0]
    assert gateway["prefix"] == "10.20.1.1/24" and gateway["dhcpEnabled"] is True
    assert (gateway["dhcpStartIp"], gateway["dhcpEndIp"]) == ("10.20.1.8", "10.20.1.31")
    assert ports_of(netris, f"osac-sn-{SUBNET}") == [62] and ports_of(netris, PROVISIONING) == []
    vnet_id = str(tenant_vnet(netris)["id"])
    writes = [(m, p.rsplit("/", 1)[-1]) for m, p in netris.writes()]
    assert writes[0] == ("PUT", vnet_id), writes
    assert ("PUT", "900") in writes[1:]


def test_create_subnet_rerun_keeps_the_dhcp_of_an_evpn_subnet(tmp_path, netris) -> None:
    prepare(tmp_path, netris, "evpn")
    ok(*run(tmp_path, netris, att_job(network_type="evpn")))
    netris.calls.clear()
    first = ok(*run(tmp_path, netris, subnet_job("create_subnet", "evpn")))
    assert first == {"network_segment": {"type": "evpn", "l2_vni": 10101}}
    assert netris.writes() == []
    assert tenant_vnet(netris)["gateways"][0]["dhcpEnabled"] is True


def test_evpn_apply_without_a_physical_range_is_final_and_writes_nothing(tmp_path, netris) -> None:
    prepare(tmp_path, netris, "evpn")
    ipam = {"gateway_ipv4": "10.20.1.1", "physical_ipv4_cidrs": []}
    message = err(*run(tmp_path, netris, att_job(network_type="evpn", ipam=ipam)), retryable=False)
    assert "no fabric address range for bare-metal DHCP" in message
    assert netris.writes() == []


@pytest.mark.parametrize("bad", [{"mac": "nope"}, {"host": ""}, {"iface": ""}])
def test_apply_with_a_bad_physical_input_is_final_and_writes_nothing(tmp_path, netris, bad) -> None:
    prepare(tmp_path, netris)
    err(*run(tmp_path, netris, att_job(**bad)), retryable=False)
    assert netris.writes() == []


def test_apply_on_an_unknown_network_type_is_final(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    err(*run(tmp_path, netris, att_job(network_type="vlan")), retryable=False)
    assert netris.writes() == []


def test_apply_for_a_host_that_is_not_registered_is_retryable(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    err(*run(tmp_path, netris, att_job(host="other-host")), retryable=True)
    assert netris.writes() == []


def test_apply_for_a_missing_interface_is_final_and_writes_nothing(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    message = err(*run(tmp_path, netris, att_job(iface="eth9")), retryable=False)
    assert "eth9" in message
    assert netris.writes() == []


def test_apply_before_the_subnet_exists_is_retryable(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    seed_host(netris)
    netris.calls.clear()
    err(*run(tmp_path, netris, att_job()), retryable=True)
    assert netris.writes() == []


def test_apply_before_the_virtual_network_exists_is_retryable(tmp_path, netris) -> None:
    seed_host(netris)
    err(*run(tmp_path, netris, att_job()), retryable=True)
    assert netris.writes() == []


@pytest.mark.parametrize("owner", [{"tenant": {"id": 99}, "vpc": {"id": 101}}, {"tenant": {"id": 2}, "vpc": {"id": 555}}])
def test_apply_never_touches_a_v_net_of_another_tenant_or_vpc(tmp_path, netris, owner) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    seed_host(netris)
    netris.state["vnets"].append({"id": 777, "name": f"osac-sn-{SUBNET}", "state": "active", **owner,
                                  "gateways": [{"prefix": "10.20.1.1/24", "dhcpEnabled": False}], "ports": []})
    netris.calls.clear()
    message = err(*run(tmp_path, netris, att_job()), retryable=False)
    assert "another tenant or VPC" in message
    assert netris.writes() == []


def test_apply_proves_the_owner_of_a_v_net_the_list_does_not_describe(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    netris.state["list_omit"][str(tenant_vnet(netris)["id"])] = ["vpc", "tenant"]
    assert ok(*run(tmp_path, netris, att_job())) == {}
    assert ports_of(netris, f"osac-sn-{SUBNET}") == [62]


def test_apply_provider_failure_is_retryable(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    netris.state["responses"]["GET /api/v2/hw"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, att_job()), retryable=True)


def test_apply_with_a_missing_setting_is_retryable_and_named(tmp_path, netris) -> None:
    rc, stats = run(tmp_path, netris, att_job(), env_drop=["NETRIS_BM_PROVISIONING_VNET"])
    assert "NETRIS_BM_PROVISIONING_VNET" in err(rc, stats, retryable=True)
    assert netris.calls == []


def test_delete_moves_the_ports_back_and_leaves_dhcp_on(tmp_path, netris) -> None:
    prepare(tmp_path, netris, "evpn")
    ok(*run(tmp_path, netris, att_job(network_type="evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, att_job("delete_network_attachment", network_type="evpn"))) == {}
    assert ports_of(netris, f"osac-sn-{SUBNET}") == []
    assert ports_of(netris, PROVISIONING) == [62]
    assert tenant_vnet(netris)["gateways"][0]["dhcpEnabled"] is True
    assert all(p.rsplit("/", 1)[-1] in ("900", str(tenant_vnet(netris)["id"])) for _, p in netris.writes())


SUBNET2 = "1b2c3d4e-5f60-4a71-8b82-93a4b5c6d7e8"
IPAM2 = {"gateway_ipv4": "10.20.2.1", "physical_ipv4_cidrs": ["10.20.2.8/29"]}


def test_delete_moves_back_only_its_own_port(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    job2 = subnet_job("create_subnet", ipam=IPAM2)
    job2["resource"] = snap("Subnet", SUBNET2, {"ipv4Cidr": "10.20.2.0/24"})
    ok(*run(tmp_path, netris, job2))
    na2 = att_job(iface="eth0", subnet_ref=SUBNET2, ipam=IPAM2)
    na2["subnet"] = snap("Subnet", SUBNET2, {"ipv4Cidr": "10.20.2.0/24"})
    ok(*run(tmp_path, netris, att_job(iface="eth1")))
    ok(*run(tmp_path, netris, na2))
    other = f"osac-sn-{SUBNET2}"
    assert ports_of(netris, f"osac-sn-{SUBNET}") == [62] and ports_of(netris, other) == [61]
    na1_delete = att_job("delete_network_attachment", iface="eth1")
    ok(*run(tmp_path, netris, na1_delete))
    assert ports_of(netris, PROVISIONING) == [62]
    assert ports_of(netris, f"osac-sn-{SUBNET}") == []
    assert ports_of(netris, other) == [61]


def test_delete_rerun_makes_no_write(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    ok(*run(tmp_path, netris, att_job()))
    ok(*run(tmp_path, netris, att_job("delete_network_attachment")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, att_job("delete_network_attachment"))) == {}
    assert netris.writes() == []


def test_delete_of_an_absent_host_or_v_net_succeeds(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    seed_host(netris)
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, att_job("delete_network_attachment"))) == {}
    assert ports_of(netris, PROVISIONING) == [61, 62]
    netris.state["servers"].clear()
    assert ok(*run(tmp_path, netris, att_job("delete_network_attachment"))) == {}
    assert netris.writes() == []


def test_delete_never_touches_a_v_net_of_another_tenant(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    seed_host(netris)
    netris.state["vnets"].append({"id": 777, "name": f"osac-sn-{SUBNET}", "state": "active",
                                  "tenant": {"id": 99}, "vpc": {"id": 101}, "gateways": [], "ports": [{"id": 62}]})
    netris.calls.clear()
    assert "another tenant or VPC" in err(*run(tmp_path, netris, att_job("delete_network_attachment")), retryable=False)
    assert netris.writes() == []


def lease_host(netris, mac: str = MAC.lower(), address: str = "10.20.1.10", name: str = "bm-host") -> None:
    ipam = next(s for s in netris.state["ipam_subnets"] if s["name"] == f"osac-sn-{SUBNET}")
    netris.state["ipam_hosts"].setdefault(str(ipam["id"]), []).append(
        {"name": name, "address": address, "mac": [{"address": mac, "source": "dhcp", "state": "active"}]})


def test_query_dhcp_lease_returns_the_lease_of_the_interface_mac(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    lease_host(netris)
    lease_host(netris, mac="aa:bb:cc:dd:ee:02", address="10.20.1.11")
    result = ok(*run(tmp_path, netris, att_job("query_dhcp_lease")))
    assert result == {"dhcp_lease": {"ip_address": "10.20.1.10", "mac_address": MAC}}
    assert netris.writes() == []


def test_query_dhcp_lease_without_a_lease_is_retryable(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    assert "no DHCP lease" in err(*run(tmp_path, netris, att_job("query_dhcp_lease")), retryable=True)


def test_query_dhcp_lease_with_two_matches_is_retryable(tmp_path, netris) -> None:
    prepare(tmp_path, netris)
    lease_host(netris)
    lease_host(netris, address="10.20.1.12", name="second")
    assert "more than one" in err(*run(tmp_path, netris, att_job("query_dhcp_lease")), retryable=True)


def test_query_dhcp_lease_before_the_subnet_exists_is_retryable(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    err(*run(tmp_path, netris, att_job("query_dhcp_lease")), retryable=True)


def test_query_dhcp_lease_ignores_an_ipam_subnet_of_another_tenant(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vs.vn_job("create_virtual_network")))
    netris.state["ipam_subnets"].append({"id": 888, "name": f"osac-sn-{SUBNET}", "prefix": SUBNET_CIDR,
                                         "tenant": {"id": 99}, "vpc": {"id": 101}})
    netris.state["ipam_hosts"]["888"] = [{"address": "10.20.1.10", "mac": [{"address": MAC.lower()}]}]
    netris.calls.clear()
    message = err(*run(tmp_path, netris, att_job("query_dhcp_lease")), retryable=False)
    assert "another tenant or VPC" in message and "10.20.1.10" not in message
    assert netris.writes() == []
    assert not any(p.startswith("/api/v2/ipam/hosts") for _, p, _ in netris.calls)


def test_query_dhcp_lease_of_an_overlay_interface_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, att_job("query_dhcp_lease", itype="overlay")), retryable=False)
    assert netris.calls == []
