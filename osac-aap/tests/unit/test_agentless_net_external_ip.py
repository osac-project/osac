import re
import sys
from pathlib import Path
from types import SimpleNamespace

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
sys.path.insert(0, str(MODULE_UTILS.parents[4]))

from ansible_collections.osac.templates.plugins.module_utils import agentless_net_external_ip as external_ip
from ansible_collections.osac.templates.plugins.module_utils import agentless_net_network as network


ATTACHMENT_UID = "66666666-6666-4666-8666-666666666666"
EXTERNAL_IP = "198.51.100.10"
TARGET_IP = "10.0.1.10"
NEXT_HOP = "10.0.0.1/31"


def attachment_state():
    return {
        "uid": ATTACHMENT_UID,
        "fulfillment_id": "77777777-7777-4777-8777-777777777777",
        "tenant_id": "tenant-a",
        "external_ip_uid": "88888888-8888-4888-8888-888888888888",
        "virtual_network_uid": "11111111-1111-4111-8111-111111111111",
        "subnet_uid": "22222222-2222-4222-8222-222222222222",
        "external_address": EXTERNAL_IP,
        "phase": "provisioning",
        "context": {
            "external_ip": {"address": EXTERNAL_IP},
            "target": {"ip_address": TARGET_IP},
        },
        "provider": {
            "namespace_name": "osacvn-test",
            "namespace_interface": "osacvn11111111",
            "host_interface": "osacvn11111111",
            "external_interface": "eth0",
            "next_hop": NEXT_HOP,
        },
    }


def test_forwarding_rules_are_address_and_interface_scoped():
    inbound, outbound = external_ip._forwarding_rules(attachment_state())

    assert inbound == [
        "-i", "eth0", "-o", "osacvn11111111", "-d", f"{EXTERNAL_IP}/32",
        "-m", "comment", "--comment", f"osac-eia:{ATTACHMENT_UID}:in", "-j", "ACCEPT",
    ]
    assert outbound == [
        "-i", "osacvn11111111", "-o", "eth0", "-s", f"{EXTERNAL_IP}/32",
        "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment",
        "--comment", f"osac-eia:{ATTACHMENT_UID}:out", "-j", "ACCEPT",
    ]


def test_attach_installs_whole_address_dnat_before_external_route(monkeypatch):
    events = []
    attachment = attachment_state()
    parent = {
        "uid": attachment["virtual_network_uid"],
        "namespace_name": attachment["provider"]["namespace_name"],
    }
    subnet = {"vlan_interface": "s0123456789ab", "virtual_network_uid": parent["uid"], "phase": "ready"}

    monkeypatch.setattr(external_ip, "_validate_parent", lambda *_args: None)
    monkeypatch.setattr(network, "namespace_exists", lambda _namespace: True)
    monkeypatch.setattr(network, "link_details", lambda *_args: {"flags": ["UP"]})

    def run(command, *, check=True):
        events.append(("command", command))
        if "PREROUTING" in command and "-A" in command:
            dnat_installed[0] = True
        if "PREROUTING" in command and "-C" in command:
            return SimpleNamespace(returncode=0 if dnat_installed[0] else 1, stdout="")
        return SimpleNamespace(returncode=0, stdout="1\n")

    dnat_installed = [False]
    monkeypatch.setattr(external_ip, "_run", run)
    monkeypatch.setattr(external_ip, "_ensure_external_forwarding_chain", lambda: events.append(("chain",)) or True)

    def ensure_rule(table, chain, rule, *, insert):
        events.append(("rule", table, chain, rule, insert))
        return True

    monkeypatch.setattr(external_ip, "_ensure_iptables_rule", ensure_rule)
    monkeypatch.setattr(external_ip, "ensure_external_route", lambda address, hop: events.append(("route", address, hop)) or True)
    monkeypatch.setattr(external_ip, "_route_uses_next_hop", lambda *_args: True)

    assert external_ip.reconcile_external_ip_attachment(parent, subnet, attachment) is True
    dnat = next(event[1] for event in events if event[0] == "command" and "PREROUTING" in event[1])
    assert "-d" in dnat and f"{EXTERNAL_IP}/32" in dnat
    assert "-p" not in dnat and "--dport" not in dnat
    assert dnat.index("-j") < dnat.index("DNAT")
    assert next(i for i, event in enumerate(events) if event[0] == "route") > max(
        i for i, event in enumerate(events) if event[0] == "rule"
    )


