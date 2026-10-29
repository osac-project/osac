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
from concurrent.futures import ThreadPoolExecutor
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
import agentless_net_subnet  # noqa: E402
import agentless_net_state  # noqa: E402
from agentless_net_state import StateCorrupt, StateError, StateStore  # noqa: E402

UID_ONE = "11111111-1111-4111-8111-111111111111"
UID_TWO = "22222222-2222-4222-8222-222222222222"
UID_THREE = "33333333-3333-4333-8333-333333333333"
TENANT_ONE = "tenant-one"
TENANT_TWO = "tenant-two"
POOL_UID = "44444444-4444-4444-8444-444444444444"
POOL_FULFILLMENT_ID = "55555555-5555-4555-8555-555555555555"
POOL_CIDR = "198.51.100.0/24"
ATTACHMENT_UID = "66666666-6666-4666-8666-666666666666"
ATTACHMENT_FULFILLMENT_ID = "77777777-7777-4777-8777-777777777777"
EXTERNAL_IP_FULFILLMENT_ID = "88888888-8888-4888-8888-888888888888"
SUBNET_FULFILLMENT_ID = "99999999-9999-4999-8999-999999999999"
VIRTUAL_NETWORK_FULFILLMENT_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
BARE_METAL_INSTANCE_UID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
BARE_METAL_INSTANCE_FULFILLMENT_ID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
NAT_GATEWAY_UID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
NAT_GATEWAY_FULFILLMENT_ID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
SECOND_SUBNET_UID = "ffffffff-ffff-4fff-8fff-ffffffffffff"


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


def register_external_ip_pool(store, *, tenant_id=TENANT_ONE, cidr=POOL_CIDR):
    return store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        tenant_id,
        "IPv4",
        [cidr],
    )


def reserve_external_address(
    store,
    uid,
    fulfillment_id,
    *,
    tenant_id=TENANT_ONE,
    pool_uid=POOL_UID,
    pool_fulfillment_id=POOL_FULFILLMENT_ID,
    pool_tenant_id=TENANT_ONE,
    pool_cidr=POOL_CIDR,
    check_mode=False,
):
    return store.reserve_external_ip(
        uid,
        fulfillment_id,
        tenant_id,
        pool_uid,
        pool_fulfillment_id,
        pool_tenant_id,
        pool_cidr,
        check_mode=check_mode,
    )


def external_ip_attachment_context(
    store, *, target_ip="10.0.1.10", external_ip_uid=UID_THREE
):
    subnet = store.get_subnet(UID_TWO, TENANT_ONE)
    with sqlite3.connect(store.path) as connection:
        external_ip = connection.execute(
            "SELECT fulfillment_id, address FROM external_ips WHERE uid = ?",
            (external_ip_uid,),
        ).fetchone()
    assert external_ip is not None
    parent = store.get_virtual_network(UID_ONE, TENANT_ONE)
    return {
        "external_ip": {
            "name": "test-eip",
            "namespace": "test-networking",
            "fulfillment_id": external_ip[0],
            "kubernetes_uid": external_ip_uid,
            "tenant_id": TENANT_ONE,
            "address": external_ip[1],
            "state": "Allocated",
            "phase": "Ready",
        },
        "target": {
            "kind": "BareMetalInstance",
            "name": "test-bmi",
            "namespace": "bmaas",
            "fulfillment_id": BARE_METAL_INSTANCE_FULFILLMENT_ID,
            "kubernetes_uid": BARE_METAL_INSTANCE_UID,
            "tenant_id": TENANT_ONE,
            "ip_address": target_ip,
            "ready": True,
        },
        "subnet": {
            "name": "test-subnet",
            "namespace": "test-networking",
            "fulfillment_id": SUBNET_FULFILLMENT_ID,
            "kubernetes_uid": UID_TWO,
            "tenant_id": TENANT_ONE,
            "ipv4_cidr": subnet["ipv4_cidr"],
            "virtual_network_id": VIRTUAL_NETWORK_FULFILLMENT_ID,
            "phase": "Ready",
        },
        "virtual_network": {
            "name": "test-network",
            "namespace": "test-networking",
            "fulfillment_id": VIRTUAL_NETWORK_FULFILLMENT_ID,
            "kubernetes_uid": UID_ONE,
            "tenant_id": TENANT_ONE,
            "phase": "Ready",
        },
    }


def nat_gateway_context(store):
    with sqlite3.connect(store.path) as connection:
        address = connection.execute(
            "SELECT address FROM external_ips WHERE uid = ?", (UID_THREE,)
        ).fetchone()[0]
    parent = store.get_virtual_network(UID_ONE, TENANT_ONE)
    return {
        "external_ip": {
            "name": "test-eip",
            "namespace": "test-networking",
            "fulfillment_id": EXTERNAL_IP_FULFILLMENT_ID,
            "kubernetes_uid": UID_THREE,
            "tenant_id": TENANT_ONE,
            "address": address,
            "state": "Allocated",
            "phase": "Ready",
        },
        "virtual_network": {
            "name": "test-network",
            "namespace": "test-networking",
            "fulfillment_id": VIRTUAL_NETWORK_FULFILLMENT_ID,
            "kubernetes_uid": UID_ONE,
            "tenant_id": TENANT_ONE,
            "ipv4_cidr": parent["virtual_network_cidr"],
            "phase": "Ready",
        },
    }


def prepare_attachment_state(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO, parent_uid=UID_ONE, cidr="10.0.1.0/24")
    mark_subnet_ready(store, subnet)
    register_external_ip_pool(store)
    reserve_external_address(store, UID_THREE, EXTERNAL_IP_FULFILLMENT_ID)
    monkeypatch.setattr(agentless_net_state, "reconcile_virtual_network", lambda *args, **kwargs: False)
    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "reconcile_external_ip_attachment",
        lambda *args, **kwargs: False,
    )
    return store


def prepare_nat_gateway_state(tmp_path, monkeypatch, *, with_subnet=True):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    if with_subnet:
        subnet, _ = reserve_subnet(store, uid=UID_TWO, parent_uid=UID_ONE, cidr="10.0.1.0/24")
        mark_subnet_ready(store, subnet)
    register_external_ip_pool(store)
    reserve_external_address(store, UID_THREE, EXTERNAL_IP_FULFILLMENT_ID)
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "reconcile_nat_gateway",
        lambda *_args, **_kwargs: False,
    )
    return store


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
    observed_vlan_ids=(),
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
        set(observed_vlan_ids),
        vip,
        check_mode=check_mode,
    )


def mark_subnet_ready(store, subnet):
    ready = {**subnet, "phase": "ready"}
    with sqlite3.connect(store.path) as connection:
        connection.execute(
            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
            (
                "ready",
                json.dumps(ready, sort_keys=True, separators=(",", ":")),
                subnet["uid"],
            ),
        )
    return ready


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


def test_state_database_uses_current_schema_v1_and_preserves_uid_and_transit_indexes(tmp_path):
    store = store_for(tmp_path)
    first = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    second = ensure_virtual_network(store, UID_TWO, "10.0.0.0/16")

    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == {
            "virtual_networks",
            "subnets",
            "external_ip_pools",
            "external_ips",
            "attachments",
            "nat_gateways",
        }
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


