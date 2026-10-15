import pytest

import network as filter_plugin


def test_next_available_ip_returns_first_free_host():
    assert filter_plugin.next_available_ip("192.0.2.0/30", []) == "192.0.2.1"


def test_next_available_ip_skips_allocated_addresses_across_cidrs():
    assert filter_plugin.next_available_ip(
        ["192.0.2.0/30", "198.51.100.0/30"],
        ["192.0.2.1", "192.0.2.2"],
    ) == "198.51.100.1"


def test_next_available_ip_returns_none_when_all_host_addresses_are_allocated():
    assert filter_plugin.next_available_ip("192.0.2.0/30", ["192.0.2.1", "192.0.2.2"]) is None


def test_next_available_ip_stops_early_for_large_pool():
    assert filter_plugin.next_available_ip("10.0.0.0/8", ["10.0.0.1"]) == "10.0.0.2"


def test_next_available_ip_rejects_ipv6():
    with pytest.raises(ValueError, match="requires an IPv4 CIDR"):
        filter_plugin.next_available_ip("2001:db8::/120", [])