def test_detach_withdraws_route_before_removing_rules_and_conntrack(monkeypatch):
    events = []
    attachment = attachment_state()
    monkeypatch.setattr(external_ip, "withdraw_external_route", lambda address, hop: events.append(("route", address, hop)) or True)
    monkeypatch.setattr(
        external_ip,
        "_delete_iptables_rule",
        lambda table, chain, rule, *, namespace=None: events.append(
            ("rule", table, chain, rule, namespace)
        )
        or True,
    )
    monkeypatch.setattr(external_ip, "_delete_external_conntrack", lambda namespace, address: events.append(("conntrack", namespace, address)) or True)
    monkeypatch.setattr(external_ip, "_cleanup_empty_external_chain", lambda: events.append(("chain",)) or True)
    monkeypatch.setattr(network, "namespace_exists", lambda _namespace: True)

    assert external_ip.delete_external_ip_attachment(attachment) is True
    assert [event[0] for event in events] == ["route", "rule", "rule", "rule", "conntrack", "chain"]
    assert events[0] == ("route", EXTERNAL_IP, NEXT_HOP)
    assert events[1][1:3] == ("nat", "PREROUTING")
    assert events[1][4] == attachment["provider"]["namespace_name"]
    assert events[2][4] is None and events[3][4] is None


def test_detach_removes_dnat_from_saved_virtual_network_namespace(monkeypatch):
    attachment = attachment_state()
    namespace = attachment["provider"]["namespace_name"]
    nat_rule = [
        "-d", f"{EXTERNAL_IP}/32", "-m", "comment", "--comment",
        f"osac-eia:{ATTACHMENT_UID}:dnat", "-j", "DNAT", "--to-destination", TARGET_IP,
    ]
    present = {(namespace, "nat", "PREROUTING", tuple(nat_rule))}
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        if command[:3] == ["ip", "netns", "exec"]:
            command_namespace = command[3]
            start = 4
        else:
            command_namespace = None
            start = 0
        assert command[start] == "iptables"
        table = command[start + 3]
        action = command[start + 4]
        chain = command[start + 5]
        key = (command_namespace, table, chain, tuple(command[start + 6 :]))
        if action == "-C":
            return SimpleNamespace(returncode=int(key not in present), stdout="")
        if action == "-D":
            present.remove(key)
            return SimpleNamespace(returncode=0, stdout="")
        raise AssertionError(f"unexpected iptables action: {command}")

    monkeypatch.setattr(external_ip, "_run", run)
    monkeypatch.setattr(external_ip, "withdraw_external_route", lambda *_args: False)
    monkeypatch.setattr(external_ip, "_delete_external_conntrack", lambda *_args: False)
    monkeypatch.setattr(external_ip, "_cleanup_empty_external_chain", lambda: False)
    monkeypatch.setattr(network, "namespace_exists", lambda _namespace: True)

    external_ip.delete_external_ip_attachment(attachment)

    assert not present
    assert commands[0][:8] == [
        "ip", "netns", "exec", namespace, "iptables", "-w", "-t", "nat"
    ]
    assert commands[1][8:10] == ["-D", "PREROUTING"]


def test_route_helper_rejects_a_different_saved_next_hop(monkeypatch):
    address = EXTERNAL_IP

    def run(command, *, check=True):
        if command[0:2] == ["vtysh", "-c"] and "show running-config | include" in command[2]:
            return SimpleNamespace(returncode=0, stdout=f"ip route {address}/32 10.0.0.3\n")
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(external_ip, "_run", run)
    with pytest.raises(external_ip.ExternalIPProviderError, match="different next hop"):
        external_ip.ensure_external_route(address, NEXT_HOP)