def test_incomplete_schema_v1_fails_closed_without_modification(tmp_path):
    store = store_for(tmp_path)
    original = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    with sqlite3.connect(store.path) as connection:
        connection.execute("DROP TABLE IF EXISTS external_ips")
        connection.execute("DROP TABLE IF EXISTS external_ip_pools")
        connection.execute("DROP TABLE IF EXISTS attachments")
        connection.execute("DROP TABLE IF EXISTS nat_gateways")
        connection.execute("DROP TABLE subnets")
        connection.execute("PRAGMA user_version = 1")

    with pytest.raises(StateCorrupt, match="invalid schema"):
        store.get_virtual_network(UID_ONE, TENANT_ONE, check_mode=True)
    with pytest.raises(StateCorrupt, match="invalid schema"):
        store.get_virtual_network(UID_ONE, TENANT_ONE)

    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute(
            "SELECT name FROM sqlite_master WHERE type = 'table'"
        ).fetchall() == [("virtual_networks",)]
        assert connection.execute("SELECT count(*) FROM virtual_networks").fetchone()[0] == 1
    assert original["uid"] == UID_ONE


@pytest.mark.parametrize("check_mode", [False, True], ids=["normal", "check-mode"])
def test_previous_v1_schema_adds_consumer_tables_without_losing_state(
    tmp_path, check_mode
):
    store = store_for(tmp_path)
    original, _ = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )
    with sqlite3.connect(store.path) as connection:
        connection.execute("DROP TABLE attachments")
        connection.execute("DROP TABLE nat_gateways")
        connection.execute("PRAGMA user_version = 1")
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == agentless_net_state.PREVIOUS_V1_SCHEMA_TABLES

    migrated, changed = store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=check_mode,
    )

    assert migrated == original
    assert changed is False
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == agentless_net_state.SCHEMA_TABLES
        attachment_indexes = {
            row[1] for row in connection.execute("PRAGMA index_list(attachments)")
        }
        assert {"attachments_by_vn", "attachments_by_subnet"}.issubset(
            attachment_indexes
        )
        assert connection.execute("SELECT count(*) FROM attachments").fetchone() == (0,)
        assert connection.execute("SELECT count(*) FROM nat_gateways").fetchone() == (0,)
        assert connection.execute("SELECT count(*) FROM external_ip_pools").fetchone() == (1,)


@pytest.mark.parametrize("check_mode", [False, True], ids=["normal", "check-mode"])
def test_attachment_schema_v1_adds_nat_gateway_table_without_losing_state(
    tmp_path, check_mode
):
    store = store_for(tmp_path)
    original, _ = register_external_ip_pool(store)
    with sqlite3.connect(store.path) as connection:
        connection.execute("DROP TABLE nat_gateways")
        connection.execute("PRAGMA user_version = 1")
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == agentless_net_state.PRE_NAT_GATEWAY_V1_SCHEMA_TABLES

    migrated, changed = store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=check_mode,
    )

    assert migrated == original
    assert changed is False
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == agentless_net_state.SCHEMA_TABLES
        assert connection.execute("SELECT count(*) FROM nat_gateways").fetchone() == (0,)


def test_invalid_previous_v1_schema_is_rejected_without_modification(tmp_path):
    store = store_for(tmp_path)
    store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )
    with sqlite3.connect(store.path) as connection:
        connection.execute("DROP TABLE attachments")
        connection.execute("DROP TABLE nat_gateways")
        connection.execute("ALTER TABLE external_ip_pools ADD COLUMN unexpected TEXT")
        connection.execute("PRAGMA user_version = 1")
    original_database = store.path.read_bytes()

    with pytest.raises(StateCorrupt, match="external_ip_pools table has an invalid schema"):
        store.ensure_external_ip_pool(
            POOL_UID,
            POOL_FULFILLMENT_ID,
            TENANT_ONE,
            "IPv4",
            [POOL_CIDR],
            check_mode=True,
        )

    assert store.path.read_bytes() == original_database


def test_schema_v2_fails_closed_without_downgrade_or_modification(tmp_path):
    store = store_for(tmp_path)
    original = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    with sqlite3.connect(store.path) as connection:
        connection.execute("PRAGMA user_version = 2")

    with pytest.raises(StateCorrupt, match="unsupported state schema: 2"):
        get_virtual_network(store, UID_ONE)

    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 2
        assert connection.execute("SELECT count(*) FROM virtual_networks").fetchone()[0] == 1
        assert connection.execute("SELECT count(*) FROM subnets").fetchone()[0] == 0
    assert original["uid"] == UID_ONE


def test_external_ip_pool_registers_identity_in_current_schema_v1(tmp_path):
    store = store_for(tmp_path)

    pool, changed = store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
    )

    assert changed is True
    assert pool == {
        "uid": POOL_UID,
        "fulfillment_id": POOL_FULFILLMENT_ID,
        "tenant_id": TENANT_ONE,
        "ip_family": "IPv4",
        "ipv4_cidr": POOL_CIDR,
    }
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute(
            "SELECT uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr "
            "FROM external_ip_pools"
        ).fetchall() == [
            (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", POOL_CIDR)
        ]
        assert {
            row[0]
            for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )
        } == {
            "virtual_networks",
            "subnets",
            "external_ip_pools",
            "external_ips",
            "attachments",
            "nat_gateways",
        }


def test_external_ip_pool_registration_retry_preserves_saved_identity(tmp_path):
    store = store_for(tmp_path)
    first, first_changed = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    retry, retry_changed = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    assert first_changed is True
    assert retry_changed is False
    assert retry == first
    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT count(*) FROM external_ip_pools"
        ).fetchone() == (1,)


def test_external_ip_pool_schema_has_required_identity_uniqueness(tmp_path):
    store = store_for(tmp_path)
    store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    with sqlite3.connect(store.path) as connection:
        columns = {
            row[1]: row for row in connection.execute("PRAGMA table_info(external_ip_pools)")
        }
        unique_columns = set()
        for index_row in connection.execute("PRAGMA index_list(external_ip_pools)"):
            if not index_row[2] or index_row[4]:
                continue
            quoted_index = "'" + index_row[1].replace("'", "''") + "'"
            index_info = connection.execute(
                f"PRAGMA index_info({quoted_index})"
            ).fetchall()
            if len(index_info) == 1:
                unique_columns.add(index_info[0][2])

        assert set(columns) == {
            "uid",
            "fulfillment_id",
            "tenant_id",
            "ip_family",
            "ipv4_cidr",
        }
        assert all(row[2].upper() == "TEXT" and row[3] == 1 for row in columns.values())
        assert unique_columns == {"uid", "fulfillment_id"}

        connection.execute("BEGIN")
        with pytest.raises(sqlite3.IntegrityError):
            connection.execute(
                "INSERT INTO external_ip_pools "
                "(uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr) "
                "VALUES (?, ?, ?, ?, ?)",
                (
                    UID_TWO,
                    POOL_FULFILLMENT_ID,
                    TENANT_ONE,
                    "IPv4",
                    "203.0.113.0/24",
                ),
            )
        connection.rollback()


def test_malformed_saved_external_ip_pool_is_not_repaired(tmp_path):
    store = store_for(tmp_path)
    store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )
    with sqlite3.connect(store.path) as connection:
        connection.execute(
            "UPDATE external_ip_pools SET ipv4_cidr = ? WHERE uid = ?",
            ("198.51.100.1/24", POOL_UID),
        )

    with pytest.raises(StateCorrupt, match="malformed ExternalIPPool state"):
        store.ensure_external_ip_pool(
            POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
        )

    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT ipv4_cidr FROM external_ip_pools WHERE uid = ?", (POOL_UID,)
        ).fetchone() == ("198.51.100.1/24",)


