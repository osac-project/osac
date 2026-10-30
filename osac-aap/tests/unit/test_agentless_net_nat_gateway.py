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

from ansible_collections.osac.templates.plugins.module_utils import agentless_net_nat_gateway as nat_gateway


GATEWAY_UID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
EXTERNAL_IP = "198.51.100.10"
NAMESPACE = "n0123456789abcd"
NAMESPACE_INTERFACE = "osacvn12345678n"
HOST_INTERFACE = "osacvn12345678h"
EXTERNAL_INTERFACE = "eth0"
NEXT_HOP = "10.0.0.1/31"


def parent_state():
    return {
        "uid": "11111111-1111-4111-8111-111111111111",
        "namespace_name": NAMESPACE,
        "virtual_network_cidr": "10.0.0.0/16",
        "transit": {
            "cidr": "10.0.0.0/31",
            "namespace_ip": NEXT_HOP,
            "host_ip": "10.0.0.0/31",
            "gateway": "10.0.0.0",
        },
    }


def gateway_state():
    return {
        "uid": GATEWAY_UID,
        "external_address": EXTERNAL_IP,
        "context": {"virtual_network": {"ipv4_cidr": "10.0.0.0/16"}},
        "provider": {
            "namespace_name": NAMESPACE,
            "namespace_interface": NAMESPACE_INTERFACE,
            "host_interface": HOST_INTERFACE,
            "external_interface": EXTERNAL_INTERFACE,
            "next_hop": NEXT_HOP,
        },
    }


def test_rule_builders_use_the_vn_cidr_and_transit_exemption():
    gateway = gateway_state()
    assert nat_gateway._infra_bypass_rule(gateway) == [
        "-s", "10.0.0.0/31", "-o", NAMESPACE_INTERFACE,
        "-m", "comment", "--comment", f"osac-ngw:{GATEWAY_UID}:infra-bypass",
        "-j", "ACCEPT",
    ]
    assert nat_gateway._snat_rule(gateway) == [
        "-s", "10.0.0.0/16", "-o", NAMESPACE_INTERFACE,
        "-m", "comment", "--comment", f"osac-ngw:{GATEWAY_UID}:snat",
        "-j", "SNAT", "--to-source", EXTERNAL_IP,
    ]


@pytest.mark.parametrize(
    "mutation",
    [
        lambda gateway: gateway["context"]["virtual_network"].update(ipv4_cidr="2001:db8::/64"),
        lambda gateway: gateway["provider"].update(next_hop="2001:db8::1/31"),
        lambda gateway: gateway["provider"].update(next_hop="10.0.0.1/30"),
        lambda gateway: gateway["provider"].update(next_hop="192.0.2.1/31"),
        lambda gateway: gateway["context"]["virtual_network"].update(ipv4_cidr="10.1.0.0/16"),
        lambda gateway: gateway["provider"].update(next_hop="10.0.0.3/31"),
    ],
)
def test_validate_parent_rejects_invalid_or_mismatched_address_snapshots(monkeypatch, mutation):
    gateway = gateway_state()
    mutation(gateway)
    parent = parent_state()
    monkeypatch.setattr(nat_gateway.network, "namespace_exists", lambda _name: True)
    monkeypatch.setattr(
        nat_gateway.network,
        "link_details",
        lambda _namespace, interface: {
            "ifalias": f"osac-vn:{parent['uid']}" if interface == HOST_INTERFACE else "",
            "linkinfo": {"info_kind": "veth"} if interface == HOST_INTERFACE else {},
            "flags": ["UP"],
        },
    )
    monkeypatch.setattr(
        nat_gateway,
        "_run",
        lambda *_args, **_kwargs: SimpleNamespace(returncode=0, stdout="1\n"),
    )
    with pytest.raises(nat_gateway.NATGatewayProviderError):
        nat_gateway._validate_parent(parent, gateway)


class ChainModel:
    def __init__(self, rules):
        self.rules = list(rules)
        self.commands = []

    def run(self, command, *, check=True):
        self.commands.append(command)
        if "-S" in command:
            lines = ["-A POSTROUTING" + (" " + " ".join(rule) if rule else "") for rule in self.rules]
            return SimpleNamespace(returncode=0, stdout="\n".join(lines))
        if "-I" in command:
            index = command.index("-I") + 2
            self.rules.insert(int(command[index]) - 1, command[index + 1:])
        elif "-D" in command:
            index = command.index("-D") + 2
            del self.rules[int(command[index]) - 1]
        return SimpleNamespace(returncode=0, stdout="")


