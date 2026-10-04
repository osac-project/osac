import fcntl
import ipaddress
import json
import os
import subprocess
import sys
import threading
import uuid
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

import agentless_net_network  # noqa: E402
import agentless_net_state  # noqa: E402
from agentless_net_state import StateCorrupt, StateError, StateStore  # noqa: E402

UID_ONE = "11111111-1111-4111-8111-111111111111"
UID_TWO = "22222222-2222-4222-8222-222222222222"
UID_THREE = "33333333-3333-4333-8333-333333333333"


def store_for(tmp_path):
    return StateStore(tmp_path / "agentless_network_state.json")


def assert_store_lock_is_free(store):
    lock_fd = os.open(store.lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        fcntl.flock(lock_fd, fcntl.LOCK_UN)
    finally:
        os.close(lock_fd)


def write_state(path, state):
    path.write_text(json.dumps(state))
    path.chmod(0o600)


def test_virtual_network_retry_reuses_uid_mapping_and_canonical_slash_31(tmp_path):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    retry = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")

    transit = ipaddress.ip_network(first["transit"]["cidr"])
    assert retry == first
    assert transit.subnet_of(ipaddress.ip_network(first["virtual_network_cidr"]))
    assert transit.prefixlen == 31
    assert set(first["transit"]) == {"cidr", "namespace_ip", "host_ip", "gateway"}
    assert first["transit"]["namespace_ip"] == f"{transit.network_address + 1}/31"
    assert first["transit"]["host_ip"] == f"{transit.network_address}/31"
    assert first["transit"]["gateway"] == str(transit.network_address)
    assert first["virtual_network_cidr"] == "10.0.0.0/16"
    assert json.loads(store.path.read_text())["schema_version"] == agentless_net_state.SCHEMA_VERSION
    assert store.path.stat().st_mode & 0o777 == 0o600
    assert store.lock_path.exists()


def test_resource_lock_file_pool_is_bounded(tmp_path):
    store = store_for(tmp_path)

    for uid_number in range(1, agentless_net_state.RESOURCE_LOCK_SHARDS * 2 + 1):
        uid = str(uuid.UUID(int=uid_number))
        with store._resource_locked(uid):
            pass

    resource_locks = list(tmp_path.glob(f"{store.path.name}.uid-lock-*.lock"))
    assert len(resource_locks) <= agentless_net_state.RESOURCE_LOCK_SHARDS


def test_overlapping_virtual_networks_get_independent_names_and_transit(tmp_path):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    second = store.ensure_virtual_network(UID_TWO, "10.0.0.0/16")

    assert first["namespace_name"] != second["namespace_name"]
    assert first["uplink"] != second["uplink"]
    assert first["transit"]["cidr"] != second["transit"]["cidr"]
    assert not ipaddress.ip_network(first["transit"]["cidr"]).overlaps(
        ipaddress.ip_network(second["transit"]["cidr"])
    )


@pytest.mark.parametrize(
    ("uid", "cidr", "message"),
    [
        ("not-a-uuid", "10.0.0.0/16", "canonical UUID"),
        ("AAAAAAAA-1111-4111-8111-111111111111", "10.0.0.0/16", "canonical UUID"),
        (UID_ONE, "10.0.0.1/16", "invalid VirtualNetwork IPv4 CIDR"),
        (UID_ONE, "10.0.0.0/016", "canonical"),
        (UID_ONE, "10.0.0.0/16 ", "invalid VirtualNetwork IPv4 CIDR"),
        (UID_ONE, "fd00::/64", "IPv4 CIDRs"),
    ],
)
def test_invalid_uid_or_virtual_network_cidr_is_rejected(tmp_path, uid, cidr, message):
    store = store_for(tmp_path)

    with pytest.raises(StateError, match=message):
        store.ensure_virtual_network(uid, cidr)

    assert not store.path.exists()


def test_transit_route_conflict_keeps_saved_allocation_for_retry(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    route_command = ["ip", "-j", "-4", "route", "show", "table", "all"]

    def conflicting_route(command, check=True):
        assert_store_lock_is_free(store)
        assert command == route_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout='[{"dst":"10.0.0.0/8","dev":"eth0"}]',
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", conflicting_route)
    with pytest.raises(StateError, match="overlaps existing host route"):
        store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")

    saved = store.get_virtual_network(UID_ONE)
    assert saved is not None
    assert saved["virtual_network_cidr"] == "10.0.0.0/16"

    def own_host_route(command, check=True):
        assert command == route_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout=json.dumps(
                [{"dst": saved["transit"]["cidr"], "dev": saved["uplink"]["host_interface"]}]
            ),
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", own_host_route)
    monkeypatch.setattr(
        agentless_net_state,
        "reconcile_virtual_network",
        lambda entry, **kwargs: False,
    )
    retry, state_changed, network_changed = store.ensure_and_reconcile_virtual_network(
        UID_ONE, "10.0.0.0/16"
    )

    assert retry == saved
    assert state_changed is False
    assert network_changed is False


def test_transit_route_may_override_host_default_route(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    entry = store.ensure_virtual_network(UID_ONE, "1.1.1.0/31")
    expected_command = ["ip", "-j", "-4", "route", "show", "table", "all"]

    def default_route(command, check=True):
        assert command == expected_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout=json.dumps([{"dst": "default", "gateway": "192.0.2.1", "dev": "eth0"}]),
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", default_route)
    agentless_net_state._assert_transit_route_available(entry)


def test_stalled_provider_work_does_not_block_another_uid_allocation(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    provider_started = threading.Event()
    finish_provider = threading.Event()
    provider_errors = []

    monkeypatch.setattr(
        agentless_net_state,
        "_run",
        lambda command, check=True: subprocess.CompletedProcess(
            command, 0, stdout="[]", stderr=""
        ),
    )

    def reconcile(entry, **kwargs):
        assert_store_lock_is_free(store)
        provider_started.set()
        assert finish_provider.wait(5)
        return False

    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", reconcile)

    def create_first():
        try:
            store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")
        except Exception as error:  # surfaced in the main test thread
            provider_errors.append(error)

    first_thread = threading.Thread(target=create_first)
    first_thread.start()
    assert provider_started.wait(2)

    second = store.ensure_virtual_network(UID_TWO, "10.0.0.0/16")
    assert second["uid"] == UID_TWO
    assert store.get_virtual_network(UID_ONE) is not None

    finish_provider.set()
    first_thread.join(5)
    assert not first_thread.is_alive()
    assert provider_errors == []


def test_same_uid_delete_waits_for_ensure_provider_work(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    provider_started = threading.Event()
    finish_provider = threading.Event()
    delete_started = threading.Event()
    delete_finished = threading.Event()
    failures = []

    monkeypatch.setattr(
        agentless_net_state,
        "_run",
        lambda command, check=True: subprocess.CompletedProcess(
            command, 0, stdout="[]", stderr=""
        ),
    )

    def reconcile(entry, **kwargs):
        provider_started.set()
        assert finish_provider.wait(5)
        return False

    def delete_provider(entry, **kwargs):
        assert_store_lock_is_free(store)
        return True

    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", reconcile)
    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", delete_provider)

    def create_first():
        try:
            store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")
        except Exception as error:
            failures.append(error)

    def delete_first():
        delete_started.set()
        try:
            store.delete_and_remove_virtual_network(UID_ONE)
        except Exception as error:
            failures.append(error)
        finally:
            delete_finished.set()

    create_thread = threading.Thread(target=create_first)
    create_thread.start()
    assert provider_started.wait(2)
    delete_thread = threading.Thread(target=delete_first)
    delete_thread.start()
    assert delete_started.wait(1)
    assert not delete_finished.wait(0.1)

    finish_provider.set()
    create_thread.join(5)
    delete_thread.join(5)
    assert not create_thread.is_alive()
    assert not delete_thread.is_alive()
    assert failures == []
    assert store.get_virtual_network(UID_ONE) is None


def test_conntrack_insert_uses_chain_before_position(tmp_path, monkeypatch):
    entry = store_for(tmp_path).ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    commands = []

    monkeypatch.setattr(agentless_net_state, "ensure_veth_pair", lambda *args, **kwargs: False)
    monkeypatch.setattr(agentless_net_state, "configure_uplink", lambda *args: False)
    monkeypatch.setattr(agentless_net_state, "ensure_ipv4_forwarding", lambda *args: False)
    monkeypatch.setattr(agentless_net_state, "_ensure_host_forwarding_isolation", lambda *args: False)
    monkeypatch.setattr(agentless_net_state, "_verify_virtual_network", lambda *args: None)

    def iptables(command, check=True):
        commands.append(command.copy())
        if command[-3:] == ["-S", "FORWARD"]:
            return subprocess.CompletedProcess(command, 0, stdout="-P FORWARD DROP\n", stderr="")
        if "-C" in command:
            return subprocess.CompletedProcess(command, 1, stdout="", stderr="missing")
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_state, "_run", iptables)
    agentless_net_state.reconcile_virtual_network(entry)

    insertions = [command for command in commands if "-I" in command]
    assert insertions == [
        [
            "ip",
            "netns",
            "exec",
            entry["namespace_name"],
            "iptables",
            "-w",
            "-t",
            "filter",
            "-I",
            "FORWARD",
            "1",
            "-m",
            "conntrack",
            "--ctstate",
            "ESTABLISHED,RELATED",
            "-j",
            "ACCEPT",
        ]
    ], repr(insertions)


def test_host_forwarding_drop_insertion_has_valid_iptables_argument_order(monkeypatch):
    commands = []
    rules = []
    interface_pattern = f"{agentless_net_state.AGENTLESS_NET_HOST_INTERFACE_PREFIX}+"

    def iptables(command, check=True):
        commands.append(command)
        if "-S" in command:
            output = "-P FORWARD ACCEPT\n" + "".join(f"{rule}\n" for rule in rules)
            return subprocess.CompletedProcess(command, 0, stdout=output, stderr="")
        direction = next((part for part in ("-i", "-o") if part in command), None)
        interface_pattern = command[command.index(direction) + 1]
        expected = f"-A FORWARD {direction} {interface_pattern} -j DROP"
        if "-C" in command:
            return subprocess.CompletedProcess(
                command, 0 if expected in rules else 1, stdout="", stderr=""
            )
        if "-I" in command:
            rules.insert(0, expected)
        elif "-D" in command:
            rules.remove(expected)
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_state, "_run", iptables)
    assert agentless_net_state._ensure_host_forwarding_isolation() is True
    assert agentless_net_state._ensure_host_forwarding_isolation() is False

    insertions = [command for command in commands if "-I" in command]
    assert rules == [
        f"-A FORWARD -o {interface_pattern} -j DROP",
        f"-A FORWARD -i {interface_pattern} -j DROP",
    ]
    assert [command[4:] for command in insertions] == [
        ["-I", "FORWARD", "1", "-i", interface_pattern, "-j", "DROP"],
        ["-I", "FORWARD", "1", "-o", interface_pattern, "-j", "DROP"],
    ]


def test_shared_host_forwarding_rules_remain_until_last_agentless_veth_is_deleted(monkeypatch):
    interface_prefix = agentless_net_state.AGENTLESS_NET_HOST_INTERFACE_PREFIX
    rules = [
        f"-A FORWARD -i {interface_prefix}+ -j DROP",
        f"-A FORWARD -o {interface_prefix}+ -j DROP",
    ]
    links = [{"ifname": f"{interface_prefix}12345678h"}]

    def command_runner(command, check=True):
        if command == ["ip", "-j", "link", "show"]:
            return subprocess.CompletedProcess(command, 0, stdout=json.dumps(links), stderr="")
        direction = next((part for part in ("-i", "-o") if part in command), None)
        interface_pattern = command[command.index(direction) + 1]
        expected = f"-A FORWARD {direction} {interface_pattern} -j DROP"
        if "-C" in command:
            return subprocess.CompletedProcess(
                command, 0 if expected in rules else 1, stdout="", stderr=""
            )
        if "-D" in command:
            rules.remove(expected)
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_state, "_run", command_runner)
    assert agentless_net_state._remove_host_forwarding_isolation() is False
    assert len(rules) == 2

    links.clear()
    assert agentless_net_state._remove_host_forwarding_isolation() is True
    assert rules == []


def test_create_and_retry_run_provider_outside_state_lock_and_preserve_allocation(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)
    calls = []
    monkeypatch.setattr(
        agentless_net_state,
        "_run",
        lambda command, check=True: subprocess.CompletedProcess(
            command, 0, stdout="[]", stderr=""
        ),
    )

    def reconcile(entry, **kwargs):
        assert_store_lock_is_free(store)
        calls.append(entry)
        if len(calls) == 1:
            raise StateError("injected provider setup failure")
        return False

    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", reconcile)
    with pytest.raises(StateError, match="injected provider setup failure"):
        store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")

    saved = store.get_virtual_network(UID_ONE)
    assert saved == calls[0]
    retry, state_changed, network_changed = store.ensure_and_reconcile_virtual_network(
        UID_ONE, "10.0.0.0/16"
    )
    assert retry == saved
    assert state_changed is False
    assert network_changed is False
    assert calls == [saved, saved]


def test_command_timeout_returns_error_and_keeps_create_retryable(tmp_path, monkeypatch):
    store = store_for(tmp_path)

    def timeout(command, **kwargs):
        assert kwargs["timeout"] == agentless_net_network.COMMAND_TIMEOUT_SECONDS
        raise subprocess.TimeoutExpired(command, kwargs["timeout"])

    monkeypatch.setattr(agentless_net_network.subprocess, "run", timeout)
    with pytest.raises(agentless_net_network.NetworkCommandError, match="timed out"):
        agentless_net_network.run_command(["ip", "-j", "route"])

    with pytest.raises(StateError, match="timed out"):
        store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")
    first = store.get_virtual_network(UID_ONE)
    assert first is not None

    monkeypatch.setattr(
        agentless_net_network.subprocess,
        "run",
        lambda command, **kwargs: subprocess.CompletedProcess(command, 0, stdout="[]", stderr=""),
    )
    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", lambda *args, **kwargs: False)
    retry, state_changed, _ = store.ensure_and_reconcile_virtual_network(UID_ONE, "10.0.0.0/16")
    assert retry == first
    assert state_changed is False


def test_delete_keeps_state_until_provider_cleanup_and_preserves_peer(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    second = store.ensure_virtual_network(UID_TWO, "10.0.0.0/16")
    deleted = []

    def delete_from_node(entry, **kwargs):
        assert_store_lock_is_free(store)
        assert entry == first
        current = json.loads(store.path.read_text())
        assert first in current["virtual_networks"]
        assert second in current["virtual_networks"]
        deleted.append(entry)
        return True

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", delete_from_node)
    assert store.delete_and_remove_virtual_network(UID_ONE) is True
    assert deleted == [first]
    assert store.get_virtual_network(UID_ONE) is None
    assert store.get_virtual_network(UID_TWO) == second

    replacement = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    assert replacement["transit"]["cidr"] == first["transit"]["cidr"]


def test_failed_delete_preserves_its_entry_and_peer(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    first = store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    second = store.ensure_virtual_network(UID_TWO, "10.0.0.0/16")

    def failed_cleanup(entry, **kwargs):
        assert_store_lock_is_free(store)
        assert first in json.loads(store.path.read_text())["virtual_networks"]
        raise StateError("injected cleanup failure")

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", failed_cleanup)
    with pytest.raises(StateError, match="injected cleanup failure"):
        store.delete_and_remove_virtual_network(UID_ONE)

    assert store.get_virtual_network(UID_ONE) == first
    assert store.get_virtual_network(UID_TWO) == second


def test_delete_cleans_deterministic_residue_even_when_state_entry_is_absent(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)
    observed = []

    def cleanup_residue(entry, **kwargs):
        assert entry["uid"] == UID_THREE
        assert kwargs["require_alias"] is True
        assert_store_lock_is_free(store)
        observed.append(entry)
        return True

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", cleanup_residue)
    assert store.delete_and_remove_virtual_network(UID_THREE) is True
    assert len(observed) == 1
    assert not store.path.exists()


def test_absent_state_entry_without_provider_residue_is_idempotent(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", lambda *args, **kwargs: False)

    assert store.delete_and_remove_virtual_network(UID_THREE) is False


def test_corrupt_state_or_surviving_backup_fails_before_provider_cleanup(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    write_state(store.path, {"schema_version": 99, "virtual_networks": []})
    cleanup_called = False

    def cleanup(entry, **kwargs):
        nonlocal cleanup_called
        cleanup_called = True
        return True

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", cleanup)
    with pytest.raises(StateCorrupt, match="unsupported state schema"):
        store.delete_and_remove_virtual_network(UID_ONE)
    assert cleanup_called is False

    store.path.unlink()
    store.backup_path.write_text("{}")
    store.backup_path.chmod(0o600)
    with pytest.raises(StateCorrupt, match="backup exists"):
        store.delete_and_remove_virtual_network(UID_ONE)
    assert cleanup_called is False


def test_state_file_rejects_unsafe_mode_and_unknown_schema(tmp_path):
    store = store_for(tmp_path)
    store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    store.path.chmod(0o644)
    with pytest.raises(StateCorrupt, match="unsafe owner or mode"):
        store.get_virtual_network(UID_ONE)

    store.path.chmod(0o600)
    write_state(store.path, {"schema_version": 2, "virtual_networks": []})
    with pytest.raises(StateCorrupt, match="unsupported state schema: 2"):
        store.get_virtual_network(UID_ONE)


def test_smallest_virtual_network_cidr_allocates_one_transit_link(tmp_path):
    entry = store_for(tmp_path).ensure_virtual_network(UID_ONE, "10.0.0.0/31")
    assert entry["transit"]["cidr"] == "10.0.0.0/31"
    assert entry["transit"]["host_ip"] == "10.0.0.0/31"
    assert entry["transit"]["namespace_ip"] == "10.0.0.1/31"


def test_virtual_network_cidr_smaller_than_transit_link_fails_without_state(tmp_path):
    store = store_for(tmp_path)
    with pytest.raises(StateError, match="too small for a /31"):
        store.ensure_virtual_network(UID_ONE, "10.0.0.0/32")
    assert not store.path.exists()


def test_schema_rejects_non_slash_31_transit_and_noncanonical_cidr(tmp_path):
    store = store_for(tmp_path)
    store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    state = json.loads(store.path.read_text())
    entry = state["virtual_networks"][0]
    entry["transit"] = {
        "cidr": "10.0.0.0/30",
        "namespace_ip": "10.0.0.1/30",
        "host_ip": "10.0.0.0/30",
        "gateway": "10.0.0.0",
    }
    write_state(store.path, state)
    with pytest.raises(StateCorrupt, match="transit CIDR must be an IPv4 /31"):
        store.get_virtual_network(UID_ONE)

    entry["transit"] = {
        "cidr": "10.0.0.0/31",
        "namespace_ip": "10.0.0.1/31",
        "host_ip": "10.0.0.0/31",
        "gateway": "10.0.0.0",
    }
    entry["virtual_network_cidr"] = "10.0.0.0/016"
    write_state(store.path, state)
    with pytest.raises(StateCorrupt, match="VirtualNetwork CIDR must be canonical"):
        store.get_virtual_network(UID_ONE)


def test_successful_write_keeps_last_valid_backup(tmp_path):
    store = store_for(tmp_path)
    store.ensure_virtual_network(UID_ONE, "10.0.0.0/16")
    previous = json.loads(store.path.read_text())
    store.ensure_virtual_network(UID_TWO, "10.0.0.0/16")

    assert json.loads(store.backup_path.read_text()) == previous
    assert store.backup_path.stat().st_mode & 0o777 == 0o600
