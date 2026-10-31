"""Contract tests for the SecurityGroup rule plan the Netris role enforces."""
from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest

PLUGIN = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/plugins/filter/security_group_plan.py"
)

_spec = importlib.util.spec_from_file_location("security_group_plan", PLUGIN)
assert _spec is not None and _spec.loader is not None
module = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(module)
security_group_plan = module.security_group_plan

GROUP_ID = "6f0f8a9c-3f4e-4d70-9a3a-2b1c0d9e8f71"


def plan(**spec):
    return security_group_plan(spec, GROUP_ID)


@pytest.mark.parametrize(
    "protocol, expected",
    [
        ("PROTOCOL_TCP", "tcp"),
        ("PROTOCOL_UDP", "udp"),
        ("PROTOCOL_ICMP", "icmp"),
        ("PROTOCOL_ALL", "all"),
        ("tcp", "tcp"),
        ("udp", "udp"),
        ("icmp", "icmp"),
        ("all", "all"),
    ],
)
def test_protocol_api_values_map_to_the_contract_names(protocol, expected) -> None:
    rule = {"protocol": protocol, "sourceCidr": "10.0.1.0/24"}
    if expected in ("tcp", "udp"):
        rule.update({"portFrom": 22, "portTo": 22})

    result = plan(ingressRules=[rule])

    assert result["error"] == ""
    assert result["ingress"][0]["protocol"] == expected


def test_icmp_means_every_type_and_carries_no_ports() -> None:
    result = plan(ingressRules=[{"protocol": "icmp", "sourceCidr": "0.0.0.0/0"}])

    assert result["error"] == ""
    assert result["ingress"][0]["port_from"] is None
    assert result["ingress"][0]["port_to"] is None


@pytest.mark.parametrize(
    "rule",
    [
        {"protocol": "tcp", "sourceCidr": "0.0.0.0/0", "portFrom": 22},
        {"protocol": "tcp", "sourceCidr": "0.0.0.0/0", "portTo": 22},
        {"protocol": "udp", "sourceCidr": "0.0.0.0/0"},
    ],
)
def test_tcp_and_udp_require_both_ports(rule) -> None:
    result = plan(ingressRules=[rule])

    assert result["error"] == "SecurityGroup ingress rule 0: portFrom and portTo are both required"
    assert result["ingress"] == []


@pytest.mark.parametrize("protocol", ["PROTOCOL_ICMP", "PROTOCOL_ALL", "icmp", "all"])
def test_non_port_protocols_reject_ports(protocol) -> None:
    result = plan(
        ingressRules=[
            {"protocol": protocol, "sourceCidr": "0.0.0.0/0", "portFrom": 1, "portTo": 2}
        ]
    )

    assert result["error"] == (
        "SecurityGroup ingress rule 0: ports are only valid for tcp and udp rules"
    )


@pytest.mark.parametrize(
    "ports, expected",
    [
        ({"portFrom": 0, "portTo": 22}, "portFrom must be an integer from 1 to 65535"),
        ({"portFrom": 22, "portTo": 65536}, "portTo must be an integer from 1 to 65535"),
        ({"portFrom": 23, "portTo": 22}, "portFrom must not exceed portTo"),
        ({"portFrom": "22", "portTo": 22}, "portFrom must be an integer from 1 to 65535"),
        ({"portFrom": True, "portTo": 1}, "portFrom must be an integer from 1 to 65535"),
    ],
)
def test_port_ranges_are_exact(ports, expected) -> None:
    result = plan(ingressRules=[{"protocol": "tcp", "sourceCidr": "0.0.0.0/0", **ports}])

    assert result["error"] == f"SecurityGroup ingress rule 0: {expected}"


def test_ingress_requires_a_source_cidr_and_no_destination_cidr() -> None:
    assert plan(ingressRules=[{"protocol": "all"}])["error"] == (
        "SecurityGroup ingress rule 0: sourceCidr must be a non-empty IPv4 CIDR"
    )
    assert plan(
        ingressRules=[
            {"protocol": "all", "sourceCidr": "10.0.1.0/24", "destinationCidr": "10.0.2.0/24"}
        ]
    )["error"] == ("SecurityGroup ingress rule 0: destinationCidr is not valid for an ingress rule")


def test_egress_requires_a_destination_cidr_and_no_source_cidr() -> None:
    result = plan(egressRules=[{"protocol": "all", "destinationCidr": "10.0.0.0/8"}])

    assert result["error"] == ""
    assert result["egress"][0]["remote_cidr"] == "10.0.0.0/8"
    assert plan(egressRules=[{"protocol": "all"}])["error"] == (
        "SecurityGroup egress rule 0: destinationCidr must be a non-empty IPv4 CIDR"
    )
    assert plan(
        egressRules=[{"protocol": "all", "destinationCidr": "10.0.0.0/8", "sourceCidr": "10.0.1.0/24"}]
    )["error"] == ("SecurityGroup egress rule 0: sourceCidr is not valid for an egress rule")


@pytest.mark.parametrize(
    "cidr, expected",
    [
        ("10.0.1.5/24", "must be a canonical IPv4 CIDR"),
        ("2001:db8::/64", "must be IPv4"),
        ("not-a-cidr", "must be a canonical IPv4 CIDR"),
    ],
)
def test_remote_cidrs_must_be_canonical_ipv4(cidr, expected) -> None:
    result = plan(ingressRules=[{"protocol": "all", "sourceCidr": cidr}])

    assert expected in result["error"]
    assert result["error"].startswith("SecurityGroup ingress rule 0: sourceCidr")


def test_an_empty_direction_plans_no_rules() -> None:
    result = plan()

    assert result == {"ingress": [], "egress": [], "error": ""}


def test_acl_names_are_keyed_by_the_group_api_id() -> None:
    result = plan(
        ingressRules=[{"protocol": "all", "sourceCidr": "10.0.1.0/24"}],
        egressRules=[{"protocol": "all", "destinationCidr": "0.0.0.0/0"}],
    )

    assert result["ingress"][0]["name"] == f"{GROUP_ID}-ingress-0"
    assert result["egress"][0]["name"] == f"{GROUP_ID}-egress-0"


def test_a_large_rule_set_is_accepted() -> None:
    rules = [
        {"protocol": "tcp", "sourceCidr": "0.0.0.0/0", "portFrom": port, "portTo": port}
        for port in range(1, 1001)
    ]

    result = plan(ingressRules=rules)

    assert result["error"] == ""
    assert len(result["ingress"]) == 1000


@pytest.mark.parametrize(
    "spec, error",
    [
        ("not-a-mapping", "SecurityGroup spec must be a mapping"),
        ({"ingressRules": "not-a-list"}, "ingressRules must be a list"),
        ({"ingressRules": ["not-a-mapping"]}, "SecurityGroup ingress rule 0: rule must be a mapping"),
        (
            {"ingressRules": [{"protocol": "PROTOCOL_SCTP", "sourceCidr": "0.0.0.0/0"}]},
            "SecurityGroup ingress rule 0: protocol 'PROTOCOL_SCTP' is not one of",
        ),
        (
            {"egressRules": [{"sourceCidr": "0.0.0.0/0"}]},
            "SecurityGroup egress rule 0: protocol must be a string",
        ),
    ],
)
def test_malformed_input_reports_one_plain_message(spec, error) -> None:
    result = security_group_plan(spec, GROUP_ID)

    assert result["error"].startswith(error)
    assert result["ingress"] == []
    assert result["egress"] == []
