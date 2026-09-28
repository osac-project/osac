import fcntl
import ipaddress
import json
import os
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
