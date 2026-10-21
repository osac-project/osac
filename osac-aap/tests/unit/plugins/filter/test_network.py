import pytest

import network as filter_plugin


def test_validate_single_ipv4_cidr_accepts_one_canonical_ipv4_cidr():
    assert filter_plugin.validate_single_ipv4_cidr(["192.0.2.0/24"]) == "192.0.2.0/24"


@pytest.mark.parametrize(
    "cidrs",
    [
        [],
        ["192.0.2.0/24", "198.51.100.0/24"],
        ["192.0.2.1/24"],
        ["2001:db8::/64"],
    ],
)
def test_validate_single_ipv4_cidr_rejects_unsupported_pool_values(cidrs):
    with pytest.raises(ValueError):
        filter_plugin.validate_single_ipv4_cidr(cidrs)


def test_validate_ipv4_address_in_cidr_accepts_canonical_member():
    assert filter_plugin.validate_ipv4_address_in_cidr("192.0.2.10", "192.0.2.0/24") == "192.0.2.10"


@pytest.mark.parametrize(
    ("address", "cidr"),
    [
        ("192.0.3.10", "192.0.2.0/24"),
        ("192.0.2.010", "192.0.2.0/24"),
        ("2001:db8::1", "192.0.2.0/24"),
        ("0.0.0.0", "0.0.0.0/0"),
        ("192.0.2.10", "192.0.2.1/24"),
    ],
)
def test_validate_ipv4_address_in_cidr_rejects_nonmembers_and_invalid_values(address, cidr):
    with pytest.raises(ValueError):
        filter_plugin.validate_ipv4_address_in_cidr(address, cidr)


def test_next_available_ip_returns_first_free_host():
    assert filter_plugin.next_available_ip("192.0.2.0/30", []) == "192.0.2.1"


def test_next_available_ip_skips_allocated_addresses_across_cidrs():
    assert filter_plugin.next_available_ip(
        ["192.0.2.0/30", "198.51.100.0/30"],
        ["192.0.2.1", "192.0.2.2"],
    ) == "198.51.100.1"


def test_next_available_ip_returns_none_when_all_host_addresses_are_allocated():
    assert filter_plugin.next_available_ip("192.0.2.0/30", ["192.0.2.1", "192.0.2.2"]) is None


def test_next_available_ip_accepts_nico_single_cidr_registry_mapping_when_exhausted():
    allocated_ips = {
        "192.0.2.1": "cluster-a",
        "192.0.2.2": "cluster-b",
    }

    assert filter_plugin.next_available_ip("192.0.2.0/30", allocated_ips) is None


def test_next_available_ip_stops_early_for_large_pool():
    assert filter_plugin.next_available_ip("10.0.0.0/8", ["10.0.0.1"]) == "10.0.0.2"


def test_next_available_ip_rejects_ipv6():
    with pytest.raises(ValueError, match="requires an IPv4 CIDR"):
        filter_plugin.next_available_ip("2001:db8::/120", [])
