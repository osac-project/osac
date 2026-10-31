"""FabricDomain, cluster ingress and the NAT-free VirtualNetwork tasks of the Netris Fabric Manager.

The pure rules (Server Cluster template choice) are tested on the
filter module. The task files run through the generic networking playbook in a
child Ansible process against the stateful Netris double in netris_fake.py.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_netris_role import GOOD_ENV, MANAGER, VN, nc  # noqa: E402
from test_netris_vn_subnet import CIDR, err, netris, ok, run, snap, vn_job  # noqa: E402,F401

FD = "6e1d2c3b-4a59-4f68-8b7a-9c0d1e2f3a4b"

# Settings removed with the per-resource DNS; a job that still sets them must behave the same.
REMOVED_NETRIS_SETTINGS = {"NETRIS_DNS_RESOLVERS": "not-an-address", "NETRIS_DNS_SNAT_CIDR": "not-a-cidr"}


# ---------------------------------------------------------------------------
# Pure rules
# ---------------------------------------------------------------------------


def test_template_is_the_single_template_of_the_class_map() -> None:
    assert nc.netris_fabric_domain_template({"gpu": {"server_cluster_template_id": 7}}) == {"template_id": 7, "error": ""}
    same = {"a": {"server_cluster_template_id": "7"}, "b": {"server_cluster_template_id": 7}}
    assert nc.netris_fabric_domain_template(same)["template_id"] == 7
    assert nc.netris_fabric_domain_template('{"a": {"server_cluster_template_id": 9}}')["template_id"] == 9


@pytest.mark.parametrize(
    "class_map",
    [
        {},
        {"a": {"mgmt_interface": "ens4"}},
        {"a": {"server_cluster_template_id": 7}, "b": {"server_cluster_template_id": 8}},
        {"a": {"server_cluster_template_id": 0}},
        {"a": {"server_cluster_template_id": True}},
        {"a": {"server_cluster_template_id": "x"}},
        "not json",
        None,
    ],
)
def test_template_error_cases(class_map) -> None:
    out = nc.netris_fabric_domain_template(class_map)
    assert out["error"] and out["template_id"] == 0


# ---------------------------------------------------------------------------
# VirtualNetwork tasks create no NAT rule
# ---------------------------------------------------------------------------


def nat_calls(server) -> list[tuple]:
    return [c for c in server.calls if c[1].startswith("/api/v2/nat")]


def test_create_vn_with_network_type_creates_no_nat_rule(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn"))) == {"backend_id": "101", "l3_vni": 10001}
    assert netris.state["nats"] == []
    assert nat_calls(netris) == []


def test_create_vn_without_network_type_creates_no_nat_rule(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network"))) == {"backend_id": "101"}
    assert nat_calls(netris) == []


def test_create_vn_without_a_cidr_creates_no_nat_rule(tmp_path, netris) -> None:
    job = vn_job("create_virtual_network", "evpn")
    del job["resource"]["spec"]["ipv4Cidr"]
    assert ok(*run(tmp_path, netris, job)) == {"backend_id": "101", "l3_vni": 10001}
    assert nat_calls(netris) == []


def test_create_vn_rerun_makes_no_write(tmp_path, netris) -> None:
    first = ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn"))) == first
    assert netris.writes() == []


def test_create_vn_ignores_the_removed_dns_settings(tmp_path, netris, monkeypatch) -> None:
    for name, value in REMOVED_NETRIS_SETTINGS.items():
        monkeypatch.setitem(GOOD_ENV, name, value)
    assert ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn"))) == {"backend_id": "101", "l3_vni": 10001}
    assert nat_calls(netris) == []


def test_delete_vn_deletes_the_allocation_and_vpc_only(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, vn_job("delete_virtual_network", "evpn"))) == {}
    assert [m + " " + p.rsplit("/", 2)[-2] for m, p in netris.writes()] == ["DELETE allocation", "DELETE vpc"]
    assert nat_calls(netris) == []
    assert netris.state["vpcs"] == []


def test_delete_vn_rerun_with_nothing_left_succeeds(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    ok(*run(tmp_path, netris, vn_job("delete_virtual_network", "evpn")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, vn_job("delete_virtual_network", "evpn"))) == {}
    assert netris.writes() == []


# ---------------------------------------------------------------------------
# FabricDomain
# ---------------------------------------------------------------------------

SERVERS = ["server-a", "server-b"]


def host(host_id: int, name: str, site: int = 1) -> dict:
    return {"id": host_id, "name": name, "site": {"id": site, "name": f"site-{site}"}}


def fd_job(task: str, servers=None, with_vn: bool = True) -> dict:
    job = {
        "manager": MANAGER, "task": task,
        "resource": snap("FabricDomain", FD, {"type": "ethernet_ew", "servers": SERVERS if servers is None else servers}),
    }
    if with_vn:
        job["virtual_network"] = snap("VirtualNetwork", VN, {"ipv4Cidr": CIDR})
    return job


def prepare_fd(tmp_path, netris, hosts=None) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network")))
    netris.state["servers"].extend(hosts if hosts is not None else [host(1, "server-a"), host(2, "server-b")])
    netris.calls.clear()


def test_create_fabric_domain_creates_the_server_cluster_in_the_vpc(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    assert ok(*run(tmp_path, netris, fd_job("create_fabric_domain"))) == {"backend_id": "601"}
    (cluster,) = netris.state["clusters"]
    assert cluster["name"] == f"osac-fd-{FD}"
    assert cluster["site"] == {"id": 1}
    assert cluster["vpc"]["id"] == 101
    assert cluster["srvClusterTemplate"] == {"id": 7}
    assert cluster["servers"] == [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-b"}]
    assert netris.writes() == [("POST", "/api/v2/server-cluster")]


def test_create_fabric_domain_rerun_updates_membership_without_a_second_cluster(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    first = ok(*run(tmp_path, netris, fd_job("create_fabric_domain")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, fd_job("create_fabric_domain"))) == first
    assert netris.writes() == [("PUT", "/api/v2/server-cluster/601")]
    assert len(netris.state["clusters"]) == 1


def test_create_fabric_domain_resizes_to_the_new_servers(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    ok(*run(tmp_path, netris, fd_job("create_fabric_domain")))
    ok(*run(tmp_path, netris, fd_job("create_fabric_domain", servers=["server-b"])))
    assert netris.state["clusters"][0]["servers"] == [{"id": 2, "name": "server-b"}]


def test_create_fabric_domain_with_a_missing_server_writes_nothing(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris, hosts=[host(1, "server-a")])
    rc, stats = run(tmp_path, netris, fd_job("create_fabric_domain"))
    assert rc != 0 and "osac_result" not in stats and "osac_error" in stats
    assert netris.writes() == []


def test_create_fabric_domain_uses_servers_of_the_configured_site_only(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris, hosts=[host(1, "server-a", site=2), host(2, "server-b")])
    rc, stats = run(tmp_path, netris, fd_job("create_fabric_domain"))
    assert rc != 0 and "osac_error" in stats
    assert netris.writes() == []


def test_create_fabric_domain_without_the_vpc_is_retryable_and_writes_nothing(tmp_path, netris) -> None:
    netris.state["servers"].extend([host(1, "server-a"), host(2, "server-b")])
    message = err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=True)
    assert f"osac-vn-{VN}" in message
    assert netris.writes() == []


def test_create_fabric_domain_ignores_a_vpc_of_another_tenant(tmp_path, netris) -> None:
    netris.state["vpcs"].append({"id": 55, "name": f"osac-vn-{VN}", "adminTenant": {"id": 9}})
    netris.state["servers"].extend([host(1, "server-a"), host(2, "server-b")])
    err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=False)
    assert netris.writes() == []


def test_create_fabric_domain_refuses_a_server_cluster_of_another_vpc(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    netris.state["clusters"].append({"id": 70, "name": f"osac-fd-{FD}", "site": {"id": 1}, "vpc": {"id": 999},
                                     "servers": [], "status": {"label": "Active"}})
    message = err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=False)
    assert f"osac-fd-{FD}" in message
    assert netris.writes() == []


@pytest.mark.parametrize(
    "class_map",
    ['{"a": {"server_cluster_template_id": 7}, "b": {"server_cluster_template_id": 8}}',
     '{"a": {"mgmt_interface": "ens4"}}'],
    ids=["ambiguous", "no-template"],
)
def test_create_fabric_domain_needs_one_template(tmp_path, netris, monkeypatch, class_map) -> None:
    prepare_fd(tmp_path, netris)
    monkeypatch.setitem(GOOD_ENV, "NETRIS_RESOURCE_CLASS_MAP", class_map)
    message = err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=False)
    assert "server_cluster_template_id" in message
    assert netris.writes() == []


def test_create_fabric_domain_needs_the_class_map_setting(tmp_path, netris, monkeypatch) -> None:
    prepare_fd(tmp_path, netris)
    monkeypatch.delitem(GOOD_ENV, "NETRIS_RESOURCE_CLASS_MAP")
    assert "NETRIS_RESOURCE_CLASS_MAP" in err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=True)


@pytest.mark.parametrize(
    "job_change",
    [
        lambda j: j.pop("virtual_network"),
        lambda j: j["virtual_network"].update(id="not-a-uuid"),
        lambda j: j["resource"]["spec"].update(servers=[]),
        lambda j: j["resource"]["spec"].update(servers="server-a"),
        lambda j: j["resource"]["spec"].update(servers=["server-a", "server-a"]),
        lambda j: j["resource"]["spec"].update(servers=["server-a", ""]),
        lambda j: j["resource"].update(id="x"),
    ],
    ids=["no-vn", "bad-vn-id", "no-servers", "servers-not-a-list", "duplicate-server", "empty-name", "bad-id"],
)
def test_create_fabric_domain_with_a_bad_input_is_final(tmp_path, netris, job_change) -> None:
    prepare_fd(tmp_path, netris)
    job = fd_job("create_fabric_domain")
    job_change(job)
    err(*run(tmp_path, netris, job), retryable=False)
    assert netris.writes() == []


def test_create_fabric_domain_provider_failure_is_retryable(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    netris.state["responses"]["POST /api/v2/server-cluster"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, fd_job("create_fabric_domain")), retryable=True)


def test_delete_fabric_domain_deletes_the_server_cluster(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    ok(*run(tmp_path, netris, fd_job("create_fabric_domain")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, fd_job("delete_fabric_domain"))) == {}
    assert netris.writes() == [("DELETE", "/api/v2/server-cluster/601")]
    assert netris.state["clusters"] == []


def test_delete_fabric_domain_of_an_absent_cluster_succeeds(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    assert ok(*run(tmp_path, netris, fd_job("delete_fabric_domain"))) == {}
    assert netris.writes() == []


def test_delete_fabric_domain_with_the_vpc_gone_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, fd_job("delete_fabric_domain"))) == {}
    assert netris.writes() == []


def test_delete_fabric_domain_never_deletes_a_cluster_of_another_vpc(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    netris.state["clusters"].append({"id": 70, "name": f"osac-fd-{FD}", "site": {"id": 1}, "vpc": {"id": 999}})
    assert ok(*run(tmp_path, netris, fd_job("delete_fabric_domain"))) == {}
    assert netris.writes() == []
    assert len(netris.state["clusters"]) == 1


def test_delete_fabric_domain_without_the_parent_looks_up_by_its_unique_name(tmp_path, netris) -> None:
    prepare_fd(tmp_path, netris)
    ok(*run(tmp_path, netris, fd_job("create_fabric_domain")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, fd_job("delete_fabric_domain", with_vn=False))) == {}
    assert netris.writes() == [("DELETE", "/api/v2/server-cluster/601")]


def test_delete_fabric_domain_with_a_bad_input_is_final(tmp_path, netris) -> None:
    job = fd_job("delete_fabric_domain")
    job["resource"]["id"] = "x"
    err(*run(tmp_path, netris, job), retryable=False)
    assert netris.writes() == []


# ---------------------------------------------------------------------------
# Cluster ingress
# ---------------------------------------------------------------------------


def ingress_job(task: str) -> dict:
    return {"manager": MANAGER, "task": task, "resource": snap("Subnet", FD),
            "virtual_network": snap("VirtualNetwork", VN, {"ipv4Cidr": CIDR})}


def test_reserve_cluster_ingress_ip_is_a_final_error(tmp_path, netris) -> None:
    message = err(*run(tmp_path, netris, ingress_job("reserve_cluster_ingress_ip")), retryable=False)
    assert "not supported by the Netris Fabric Manager yet" in message
    assert netris.calls == []


def test_release_cluster_ingress_ip_is_a_noop(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, ingress_job("release_cluster_ingress_ip"))) == {}
    assert netris.calls == []