@pytest.mark.parametrize(
    ("uid", "fulfillment_id", "tenant_id", "ip_family", "cidrs"),
    [
        ("not-a-uuid", POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]),
        ("AAAAAAAA-1111-4111-8111-111111111111", POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]),
        (POOL_UID, "not-a-uuid", TENANT_ONE, "IPv4", [POOL_CIDR]),
        (POOL_UID, "AAAAAAAA-1111-4111-8111-111111111111", TENANT_ONE, "IPv4", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, "", "IPv4", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, " tenant-one ", "IPv4", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv6", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IP_FAMILY_IPV4", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "", [POOL_CIDR]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", []),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR, "203.0.113.0/24"]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", "198.51.100.0/24"),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [None]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", ["198.51.100.1/24"]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", ["198.51.100.0/024"]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", ["198.51.100.0/24 "]),
        (POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", ["2001:db8::/64"]),
    ],
)
def test_invalid_external_ip_pool_identity_or_cidr_creates_no_state(
    tmp_path, uid, fulfillment_id, tenant_id, ip_family, cidrs
):
    store = store_for(tmp_path)

    with pytest.raises(StateError):
        store.ensure_external_ip_pool(uid, fulfillment_id, tenant_id, ip_family, cidrs)

    assert not store.path.exists()


@pytest.mark.parametrize(
    ("fulfillment_id", "tenant_id", "cidrs"),
    [
        ("77777777-7777-4777-8777-777777777777", TENANT_ONE, [POOL_CIDR]),
        (POOL_FULFILLMENT_ID, TENANT_TWO, [POOL_CIDR]),
        (POOL_FULFILLMENT_ID, TENANT_ONE, ["203.0.113.0/24"]),
    ],
)
def test_conflicting_external_ip_pool_retry_never_overwrites_saved_identity(
    tmp_path, fulfillment_id, tenant_id, cidrs
):
    store = store_for(tmp_path)
    original, _ = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    with pytest.raises(StateError, match="identity does not match saved state"):
        store.ensure_external_ip_pool(POOL_UID, fulfillment_id, tenant_id, "IPv4", cidrs)

    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr "
            "FROM external_ip_pools"
        ).fetchall() == [
            (
                original["uid"],
                original["fulfillment_id"],
                original["tenant_id"],
                original["ip_family"],
                original["ipv4_cidr"],
            )
        ]


def test_invalid_external_ip_pool_retry_does_not_change_existing_state(tmp_path):
    store = store_for(tmp_path)
    original, _ = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    with pytest.raises(StateError):
        store.ensure_external_ip_pool(
            POOL_UID,
            POOL_FULFILLMENT_ID,
            TENANT_ONE,
            "IPv4",
            ["198.51.100.1/24"],
        )

    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr "
            "FROM external_ip_pools"
        ).fetchall() == [
            (
                original["uid"],
                original["fulfillment_id"],
                original["tenant_id"],
                original["ip_family"],
                original["ipv4_cidr"],
            )
        ]


def test_external_ip_pool_fulfillment_identity_is_unique(tmp_path):
    store = store_for(tmp_path)
    store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    with pytest.raises(StateError, match="Fulfillment identity is already registered"):
        store.ensure_external_ip_pool(
            UID_TWO, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", ["203.0.113.0/24"]
        )

    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT uid, fulfillment_id FROM external_ip_pools"
        ).fetchall() == [(POOL_UID, POOL_FULFILLMENT_ID)]


def test_external_ip_pool_check_mode_reads_without_creating_state(tmp_path):
    store = store_for(tmp_path)

    pool, changed = store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=True,
    )

    assert pool["uid"] == POOL_UID
    assert changed is True
    assert not store.path.exists()
    assert list(tmp_path.iterdir()) == []
    assert store.delete_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=True,
    ) == (None, False)
    assert list(tmp_path.iterdir()) == []


def test_external_ip_pool_check_mode_preserves_existing_identity(tmp_path):
    store = store_for(tmp_path)
    original, _ = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )

    pool, changed = store.ensure_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=True,
    )

    assert pool == original
    assert changed is False
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute("SELECT count(*) FROM external_ip_pools").fetchone() == (1,)


def test_external_ip_pool_delete_validates_identity_and_is_idempotent(tmp_path):
    store = store_for(tmp_path)
    primary, _ = store.ensure_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )
    peer, _ = store.ensure_external_ip_pool(
        "66666666-6666-4666-8666-666666666666",
        "77777777-7777-4777-8777-777777777777",
        TENANT_TWO,
        "IPv4",
        ["203.0.113.0/24"],
    )
    with pytest.raises(StateError, match="identity does not match saved state"):
        store.delete_external_ip_pool(
            POOL_UID, POOL_FULFILLMENT_ID, TENANT_TWO, "IPv4", [POOL_CIDR]
        )

    assert store.delete_external_ip_pool(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=True,
    ) == (primary, True)

    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT uid FROM external_ip_pools WHERE uid = ?", (POOL_UID,)
        ).fetchone() == (POOL_UID,)
        assert connection.execute(
            "SELECT uid FROM external_ip_pools WHERE uid = ?", (peer["uid"],)
        ).fetchone() == (peer["uid"],)

    assert store.delete_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    ) == (primary, True)
    assert store.delete_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    ) == (None, False)
    assert store.delete_external_ip_pool(
        peer["uid"], peer["fulfillment_id"], peer["tenant_id"], "IPv4", [peer["ipv4_cidr"]]
    ) == (peer, True)


def test_external_ip_pool_delete_of_absent_database_does_not_create_state(tmp_path):
    store = store_for(tmp_path)

    assert store.delete_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    ) == (None, False)
    assert not store.path.exists()


def test_external_ip_pool_simultaneous_retry_creates_one_row(tmp_path):
    store = store_for(tmp_path)
    start = threading.Barrier(2)

    def register():
        start.wait(timeout=5)
        return store.ensure_external_ip_pool(
            POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
        )

    with ThreadPoolExecutor(max_workers=2) as executor:
        futures = [executor.submit(register) for _ in range(2)]
        results = [future.result(timeout=10) for future in futures]

    assert sorted(changed for _, changed in results) == [False, True]
    with sqlite3.connect(store.path) as connection:
        assert connection.execute(
            "SELECT count(*) FROM external_ip_pools"
        ).fetchone() == (1,)


def test_subnet_lifecycle_stays_functional_at_schema_v1(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO)

    assert store.get_subnet(UID_TWO, TENANT_ONE) == subnet
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute(
            "SELECT count(*) FROM subnets WHERE uid = ?", (UID_TWO,)
        ).fetchone() == (1,)

    monkeypatch.setattr(agentless_net_state, "prepare_subnet_delete_data_plane", lambda *args: False)
    deleting, changed = store.prepare_delete_subnet(UID_TWO, TENANT_ONE, "systemd")
    assert changed is True
    assert deleting["phase"] == "deleting"
    with pytest.raises(StateError, match="saved Subnets remain"):
        delete_virtual_network(store, UID_ONE)


