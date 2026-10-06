import fcntl
import importlib
import ipaddress
import json
import os
import sqlite3
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

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
TENANT_ONE = "tenant-one"
TENANT_TWO = "tenant-two"


def store_for(tmp_path):
    return StateStore(tmp_path / "agentless_network_state.sqlite3")


def ensure_virtual_network(store, uid, cidr, tenant_id=TENANT_ONE):
    entry, _, _ = store._ensure_virtual_network(uid, cidr, tenant_id, reconcile=False)
    return entry


def ensure_and_reconcile_virtual_network(store, uid, cidr, tenant_id=TENANT_ONE):
    return store.ensure_and_reconcile_virtual_network(uid, cidr, tenant_id)


def get_virtual_network(store, uid, tenant_id=TENANT_ONE):
    return store.get_virtual_network(uid, tenant_id)


def delete_virtual_network(store, uid, tenant_id=TENANT_ONE):
    return store.delete_and_remove_virtual_network(uid, tenant_id)


def available_subnet_cidr(parent, *, prefix=24, exclude=()):
    parent_network = ipaddress.ip_network(parent["virtual_network_cidr"])
    transit = ipaddress.ip_network(parent["transit"]["cidr"])
    excluded = [ipaddress.ip_network(cidr) for cidr in exclude]
    for candidate in parent_network.subnets(new_prefix=prefix):
        if candidate.overlaps(transit) or any(candidate.overlaps(item) for item in excluded):
            continue
        return str(candidate)
    raise AssertionError("test VirtualNetwork has no free Subnet candidate")


def reserve_subnet(
    store,
    uid=UID_TWO,
    parent_uid=UID_ONE,
    *,
    tenant_id=TENANT_ONE,
    cidr=None,
    trunk="eth1",
    vlan_start=100,
    vlan_end=199,
    vip="",
    check_mode=False,
):
    parent = store.get_virtual_network(parent_uid, tenant_id)
    if cidr is None:
        cidr = available_subnet_cidr(parent)
    return store.reserve_subnet(
        uid,
        parent_uid,
        tenant_id,
        cidr,
        trunk,
        vlan_start,
        vlan_end,
        vip,
        check_mode=check_mode,
    )


