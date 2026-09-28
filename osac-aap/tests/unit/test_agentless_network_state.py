import fcntl
import ipaddress
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

MODULE_UTILS = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "module_utils"
)
sys.path.insert(0, str(MODULE_UTILS))
sys.path.insert(0, str(MODULE_UTILS.parents[4]))

import agentless_net_state
from agentless_net_state import StateCorrupt, StateError, StateStore  # noqa: E402


def assert_store_lock_is_held(store):
    lock_fd = os.open(store.lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        with pytest.raises(BlockingIOError):
            fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    finally:
        os.close(lock_fd)


def store_for(tmp_path):
    return StateStore(tmp_path / "agentless_network_state.json")


def test_virtual_network_retry_reuses_uid_mapping_and_transit(tmp_path):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network("vn-uid", "10.0.0.0/16")

    retry = store.ensure_virtual_network("vn-uid", "10.0.0.0/16")

    assert retry == first
    transit_cidr = ipaddress.ip_network(first["transit"]["cidr"])
    assert transit_cidr.subnet_of(ipaddress.ip_network(first["virtual_network_cidr"]))
    assert transit_cidr.prefixlen == 30
    assert first["virtual_network_cidr"] == "10.0.0.0/16"
    assert "external_interface" not in first["transit"]
    assert first["external_reachability"] == {
        "mode": "bgp",
        "route_prefix_length": 32,
    }
    assert first["default_forward_policy"] == "permit_all"
    assert json.loads(store.path.read_text())["schema_version"] == 3
    assert store.lock_path.exists()


def test_overlapping_virtual_networks_get_independent_names_and_transit(tmp_path):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network("vn-one", "10.0.0.0/16")
    second = store.ensure_virtual_network("vn-two", "10.0.0.0/16")

    assert first["namespace_name"] != second["namespace_name"]
    assert first["uplink"] != second["uplink"]
    assert first["transit"]["cidr"] != second["transit"]["cidr"]
    assert not ipaddress.ip_network(first["transit"]["cidr"]).overlaps(
        ipaddress.ip_network(second["transit"]["cidr"])
    )


def test_transit_allocation_rejects_existing_host_route_before_writing_state(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)

    def existing_host_route(command, check=True):
        assert command == ["ip", "-j", "-4", "route", "show", "table", "all"]
        return subprocess.CompletedProcess(
            command,
            0,
            stdout='[{"dst":"10.0.0.0/8","dev":"eth0"}]',
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", existing_host_route)

    with pytest.raises(StateError, match="overlaps existing host route"):
        store.ensure_and_reconcile_virtual_network("vn-one", "10.0.0.0/16")

    assert not store.path.exists()


def test_transit_retry_allows_its_own_host_veth_route(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    entry = store.ensure_virtual_network("vn-one", "10.0.0.0/16")

    def own_host_route(command, check=True):
        assert command == ["ip", "-j", "-4", "route", "show", "table", "all"]
        return subprocess.CompletedProcess(
            command,
            0,
            stdout=json.dumps(
                [
                    {
                        "dst": entry["transit"]["cidr"],
                        "dev": entry["uplink"]["host_interface"],
                    }
                ]
            ),
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", own_host_route)
    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", lambda _: False)

    retry, state_changed, network_changed = store.ensure_and_reconcile_virtual_network(
        "vn-one", "10.0.0.0/16"
    )

    assert retry == entry
    assert state_changed is False
    assert network_changed is False


def test_host_forwarding_isolation_rules_are_interface_scoped(monkeypatch):
    commands = []
    rules = []

    def iptables(command, check=True):
        commands.append(command)
        if "-S" in command:
            output = "-P FORWARD ACCEPT\n" + "".join(f"{rule}\n" for rule in rules)
            return subprocess.CompletedProcess(command, 0, stdout=output, stderr="")
        direction = next((part for part in ("-i", "-o") if part in command), None)
        if "-C" in command:
            return subprocess.CompletedProcess(
                command,
                0
                if f"-A FORWARD {direction} vnet-host -j DROP" in rules
                else 1,
                stdout="",
                stderr="",
            )
        if "-I" in command:
            rules.insert(0, f"-A FORWARD {direction} vnet-host -j DROP")
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_state, "_run", iptables)

    changed = agentless_net_state._ensure_host_forwarding_isolation("vnet-host")

    inserted = [command for command in commands if "-I" in command]
    assert changed is True
    assert "-A FORWARD -i vnet-host -j DROP" in rules
    assert "-A FORWARD -o vnet-host -j DROP" in rules
    assert all(
        command[4:11] == ["-I", "FORWARD", "1", direction, "vnet-host", "-j", "DROP"]
        for command, direction in zip(inserted, ("-i", "-o"), strict=True)
    )


def test_host_forwarding_is_blocked_before_host_ip_forwarding_is_enabled(
    tmp_path, monkeypatch
):
    entry = store_for(tmp_path).ensure_virtual_network("vn-one", "10.0.0.0/16")
    events = []
    rules = []

    monkeypatch.setattr(
        agentless_net_state,
        "ensure_veth_pair",
        lambda *args: events.append("veth-pair") or False,
    )
    monkeypatch.setattr(
        agentless_net_state,
        "configure_uplink",
        lambda *args: events.append("address-and-route") or False,
    )

    def enable_forwarding(*args, **kwargs):
        events.append("forwarding")
        assert kwargs == {}
        return True

    def iptables(command, check=True):
        if "-S" in command:
            output = "-P FORWARD ACCEPT\n" + "".join(f"{rule}\n" for rule in rules)
            return subprocess.CompletedProcess(
                command, 0, stdout=output, stderr=""
            )
        if "-C" in command:
            direction = next((part for part in ("-i", "-o") if part in command), None)
            if direction is None:
                return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
            return subprocess.CompletedProcess(
                command,
                0
                if (
                    f"-A FORWARD {direction} {entry['uplink']['host_interface']} -j DROP"
                    in rules
                )
                else 1,
                stdout="",
                stderr="",
            )
        if "-I" in command:
            direction = next(part for part in ("-i", "-o") if part in command)
            rules.insert(
                0,
                f"-A FORWARD {direction} {entry['uplink']['host_interface']} -j DROP",
            )
            events.append("host-filter")
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_state, "ensure_ipv4_forwarding", enable_forwarding)
    monkeypatch.setattr(agentless_net_state, "_run", iptables)
    monkeypatch.setattr(agentless_net_state, "_verify_virtual_network", lambda _: None)

    agentless_net_state.reconcile_virtual_network(entry)

    assert events.index("veth-pair") < events.index("host-filter")
    assert events.index("host-filter") < events.index("address-and-route")
    assert events.index("host-filter") < events.index("forwarding")


def test_remove_virtual_network_releases_only_its_saved_transit_entry(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network("vn-one", "10.0.0.0/16")
    second = store.ensure_virtual_network("vn-two", "10.0.0.0/16")

    deleted = []

    def delete_from_node(entry):
        assert_store_lock_is_held(store)
        assert entry in json.loads(store.path.read_text())["virtual_networks"]
        deleted.append(entry)

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", delete_from_node)
    assert store.delete_and_remove_virtual_network("vn-one") is True
    assert store.delete_and_remove_virtual_network("vn-one") is False
    assert deleted == [first]
    assert store.get_virtual_network("vn-one") is None
    assert store.get_virtual_network("vn-two") == second
    replacement = store.ensure_virtual_network("vn-one", "10.0.0.0/16")
    assert replacement["transit"]["cidr"] == first["transit"]["cidr"]


def test_small_virtual_network_cidr_fails_without_writing_state(tmp_path):
    store = store_for(tmp_path)

    with pytest.raises(StateError, match="too small for a /30"):
        store.ensure_virtual_network("vn-one", "10.0.0.0/31")

    assert not store.path.exists()


def test_invalid_state_fails_closed_and_successful_write_keeps_backup(tmp_path):
    store = store_for(tmp_path)
    store.path.write_text('{"schema_version": 99}')

    with pytest.raises(StateCorrupt, match="unsupported state schema"):
        store.get_virtual_network("vn-one")
    assert store.path.read_text() == '{"schema_version": 99}'

    store.path.unlink()
    store.ensure_virtual_network("vn-one", "10.0.0.0/16")
    previous = json.loads(store.path.read_text())
    store.ensure_virtual_network("vn-two", "10.0.0.0/16")

    assert json.loads(store.backup_path.read_text()) == previous


def test_create_and_retry_reconcile_inside_locked_transaction(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    reconciled = []

    def reconcile_on_node(entry):
        assert_store_lock_is_held(store)
        assert json.loads(store.path.read_text())["virtual_networks"] == [entry]
        reconciled.append(entry)
        return len(reconciled) == 1

    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", reconcile_on_node)
    monkeypatch.setattr(
        agentless_net_state,
        "_run",
        lambda command, check=True: subprocess.CompletedProcess(
            command, 0, stdout="[]", stderr=""
        ),
    )

    entry, state_changed, network_changed = store.ensure_and_reconcile_virtual_network("vn-uid", "10.0.0.0/16")
    assert state_changed is True
    assert network_changed is True

    retry, state_changed, network_changed = store.ensure_and_reconcile_virtual_network("vn-uid", "10.0.0.0/16")
    assert retry == entry
    assert state_changed is False
    assert network_changed is False
    assert reconciled == [entry, entry]


def test_legacy_state_is_migrated_and_cr_cidr_is_recorded(tmp_path):
    store = store_for(tmp_path)
    store.ensure_virtual_network("vn-legacy", "10.0.0.0/16")
    legacy = json.loads(store.path.read_text())
    legacy["schema_version"] = 2
    legacy_entry = legacy["virtual_networks"][0]
    legacy_entry.pop("virtual_network_cidr")
    legacy_entry["transit"] = {
        "cidr": "198.51.100.0/30",
        "namespace_ip": "198.51.100.1/30",
        "host_ip": "198.51.100.2/30",
        "next_hop": "198.51.100.1",
        "gateway": "198.51.100.2",
        "external_interface": "eth0",
    }
    legacy_payload = json.dumps(legacy, indent=2, sort_keys=True) + "\n"
    store.path.write_text(legacy_payload)

    migrated = store.ensure_virtual_network("vn-legacy", "10.0.0.0/16")
    current = json.loads(store.path.read_text())

    assert migrated["virtual_network_cidr"] == "10.0.0.0/16"
    assert ipaddress.ip_network(migrated["transit"]["cidr"]).subnet_of(
        ipaddress.ip_network("10.0.0.0/16")
    )
    assert "external_interface" not in migrated["transit"]
    assert current["schema_version"] == 3
    assert json.loads(store.backup_path.read_text()) == legacy