def test_query_dhcp_lease_returns_matching_ready_subnet_lease_without_writes(
    tmp_path, monkeypatch
):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO, cidr="10.0.1.0/24")
    subnet = mark_subnet_ready(store, subnet)
    lease_root = tmp_path / "dhcp"
    lease_dir = lease_root / UID_ONE
    lease_dir.mkdir(parents=True, mode=0o700)
    lease_dir.chmod(0o700)
    marker = lease_dir / ".initialized"
    marker.write_text("initialized\n")
    marker.chmod(0o600)
    lease_file = lease_dir / "dnsmasq.leases"
    lease_file.write_bytes(
        b"200 aa:bb:cc:dd:ee:ff 10.0.1.22 client-a *\n"
        b"200 02:00:00:00:00:02 10.0.2.22 client-b *\n"
    )
    lease_file.chmod(0o600)
    monkeypatch.setattr(
        agentless_net_state,
        "dhcp_lease_path",
        lambda uid: lease_root / uid / "dnsmasq.leases",
    )
    database_before = store.path.read_bytes()
    lease_before = lease_file.read_bytes()

    result = store.query_dhcp_lease(
        UID_TWO, TENANT_ONE, "AA:BB:CC:DD:EE:FF", now=100
    )

    assert result == {
        "ip_address": "10.0.1.22",
        "mac_address": "aa:bb:cc:dd:ee:ff",
    }
    assert store.path.read_bytes() == database_before
    assert lease_file.read_bytes() == lease_before


def test_query_dhcp_lease_returns_none_for_missing_subnet_or_match(tmp_path):
    store = store_for(tmp_path)

    assert store.query_dhcp_lease(UID_TWO, TENANT_ONE, "aa:bb:cc:dd:ee:ff") is None


def test_query_dhcp_lease_rejects_wrong_tenant_and_non_ready_subnet(tmp_path):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    reserve_subnet(store, uid=UID_TWO, cidr="10.0.1.0/24")

    with pytest.raises(StateError, match="tenant"):
        store.query_dhcp_lease(UID_TWO, TENANT_TWO, "aa:bb:cc:dd:ee:ff")
    with pytest.raises(StateError, match="ready Subnet"):
        store.query_dhcp_lease(UID_TWO, TENANT_ONE, "aa:bb:cc:dd:ee:ff")


def test_query_dhcp_lease_rejects_missing_initialized_lease_file(tmp_path, monkeypatch):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO, cidr="10.0.1.0/24")
    mark_subnet_ready(store, subnet)
    monkeypatch.setattr(
        agentless_net_state,
        "dhcp_lease_path",
        lambda uid: tmp_path / "dhcp" / uid / "dnsmasq.leases",
    )

    with pytest.raises(agentless_net_state.SubnetProviderError, match="lease directory"):
        store.query_dhcp_lease(UID_TWO, TENANT_ONE, "aa:bb:cc:dd:ee:ff")


def test_check_mode_subnet_reservation_does_not_create_a_row(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    cidr = available_subnet_cidr(parent, prefix=30)

    proposed, changed = reserve_subnet(
        store, cidr=cidr, check_mode=True, observed_vlan_ids=range(1, 321)
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
    assert first["vlan_id"] == 1
    assert peer["vlan_id"] == 2
    assert peer["virtual_network_uid"] == parent_two["uid"]
    assert first["vlan_interface"].startswith("s")
    assert len(first["vlan_interface"]) <= 15


def test_internal_vlan_allocator_preserves_saved_ids_and_reuses_them_on_retry(tmp_path):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    first_cidr = available_subnet_cidr(parent)
    saved, changed = reserve_subnet(
        store, uid=UID_TWO, cidr=first_cidr, observed_vlan_ids=range(1, 100)
    )
    next_subnet = reserve_subnet(
        store,
        uid=UID_THREE,
        cidr=available_subnet_cidr(parent, exclude=(first_cidr,)),
    )[0]
    retry, retry_changed = reserve_subnet(
        store,
        uid=UID_TWO,
        cidr=first_cidr,
        observed_vlan_ids=range(1, 4095),
    )

    assert changed is True
    assert saved["vlan_id"] == 100
    assert next_subnet["vlan_id"] == 1
    assert retry == saved
    assert retry_changed is False


@pytest.mark.parametrize("invalid_vlan", [0, 4095, True, "1"])
def test_subnet_state_rejects_invalid_vlan_ids(tmp_path, invalid_vlan):
    store = store_for(tmp_path)
    ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")
    subnet, _ = reserve_subnet(store, uid=UID_TWO)

    with pytest.raises(StateCorrupt, match="Subnet VLAN ID"):
        store._validate_subnet_entry({**subnet, "vlan_id": invalid_vlan})


@pytest.mark.parametrize("invalid_vlan", [0, 4095, True, "1"])
def test_allocator_rejects_malformed_observed_vlan_ids(tmp_path, invalid_vlan):
    store = store_for(tmp_path)
    parent = ensure_virtual_network(store, UID_ONE, "10.0.0.0/16")

    with pytest.raises(StateError, match="Observed Cumulus VLAN ID"):
        reserve_subnet(
            store,
            uid=UID_TWO,
            cidr=available_subnet_cidr(parent),
            observed_vlan_ids={invalid_vlan},
        )


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
    reserve_subnet(
        store, uid=UID_TWO, cidr=cidr, observed_vlan_ids=range(1, 4094)
    )
    with pytest.raises(StateError, match="VLAN range is exhausted"):
        reserve_subnet(
            store,
            uid=UID_THREE,
            cidr=available_subnet_cidr(parent, exclude=(cidr,)),
            observed_vlan_ids=range(1, 4094),
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


@pytest.mark.parametrize(
    ("action", "method"),
    [
        ("ensure_external_ip_pool", "ensure_external_ip_pool"),
        ("delete_external_ip_pool", "delete_external_ip_pool"),
    ],
)
def test_external_ip_pool_module_dispatches_validated_identity_and_check_mode(
    monkeypatch, action, method
):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    params = {
        "action": action,
        "state_file": "/unused/state.sqlite3",
        "uid": POOL_UID,
        "fulfillment_id": POOL_FULFILLMENT_ID,
        "tenant_id": TENANT_ONE,
        "ip_family": "IPv4",
        "cidrs": [POOL_CIDR],
    }
    pool = {
        "uid": POOL_UID,
        "fulfillment_id": POOL_FULFILLMENT_ID,
        "tenant_id": TENANT_ONE,
        "ip_family": "IPv4",
        "ipv4_cidr": POOL_CIDR,
    }

    class ModuleExit(Exception):
        pass

    calls = {}

    def exit_json(**kwargs):
        raise ModuleExit(kwargs)

    module_object = SimpleNamespace(
        params=params,
        check_mode=True,
        exit_json=exit_json,
        fail_json=lambda **kwargs: pytest.fail(f"unexpected module error: {kwargs}"),
    )
    monkeypatch.setattr(
        module, "AnsibleModule", lambda **kwargs: calls.update(kwargs) or module_object
    )
    store = Mock()
    getattr(store, method).return_value = (pool, False)
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    with pytest.raises(ModuleExit) as result:
        module.main()

    assert result.value.args[0] == {
        "changed": False,
        "backend_network_id": POOL_UID,
        "pool": pool,
    }
    getattr(store, method).assert_called_once_with(
        POOL_UID,
        POOL_FULFILLMENT_ID,
        TENANT_ONE,
        "IPv4",
        [POOL_CIDR],
        check_mode=True,
    )
    assert (
        "action",
        action,
        ["fulfillment_id", "ip_family", "cidrs"],
    ) in calls["required_if"]


@pytest.mark.parametrize(
    ("action", "method"),
    [
        ("ensure_external_ip_pool", "ensure_external_ip_pool"),
        ("delete_external_ip_pool", "delete_external_ip_pool"),
    ],
)
def test_external_ip_pool_module_errors_do_not_disclose_state_values(
    monkeypatch, action, method
):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    sensitive = "dummy-sensitive-marker 198.51.100.0/24 tenant-secret"
    module_object = SimpleNamespace(
        params={
            "action": action,
            "state_file": "/unused/state.sqlite3",
            "uid": POOL_UID,
            "fulfillment_id": POOL_FULFILLMENT_ID,
            "tenant_id": TENANT_ONE,
            "ip_family": "IPv4",
            "cidrs": [POOL_CIDR],
        },
        check_mode=False,
        exit_json=lambda **kwargs: pytest.fail("failed pool operation must not succeed"),
        fail_json=Mock(),
    )
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: module_object)
    store = Mock()
    getattr(store, method).side_effect = module.StateError(sensitive)
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    module.main()

    module_object.fail_json.assert_called_once_with(
        msg="AgentlessNet ExternalIPPool operation failed; inspect the network node and retry."
    )
    assert sensitive not in str(module_object.fail_json.call_args)


def test_external_ip_pool_delete_module_omits_pool_when_state_is_absent(monkeypatch):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )

    class ModuleExit(Exception):
        pass

    def exit_json(**kwargs):
        raise ModuleExit(kwargs)

    module_object = SimpleNamespace(
        params={
            "action": "delete_external_ip_pool",
            "state_file": "/unused/state.sqlite3",
            "uid": POOL_UID,
            "fulfillment_id": POOL_FULFILLMENT_ID,
            "tenant_id": TENANT_ONE,
            "ip_family": "IPv4",
            "cidrs": [POOL_CIDR],
        },
        check_mode=False,
        exit_json=exit_json,
        fail_json=lambda **kwargs: pytest.fail(f"unexpected module error: {kwargs}"),
    )
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: module_object)
    store = Mock()
    store.delete_external_ip_pool.return_value = (None, False)
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    with pytest.raises(ModuleExit) as result:
        module.main()

    assert result.value.args[0] == {
        "changed": False,
        "backend_network_id": POOL_UID,
    }


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



