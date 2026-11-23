from __future__ import annotations

import ipaddress
from collections.abc import Callable

import pytest

from tests.e2e.networking.storage.endpoint import (
    StorageEndpoint,
    assert_tenants_are_distinct,
    client_addresses,
    expected_network,
    from_env,
    mount_command,
    wait_for_client,
)


def _fake_run(log: str) -> Callable[[StorageEndpoint, str, int], tuple[str, int]]:
    def run(_endpoint: StorageEndpoint, command: str, _timeout: int) -> tuple[str, int]:
        return ("1\n" if command.startswith("wc -l") else log), 0

    return run


ENDPOINT = StorageEndpoint(
    server="192.168.123.10",
    export="/srv/osac-storage",
    ssh_host="osac@192.168.123.10",
    log_path="/var/log/osac-nfs-clients.log",
)


def test_from_env_requires_every_endpoint_value():
    with pytest.raises(RuntimeError, match="OSAC_STORAGE_NFS_EXPORT"):
        from_env({"OSAC_STORAGE_NFS_SERVER": "192.168.123.10"})


def test_client_addresses_ignores_ports_and_finds_both_sides():
    line = "10:00:00.1 IP 198.51.100.25.43210 > 192.168.123.10.2049: Flags [S]"
    assert client_addresses(line) == [ipaddress.IPv4Address("198.51.100.25"), ipaddress.IPv4Address("192.168.123.10")]


def test_expected_network_accepts_a_pool_cidr_and_a_single_address():
    assert expected_network("198.51.100.24/29") == ipaddress.ip_network("198.51.100.24/29")
    assert expected_network("198.51.100.25") == ipaddress.ip_network("198.51.100.25/32")


def test_wait_for_client_returns_the_nat_address_not_the_tenant_address():
    log = "IP 198.51.100.25.43210 > 192.168.123.10.2049: Flags [S]\n"

    assert wait_for_client(ENDPOINT, "198.51.100.24/29", run=_fake_run(log), timeout=0) == ipaddress.IPv4Address(
        "198.51.100.25"
    )


def test_wait_for_client_fails_when_only_the_tenant_address_is_logged():
    log = "IP 192.168.101.12.43210 > 192.168.123.10.2049: Flags [S]\n"

    with pytest.raises(TimeoutError, match="logged no client"):
        wait_for_client(ENDPOINT, "198.51.100.24/29", run=_fake_run(log), timeout=0)


def test_wait_for_client_ignores_earlier_lines():
    log = "IP 198.51.100.25.43210 > 192.168.123.10.2049: Flags [S]\n"

    with pytest.raises(TimeoutError):
        wait_for_client(ENDPOINT, "198.51.100.24/29", since=5, run=_fake_run(log), timeout=0)


def test_mount_command_uses_nfsv41_and_noresvport():
    command = mount_command(ENDPOINT, "/mnt/osac-storage")
    assert "vers=4.1" in command
    assert "noresvport" in command
    assert "192.168.123.10:/srv/osac-storage" in command


def test_two_distinct_tenant_addresses():
    assert_tenants_are_distinct([ipaddress.IPv4Address("198.51.100.25"), ipaddress.IPv4Address("198.51.100.26")])
    with pytest.raises(AssertionError):
        assert_tenants_are_distinct([ipaddress.IPv4Address("198.51.100.25")] * 2)