def assert_store_lock_is_free(store):
    lock_fd = os.open(store.lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        fcntl.flock(lock_fd, fcntl.LOCK_UN)
    finally:
        os.close(lock_fd)


@pytest.fixture
def observed_virtual_network(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    entry = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    namespace = entry["namespace_name"]
    host = entry["uplink"]["host_interface"]
    peer = entry["uplink"]["namespace_interface"]
    transit = entry["transit"]
    prefix = ("ip", "netns", "exec", namespace)
    observations = {
        (*prefix, "true"): "",
        ("ip", "-o", "link", "show", "dev", host): "",
        (*prefix, "ip", "-o", "link", "show", "dev", peer): "",
        ("iptables", "-w", "-t", "filter", "-S", "FORWARD"): (
            "-P FORWARD ACCEPT\n-A FORWARD -i osacvn+ -j DROP\n"
            "-A FORWARD -o osacvn+ -j DROP\n-A FORWARD -j ACCEPT\n"
        ),
        (*prefix, "iptables", "-w", "-t", "filter", "-S", "FORWARD"): "-P FORWARD ACCEPT\n",
        (*prefix, "iptables", "-w", "-t", "filter", "-C", "FORWARD",
         "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"): "",
        (*prefix, "sysctl", "-n", "net.ipv4.ip_forward"): "1\n",
        (*prefix, "ip", "-j", "-4", "route", "show", "default"): json.dumps([
            {"dst": "default", "gateway": transit["gateway"], "dev": peer}
        ]),
    }
    for interface, address, command_prefix in (
        (host, transit["host_ip"], ()),
        (peer, transit["namespace_ip"], prefix),
    ):
        observations[(*command_prefix, "ip", "-j", "-d", "link", "show", "dev", interface)] = json.dumps([
            {"ifname": interface, "flags": ["UP"], "linkinfo": {"info_kind": "veth"},
             "ifalias": f"osac-vn:{entry['uid']}" if interface == host else ""}
        ])
        observations[(*command_prefix, "ip", "-j", "-4", "address", "show", "dev", interface)] = json.dumps([
            {"ifname": interface, "addr_info": [
                {"family": "inet", "local": address.split("/")[0], "prefixlen": 31}
            ]}
        ])
        observations[("ip", "-j", "-4", "route", "get", "fibmatch", address.split("/")[0])] = '[{"dst":"default","dev":"eth0"}]'
    mutations = {
        ("ip", "address", "replace", transit["host_ip"], "dev", host),
        ("ip", "link", "set", "dev", host, "up"),
        (*prefix, "ip", "link", "set", "dev", "lo", "up"),
        (*prefix, "ip", "address", "replace", transit["namespace_ip"], "dev", peer),
        (*prefix, "ip", "link", "set", "dev", peer, "up"),
        (*prefix, "ip", "route", "replace", "default", "via", transit["gateway"], "dev", peer),
        (*prefix, "iptables", "-w", "-t", "filter", "-P", "FORWARD", "ACCEPT"),
    }

    def run(command, check=True):
        key = tuple(command)
        if key in observations:
            return subprocess.CompletedProcess(command, 0, stdout=observations[key], stderr="")
        assert key in mutations, f"Unexpected Linux command: {command!r}"
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    network = importlib.import_module(
        "ansible_collections.osac.templates.plugins.module_utils.agentless_net_network"
    )
    monkeypatch.setattr(network, "run_command", run)
    monkeypatch.setattr(agentless_net_state, "run_command", run)
    return store, entry, observations


def test_real_reconciliation_verifies_observed_namespace_and_uplink(observed_virtual_network):
    store, entry, _ = observed_virtual_network
    retry, state_changed, network_changed = ensure_and_reconcile_virtual_network(
        store, UID_ONE, "10.0.0.0/16"
    )
    assert retry == entry
    assert state_changed is False
    assert network_changed is False


@pytest.mark.parametrize("incorrect_observation", ["address", "route"])
def test_real_verification_rejects_unconverged_state_and_keeps_reservation(
    observed_virtual_network, incorrect_observation
):
    store, entry, observations = observed_virtual_network
    if incorrect_observation == "address":
        key = ("ip", "-j", "-4", "address", "show", "dev", entry["uplink"]["host_interface"])
        observations[key] = "[]"
        message = "host uplink address did not converge"
    else:
        key = ("ip", "netns", "exec", entry["namespace_name"], "ip", "-j", "-4", "route", "show", "default")
        observations[key] = "[]"
        message = "default route did not converge"
    with pytest.raises(StateError, match=message):
        ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")
    assert get_virtual_network(StateStore(store.path), UID_ONE) == entry


def test_virtual_network_retry_reuses_uid_mapping_and_canonical_slash_31(tmp_path):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    retry = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")

    transit = ipaddress.ip_network(first["transit"]["cidr"])
    assert retry == first
    assert transit.subnet_of(ipaddress.ip_network(first["virtual_network_cidr"]))
    assert transit.prefixlen == 31
    assert set(first["transit"]) == {"cidr", "namespace_ip", "host_ip", "gateway"}
    assert first["transit"]["namespace_ip"] == f"{transit.network_address + 1}/31"
    assert first["transit"]["host_ip"] == f"{transit.network_address}/31"
    assert first["transit"]["gateway"] == str(transit.network_address)
    assert first["virtual_network_cidr"] == "10.0.0.0/16"
    assert get_virtual_network(StateStore(store.path), UID_ONE) == first
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
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")

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
        ensure_virtual_network(store, uid, cidr)

    assert not store.path.exists()


def test_transit_route_conflict_keeps_saved_allocation_for_retry(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    route_command = ["ip", "-j", "-4", "route", "get", "fibmatch"]

    def conflicting_route(command, check=True):
        assert_store_lock_is_free(store)
        assert command[:-1] == route_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout=json.dumps([{"dst": f"{command[-1]}/32", "dev": "eth0"}]),
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", conflicting_route)
    with pytest.raises(StateError, match="overlaps existing host route"):
        ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")

    saved = get_virtual_network(store, UID_ONE)
    assert saved is not None
    assert saved["virtual_network_cidr"] == "10.0.0.0/16"

    def own_host_route(command, check=True):
        assert command[:-1] == route_command
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
    retry, state_changed, network_changed = ensure_and_reconcile_virtual_network(
        store,
        UID_ONE, "10.0.0.0/16"
    )

    assert retry == saved
    assert state_changed is False
    assert network_changed is False


def test_transit_route_may_override_host_default_route(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    entry = ensure_virtual_network(store, UID_ONE, "1.1.1.0/31")
    expected_command = ["ip", "-j", "-4", "route", "get", "fibmatch"]

    def default_route(command, check=True):
        assert command[:-1] == expected_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout=json.dumps([{"dst": "default", "gateway": "192.0.2.1", "dev": "eth0"}]),
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", default_route)
    agentless_net_state._assert_transit_route_available(entry)


def test_transit_route_may_override_a_less_specific_host_route(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    entry = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    expected_command = ["ip", "-j", "-4", "route", "get", "fibmatch"]

    def less_specific_route(command, check=True):
        assert command[:-1] == expected_command
        return subprocess.CompletedProcess(
            command,
            0,
            stdout='[{"dst":"10.0.0.0/16","dev":"eth0"}]',
            stderr="",
        )

    monkeypatch.setattr(agentless_net_state, "_run", less_specific_route)
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
            command, 0, stdout='[{"dst":"default","dev":"eth0"}]', stderr=""
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
            ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")
        except Exception as error:  # surfaced in the main test thread
            provider_errors.append(error)

    first_thread = threading.Thread(target=create_first)
    first_thread.start()
    assert provider_started.wait(2)

    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")
    assert second["uid"] == UID_TWO
    assert get_virtual_network(store, UID_ONE) is not None

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
            command, 0, stdout='[{"dst":"default","dev":"eth0"}]', stderr=""
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
            ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")
        except Exception as error:
            failures.append(error)

    def delete_first():
        delete_started.set()
        try:
            delete_virtual_network(store, UID_ONE)
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
    assert get_virtual_network(store, UID_ONE) is None


def test_conntrack_insert_uses_chain_before_position(tmp_path, monkeypatch):
    entry = ensure_virtual_network(store_for(tmp_path), UID_ONE, "10.0.0.0/16")
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


def test_shared_host_forwarding_rules_skip_link_scan_while_vns_remain(monkeypatch):
    monkeypatch.setattr(
        agentless_net_state,
        "_agentless_host_veth_present",
        lambda: pytest.fail("active state should avoid listing all host links"),
    )

    assert (
        agentless_net_state._remove_host_forwarding_isolation(
            remaining_virtual_network=True
        )
        is False
    )


def test_create_and_retry_run_provider_outside_state_lock_and_preserve_allocation(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)
    calls = []
    monkeypatch.setattr(
        agentless_net_state,
        "_run",
        lambda command, check=True: subprocess.CompletedProcess(
            command, 0, stdout='[{"dst":"default","dev":"eth0"}]', stderr=""
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
        ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")

    saved = get_virtual_network(store, UID_ONE)
    assert saved == calls[0]
    retry, state_changed, network_changed = ensure_and_reconcile_virtual_network(
        store,
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
        ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")
    first = get_virtual_network(store, UID_ONE)
    assert first is not None

    monkeypatch.setattr(
        agentless_net_network.subprocess,
        "run",
        lambda command, **kwargs: subprocess.CompletedProcess(
            command,
            0,
            stdout='[{"dst":"default","dev":"eth0"}]',
            stderr="",
        ),
    )
    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", lambda *args, **kwargs: False)
    retry, state_changed, _ = ensure_and_reconcile_virtual_network(store, UID_ONE, "10.0.0.0/16")
    assert retry == first
    assert state_changed is False


def test_delete_keeps_state_until_provider_cleanup_and_preserves_peer(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")
    deleted = []

    def delete_from_node(entry, **kwargs):
        assert_store_lock_is_free(store)
        assert entry == first
        assert kwargs["remaining_virtual_network"] is True
        assert get_virtual_network(store, UID_ONE) == first
        assert get_virtual_network(store, UID_TWO) == second
        deleted.append(entry)
        return True

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", delete_from_node)
    assert delete_virtual_network(store, UID_ONE) is True
    assert deleted == [first]
    assert get_virtual_network(store, UID_ONE) is None
    assert get_virtual_network(store, UID_TWO) == second

    replacement = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    assert replacement["transit"]["cidr"] == first["transit"]["cidr"]


def test_failed_delete_preserves_its_entry_and_peer(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")

    def failed_cleanup(entry, **kwargs):
        assert_store_lock_is_free(store)
        assert get_virtual_network(store, UID_ONE) == first
        raise StateError("injected cleanup failure")

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", failed_cleanup)
    with pytest.raises(StateError, match="injected cleanup failure"):
        delete_virtual_network(store, UID_ONE)

    assert get_virtual_network(store, UID_ONE) == first
    assert get_virtual_network(store, UID_TWO) == second


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
    assert delete_virtual_network(store, UID_THREE) is True
    assert len(observed) == 1
    assert not store.path.exists()


def test_absent_state_entry_without_provider_residue_is_idempotent(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", lambda *args, **kwargs: False)

    assert delete_virtual_network(store, UID_THREE) is False


def test_unknown_database_schema_fails_before_provider_cleanup(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    with sqlite3.connect(store.path) as connection:
        connection.execute("PRAGMA user_version = 99")
    cleanup_called = False

    def cleanup(entry, **kwargs):
        nonlocal cleanup_called
        cleanup_called = True
        return True

    monkeypatch.setattr(agentless_net_state, "delete_virtual_network", cleanup)
    with pytest.raises(StateCorrupt, match="unsupported state schema: 99"):
        delete_virtual_network(store, UID_ONE)
    assert cleanup_called is False


def test_state_database_rejects_unsafe_mode_and_corrupt_payload(tmp_path):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    store.path.chmod(0o644)
    with pytest.raises(StateCorrupt, match="unsafe owner or mode"):
        get_virtual_network(store, UID_ONE)

    store.path.chmod(0o600)
    with sqlite3.connect(store.path) as connection:
        connection.execute("UPDATE virtual_networks SET payload = ? WHERE uid = ?", ("{", UID_ONE))
    with pytest.raises(StateCorrupt, match="malformed VirtualNetwork state payload"):
        get_virtual_network(store, UID_ONE)


def test_smallest_virtual_network_cidr_allocates_one_transit_link(tmp_path):
    entry = ensure_virtual_network(store_for(tmp_path), UID_ONE, "10.0.0.0/31")
    assert entry["transit"]["cidr"] == "10.0.0.0/31"
    assert entry["transit"]["host_ip"] == "10.0.0.0/31"
    assert entry["transit"]["namespace_ip"] == "10.0.0.1/31"


def test_virtual_network_cidr_smaller_than_transit_link_fails_without_state(tmp_path):
    store = store_for(tmp_path)
    with pytest.raises(StateError, match="too small for a /31"):
        ensure_virtual_network(store, UID_ONE, "10.0.0.0/32")
    assert not store.path.exists()


def test_schema_rejects_non_slash_31_transit_and_noncanonical_cidr(tmp_path):
    store = store_for(tmp_path)
    entry = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    entry["transit"] = {
        "cidr": "10.0.0.0/30",
        "namespace_ip": "10.0.0.1/30",
        "host_ip": "10.0.0.0/30",
        "gateway": "10.0.0.0",
    }
    with sqlite3.connect(store.path) as connection:
        connection.execute(
            "UPDATE virtual_networks SET payload = ? WHERE uid = ?",
            (json.dumps(entry), UID_ONE),
        )
    with pytest.raises(StateCorrupt, match="transit CIDR must be an IPv4 /31"):
        get_virtual_network(store, UID_ONE)

    entry["transit"] = {
        "cidr": "10.0.0.0/31",
        "namespace_ip": "10.0.0.1/31",
        "host_ip": "10.0.0.0/31",
        "gateway": "10.0.0.0",
    }
    entry["virtual_network_cidr"] = "10.0.0.0/016"
    with sqlite3.connect(store.path) as connection:
        connection.execute(
            "UPDATE virtual_networks SET payload = ?, transit_cidr = ?, transit_start = ? WHERE uid = ?",
            (
                json.dumps(entry),
                entry["transit"]["cidr"],
                int(ipaddress.ip_network(entry["transit"]["cidr"]).network_address),
                UID_ONE,
            ),
        )
    with pytest.raises(StateCorrupt, match="VirtualNetwork CIDR must be canonical"):
        get_virtual_network(store, UID_ONE)


def test_state_database_uses_schema_v2_and_preserves_uid_and_transit_indexes(tmp_path):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")

    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 2
        assert connection.execute(
            "SELECT name FROM sqlite_master WHERE type = 'table'"
        ).fetchall() == [("virtual_networks",), ("subnets",)]
        assert connection.execute("SELECT count(*) FROM virtual_networks").fetchone()[0] == 2
        with pytest.raises(sqlite3.IntegrityError):
            connection.execute("INSERT INTO virtual_networks VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                               store._row_for_entry(first))
        with pytest.raises(sqlite3.IntegrityError):
            connection.execute("UPDATE virtual_networks SET transit_start = ? WHERE uid = ?",
                               (int(ipaddress.ip_network(first["transit"]["cidr"]).network_address), UID_TWO))

    reopened = StateStore(store.path)
    assert get_virtual_network(reopened, UID_ONE) == first
    assert get_virtual_network(reopened, UID_TWO) == second


def test_schema_v1_migrates_additively_and_check_mode_reads_without_migration(tmp_path):
    store = store_for(tmp_path)
    original = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    with sqlite3.connect(store.path) as connection:
        connection.execute("DROP TABLE subnets")
        connection.execute("PRAGMA user_version = 1")

    assert store.get_virtual_network(UID_ONE, TENANT_ONE, check_mode=True) == original
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute(
            "SELECT name FROM sqlite_master WHERE type = 'table'"
        ).fetchall() == [("virtual_networks",)]

    assert get_virtual_network(store, UID_ONE) == original
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 2
        assert connection.execute(
            "SELECT count(*) FROM subnets"
        ).fetchone()[0] == 0
    assert get_virtual_network(store, UID_ONE) == original


def test_check_mode_subnet_reservation_does_not_create_a_row(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    cidr = available_subnet_cidr(parent, prefix=30)

    proposed, changed = reserve_subnet(
        store, cidr=cidr, check_mode=True, vlan_start=321, vlan_end=321
    )

    assert changed is True
    assert proposed["vlan_id"] == 321
    assert proposed["gateway_ipv4"] == str(ipaddress.ip_network(cidr).network_address + 1)
    assert store.get_subnet(UID_TWO, TENANT_ONE) is None


def test_subnet_reservation_is_idempotent_and_vlan_ids_are_global(tmp_path):
    store = store_for(tmp_path)
    parent_one = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    parent_two = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")
    cidr = available_subnet_cidr(parent_one)

    first, changed = reserve_subnet(store, uid=UID_THREE, cidr=cidr)
    retry, retry_changed = reserve_subnet(store, uid=UID_THREE, cidr=cidr)
    peer, peer_changed = reserve_subnet(
        store,
        uid=UID_TWO,
        parent_uid=UID_TWO,
        cidr=cidr,
    )

    assert changed is True
    assert retry_changed is False
    assert retry == first
    assert peer_changed is True
    assert first["vlan_id"] == 100
    assert peer["vlan_id"] == 101
    assert peer["virtual_network_uid"] == parent_two["uid"]
    assert first["vlan_interface"].startswith("s")
    assert len(first["vlan_interface"]) <= 15


def test_subnet_retry_rejects_changed_parent_cidr_trunk_or_vip(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    other_parent = ensure_virtual_network(store, UID_THREE, "10.0.0.0/16")
    other_transit = ipaddress.ip_network(other_parent["transit"]["cidr"])
    cidr = next(
        str(candidate)
        for candidate in ipaddress.ip_network(parent["virtual_network_cidr"]).subnets(
            new_prefix=24
        )
        if not candidate.overlaps(ipaddress.ip_network(parent["transit"]["cidr"]))
        and not candidate.overlaps(other_transit)
    )
    subnet_uid = "44444444-4444-4444-8444-444444444444"
    reserve_subnet(store, uid=subnet_uid, cidr=cidr)

    with pytest.raises(StateError, match="does not match its saved allocation"):
        reserve_subnet(store, uid=subnet_uid, parent_uid=other_parent["uid"], cidr=cidr)
    with pytest.raises(StateError, match="does not match its saved allocation"):
        reserve_subnet(
            store,
            uid=subnet_uid,
            cidr=available_subnet_cidr(parent, exclude=(cidr,)),
        )
    with pytest.raises(StateError, match="does not match its saved allocation"):
        reserve_subnet(store, uid=subnet_uid, cidr=cidr, trunk="eth2")
    with pytest.raises(StateError, match="does not match its saved allocation"):
        reserve_subnet(
            store,
            uid=subnet_uid,
            cidr=cidr,
            vip=f"{ipaddress.ip_network(cidr).broadcast_address}/32",
        )


def test_subnet_reservation_checks_parent_tenant_transit_and_siblings(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    cidr = available_subnet_cidr(parent)
    reserve_subnet(store, uid=UID_TWO, cidr=cidr)

    with pytest.raises(StateError, match="saved sibling"):
        reserve_subnet(store, uid=UID_THREE, cidr=cidr)
    with pytest.raises(StateError, match="tenant does not match"):
        reserve_subnet(store, uid=UID_THREE, cidr=available_subnet_cidr(parent), tenant_id=TENANT_TWO)
    transit_supernet = ipaddress.ip_network(parent["transit"]["cidr"]).supernet(new_prefix=30)
    with pytest.raises(StateError, match="overlaps its parent VirtualNetwork transit"):
        reserve_subnet(store, uid=UID_THREE, cidr=str(transit_supernet))


def test_subnet_reservation_validates_pool_exhaustion_and_vip_host_space(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    cidr = available_subnet_cidr(parent)
    reserve_subnet(store, uid=UID_TWO, cidr=cidr, vlan_start=4094, vlan_end=4094)
    with pytest.raises(StateError, match="VLAN pool is exhausted"):
        reserve_subnet(
            store,
            uid=UID_THREE,
            cidr=available_subnet_cidr(parent, exclude=(cidr,)),
            vlan_start=4094,
            vlan_end=4094,
        )

    small_cidr = available_subnet_cidr(parent, prefix=30)
    network = ipaddress.ip_network(small_cidr)
    with pytest.raises(StateError, match="leaves no DHCP address"):
        reserve_subnet(
            store,
            uid="44444444-4444-4444-8444-444444444444",
            cidr=small_cidr,
            vip=f"{network.network_address + 2}/31",
        )


def test_subnet_delete_retains_vlan_until_release_and_blocks_parent_delete(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO)
    monkeypatch.setattr(
        agentless_net_state,
        "prepare_subnet_delete_data_plane",
        lambda *args: False,
    )
    monkeypatch.setattr(
        agentless_net_state,
        "cleanup_virtual_network_dhcp",
        lambda *args, **kwargs: False,
    )
    monkeypatch.setattr(
        agentless_net_state,
        "delete_virtual_network",
        lambda *args, **kwargs: False,
    )

    deleting, changed = store.prepare_delete_subnet(UID_TWO, TENANT_ONE, "systemd")
    assert changed is True
    assert deleting["phase"] == "deleting"
    with pytest.raises(StateError, match="already deleting"):
        reserve_subnet(store, uid=UID_TWO, cidr=subnet["ipv4_cidr"])
    with pytest.raises(StateError, match="saved Subnets remain"):
        store.delete_and_remove_virtual_network(UID_ONE, TENANT_ONE)

    released, release_changed = store.release_subnet(UID_TWO, TENANT_ONE)
    assert release_changed is True
    assert released["vlan_id"] == subnet["vlan_id"]
    assert store.get_subnet(UID_TWO, TENANT_ONE) is None
    assert store.delete_and_remove_virtual_network(UID_ONE, TENANT_ONE) is True


def test_sibling_subnet_provider_operations_share_parent_resource_lock(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    first_cidr = available_subnet_cidr(parent)
    second_cidr = available_subnet_cidr(parent, exclude=(first_cidr,))
    reserve_subnet(store, uid=UID_TWO, cidr=first_cidr)
    reserve_subnet(store, uid=UID_THREE, cidr=second_cidr)
    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", lambda *args, **kwargs: False)

    active = 0
    maximum_active = 0
    guard = threading.Lock()

    def reconcile_subnets(*args):
        nonlocal active, maximum_active
        with guard:
            active += 1
            maximum_active = max(maximum_active, active)
        time.sleep(0.08)
        with guard:
            active -= 1
        return False

    monkeypatch.setattr(agentless_net_state, "ensure_subnet_data_plane", reconcile_subnets)
    threads = [
        threading.Thread(
            target=store.ensure_and_reconcile_subnet,
            args=(uid, TENANT_ONE, "systemd"),
        )
        for uid in (UID_TWO, UID_THREE)
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=2)

    assert all(not thread.is_alive() for thread in threads)
    assert maximum_active == 1


def test_transit_address_exhaustion_preserves_existing_reservation(tmp_path):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/31")
    with pytest.raises(StateError, match="no free /31 transit block"):
        ensure_virtual_network(store, UID_TWO, "10.0.0.0/31")
    assert get_virtual_network(store, UID_ONE) == first
    assert get_virtual_network(store, UID_TWO) is None


def test_extra_development_schema_fails_before_provider_cleanup(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    with sqlite3.connect(store.path) as connection:
        connection.execute("CREATE TABLE obsolete_development_state (id INTEGER)")
    monkeypatch.setattr(agentless_net_state, "delete_virtual_network",
                        lambda *args, **kwargs: pytest.fail("invalid schema must not reach provider cleanup"))
    with pytest.raises(StateCorrupt, match="invalid schema"):
        delete_virtual_network(store, UID_ONE)
    assert store.path.exists()


@pytest.mark.parametrize("action", ["ensure_virtual_network", "delete_virtual_network"])
def test_module_failure_does_not_disclose_state_values(monkeypatch, action):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    sensitive = "dummy-sensitive-marker 10.20.0.0/16 inventory-value"
    ansible_module = SimpleNamespace(
        params={"action": action, "state_file": "/unused", "uid": UID_ONE,
                "tenant_id": TENANT_ONE, "virtual_network_cidr": "10.20.0.0/16",
                "dhcp_supervisor": "systemd"},
        check_mode=False,
        fail_json=Mock(),
        exit_json=lambda **kwargs: pytest.fail("failed provider work must not succeed"),
    )
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: ansible_module)
    store = Mock()
    store.ensure_and_reconcile_virtual_network.side_effect = module.StateError(sensitive)
    store.delete_and_remove_virtual_network.side_effect = module.StateError(sensitive)
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    module.main()

    ansible_module.fail_json.assert_called_once_with(
        msg="AgentlessNet VirtualNetwork operation failed; inspect the network node and retry."
    )
    assert sensitive not in str(ansible_module.fail_json.call_args)


def test_tenant_identity_is_checked_on_retry_read_and_delete(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16", tenant_id=TENANT_ONE)

    with pytest.raises(StateError, match="tenant does not match"):
        ensure_virtual_network(store, UID_ONE, "10.0.0.0/16", tenant_id=TENANT_TWO)
    with pytest.raises(StateError, match="tenant does not match"):
        get_virtual_network(store, UID_ONE, tenant_id=TENANT_TWO)
    monkeypatch.setattr(
        agentless_net_state,
        "delete_virtual_network",
        lambda *args, **kwargs: pytest.fail("wrong tenant must not reach provider cleanup"),
    )
    with pytest.raises(StateError, match="tenant does not match"):
        delete_virtual_network(store, UID_ONE, tenant_id=TENANT_TWO)