def test_external_ip_attachment_creates_schema_v1_state_and_is_idempotent(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)

    attachment, changed = store.ensure_external_ip_attachment(
        ATTACHMENT_UID,
        ATTACHMENT_FULFILLMENT_ID,
        TENANT_ONE,
        context,
        "eth0",
    )

    assert changed is True
    assert attachment["phase"] == "ready"
    assert attachment["external_ip_uid"] == UID_THREE
    assert attachment["subnet_uid"] == UID_TWO
    assert attachment["virtual_network_uid"] == UID_ONE
    assert attachment["provider"]["next_hop"] == get_virtual_network(store, UID_ONE)["transit"]["namespace_ip"]
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("PRAGMA user_version").fetchone()[0] == 1
        assert connection.execute("SELECT count(*) FROM attachments").fetchone()[0] == 1

    retried, changed = store.ensure_external_ip_attachment(
        ATTACHMENT_UID,
        ATTACHMENT_FULFILLMENT_ID,
        TENANT_ONE,
        context,
        "eth0",
    )
    assert changed is False
    assert retried == attachment


def test_external_ip_attachment_rejects_duplicate_fulfillment_id(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)
    store.ensure_external_ip_attachment(
        ATTACHMENT_UID,
        ATTACHMENT_FULFILLMENT_ID,
        TENANT_ONE,
        context,
        "eth0",
    )
    duplicate_uid = "88888888-8888-4888-8888-888888888888"
    duplicate_external_ip_fulfillment_id = "99999999-9999-4999-8999-999999999999"
    reserve_external_address(store, duplicate_uid, duplicate_external_ip_fulfillment_id)
    duplicate_context = external_ip_attachment_context(store, external_ip_uid=duplicate_uid)

    with pytest.raises(StateError, match="Fulfillment identity is already reserved"):
        store.ensure_external_ip_attachment(
            "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
            ATTACHMENT_FULFILLMENT_ID,
            TENANT_ONE,
            duplicate_context,
            "eth0",
        )


def test_external_ip_attachment_changed_target_cleans_old_path_first(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)
    events = []

    def record_delete(entry):
        events.append(("delete", entry["context"]["target"]["ip_address"]))
        return True

    def record_reconcile(parent, subnet, entry):
        events.append(("ensure", entry["context"]["target"]["ip_address"]))
        return True

    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "delete_external_ip_attachment",
        record_delete,
    )
    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "reconcile_external_ip_attachment",
        record_reconcile,
    )
    store.ensure_external_ip_attachment(
        ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE, context, "eth0"
    )

    replacement_context = external_ip_attachment_context(store, target_ip="10.0.1.11")
    store.ensure_external_ip_attachment(
        ATTACHMENT_UID,
        ATTACHMENT_FULFILLMENT_ID,
        TENANT_ONE,
        replacement_context,
        "eth0",
    )
    assert events == [
        ("ensure", "10.0.1.10"),
        ("delete", "10.0.1.10"),
        ("ensure", "10.0.1.11"),
    ]


def test_external_ip_attachment_failure_retains_saved_cleanup_state(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)

    def fail_reconcile(*_args, **_kwargs):
        raise agentless_net_state.NetworkCommandError("iptables failed")

    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "reconcile_external_ip_attachment",
        fail_reconcile,
    )
    with pytest.raises(StateError, match="iptables failed"):
        store.ensure_external_ip_attachment(
            ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE, context, "eth0"
        )

    with sqlite3.connect(store.path) as connection:
        payload = json.loads(connection.execute(
            "SELECT payload FROM attachments WHERE uid = ?", (ATTACHMENT_UID,)
        ).fetchone()[0])
    assert payload["phase"] == "provisioning"
    assert payload["context"] == context


def test_external_ip_attachment_blocks_dependency_release_until_cleanup(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)
    store.ensure_external_ip_attachment(
        ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE, context, "eth0"
    )

    with pytest.raises(StateError, match="ExternalIPAttachment"):
        store.release_external_ip(UID_THREE, EXTERNAL_IP_FULFILLMENT_ID, TENANT_ONE, POOL_UID)
    with pytest.raises(StateError, match="ExternalIPAttachment"):
        store.prepare_delete_subnet(UID_TWO, TENANT_ONE)
    with pytest.raises(StateError, match="ExternalIPAttachments"):
        store.delete_and_remove_virtual_network(UID_ONE, TENANT_ONE)

    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "delete_external_ip_attachment",
        lambda *_args, **_kwargs: True,
    )
    removed, changed = store.delete_external_ip_attachment(
        ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE
    )
    assert changed is True
    assert removed["phase"] == "deleting"
    assert store.delete_external_ip_attachment(
        ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE
    ) == (None, False)
    assert store.release_external_ip(
        UID_THREE, EXTERNAL_IP_FULFILLMENT_ID, TENANT_ONE, POOL_UID
    )[1] is True


def test_external_ip_attachment_rejects_target_outside_subnet(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store, target_ip="10.0.2.10")
    with pytest.raises(StateError, match="outside its canonical IPv4 Subnet"):
        store.ensure_external_ip_attachment(
            ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE, context, "eth0"
        )
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM attachments").fetchone()[0] == 0


