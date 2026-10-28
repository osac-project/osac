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
SUBNET_UID = "22222222-2222-4222-8222-222222222222"
EXTERNAL_IP = "198.51.100.10"
SOURCE_CIDR = "10.0.1.0/24"
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
    }


def gateway_state():
    return {
        "uid": GATEWAY_UID,
        "external_address": EXTERNAL_IP,
        "provider": {
            "namespace_name": NAMESPACE,
            "namespace_interface": NAMESPACE_INTERFACE,
            "host_interface": HOST_INTERFACE,
            "external_interface": EXTERNAL_INTERFACE,
            "next_hop": NEXT_HOP,
        },
    }


def source_state():
    return [{"subnet_uid": SUBNET_UID, "cidr": SOURCE_CIDR}]


def test_rule_builders_scope_snat_forwarding_and_bypass_by_identity():
    gateway = gateway_state()

    assert nat_gateway._snat_rule(gateway, source_state()[0]) == [
        "-s", SOURCE_CIDR, "-o", NAMESPACE_INTERFACE,
        "-m", "comment", "--comment", f"osac-ngw:{GATEWAY_UID}:snat:{SUBNET_UID}",
        "-j", "SNAT", "--to-source", EXTERNAL_IP,
    ]
    outbound, inbound = nat_gateway._forward_rules(gateway)
    assert outbound == [
        "-i", HOST_INTERFACE, "-o", EXTERNAL_INTERFACE, "-s", f"{EXTERNAL_IP}/32",
        "-m", "comment", "--comment", f"osac-ngw:{GATEWAY_UID}:forward-out",
        "-j", "ACCEPT",
    ]
    assert inbound == [
        "-i", EXTERNAL_INTERFACE, "-o", HOST_INTERFACE, "-d", f"{EXTERNAL_IP}/32",
        "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment",
        "--comment", f"osac-ngw:{GATEWAY_UID}:forward-in", "-j", "ACCEPT",
    ]
    assert nat_gateway._bypass_rule(gateway) == [
        "-s", f"{EXTERNAL_IP}/32", "-m", "comment", "--comment",
        f"osac-ngw:{GATEWAY_UID}:bypass", "-j", "ACCEPT",
    ]


def test_ipv4_network_wraps_the_shared_parser_error():
    assert nat_gateway._ipv4_network("10.0.1.0/24", "NATGateway source CIDR") == "10.0.1.0/24"
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="source CIDR must be an IPv4 CIDR"):
        nat_gateway._ipv4_network("2001:db8::/64", "NATGateway source CIDR")


@pytest.mark.parametrize(
    ("sources", "expected_rule_count"),
    [(source_state(), 4), ([], 3)],
    ids=("with-subnet", "empty-virtual-network"),
)
def test_reconcile_keeps_shared_chain_return_rules_last_before_route(
    monkeypatch: pytest.MonkeyPatch,
    sources: list[dict[str, str]],
    expected_rule_count: int,
) -> None:
    events = []
    chains = {}
    gateway = gateway_state()

    monkeypatch.setattr(nat_gateway, "_validate_parent", lambda *_args: None)

    def ensure_chain(
        table: str, parent_chain: str, chain: str, *, namespace: str | None = None
    ) -> bool:
        events.append(("chain", table, chain))
        chains.setdefault((table, parent_chain), [])
        chains.setdefault((table, chain), [["-j", "RETURN"]])
        hook = ["-j", chain]
        if hook not in chains[(table, parent_chain)]:
            chains[(table, parent_chain)].insert(0, hook)
        return True

    monkeypatch.setattr(nat_gateway, "_ensure_shared_chain", ensure_chain)
    monkeypatch.setattr(
        nat_gateway,
        "_chain_rules",
        lambda table, chain, *, namespace=None: [] if namespace else chains.get((table, chain), []),
    )
    monkeypatch.setattr(nat_gateway, "_existing_snat_rules", lambda *_args: [])

    def ensure_rule(
        table: str,
        chain: str,
        rule: list[str],
        *,
        insert: bool = False,
    ) -> bool:
        events.append(("rule", table, chain, rule, insert))
        rules = chains[(table, chain)]
        if insert:
            rules.insert(0, rule)
        else:
            rules.append(rule)
        return True

    monkeypatch.setattr(nat_gateway, "_ensure_rule", ensure_rule)

    def ensure_rules(_runner, table, chain, rules, **_kwargs):
        for rule in rules:
            events.append(("rule", table, chain, rule))
        return bool(rules)

    monkeypatch.setattr(nat_gateway.network, "ensure_iptables_rules", ensure_rules)
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "ensure_external_route",
        lambda address, next_hop: events.append(("route", address, next_hop)) or True,
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "_route_uses_next_hop",
        lambda *_args: True,
    )

    assert nat_gateway.reconcile_nat_gateway(parent_state(), gateway, sources) is True

    route_index = next(index for index, event in enumerate(events) if event[0] == "route")
    rules = [event for event in events[:route_index] if event[0] == "rule"]
    assert len(rules) == expected_rule_count
    if sources:
        namespace_snat = next(event[3] for event in rules if event[1:3] == ("nat", "POSTROUTING"))
        assert namespace_snat[-2:] == ["--to-source", EXTERNAL_IP]
        assert "-j" in namespace_snat and namespace_snat[namespace_snat.index("-j") + 1] == "SNAT"
    host_rules = [event for event in rules if event[1:3] in {
        ("filter", nat_gateway.SNAT_FORWARD_CHAIN),
        ("nat", nat_gateway.SNAT_BYPASS_CHAIN),
    }]
    assert len(host_rules) == 3
    assert all(event[4] for event in host_rules)
    assert chains[("filter", nat_gateway.SNAT_FORWARD_CHAIN)][-1] == ["-j", "RETURN"]
    assert chains[("nat", nat_gateway.SNAT_BYPASS_CHAIN)][-1] == ["-j", "RETURN"]
    assert events[route_index] == ("route", EXTERNAL_IP, NEXT_HOP)


