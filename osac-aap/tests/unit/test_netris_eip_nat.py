"""ExternalIPPool, ExternalIP, ExternalIPAttachment and NATGateway tasks.

The pure rules (NAT plan, attachment destination) are tested on the filter
module. The task files run through the generic networking playbook in a child
Ansible process against the stateful Netris double in netris_fake.py. The
ExternalIP address itself is allocated by the osac.templates.netris_external_ip
module (its own tests are in test_netris_external_ip*.py).
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_netris_role import EIP, GOOD_ENV, MANAGER, POOL, VN, SUBNET, nc  # noqa: E402
from test_netris_vn_subnet import err, netris, ok, run, snap, vn_job  # noqa: E402,F401

EIPA = "9c8b7a6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
NATGW = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
POOL_CIDR = "203.0.113.0/30"
VN_CIDR = "10.20.0.0/16"
ADDRESS = "203.0.113.1"
TARGET = "10.20.1.10"
TENANT = {"tenant": {"id": 2}}
TENANT_ID = 2
SITE = {"sites": [{"id": 1}]}


# ---------------------------------------------------------------------------
# Pure rules
# ---------------------------------------------------------------------------


def eipa_job(domain: dict | None = None, address: str = TARGET, external: str | None = ADDRESS) -> dict:
    job = {
        "manager": MANAGER, "task": "attach_external_ip",
        "resource": snap("ExternalIPAttachment", EIPA),
        "destination": {"address": address, "routing_domain": domain if domain is not None else {
            "type": "tenant_virtual_network", "virtual_network_id": VN, "subnet_id": SUBNET}},
    }
    if external is not None:
        job["external_ip"] = {"id": EIP, "address": external}
    return job


def natgw_job(cidr: str = VN_CIDR, external: str | None = ADDRESS) -> dict:
    job = {
        "manager": MANAGER, "task": "create_nat_gateway",
        "resource": snap("NATGateway", NATGW),
        "virtual_network": snap("VirtualNetwork", VN, {"ipv4Cidr": cidr}),
    }
    if external is not None:
        job["external_ip"] = {"id": EIP, "address": external}
    return job


def test_attachment_input_takes_the_vpc_network_from_the_destination() -> None:
    out = nc.netris_contract_input(eipa_job())
    assert out["error"] is None and out["virtual_network_id"] == VN and out["resource_id"] == EIPA
    names = nc.netris_contract_names(out)
    assert names["vpc"] == f"osac-vn-{VN}" and names["eipa_dnat"] == f"osac-eipa-{EIPA}"


def test_attachment_in_the_management_domain_has_no_virtual_network() -> None:
    out = nc.netris_contract_input(eipa_job({"type": "provider_management"}))
    assert out["error"] is None and out["virtual_network_id"] == ""


@pytest.mark.parametrize(
    "domain",
    [
        {"type": "tenant_virtual_network"},
        {"type": "tenant_virtual_network", "virtual_network_id": "not-a-uuid"},
        {"type": "elsewhere"},
        {},
    ],
)
def test_attachment_with_a_bad_routing_domain_is_an_input_error(domain: dict) -> None:
    assert nc.netris_nat_plan(eipa_job(domain))["error"].startswith("Netris invalid task input")


def test_dnat_plan() -> None:
    plan = nc.netris_nat_plan(eipa_job())
    assert plan == {"error": "", "action": "dnat", "external": ADDRESS, "internal": TARGET,
                    "source": "", "domain": "tenant_virtual_network"}
    assert nc.netris_nat_plan(eipa_job({"type": "provider_management"}))["domain"] == "provider_management"


def test_snat_plan() -> None:
    plan = nc.netris_nat_plan(natgw_job())
    assert plan == {"error": "", "action": "snat", "external": ADDRESS, "internal": "",
                    "source": VN_CIDR, "domain": "tenant_virtual_network"}


@pytest.mark.parametrize(
    "job",
    [
        eipa_job(external=None),
        eipa_job(external="not-an-ip"),
        eipa_job(external="203.0.113.1/32"),
        eipa_job(external="2001:db8::1"),
        eipa_job(address=""),
        eipa_job(address="10.20.1.0/24"),
        natgw_job(external=None),
        natgw_job(external="256.1.1.1"),
        natgw_job(cidr=""),
        natgw_job(cidr="10.20.0.1/16"),
        natgw_job(cidr="fd00::/64"),
    ],
)
def test_nat_plan_rejects_a_bad_input(job: dict) -> None:
    assert nc.netris_nat_plan(job)["error"].startswith("Netris invalid task input")


def test_nat_plan_only_for_nat_resources() -> None:
    assert nc.netris_nat_plan({"resource": snap("Subnet", SUBNET)})["error"]


# ---------------------------------------------------------------------------
# Task files through the generic playbook
# ---------------------------------------------------------------------------


def pool_job(task: str, cidrs: list[str] | None = None, with_spec: bool = True) -> dict:
    spec = {"cidrs": [POOL_CIDR] if cidrs is None else cidrs} if with_spec else {}
    return {"manager": MANAGER, "task": task, "resource": snap("ExternalIPPool", POOL, spec)}


def eip_job(task: str, cidrs: list[str] | None = None) -> dict:
    pool = snap("ExternalIPPool", POOL, {"cidrs": [POOL_CIDR] if cidrs is None else cidrs})
    return {"manager": MANAGER, "task": task, "resource": snap("ExternalIP", EIP, {"pool": POOL}),
            "external_ip_pool": pool}


def attach_job(task: str, domain: dict | None = None) -> dict:
    job = eipa_job(domain)
    job["task"] = task
    return job


def nat_job(task: str) -> dict:
    job = natgw_job()
    job["task"] = task
    return job


def prepare_vn(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, vn_job("create_virtual_network", "evpn")))
    netris.calls.clear()


def nat_rules(netris) -> list[dict]:
    """The NAT rules in the Netris double."""
    return list(netris.state["nats"])


def writes_of(netris) -> list[tuple[str, str]]:
    return netris.writes()


# --- ExternalIPPool --------------------------------------------------------


FOREIGN_COMMON = {
    "id": 901,
    "name": f"osac-pool-{POOL}-common",
    "prefix": POOL_CIDR,
    "purpose": "common",
    "tenant": {"id": 99},
    "sites": [{"id": 1}],
}


def pool_common(netris) -> list[dict]:
    return [s for s in netris.state["ipam_subnets"] if s["name"] == f"osac-pool-{POOL}-common"]


def test_create_pool_makes_the_allocation_and_publishes_nothing(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, pool_job("create_external_ip_pool"))) == {}
    assert len(netris.state["allocations"]) == 1
    alloc = netris.state["allocations"][0]
    assert alloc["name"] == f"osac-pool-{POOL}" and alloc["prefix"] == POOL_CIDR
    assert alloc["tenant"] == {"id": TENANT_ID} and "vpc" not in alloc


def test_create_pool_wraps_the_cidr_in_a_common_subnet(tmp_path, netris) -> None:
    """Netris blackholes the return traffic of a /32 nat address without this subnet."""
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    [common] = pool_common(netris)
    assert common["prefix"] == POOL_CIDR and common["purpose"] == "common"
    assert common["tenant"] == {"id": TENANT_ID} and common["sites"] == [{"id": 1}] and "vpc" not in common


def test_create_pool_rerun_adds_a_missing_common_subnet(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    netris.state["ipam_subnets"].clear()
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    assert len(netris.state["allocations"]) == 1 and len(pool_common(netris)) == 1


def test_common_subnet_takes_no_address_from_the_pool(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    assert ok(*run(tmp_path, netris, eip_job("create_external_ip"))) == {"external_ip": {"address": ADDRESS}}


def test_create_pool_rerun_makes_no_write(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, pool_job("create_external_ip_pool"))) == {}
    assert writes_of(netris) == []


@pytest.mark.parametrize("cidrs", [[], [POOL_CIDR, "198.51.100.0/30"], ["203.0.113.1/30"], ["fd00::/64"]])
def test_create_pool_needs_exactly_one_valid_cidr(tmp_path, netris, cidrs) -> None:
    message = err(*run(tmp_path, netris, pool_job("create_external_ip_pool", cidrs)), retryable=False)
    assert "cidrs" in message
    assert writes_of(netris) == []


def test_create_pool_without_a_spec_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool", with_spec=False)), retryable=False)
    assert writes_of(netris) == []


def test_create_pool_foreign_tenant_allocation_is_final_and_untouched(tmp_path, netris) -> None:
    netris.state["allocations"].append({"id": 900, "name": f"osac-pool-{POOL}", "prefix": POOL_CIDR, "tenant": {"id": 99}})
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=False)
    assert writes_of(netris) == []


def test_create_pool_prefix_rejected_by_netris_is_final(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/ipam/allocation"] = (400, {"message": "overlaps"})
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=False)


def test_create_pool_provider_failure_is_retryable(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/ipam/allocation"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=True)


def test_create_pool_common_subnet_rejected_by_netris_is_final(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/ipam/subnet"] = (400, {"message": "overlaps"})
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=False)


def test_create_pool_common_subnet_provider_failure_is_retryable(tmp_path, netris) -> None:
    netris.state["responses"]["POST /api/v2/ipam/subnet"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=True)


def test_create_pool_foreign_tenant_common_subnet_is_final_and_untouched(tmp_path, netris) -> None:
    netris.state["ipam_subnets"].append(FOREIGN_COMMON)
    err(*run(tmp_path, netris, pool_job("create_external_ip_pool")), retryable=False)
    assert netris.state["ipam_subnets"] == [FOREIGN_COMMON]


def test_delete_pool_removes_the_common_subnet_before_the_allocation(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, pool_job("delete_external_ip_pool", with_spec=False))) == {}
    assert netris.state["allocations"] == [] and netris.state["ipam_subnets"] == []
    deletes = [path for method, path in writes_of(netris) if method == "DELETE"]
    assert [path.rsplit("/", 1)[0] for path in deletes] == ["/api/v2/ipam/subnet", "/api/v2/ipam/allocation"]


def test_delete_pool_without_a_common_subnet_removes_the_allocation(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, pool_job("create_external_ip_pool")))
    netris.state["ipam_subnets"].clear()
    ok(*run(tmp_path, netris, pool_job("delete_external_ip_pool")))
    assert netris.state["allocations"] == []


def test_delete_pool_never_deletes_a_foreign_tenant_common_subnet(tmp_path, netris) -> None:
    netris.state["ipam_subnets"].append(FOREIGN_COMMON)
    err(*run(tmp_path, netris, pool_job("delete_external_ip_pool")), retryable=False)
    assert writes_of(netris) == [] and netris.state["ipam_subnets"] == [FOREIGN_COMMON]


def test_delete_absent_pool_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, pool_job("delete_external_ip_pool"))) == {}
    assert writes_of(netris) == []


def test_delete_pool_never_deletes_a_foreign_tenant_allocation(tmp_path, netris) -> None:
    netris.state["allocations"].append({"id": 900, "name": f"osac-pool-{POOL}", "prefix": POOL_CIDR, "tenant": {"id": 99}})
    err(*run(tmp_path, netris, pool_job("delete_external_ip_pool")), retryable=False)
    assert writes_of(netris) == [] and len(netris.state["allocations"]) == 1


# --- ExternalIP ------------------------------------------------------------


def reservations(netris) -> list[dict]:
    return [s for s in netris.state["ipam_subnets"] if s["name"].startswith("osac-eip-")]


def test_create_external_ip_returns_the_address(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, eip_job("create_external_ip"))) == {"external_ip": {"address": ADDRESS}}
    [reservation] = reservations(netris)
    assert reservation["name"] == f"osac-eip-{EIP}" and reservation["prefix"] == f"{ADDRESS}/32"
    assert reservation["purpose"] == "nat" and reservation["tenant"] == {"id": TENANT_ID} and reservation["sites"] == [{"id": 1}]


def test_create_external_ip_rerun_returns_the_same_address_without_write(tmp_path, netris) -> None:
    first = ok(*run(tmp_path, netris, eip_job("create_external_ip")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, eip_job("create_external_ip"))) == first
    assert writes_of(netris) == []


def test_a_second_external_ip_gets_the_next_address(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, eip_job("create_external_ip")))
    other = eip_job("create_external_ip")
    other["resource"] = snap("ExternalIP", "0a1b2c3d-4e5f-4a6b-8c7d-9e8f7a6b5c4d", {"pool": POOL})
    assert ok(*run(tmp_path, netris, other)) == {"external_ip": {"address": "203.0.113.2"}}


def test_create_external_ip_in_a_full_pool_is_final(tmp_path, netris) -> None:
    netris.state["ipam_subnets"].append({"id": 800, "name": "osac-eip-other", "prefix": "203.0.113.5/32", "purpose": "nat",
                                         **SITE, **TENANT})
    message = err(*run(tmp_path, netris, eip_job("create_external_ip", ["203.0.113.5/32"])), retryable=False)
    assert "full" in message
    assert reservations(netris) == [netris.state["ipam_subnets"][0]]


def test_create_external_ip_needs_the_pool(tmp_path, netris) -> None:
    job = eip_job("create_external_ip")
    del job["external_ip_pool"]
    assert "external_ip_pool" in err(*run(tmp_path, netris, job), retryable=False)
    assert writes_of(netris) == []


def test_create_external_ip_pool_without_a_cidr_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, eip_job("create_external_ip", [])), retryable=False)
    assert writes_of(netris) == []


def test_create_external_ip_foreign_tenant_reservation_is_final_and_untouched(tmp_path, netris) -> None:
    netris.state["ipam_subnets"].append({"id": 900, "name": f"osac-eip-{EIP}", "prefix": "203.0.113.2/32", "purpose": "nat",
                                         **SITE, "tenant": {"id": 99}})
    err(*run(tmp_path, netris, eip_job("create_external_ip")), retryable=False)
    assert writes_of(netris) == []


def test_create_external_ip_provider_failure_is_retryable(tmp_path, netris) -> None:
    netris.state["responses"]["GET /api/v2/ipam/subnets"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, eip_job("create_external_ip")), retryable=True)


def test_delete_external_ip_releases_the_reservation(tmp_path, netris) -> None:
    ok(*run(tmp_path, netris, eip_job("create_external_ip")))
    assert ok(*run(tmp_path, netris, eip_job("delete_external_ip"))) == {}
    assert reservations(netris) == []


def test_delete_absent_external_ip_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, eip_job("delete_external_ip"))) == {}
    assert writes_of(netris) == []


def test_delete_external_ip_never_deletes_a_foreign_tenant_reservation(tmp_path, netris) -> None:
    netris.state["ipam_subnets"].append({"id": 900, "name": f"osac-eip-{EIP}", "prefix": "203.0.113.2/32", "purpose": "nat",
                                         **SITE, "tenant": {"id": 99}})
    err(*run(tmp_path, netris, eip_job("delete_external_ip")), retryable=False)
    assert writes_of(netris) == [] and len(reservations(netris)) == 1


# --- ExternalIPAttachment and NATGateway ------------------------------------


def test_attach_creates_the_dnat_rule_in_the_vpc_of_the_destination(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    assert ok(*run(tmp_path, netris, attach_job("attach_external_ip"))) == {}
    [rule] = nat_rules(netris)
    assert rule["name"] == f"osac-eipa-{EIPA}" and rule["action"] == "DNAT"
    assert rule["vpc"]["id"] == 101 and rule["site"] == {"id": 1}
    assert rule["destinationAddress"] == f"{ADDRESS}/32" and rule["dnatToIP"] == f"{TARGET}/32"


def test_attach_in_the_management_domain_uses_the_management_vpc(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, attach_job("attach_external_ip", {"type": "provider_management"}))) == {}
    [rule] = nat_rules(netris)
    assert rule["vpc"]["id"] == 3 and rule["dnatToIP"] == f"{TARGET}/32"
    assert [m for m, _ in netris.writes()] == ["POST"]


def test_attach_in_the_management_domain_needs_the_management_vpc_setting(tmp_path, netris, monkeypatch) -> None:
    monkeypatch.delitem(GOOD_ENV, "NETRIS_MGMT_VPC_ID")
    message = err(*run(tmp_path, netris, attach_job("attach_external_ip", {"type": "provider_management"})), retryable=True)
    assert "NETRIS_MGMT_VPC_ID" in message and netris.writes() == []


def test_attach_rerun_makes_no_write(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, attach_job("attach_external_ip")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, attach_job("attach_external_ip"))) == {}
    assert netris.writes() == []


def test_attach_without_the_vpc_is_final(tmp_path, netris) -> None:
    message = err(*run(tmp_path, netris, attach_job("attach_external_ip")), retryable=False)
    assert f"osac-vn-{VN}" in message and netris.writes() == []


def test_attach_does_not_reuse_a_rule_of_another_vpc(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["nats"].append({"id": 700, "name": f"osac-eipa-{EIPA}", "action": "DNAT", "vpc": {"id": 555}, "site": {"id": 1},
                                 "protocol": "all", "state": "enabled", "sourceAddress": "0.0.0.0/0", "sourcePort": "1-65535",
                                 "destinationAddress": f"{ADDRESS}/32", "destinationPort": "1-65535",
                                 "dnatToIP": f"{TARGET}/32", "dnatToPort": ""})
    err(*run(tmp_path, netris, attach_job("attach_external_ip")), retryable=False)
    assert netris.writes() == []


def test_attach_with_a_different_target_is_final(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, attach_job("attach_external_ip")))
    netris.calls.clear()
    moved = attach_job("attach_external_ip")
    moved["destination"]["address"] = "10.20.1.99"
    err(*run(tmp_path, netris, moved), retryable=False)
    assert netris.writes() == []


def test_attach_with_a_bad_destination_is_final(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    err(*run(tmp_path, netris, attach_job("attach_external_ip", {"type": "tenant_virtual_network"})), retryable=False)
    assert netris.writes() == []


def test_attach_provider_failure_is_retryable(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["responses"]["POST /api/v2/nat"] = (500, {"message": "boom"})
    err(*run(tmp_path, netris, attach_job("attach_external_ip")), retryable=True)


def test_detach_deletes_the_rule(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, attach_job("attach_external_ip")))
    assert ok(*run(tmp_path, netris, attach_job("detach_external_ip"))) == {}
    assert nat_rules(netris) == []


def test_detach_with_only_the_resource_deletes_the_rule(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, attach_job("attach_external_ip")))
    job = {"manager": MANAGER, "task": "detach_external_ip", "resource": snap("ExternalIPAttachment", EIPA)}
    assert ok(*run(tmp_path, netris, job)) == {}
    assert nat_rules(netris) == []


def test_detach_of_an_absent_rule_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, attach_job("detach_external_ip"))) == {}
    assert netris.writes() == []


def test_detach_never_deletes_a_rule_of_another_vpc(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["nats"].append({"id": 700, "name": f"osac-eipa-{EIPA}", "action": "DNAT", "vpc": {"id": 555}, "site": {"id": 1}})
    err(*run(tmp_path, netris, attach_job("detach_external_ip")), retryable=False)
    assert netris.writes() == [] and len(nat_rules(netris)) == 1


@pytest.mark.parametrize(
    ("field", "value"),
    [("dnatToIP", "10.20.1.99/32"), ("destinationAddress", "203.0.113.2/32")],
)
def test_detach_never_deletes_a_rule_with_another_address(tmp_path, netris, field, value) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, attach_job("attach_external_ip")))
    netris.calls.clear()
    nat_rules(netris)[0][field] = value
    err(*run(tmp_path, netris, attach_job("detach_external_ip")), retryable=False)
    assert netris.writes() == [] and len(nat_rules(netris)) == 1


def test_create_nat_gateway_makes_the_snat_rule(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    assert ok(*run(tmp_path, netris, nat_job("create_nat_gateway"))) == {}
    [rule] = nat_rules(netris)
    assert rule["name"] == f"osac-natgw-{NATGW}" and rule["action"] == "SNAT"
    assert rule["vpc"]["id"] == 101 and rule["site"] == {"id": 1}
    assert rule["sourceAddress"] == VN_CIDR and rule["snatToIP"] == f"{ADDRESS}/32"


def test_create_nat_gateway_ignores_the_nat_gateway_context(tmp_path, netris) -> None:
    """The operator sends nat_gateway_context to every manager; Netris takes no part of it."""
    context = {
        "external_ip": {"name": "eip", "namespace": "osac-networking", "fulfillment_id": EIP,
                        "kubernetes_uid": "uid-eip", "tenant_id": "tenant-a", "address": "198.51.100.99",
                        "state": "Allocated", "phase": "Ready"},
        "virtual_network": {"name": "vn", "namespace": "osac-networking", "fulfillment_id": VN,
                            "kubernetes_uid": "uid-vn", "tenant_id": "tenant-a", "ipv4_cidr": "192.0.2.0/24",
                            "phase": "Ready"},
    }
    job = nat_job("create_nat_gateway")
    assert nc.netris_contract_input({**job, "nat_gateway_context": context}, ["virtual_network"]) == \
        nc.netris_contract_input(job, ["virtual_network"])
    prepare_vn(tmp_path, netris)
    assert ok(*run(tmp_path, netris, {**job, "nat_gateway_context": context})) == {}
    [rule] = nat_rules(netris)
    assert rule["sourceAddress"] == VN_CIDR and rule["snatToIP"] == f"{ADDRESS}/32"


def test_create_nat_gateway_rerun_makes_no_write(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, nat_job("create_nat_gateway")))
    netris.calls.clear()
    assert ok(*run(tmp_path, netris, nat_job("create_nat_gateway"))) == {}
    assert netris.writes() == []


def test_create_nat_gateway_needs_the_virtual_network(tmp_path, netris) -> None:
    job = nat_job("create_nat_gateway")
    del job["virtual_network"]
    assert "virtual_network" in err(*run(tmp_path, netris, job), retryable=False)


def test_create_nat_gateway_without_the_vpc_is_final(tmp_path, netris) -> None:
    err(*run(tmp_path, netris, nat_job("create_nat_gateway")), retryable=False)
    assert netris.writes() == []


def test_create_nat_gateway_does_not_reuse_a_rule_of_another_vpc(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["nats"].append({"id": 700, "name": f"osac-natgw-{NATGW}", "action": "SNAT", "vpc": {"id": 555}, "site": {"id": 1},
                                 "protocol": "all", "state": "enabled", "sourceAddress": VN_CIDR, "sourcePort": "1-65535",
                                 "destinationAddress": "0.0.0.0/0", "destinationPort": "1-65535", "snatToIP": f"{ADDRESS}/32"})
    err(*run(tmp_path, netris, nat_job("create_nat_gateway")), retryable=False)
    assert netris.writes() == []


def test_create_nat_gateway_with_a_bad_address_is_final(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    job = nat_job("create_nat_gateway")
    job["external_ip"]["address"] = "nope"
    err(*run(tmp_path, netris, job), retryable=False)
    assert netris.writes() == []


def test_delete_nat_gateway_deletes_the_rule(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, nat_job("create_nat_gateway")))
    assert ok(*run(tmp_path, netris, nat_job("delete_nat_gateway"))) == {}
    assert nat_rules(netris) == []


def test_delete_nat_gateway_with_only_the_resource(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, nat_job("create_nat_gateway")))
    job = {"manager": MANAGER, "task": "delete_nat_gateway", "resource": snap("NATGateway", NATGW)}
    assert ok(*run(tmp_path, netris, job)) == {}
    assert nat_rules(netris) == []


def test_delete_absent_nat_gateway_succeeds(tmp_path, netris) -> None:
    assert ok(*run(tmp_path, netris, nat_job("delete_nat_gateway"))) == {}
    assert netris.writes() == []


def test_delete_nat_gateway_never_deletes_a_rule_of_another_vpc(tmp_path, netris) -> None:
    prepare_vn(tmp_path, netris)
    netris.state["nats"].append({"id": 700, "name": f"osac-natgw-{NATGW}", "action": "SNAT", "vpc": {"id": 555}, "site": {"id": 1}})
    err(*run(tmp_path, netris, nat_job("delete_nat_gateway")), retryable=False)
    assert netris.writes() == [] and len(nat_rules(netris)) == 1


@pytest.mark.parametrize(
    ("field", "value"),
    [("sourceAddress", "10.99.0.0/16"), ("snatToIP", "203.0.113.2/32")],
)
def test_delete_nat_gateway_never_deletes_a_rule_with_another_address(tmp_path, netris, field, value) -> None:
    prepare_vn(tmp_path, netris)
    ok(*run(tmp_path, netris, nat_job("create_nat_gateway")))
    netris.calls.clear()
    nat_rules(netris)[0][field] = value
    err(*run(tmp_path, netris, nat_job("delete_nat_gateway")), retryable=False)
    assert netris.writes() == [] and len(nat_rules(netris)) == 1


@pytest.mark.parametrize(
    "job_of",
    [
        lambda: pool_job("create_external_ip_pool"),
        lambda: eip_job("create_external_ip"),
        lambda: attach_job("attach_external_ip", {"type": "provider_management"}),
        lambda: nat_job("create_nat_gateway"),
    ],
)
def test_an_unreachable_controller_is_retryable_and_leaks_nothing(tmp_path, netris, job_of) -> None:
    netris.shutdown()
    netris.server_close()
    message = err(*run(tmp_path, netris, job_of()), retryable=True)
    assert "connect.sid" not in message