def test_external_ip_attachment_delete_failure_retains_saved_state(tmp_path, monkeypatch):
    store = prepare_attachment_state(tmp_path, monkeypatch)
    context = external_ip_attachment_context(store)
    store.ensure_external_ip_attachment(
        ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE, context, "eth0"
    )

    def fail_delete(_entry):
        raise agentless_net_state.NetworkCommandError("route withdrawal failed")

    monkeypatch.setattr(
        agentless_net_state.agentless_net_external_ip,
        "delete_external_ip_attachment",
        fail_delete,
    )
    with pytest.raises(StateError, match="route withdrawal failed"):
        store.delete_external_ip_attachment(
            ATTACHMENT_UID, ATTACHMENT_FULFILLMENT_ID, TENANT_ONE
        )
    with sqlite3.connect(store.path) as connection:
        row = connection.execute(
            "SELECT phase, payload FROM attachments WHERE uid = ?", (ATTACHMENT_UID,)
        ).fetchone()
    assert row[0] == "deleting"
    assert json.loads(row[1])["context"] == context


def test_nat_gateway_saves_uid_owned_state_and_retries_idempotently(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    context = nat_gateway_context(store)
    events = []

    def reconcile(parent, gateway):
        events.append((parent["uid"], gateway["uid"]))
        return False

    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "reconcile_nat_gateway",
        reconcile,
    )

    gateway, changed, provider_changed = store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        context,
        "eth0",
    )

    assert changed is True
    assert provider_changed is False
    assert gateway["phase"] == "ready"
    assert gateway["external_ip_uid"] == UID_THREE
    assert gateway["virtual_network_uid"] == UID_ONE
    assert set(gateway) == agentless_net_state.NAT_GATEWAY_KEYS
    assert events == [(UID_ONE, NAT_GATEWAY_UID)]
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM nat_gateways").fetchone() == (1,)

    retried, changed, provider_changed = store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        context,
        "eth0",
    )
    assert changed is False
    assert provider_changed is False
    assert retried == gateway
    assert len(events) == 2


def test_nat_gateway_ensures_with_zero_subnets(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch, with_subnet=False)
    gateway, changed, provider_changed = store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    assert changed is True
    assert provider_changed is False
    assert gateway["phase"] == "ready"
    assert set(gateway) == agentless_net_state.NAT_GATEWAY_KEYS
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM subnets").fetchone() == (0,)


def test_nat_gateway_check_mode_does_not_query_subnets_or_write_state(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch, with_subnet=False)
    monkeypatch.setattr(
        store,
        "_subnets_for_virtual_network",
        lambda *_args: pytest.fail("check mode must not query Subnets"),
    )
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "reconcile_nat_gateway",
        lambda *_args: pytest.fail("check mode must not invoke provider commands"),
    )
    requested, changed, provider_changed = store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
        check_mode=True,
    )
    assert changed is True
    assert provider_changed is False
    assert requested["phase"] == "provisioning"
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM nat_gateways").fetchone() == (0,)


def test_legacy_nat_gateway_payload_fails_closed(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    with sqlite3.connect(store.path) as connection:
        row = connection.execute(
            "SELECT payload FROM nat_gateways WHERE uid = ?", (NAT_GATEWAY_UID,)
        ).fetchone()
        payload = json.loads(row[0])
        payload["source_cidrs"] = []
        connection.execute(
            "UPDATE nat_gateways SET payload = ? WHERE uid = ?",
            (json.dumps(payload, sort_keys=True, separators=(",", ":")), NAT_GATEWAY_UID),
        )
    with pytest.raises(StateCorrupt, match="NATGateway state entry"):
        store.ensure_nat_gateway(
            NAT_GATEWAY_UID,
            NAT_GATEWAY_FULFILLMENT_ID,
            TENANT_ONE,
            nat_gateway_context(store),
            "eth0",
        )


def test_subnet_delete_does_not_reconcile_nat_gateway(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    events = []
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "reconcile_nat_gateway",
        lambda *_args: pytest.fail("Subnet deletion must not reconcile NATGateway"),
    )
    monkeypatch.setattr(
        agentless_net_state,
        "prepare_subnet_delete_data_plane",
        lambda *_args: events.append(("subnet",)) or False,
    )

    deleting, changed = store.prepare_delete_subnet(UID_TWO, TENANT_ONE, "systemd")

    assert changed is True
    assert deleting["phase"] == "deleting"
    assert events == [("subnet",)]
    with sqlite3.connect(store.path) as connection:
        gateway = store._nat_gateway_for_uid(connection, NAT_GATEWAY_UID)
    assert set(gateway) == agentless_net_state.NAT_GATEWAY_KEYS
    assert store.release_subnet(UID_TWO, TENANT_ONE)[1] is True


def test_subnet_ensure_does_not_reconcile_nat_gateway(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    parent = get_virtual_network(store, UID_ONE)
    first_subnet = store.get_subnet(UID_TWO, TENANT_ONE)
    second_subnet, _ = reserve_subnet(
        store,
        uid=SECOND_SUBNET_UID,
        parent_uid=UID_ONE,
        cidr=available_subnet_cidr(parent, exclude=(first_subnet["ipv4_cidr"],)),
    )
    events = []

    monkeypatch.setattr(
        agentless_net_state,
        "reconcile_virtual_network",
        lambda *_args, **_kwargs: events.append(("virtual-network",)) or False,
    )
    monkeypatch.setattr(
        agentless_net_state,
        "ensure_subnet_data_plane",
        lambda _parent, active_subnets, _supervisor: events.append(
            ("subnet-provider", [item["uid"] for item in active_subnets])
        ) or False,
    )
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "reconcile_nat_gateway",
        lambda *_args: pytest.fail("Subnet ensure must not reconcile NATGateway"),
    )

    ready_subnet, state_changed, provider_changed = store.ensure_and_reconcile_subnet(
        SECOND_SUBNET_UID, TENANT_ONE, "systemd"
    )

    assert ready_subnet["phase"] == "ready"
    assert state_changed is True
    assert provider_changed is False
    assert [event[0] for event in events] == [
        "virtual-network", "subnet-provider"
    ]
    with sqlite3.connect(store.path) as connection:
        gateway = store._nat_gateway_for_uid(connection, NAT_GATEWAY_UID)
    assert set(gateway) == agentless_net_state.NAT_GATEWAY_KEYS


def test_nat_gateway_retains_external_ip_and_virtual_network_until_cleanup(
    tmp_path, monkeypatch
):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )

    with pytest.raises(StateError, match="AgentlessNet NATGateway"):
        store.release_external_ip(UID_THREE, EXTERNAL_IP_FULFILLMENT_ID, TENANT_ONE, POOL_UID)
    with pytest.raises(StateError, match="saved NATGateways"):
        store.delete_and_remove_virtual_network(UID_ONE, TENANT_ONE)
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "delete_nat_gateway",
        lambda *_args, **_kwargs: True,
    )
    deleted, changed = store.delete_nat_gateway(
        NAT_GATEWAY_UID, NAT_GATEWAY_FULFILLMENT_ID, TENANT_ONE
    )
    assert changed is True
    assert deleted["phase"] == "deleting"
    assert store.delete_nat_gateway(
        NAT_GATEWAY_UID, NAT_GATEWAY_FULFILLMENT_ID, TENANT_ONE
    ) == (None, False)
    monkeypatch.setattr(agentless_net_state, "prepare_subnet_delete_data_plane", lambda *_args: False)
    store.prepare_delete_subnet(UID_TWO, TENANT_ONE, "systemd")
    assert store.release_subnet(UID_TWO, TENANT_ONE)[1] is True
    assert store.release_external_ip(
        UID_THREE, EXTERNAL_IP_FULFILLMENT_ID, TENANT_ONE, POOL_UID
    )[1] is True