def test_reconcile_accepts_iptables_forward_rule_selector_order(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    gateway = gateway_state()
    outbound, inbound = nat_gateway._forward_rules(gateway)
    canonical_outbound = [
        "-s", f"{EXTERNAL_IP}/32", "-i", HOST_INTERFACE, "-o", EXTERNAL_INTERFACE,
        *outbound[outbound.index("-m"):],
    ]
    canonical_inbound = [
        "-d", f"{EXTERNAL_IP}/32", "-i", EXTERNAL_INTERFACE, "-o", HOST_INTERFACE,
        *inbound[inbound.index("-m"):],
    ]
    monkeypatch.setattr(nat_gateway, "_validate_parent", lambda *_args: None)
    monkeypatch.setattr(nat_gateway, "_ensure_shared_chain", lambda *_args, **_kwargs: False)
    monkeypatch.setattr(nat_gateway, "verify_snat_forwarding_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "verify_snat_bypass_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "_existing_snat_rules", lambda *_args: [])
    monkeypatch.setattr(nat_gateway.network, "ensure_iptables_rules", lambda *_args, **_kwargs: False)
    monkeypatch.setattr(nat_gateway, "_ensure_rule", lambda *_args, **_kwargs: False)
    monkeypatch.setattr(
        nat_gateway,
        "_owned_rules",
        lambda table, chain, _uid, **_kwargs: (
            [(canonical_outbound, "forward-out"), (canonical_inbound, "forward-in")]
            if (table, chain) == ("filter", nat_gateway.SNAT_FORWARD_CHAIN)
            else ([(nat_gateway._bypass_rule(gateway), "bypass")]
                  if (table, chain) == ("nat", nat_gateway.SNAT_BYPASS_CHAIN)
                  else [])
        ),
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "ensure_external_route",
        lambda *_args: True,
    )
    monkeypatch.setattr(
        nat_gateway.agentless_net_external_ip,
        "_route_uses_next_hop",
        lambda *_args: True,
    )

    assert nat_gateway.reconcile_nat_gateway(parent_state(), gateway, []) is True


def test_reconcile_rejects_a_broad_owned_snat_rule_before_route(monkeypatch):
    gateway = gateway_state()
    broad = [
        "-o", NAMESPACE_INTERFACE, "-m", "comment", "--comment",
        f"osac-ngw:{GATEWAY_UID}:snat:{SUBNET_UID}", "-j", "SNAT", "--to-source", EXTERNAL_IP,
    ]
    monkeypatch.setattr(nat_gateway, "_validate_parent", lambda *_args: None)
    monkeypatch.setattr(nat_gateway, "_ensure_shared_chain", lambda *_args, **_kwargs: False)
    monkeypatch.setattr(nat_gateway, "_verify_owned_forward_chain", lambda: None)
    monkeypatch.setattr(nat_gateway, "verify_snat_forwarding_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "verify_snat_bypass_hook", lambda: None)
    monkeypatch.setattr(nat_gateway, "_verify_bypass_chain", lambda: None)
    monkeypatch.setattr(nat_gateway, "_existing_snat_rules", lambda *_args: [(broad, f"snat:{SUBNET_UID}")])
    route_called = False

    def route(*_args):
        nonlocal route_called
        route_called = True
        return True

    monkeypatch.setattr(
        nat_gateway,
        "agentless_net_external_ip",
        SimpleNamespace(ensure_external_route=route),
    )
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="incomplete"):
        nat_gateway.reconcile_nat_gateway(parent_state(), gateway, source_state())
    assert route_called is False


def test_shared_forwarding_hook_reorders_after_external_ip_hook(monkeypatch):
    external_hook = ["-j", "OSAC-EXTERNAL"]
    nat_gateway_hook = ["-j", nat_gateway.SNAT_FORWARD_CHAIN]
    isolation_rule = ["-i", "osacvn+", "-j", "DROP"]
    chains = {
        "FORWARD": [nat_gateway_hook, external_hook, isolation_rule],
        nat_gateway.SNAT_FORWARD_CHAIN: [["-j", "RETURN"]],
    }

    def run(command, *, check=True):
        action_index = command.index("-S") if "-S" in command else None
        if action_index is not None:
            chain = command[action_index + 1]
            if chain not in chains:
                return SimpleNamespace(returncode=1, stdout="")
            lines = ["-A " + chain + (" " + " ".join(rule) if rule else "") for rule in chains[chain]]
            return SimpleNamespace(returncode=0, stdout="\n".join(lines))
        action = command[4]
        parent_chain = command[5]
        if action == "-D":
            chains[parent_chain].remove(command[6:])
        elif action == "-I":
            index = int(command[6]) - 1
            chains[parent_chain].insert(index, command[7:])
        else:
            raise AssertionError(f"unexpected firewall operation: {command}")
        return SimpleNamespace(returncode=0, stdout="")

    monkeypatch.setattr(nat_gateway, "_run", run)

    assert nat_gateway._ensure_shared_chain(
        "filter", "FORWARD", nat_gateway.SNAT_FORWARD_CHAIN
    ) is True
    assert chains["FORWARD"] == [external_hook, nat_gateway_hook, isolation_rule]


def test_forwarding_chain_rejects_wrong_owner_and_unscoped_rules(monkeypatch):
    unsafe = [
        "-i", HOST_INTERFACE, "-o", EXTERNAL_INTERFACE,
        "-m", "comment", "--comment", f"osac-ngw:{GATEWAY_UID}:forward-out",
        "-j", "ACCEPT",
    ]
    monkeypatch.setattr(
        nat_gateway,
        "_chain_rules",
        lambda *_args, **_kwargs: [unsafe, ["-j", "RETURN"]],
    )
    with pytest.raises(nat_gateway.NATGatewayProviderError, match="incomplete"):
        nat_gateway._verify_owned_forward_chain()


def test_delete_withdraws_route_before_rules_and_conntrack(monkeypatch):
    events = []
    gateway = gateway_state()
    snat_rule = nat_gateway._snat_rule(gateway, source_state()[0])
    forward_rule, inbound_rule = nat_gateway._forward_rules(gateway)
    bypass_rule = nat_gateway._bypass_rule(gateway)
    removed = set()

    def owned_rules(table, chain, uid, *, namespace=None):
        if removed:
            return []
        if (table, chain) == ("filter", nat_gateway.SNAT_FORWARD_CHAIN):
            return [(forward_rule, "forward-out"), (inbound_rule, "forward-in")]
        if (table, chain) == ("nat", nat_gateway.SNAT_BYPASS_CHAIN):
            return [(bypass_rule, "bypass")]
        if (table, chain, namespace) == ("nat", "POSTROUTING", NAMESPACE):
            return [(snat_rule, f"snat:{SUBNET_UID}")]
        return []

    def withdraw(address, next_hop):
        events.append(("route", address, next_hop))
        return True

    def delete_rule(table, chain, rule, *, namespace=None):
        events.append(("rule", table, chain, namespace, rule))
        removed.add(tuple(rule))
        return True

    monkeypatch.setattr(nat_gateway.network, "namespace_exists", lambda _namespace: True)
    monkeypatch.setattr(nat_gateway, "_owned_rules", owned_rules)
    monkeypatch.setattr(nat_gateway, "_existing_snat_rules", lambda *_args: [(snat_rule, f"snat:{SUBNET_UID}")] if not removed else [])
    monkeypatch.setattr(nat_gateway, "_delete_rule", delete_rule)
    monkeypatch.setattr(nat_gateway, "_cleanup_chain_if_empty", lambda *_args: False)
    monkeypatch.setattr(nat_gateway, "_chain_rules", lambda *_args, **_kwargs: [])
    monkeypatch.setattr(
        nat_gateway,
        "agentless_net_external_ip",
        SimpleNamespace(
            withdraw_external_route=withdraw,
            _delete_external_conntrack=lambda namespace, address: events.append(("conntrack", namespace, address)) or True,
            _route_output=lambda _address: SimpleNamespace(returncode=1, stdout=""),
        ),
    )

    assert nat_gateway.delete_nat_gateway(parent_state(), gateway) is True
    assert events[0] == ("route", EXTERNAL_IP, NEXT_HOP)
    assert next(index for index, event in enumerate(events) if event[0] == "conntrack") > 1
    assert not any(event[0] == "chain-flush" for event in events)