def test_route_withdrawal_refuses_a_different_saved_next_hop(monkeypatch):
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        if command[0:2] == ["vtysh", "-c"] and "show running-config | include" in command[2]:
            return SimpleNamespace(returncode=0, stdout=f"ip route {EXTERNAL_IP}/32 10.0.0.3\n")
        raise AssertionError(f"unexpected command: {command}")

    monkeypatch.setattr(external_ip, "_run", run)

    with pytest.raises(external_ip.ExternalIPProviderError, match="changed next hop"):
        external_ip.withdraw_external_route(EXTERNAL_IP, NEXT_HOP)

    assert len(commands) == 1


def test_external_route_lookup_filters_by_exact_prefix(monkeypatch):
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        return SimpleNamespace(
            returncode=0,
            stdout=f"ip route {EXTERNAL_IP}/32 10.0.0.3\n",
        )

    monkeypatch.setattr(external_ip, "_run", run)

    assert external_ip._external_route_lines(EXTERNAL_IP) == [
        ["ip", "route", f"{EXTERNAL_IP}/32", "10.0.0.3"]
    ]
    assert commands == [[
        "vtysh",
        "-c",
        f"show running-config | include ^ip route {re.escape(EXTERNAL_IP + '/32')}[[:space:]]",
    ]]


def test_conntrack_lookup_filters_by_source_and_destination(monkeypatch):
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        return SimpleNamespace(
            returncode=0,
            stdout=f"tcp 6 10 ESTABLISHED src={EXTERNAL_IP} dst=203.0.113.5\n",
        )

    monkeypatch.setattr(external_ip, "_run", run)

    assert external_ip._conntrack_matches("osacvn-test", EXTERNAL_IP)
    assert [command[-2:] for command in commands] == [["-s", EXTERNAL_IP], ["-d", EXTERNAL_IP]]
    assert all(command[4:8] == ["conntrack", "-L", "-f", "ipv4"] for command in commands)


def test_shared_rule_deletion_helper_enforces_the_bound(monkeypatch):
    monkeypatch.setattr(network, "MAX_RULE_DELETIONS", 2)
    check_rule = ["iptables", "-C", "CHAIN", "-j", "DROP"]
    delete_rule = ["iptables", "-D", "CHAIN", "-j", "DROP"]
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        return SimpleNamespace(returncode=0, stdout="")

    with pytest.raises(external_ip.ExternalIPProviderError, match="too many duplicate"):
        network.remove_repeated_rule(
            run,
            check_rule,
            delete_rule,
            error_message="too many duplicate rules",
            error_type=external_ip.ExternalIPProviderError,
        )

    assert commands.count(check_rule) == 3
    assert commands.count(delete_rule) == 2


def test_forwarding_chain_rejects_unowned_broad_accept(monkeypatch):
    monkeypatch.setattr(
        external_ip,
        "_chain_rules",
        lambda: [["-j", "ACCEPT"], ["-j", "RETURN"]],
    )
    with pytest.raises(external_ip.ExternalIPProviderError, match="owner comment"):
        external_ip.verify_external_forwarding_chain()


def test_forwarding_chain_accepts_kernel_reordered_selectors_and_conntrack_states(monkeypatch):
    attachment = attachment_state()
    inbound, outbound = external_ip._forwarding_rules(attachment)
    canonical_inbound = [*inbound[4:6], *inbound[:4], *inbound[6:]]
    canonical_outbound = [*outbound[4:6], *outbound[:4], *outbound[6:]]
    conntrack_state_index = canonical_outbound.index("--ctstate")
    canonical_outbound[conntrack_state_index + 1] = "RELATED,ESTABLISHED"
    monkeypatch.setattr(
        external_ip,
        "_chain_rules",
        lambda: [canonical_outbound, canonical_inbound, ["-j", "RETURN"]],
    )

    external_ip.verify_external_forwarding_chain()