def test_external_ip_cannot_be_shared_between_nat_gateway_and_attachment(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    with pytest.raises(StateError, match="already reserved"):
        store.ensure_external_ip_attachment(
            ATTACHMENT_UID,
            ATTACHMENT_FULFILLMENT_ID,
            TENANT_ONE,
            external_ip_attachment_context(store),
            "eth0",
        )


def test_nat_gateway_delete_failure_retains_consumer_reservation(tmp_path, monkeypatch):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )

    def fail_delete(*_args, **_kwargs):
        raise agentless_net_state.NetworkCommandError("route withdrawal failed")

    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "delete_nat_gateway",
        fail_delete,
    )
    with pytest.raises(StateError, match="route withdrawal failed"):
        store.delete_nat_gateway(
            NAT_GATEWAY_UID, NAT_GATEWAY_FULFILLMENT_ID, TENANT_ONE
        )

    with sqlite3.connect(store.path) as connection:
        row = connection.execute(
            "SELECT phase, payload FROM nat_gateways WHERE uid = ?",
            (NAT_GATEWAY_UID,),
        ).fetchone()
    assert row[0] == "deleting"
    assert json.loads(row[1])["external_ip_uid"] == UID_THREE
    with pytest.raises(StateError, match="AgentlessNet NATGateway"):
        store.release_external_ip(UID_THREE, EXTERNAL_IP_FULFILLMENT_ID, TENANT_ONE, POOL_UID)


def test_subnet_delete_succeeds_while_nat_gateway_cleanup_is_pending(
    tmp_path, monkeypatch
):
    store = prepare_nat_gateway_state(tmp_path, monkeypatch)
    store.ensure_nat_gateway(
        NAT_GATEWAY_UID,
        NAT_GATEWAY_FULFILLMENT_ID,
        TENANT_ONE,
        nat_gateway_context(store),
        "eth0",
    )
    monkeypatch.setattr(
        agentless_net_state.agentless_net_nat_gateway,
        "delete_nat_gateway",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            agentless_net_state.NetworkCommandError("route withdrawal failed")
        ),
    )
    with pytest.raises(StateError, match="route withdrawal failed"):
        store.delete_nat_gateway(
            NAT_GATEWAY_UID, NAT_GATEWAY_FULFILLMENT_ID, TENANT_ONE
        )

    monkeypatch.setattr(
        agentless_net_state,
        "prepare_subnet_delete_data_plane",
        lambda *_args: False,
    )
    deleting, changed = store.prepare_delete_subnet(UID_TWO, TENANT_ONE, "systemd")
    assert deleting["phase"] == "deleting"
    assert changed is True
    assert store.release_subnet(UID_TWO, TENANT_ONE)[1] is True


@pytest.mark.parametrize(
    ("action", "method"),
    [
        ("ensure_external_ip_attachment", "ensure_external_ip_attachment"),
        ("delete_external_ip_attachment", "delete_external_ip_attachment"),
    ],
)
def test_external_ip_attachment_module_dispatches_identity_and_context(
    monkeypatch, action, method
):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    context = {
        "external_ip": {"name": "public-ip"},
        "target": {"kind": "BareMetalInstance"},
        "subnet": {"name": "subnet"},
        "virtual_network": {"name": "network"},
    }
    params = {
        "action": action,
        "state_file": "/unused/state.sqlite3",
        "uid": ATTACHMENT_UID,
        "fulfillment_id": ATTACHMENT_FULFILLMENT_ID,
        "tenant_id": TENANT_ONE,
        "attachment_context": context,
        "external_interface": "eth0",
    }
    ansible_module = SimpleNamespace(
        params=params,
        check_mode=True,
        exit_json=Mock(),
        fail_json=Mock(),
    )
    monkeypatch.setattr(module, "AnsibleModule", lambda **_kwargs: ansible_module)
    store = Mock()
    getattr(store, method).return_value = ({"uid": ATTACHMENT_UID}, True)
    monkeypatch.setattr(module, "StateStore", lambda _path: store)

    module.main()

    if action == "ensure_external_ip_attachment":
        store.ensure_external_ip_attachment.assert_called_once_with(
            ATTACHMENT_UID,
            ATTACHMENT_FULFILLMENT_ID,
            TENANT_ONE,
            context,
            "eth0",
            check_mode=True,
        )
    else:
        store.delete_external_ip_attachment.assert_called_once_with(
            ATTACHMENT_UID,
            ATTACHMENT_FULFILLMENT_ID,
            TENANT_ONE,
            check_mode=True,
        )
    ansible_module.exit_json.assert_called_once_with(
        changed=True, backend_network_id=ATTACHMENT_UID
    )
    ansible_module.fail_json.assert_not_called()


def test_external_ip_reservation_reuses_uid_and_allocates_unique_addresses(tmp_path):
    store = store_for(tmp_path)
    register_external_ip_pool(store, cidr="198.51.100.0/30")

    first, first_changed = reserve_external_address(
        store, UID_ONE, POOL_UID, pool_cidr="198.51.100.0/30"
    )
    retry, retry_changed = reserve_external_address(
        store, UID_ONE, POOL_UID, pool_cidr="198.51.100.0/30"
    )
    second, second_changed = reserve_external_address(
        store, UID_TWO, UID_THREE, pool_cidr="198.51.100.0/30"
    )

    assert retry == first
    assert first_changed is True
    assert retry_changed is False
    assert {first["address"], second["address"]} == {
        "198.51.100.1",
        "198.51.100.2",
    }
    assert second_changed is True


def test_external_ip_reservation_rejects_exhausted_or_mismatched_pool(tmp_path):
    store = store_for(tmp_path)
    register_external_ip_pool(store, cidr="198.51.100.0/30")
    reserve_external_address(store, UID_ONE, POOL_UID, pool_cidr="198.51.100.0/30")
    reserve_external_address(store, UID_TWO, UID_THREE, pool_cidr="198.51.100.0/30")

    with pytest.raises(StateError, match="no available"):
        reserve_external_address(store, UID_THREE, UID_ONE, pool_cidr="198.51.100.0/30")
    with pytest.raises(StateError, match="identity does not match"):
        reserve_external_address(
            store,
            UID_ONE,
            POOL_UID,
            pool_cidr="198.51.100.4/30",
            pool_fulfillment_id=UID_THREE,
        )
    with pytest.raises(StateError, match="tenant"):
        reserve_external_address(store, UID_THREE, UID_ONE, tenant_id=TENANT_TWO)

    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM external_ips").fetchone()[0] == 2