def reconcile_namespace(monkeypatch, initial_rules):
    model = ChainModel(initial_rules)
    monkeypatch.setattr(nat_gateway, "_run", model.run)
    changed = nat_gateway._reconcile_namespace_nat_rules(gateway_state())
    return model, changed


@pytest.mark.parametrize(
    "initial",
    [
        [],
        [nat_gateway._infra_bypass_rule(gateway_state())],
        [nat_gateway._snat_rule(gateway_state())],
    ],
    ids=["neither", "infra-only", "snat-only"],
)
def test_namespace_reconcile_installs_ordered_pair(monkeypatch, initial):
    model, changed = reconcile_namespace(monkeypatch, initial)
    assert changed is True
    assert model.rules[:2] == [
        nat_gateway._infra_bypass_rule(gateway_state()),
        nat_gateway._snat_rule(gateway_state()),
    ]


def test_namespace_reconcile_is_idempotent_and_preserves_generic_rules(monkeypatch):
    generic = ["-o", NAMESPACE_INTERFACE, "-j", "MASQUERADE"]
    initial = [
        nat_gateway._snat_rule(gateway_state()),
        generic,
        nat_gateway._infra_bypass_rule(gateway_state()),
    ]
    model, changed = reconcile_namespace(monkeypatch, initial)
    assert changed is True
    assert model.rules == [
        nat_gateway._infra_bypass_rule(gateway_state()),
        nat_gateway._snat_rule(gateway_state()),
        generic,
    ]
    model.commands.clear()
    assert nat_gateway._reconcile_namespace_nat_rules(gateway_state()) is False
    assert not [command for command in model.commands if "-I" in command or "-D" in command]


def test_namespace_reconcile_consolidates_identical_duplicates(monkeypatch):
    infra = nat_gateway._infra_bypass_rule(gateway_state())
    snat = nat_gateway._snat_rule(gateway_state())
    model, changed = reconcile_namespace(monkeypatch, [infra, infra, snat, snat])
    assert changed is True
    assert model.rules == [infra, snat]


def test_namespace_reconcile_accepts_selector_order_from_iptables(monkeypatch):
    gateway = gateway_state()
    infra = nat_gateway._infra_bypass_rule(gateway)
    snat = nat_gateway._snat_rule(gateway)
    reordered = ["-o", NAMESPACE_INTERFACE, "-s", "10.0.0.0/16", *snat[4:]]
    model, changed = reconcile_namespace(monkeypatch, [infra, reordered])
    assert changed is False
    assert model.rules == [infra, reordered]


def test_namespace_reconcile_rejects_unsafe_owned_rule_before_mutation(monkeypatch):
    gateway = gateway_state()
    unsafe = nat_gateway._snat_rule(gateway)
    unsafe[unsafe.index("-s") + 1] = "0.0.0.0/0"
    model = ChainModel([unsafe])
    monkeypatch.setattr(nat_gateway, "_run", model.run)
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="changed"):
        nat_gateway._reconcile_namespace_nat_rules(gateway)
    assert not [command for command in model.commands if "-I" in command or "-D" in command]


def test_namespace_discovery_skips_uncommented_rules_and_rejects_malformed_owner(monkeypatch):
    generic = ["-j", "MASQUERADE"]
    monkeypatch.setattr(nat_gateway, "_chain_rules", lambda *_args, **_kwargs: [generic])
    assert nat_gateway._existing_namespace_nat_rules(gateway_state()) == []
    malformed = ["-m", "comment", "--comment", "osac-ngw:not-a-uid:snat"]
    monkeypatch.setattr(nat_gateway, "_chain_rules", lambda *_args, **_kwargs: [malformed])
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="malformed"):
        nat_gateway._existing_namespace_nat_rules(gateway_state())


