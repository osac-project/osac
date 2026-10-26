"""Contract tests for the Fabric gateway and DHCP plan derived from ipam."""
from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest
from ansible.errors import AnsibleFilterError

PLUGIN = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/plugins/filter/subnet_dhcp_plan.py"
)

_spec = importlib.util.spec_from_file_location("subnet_dhcp_plan", PLUGIN)
assert _spec is not None and _spec.loader is not None
module = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(module)
subnet_dhcp_plan = module.subnet_dhcp_plan

GATEWAY = "10.0.1.1"


def plan(**overrides):
    ipam = {
        "gateway_ipv4": GATEWAY,
        "overlay_ipv4_cidrs": ["10.0.1.0/25"],
        "physical_ipv4_cidrs": ["10.0.1.128/26"],
        "lb_vip_ipv4_cidrs": ["10.0.1.192/26"],
    }
    ipam.update(overrides)
    return subnet_dhcp_plan(ipam)


def test_dhcp_pool_is_the_supplied_physical_range() -> None:
    result = plan()

    assert result == {
        "gateway_ipv4": GATEWAY,
        "dhcp_enabled": True,
        "dhcp_start": "10.0.1.129",
        "dhcp_end": "10.0.1.190",
    }


def test_multiple_contiguous_physical_cidrs_form_one_pool() -> None:
    result = plan(
        physical_ipv4_cidrs=["10.0.1.144/28", "10.0.1.160/27", "10.0.1.192/28"]
    )

    assert result["dhcp_start"] == "10.0.1.145"
    assert result["dhcp_end"] == "10.0.1.206"


def test_empty_physical_range_disables_dhcp() -> None:
    result = plan(physical_ipv4_cidrs=[])

    assert result == {
        "gateway_ipv4": GATEWAY,
        "dhcp_enabled": False,
        "dhcp_start": None,
        "dhcp_end": None,
    }


def test_gateway_inside_the_physical_range_is_never_offered() -> None:
    result = plan(physical_ipv4_cidrs=["10.0.1.0/29"])

    assert result["dhcp_start"] == "10.0.1.2"
    assert result["dhcp_end"] == "10.0.1.6"


def test_network_and_broadcast_addresses_are_never_offered() -> None:
    result = plan(physical_ipv4_cidrs=["10.0.1.128/28"])

    assert result["dhcp_start"] == "10.0.1.129"
    assert result["dhcp_end"] == "10.0.1.142"


@pytest.mark.parametrize(
    "physical",
    [
        ["10.0.1.128/26", "10.0.2.0/27"],
        ["10.0.1.128/26", "10.0.1.0/26"],
    ],
)
def test_disjoint_physical_ranges_are_rejected(physical: list[str]) -> None:
    with pytest.raises(AnsibleFilterError, match="contiguous"):
        plan(physical_ipv4_cidrs=physical)


@pytest.mark.parametrize(
    "physical",
    [["10.0.1.129/26"], ["10.0.1.128"], ["2001:db8::/64"]],
)
def test_non_canonical_physical_ranges_are_rejected(physical: list[str]) -> None:
    with pytest.raises(AnsibleFilterError):
        plan(physical_ipv4_cidrs=physical)


def test_a_range_without_usable_addresses_is_rejected() -> None:
    with pytest.raises(AnsibleFilterError, match="no usable"):
        plan(physical_ipv4_cidrs=["10.0.1.128/31"])


@pytest.mark.parametrize("gateway", ["10.0.1.1/24", "not-an-address", None])
def test_invalid_gateway_is_rejected(gateway) -> None:
    with pytest.raises(AnsibleFilterError):
        plan(gateway_ipv4=gateway)