def test_external_ip_reservation_uses_gap_search_for_fragmented_large_pool(tmp_path):
    store = store_for(tmp_path)
    cidr = "10.20.0.0/16"
    register_external_ip_pool(store, cidr=cidr)
    network = ipaddress.ip_network(cidr)

    with sqlite3.connect(store.path) as connection:
        for offset in range(2, 259):
            connection.execute(
                "INSERT INTO external_ips "
                "(uid, fulfillment_id, tenant_id, pool_uid, address) "
                "VALUES (?, ?, ?, ?, ?)",
                (
                    str(uuid.uuid4()),
                    str(uuid.uuid4()),
                    TENANT_ONE,
                    POOL_UID,
                    str(ipaddress.IPv4Address(int(network.network_address) + offset)),
                ),
            )
        # Put the most recent reservation before the occupied run so the bounded
        # probe path crosses 256 allocated addresses and uses the gap search.
        connection.execute(
            "INSERT INTO external_ips "
            "(uid, fulfillment_id, tenant_id, pool_uid, address) "
            "VALUES (?, ?, ?, ?, ?)",
            (
                str(uuid.uuid4()),
                str(uuid.uuid4()),
                TENANT_ONE,
                POOL_UID,
                str(ipaddress.IPv4Address(int(network.network_address) + 1)),
            ),
        )
        address_value_sql = store._external_ip_address_value_sql()
        query_plan = connection.execute(
            f"""EXPLAIN QUERY PLAN SELECT {address_value_sql}
                FROM external_ips
                WHERE pool_uid = ?
                  AND {address_value_sql} >= ?
                  AND {address_value_sql} < ?
                ORDER BY {address_value_sql}""",
            (
                POOL_UID,
                int(network.network_address) + 1,
                int(network.network_address) + 300,
            ),
        ).fetchall()
        assert any(
            "external_ips_by_pool_address_value" in row[3] for row in query_plan
        )

    reservation, changed = reserve_external_address(
        store, str(uuid.uuid4()), str(uuid.uuid4()), pool_cidr=cidr
    )

    assert changed is True
    assert reservation["address"] == "10.20.1.3"


def test_external_ip_reservation_supports_shared_tenant_and_small_cidrs(tmp_path):
    store = store_for(tmp_path)
    register_external_ip_pool(store, tenant_id="shared", cidr="203.0.113.0/31")

    first, _ = reserve_external_address(
        store,
        UID_ONE,
        POOL_UID,
        tenant_id=TENANT_TWO,
        pool_tenant_id="shared",
        pool_cidr="203.0.113.0/31",
    )
    second, _ = reserve_external_address(
        store,
        UID_TWO,
        UID_THREE,
        tenant_id=TENANT_TWO,
        pool_tenant_id="shared",
        pool_cidr="203.0.113.0/31",
    )

    assert {first["address"], second["address"]} == {"203.0.113.0", "203.0.113.1"}


def test_external_ip_reservation_release_is_owner_checked_and_idempotent(tmp_path):
    store = store_for(tmp_path)
    register_external_ip_pool(store)
    reservation, _ = reserve_external_address(store, UID_ONE, POOL_UID)

    with pytest.raises(StateError, match="identity does not match"):
        store.release_external_ip(UID_ONE, UID_TWO, TENANT_ONE, POOL_UID)

    released, changed = store.release_external_ip(UID_ONE, POOL_UID, TENANT_ONE, POOL_UID)
    absent, unchanged = store.release_external_ip(UID_ONE, POOL_UID, TENANT_ONE, POOL_UID)

    assert released == reservation
    assert changed is True
    assert absent is None
    assert unchanged is False
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM external_ips").fetchone()[0] == 0


def test_external_ip_reservation_check_mode_does_not_write(tmp_path):
    store = store_for(tmp_path)
    register_external_ip_pool(store)

    candidate, would_change = reserve_external_address(
        store, UID_ONE, POOL_UID, check_mode=True
    )
    with sqlite3.connect(store.path) as connection:
        assert connection.execute("SELECT count(*) FROM external_ips").fetchone()[0] == 0

    assert ipaddress.ip_address(candidate["address"]) in ipaddress.ip_network(
        POOL_CIDR, strict=True
    )
    assert would_change is True


def test_external_ip_pool_cannot_be_deleted_while_addresses_are_reserved(tmp_path):
    store = store_for(tmp_path)
    pool, _ = register_external_ip_pool(store)
    reserve_external_address(store, UID_ONE, POOL_UID)

    with pytest.raises(StateError, match="ExternalIP reservation"):
        store.delete_external_ip_pool(
            POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
        )

    store.release_external_ip(UID_ONE, POOL_UID, TENANT_ONE, POOL_UID)
    deleted, changed = store.delete_external_ip_pool(
        POOL_UID, POOL_FULFILLMENT_ID, TENANT_ONE, "IPv4", [POOL_CIDR]
    )
    assert deleted == pool
    assert changed is True


@pytest.mark.parametrize(
    ("lease", "expected"),
    [
        (
            {"ip_address": "10.0.1.22", "mac_address": "aa:bb:cc:dd:ee:ff"},
            {
                "changed": False,
                "found": True,
                "lease": {"ip_address": "10.0.1.22", "mac_address": "aa:bb:cc:dd:ee:ff"},
            },
        ),
        (None, {"changed": False, "found": False}),
    ],
)
def test_query_dhcp_lease_module_dispatches_read_only_lookup(
    monkeypatch, lease, expected
):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )

    class ModuleExit(Exception):
        pass

    calls = {}
    module_object = SimpleNamespace(
        params={
            "action": "query_dhcp_lease",
            "state_file": "/unused/state.sqlite3",
            "uid": UID_TWO,
            "tenant_id": TENANT_ONE,
            "mac_address": "AA:BB:CC:DD:EE:FF",
        },
        check_mode=True,
        exit_json=lambda **kwargs: (_ for _ in ()).throw(ModuleExit(kwargs)),
        fail_json=lambda **kwargs: pytest.fail(f"unexpected module error: {kwargs}"),
    )
    monkeypatch.setattr(
        module,
        "AnsibleModule",
        lambda **kwargs: calls.update(kwargs) or module_object,
    )
    store = Mock()
    store.query_dhcp_lease.return_value = lease
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    with pytest.raises(ModuleExit) as result:
        module.main()

    assert result.value.args[0] == expected
    store.query_dhcp_lease.assert_called_once_with(
        UID_TWO, TENANT_ONE, "AA:BB:CC:DD:EE:FF"
    )
    assert ("action", "query_dhcp_lease", ["mac_address"]) in calls["required_if"]
    assert calls["supports_check_mode"] is True


@pytest.mark.parametrize(
    ("action", "method", "result", "expected"),
    [
        (
            "reserve_external_ip",
            "reserve_external_ip",
            {"uid": UID_ONE, "address": "198.51.100.1"},
            {"changed": True, "backend_network_id": UID_ONE, "external_ip": {"uid": UID_ONE, "address": "198.51.100.1"}},
        ),
        (
            "release_external_ip",
            "release_external_ip",
            None,
            {"changed": False, "backend_network_id": UID_ONE},
        ),
    ],
)
def test_external_ip_actions_return_state_results(action, method, result, expected, monkeypatch):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )

    class ModuleExit(Exception):
        pass

    def exit_json(**kwargs):
        raise ModuleExit(kwargs)

    module_object = SimpleNamespace(
        params={
            "action": action,
            "state_file": "/unused/state.sqlite3",
            "uid": UID_ONE,
            "fulfillment_id": UID_TWO,
            "tenant_id": TENANT_ONE,
            "pool_uid": POOL_UID,
            "pool_fulfillment_id": POOL_FULFILLMENT_ID,
            "pool_tenant_id": TENANT_ONE,
            "pool_cidr": POOL_CIDR,
        },
        check_mode=False,
        exit_json=exit_json,
        fail_json=lambda **kwargs: pytest.fail(f"unexpected module error: {kwargs}"),
    )
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: module_object)
    store = Mock()
    getattr(store, method).return_value = (result, expected["changed"])
    monkeypatch.setattr(module, "StateStore", lambda path: store)

    with pytest.raises(ModuleExit) as returned:
        module.main()

    assert returned.value.args[0] == expected
    getattr(store, method).assert_called_once()