def test_reconcile_installs_namespace_rules_before_route(monkeypatch):
    events = []
    gateway = gateway_state()
    monkeypatch.setattr(nat_gateway, "_validate_parent", lambda *_args: None)
    monkeypatch.setattr(
        nat_gateway,
        "_ensure_shared_chain",
        lambda table, parent, chain: events.append(("chain", table, chain)) or True,
    )
    monkeypatch.setattr(
        nat_gateway,
        "_reconcile_namespace_nat_rules",
        lambda _gateway: events.append(("namespace",)) or True,
    )
    monkeypatch.setattr(nat_gateway, "verify_snat_forwarding_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "verify_snat_bypass_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "_owned_rules", lambda *_args, **_kwargs: [])
    monkeypatch.setattr(
        nat_gateway,
        "_ensure_rule",
        lambda table, chain, rule, **kwargs: events.append(("rule", table, chain)) or True,
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "ensure_external_route",
        lambda *_args: events.append(("route",)) or True,
    )
    monkeypatch.setattr(nat_gateway.agentless_net_external_ip, "_route_uses_next_hop", lambda *_args: True)
    assert nat_gateway.reconcile_nat_gateway(parent_state(), gateway) is True
    assert events.index(("namespace",)) < events.index(("route",))


def test_delete_withdraws_route_removes_snat_before_exemption_and_conntrack(monkeypatch):
    events = []
    gateway = gateway_state()
    infra = nat_gateway._infra_bypass_rule(gateway)
    snat = nat_gateway._snat_rule(gateway)
    forward, inbound = nat_gateway._forward_rules(gateway)
    bypass = nat_gateway._bypass_rule(gateway)
    monkeypatch.setattr(nat_gateway.network, "namespace_exists", lambda _name: True)
    namespace_rules = [[snat, "snat"], [infra, "infra-bypass"]]
    host_rules = {
        nat_gateway.SNAT_FORWARD_CHAIN: [[forward, "forward-out"], [inbound, "forward-in"]],
        nat_gateway.SNAT_BYPASS_CHAIN: [[bypass, "bypass"]],
    }
    monkeypatch.setattr(
        nat_gateway,
        "_existing_namespace_nat_rules",
        lambda _gateway: list(namespace_rules),
    )
    monkeypatch.setattr(
        nat_gateway,
        "_owned_rules",
        lambda table, chain, _uid, **_kwargs: list(host_rules.get(chain, [])),
    )
    def delete_rule(_table, _chain, rule, **_kwargs):
        events.append(("rule", rule))
        if rule == snat:
            namespace_rules.remove([rule, "snat"])
        elif rule == infra:
            namespace_rules.remove([rule, "infra-bypass"])
        else:
            for chain_rules in host_rules.values():
                for owned in list(chain_rules):
                    if owned[0] == rule:
                        chain_rules.remove(owned)
        return True

    monkeypatch.setattr(nat_gateway, "_delete_rule", delete_rule)
    monkeypatch.setattr(nat_gateway, "_cleanup_chain_if_empty", lambda *_args: False)
    monkeypatch.setattr(nat_gateway, "_chain_rules", lambda *_args, **_kwargs: [])
    monkeypatch.setattr(
        nat_gateway,
        "_delete_nat_gateway_conntrack",
        lambda namespace, address: events.append(("conntrack", namespace, address)) or True,
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "withdraw_external_route",
        lambda *_args: events.append(("route",)) or True,
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "_route_output",
        lambda _address: SimpleNamespace(returncode=1, stdout=""),
    )
    assert nat_gateway.delete_nat_gateway(parent_state(), gateway) is True
    assert events[0] == ("route",)
    assert events[1][1] == snat
    assert events[2][1] == infra
    assert next(index for index, event in enumerate(events) if event[0] == "conntrack") > 2


def test_nat_gateway_conntrack_uses_reply_destination_and_verifies_cleanup(monkeypatch):
    outputs = [
        "ipv4 2 tcp 6 10 src=10.0.1.10 dst=203.0.113.5 reply_dst=198.51.100.10\n",
        "",
    ]
    commands = []

    def run(command, *, check=True):
        commands.append(command)
        if "-L" in command:
            return SimpleNamespace(returncode=0, stdout=outputs.pop(0))
        return SimpleNamespace(returncode=1, stdout="")

    monkeypatch.setattr(nat_gateway, "_run", run)
    assert nat_gateway._delete_nat_gateway_conntrack(NAMESPACE, EXTERNAL_IP) is True
    assert all("--reply-dst" in command for command in commands)
    assert any("-D" in command for command in commands)


def test_nat_gateway_conntrack_inspection_failure_is_fatal(monkeypatch):
    monkeypatch.setattr(
        nat_gateway,
        "_run",
        lambda *_args, **_kwargs: SimpleNamespace(returncode=1, stdout=""),
    )
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="inspect"):
        nat_gateway._delete_nat_gateway_conntrack(NAMESPACE, EXTERNAL_IP)
