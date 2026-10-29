"""AgentlessNet NATGateway Linux networking operations."""

from __future__ import annotations

import ipaddress
import re
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils import agentless_net_external_ip
from ansible_collections.osac.templates.plugins.module_utils import agentless_net_network as network


class NATGatewayProviderError(network.NetworkCommandError):
    """A NATGateway provider operation could not be verified."""


SNAT_FORWARD_CHAIN = "OSAC-SNAT-FORWARD"
SNAT_BYPASS_CHAIN = "OSAC-SNAT-BYPASS"
OWNER_COMMENT = re.compile(
    r"^osac-ngw:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}):"
    r"(snat|infra-bypass|forward-in|forward-out|bypass)$"
)


def _run(command: list[str], *, check: bool = True):
    try:
        return network.run_command(command, check=check)
    except network.NetworkCommandError as error:
        raise NATGatewayProviderError(str(error)) from error


def _ipv4(value: Any, context: str) -> str:
    return str(
        network.validate_ipv4_address(
            value,
            context,
            error_type=NATGatewayProviderError,
        )
    )


def _ipv4_network(value: Any, context: str) -> str:
    try:
        prefix = network.parse_canonical_ipv4_network(value, context=context)
    except ValueError as error:
        raise NATGatewayProviderError(f"{context} must be an IPv4 CIDR") from error
    except TypeError as error:
        raise NATGatewayProviderError(f"{context} must be an IPv4 CIDR") from error
    return str(prefix)


def _interface(value: Any, context: str) -> str:
    if not isinstance(value, str) or re.fullmatch(r"[A-Za-z0-9_.-]{1,15}", value) is None:
        raise NATGatewayProviderError(f"{context} is invalid")
    return value


def _owner(uid: str, kind: str) -> str:
    if not isinstance(uid, str) or re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", uid
    ) is None:
        raise NATGatewayProviderError("NATGateway UID is invalid")
    if kind not in {"snat", "infra-bypass", "forward-in", "forward-out", "bypass"}:
        raise NATGatewayProviderError("NATGateway rule type is invalid")
    comment = f"osac-ngw:{uid}:{kind}"
    if OWNER_COMMENT.fullmatch(comment) is None:
        raise NATGatewayProviderError("NATGateway rule owner is invalid")
    return comment


def _iptables_prefix(table: str, namespace: str | None = None) -> list[str]:
    return network.iptables_prefix(table, namespace)


def _ensure_rule(
    table: str,
    chain: str,
    rule: list[str],
    *,
    namespace: str | None = None,
    insert: bool = False,
) -> bool:
    return network.ensure_iptables_rule(
        _run,
        table,
        chain,
        rule,
        namespace=namespace,
        insert=insert,
        error_type=NATGatewayProviderError,
    )


def _delete_rule(
    table: str,
    chain: str,
    rule: list[str],
    *,
    namespace: str | None = None,
) -> bool:
    return network.delete_iptables_rule(
        _run,
        table,
        chain,
        rule,
        namespace=namespace,
        error_message=f"too many duplicate owned NATGateway rules in {chain}",
        error_type=NATGatewayProviderError,
    )


def _chain_rules(table: str, chain: str, *, namespace: str | None = None) -> list[list[str]]:
    return network.iptables_chain_rules(_run, table, chain, namespace=namespace)


def _rule_comment(rule: list[str]) -> str:
    try:
        index = rule.index("--comment")
        return rule[index + 1]
    except (ValueError, IndexError) as error:
        raise NATGatewayProviderError("NATGateway rule has no owner comment") from error


def _owned_rules(table: str, chain: str, uid: str, *, namespace: str | None = None):
    owned = []
    for rule in _chain_rules(table, chain, namespace=namespace):
        if rule == ["-j", "RETURN"]:
            continue
        try:
            comment = _rule_comment(rule)
        except NATGatewayProviderError as error:
            if str(error) == "NATGateway rule has no owner comment":
                continue
            raise
        match = OWNER_COMMENT.fullmatch(comment)
        if match is None:
            if comment.startswith("osac-ngw:"):
                raise NATGatewayProviderError("NATGateway rule owner is malformed")
            continue
        if match.group(1) == uid:
            owned.append((rule, match.group(2)))
    return owned


def _ensure_shared_chain(table: str, parent_chain: str, chain: str, *, namespace: str | None = None) -> bool:
    prefix = _iptables_prefix(table, namespace)
    changed = False
    chain_state = _run([*prefix, "-S", chain], check=False)
    if chain_state.returncode != 0:
        _run([*prefix, "-N", chain])
        changed = True
    rules = _chain_rules(table, parent_chain, namespace=namespace)
    hook_rule = ["-j", chain]
    hook_count = rules.count(hook_rule)
    if hook_count > 1:
        raise NATGatewayProviderError(f"duplicate {chain} hooks exist in {parent_chain}")
    if parent_chain == "FORWARD":
        external_hook = ["-j", agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN]
        if external_hook in rules:
            insertion = rules.index(external_hook) + 2
        else:
            insertion = 1
    else:
        insertion = 1
    if hook_count == 0:
        _run([*prefix, "-I", parent_chain, str(insertion), *hook_rule])
        changed = True
    elif rules.index(hook_rule) != insertion - 1:
        _run([*prefix, "-D", parent_chain, *hook_rule])
        rules = _chain_rules(table, parent_chain, namespace=namespace)
        if parent_chain == "FORWARD":
            external_hook = ["-j", agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN]
            insertion = rules.index(external_hook) + 2 if external_hook in rules else 1
        else:
            insertion = 1
        _run([*prefix, "-I", parent_chain, str(insertion), *hook_rule])
        changed = True

    chain_rules = _chain_rules(table, chain, namespace=namespace)
    returns = [index for index, rule in enumerate(chain_rules) if rule == ["-j", "RETURN"]]
    if not returns:
        _run([*prefix, "-A", chain, "-j", "RETURN"])
        changed = True
    elif returns != [len(chain_rules) - 1]:
        raise NATGatewayProviderError(f"{chain} must end with one RETURN")
    return changed


def _forward_rules(gateway: dict[str, Any]) -> tuple[list[str], list[str]]:
    provider = gateway["provider"]
    address = f"{_ipv4(gateway['external_address'], 'NATGateway address')}/32"
    outbound = [
        "-i", provider["host_interface"], "-o", provider["external_interface"],
        "-s", address, "-m", "comment", "--comment", _owner(gateway["uid"], "forward-out"),
        "-j", "ACCEPT",
    ]
    inbound = [
        "-i", provider["external_interface"], "-o", provider["host_interface"],
        "-d", address, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED",
        "-m", "comment", "--comment", _owner(gateway["uid"], "forward-in"),
        "-j", "ACCEPT",
    ]
    return outbound, inbound


def _bypass_rule(gateway: dict[str, Any]) -> list[str]:
    return [
        "-s", f"{_ipv4(gateway['external_address'], 'NATGateway address')}/32",
        "-m", "comment", "--comment", _owner(gateway["uid"], "bypass"),
        "-j", "ACCEPT",
    ]


def _snat_rule(gateway: dict[str, Any]) -> list[str]:
    return [
        "-s", _ipv4_network(
            gateway["context"]["virtual_network"]["ipv4_cidr"],
            "NATGateway VirtualNetwork CIDR",
        ),
        "-o", gateway["provider"]["namespace_interface"],
        "-m", "comment", "--comment", _owner(gateway["uid"], "snat"),
        "-j", "SNAT", "--to-source", _ipv4(gateway["external_address"], "NATGateway address"),
    ]


def _infra_bypass_rule(gateway: dict[str, Any]) -> list[str]:
    try:
        transit = ipaddress.ip_interface(gateway["provider"]["next_hop"])
    except (TypeError, ValueError) as error:
        raise NATGatewayProviderError("NATGateway transit next hop is invalid") from error
    if not isinstance(transit, ipaddress.IPv4Interface) or transit.network.prefixlen != 31:
        raise NATGatewayProviderError("NATGateway transit next hop must use an IPv4 /31")
    return [
        "-s", str(transit.network),
        "-o", gateway["provider"]["namespace_interface"],
        "-m", "comment", "--comment", _owner(gateway["uid"], "infra-bypass"),
        "-j", "ACCEPT",
    ]


def _validate_parent(parent: dict[str, Any], gateway: dict[str, Any]) -> None:
    provider = gateway["provider"]
    context = gateway["context"]
    for key in ("namespace_interface", "host_interface", "external_interface"):
        _interface(provider[key], f"NATGateway {key}")
    vn_cidr = _ipv4_network(
        context["virtual_network"]["ipv4_cidr"],
        "NATGateway VirtualNetwork CIDR",
    )
    if _ipv4_network(parent["virtual_network_cidr"], "VirtualNetwork CIDR") != vn_cidr:
        raise NATGatewayProviderError(
            "NATGateway VN CIDR does not match saved VirtualNetwork state"
        )
    try:
        next_hop = ipaddress.ip_interface(provider["next_hop"])
        transit = ipaddress.ip_network(parent["transit"]["cidr"], strict=True)
        vn_network = ipaddress.ip_network(vn_cidr, strict=True)
    except (TypeError, ValueError) as error:
        raise NATGatewayProviderError("NATGateway transit state is invalid") from error
    if (
        not isinstance(next_hop, ipaddress.IPv4Interface)
        or next_hop.network.prefixlen != 31
        or str(next_hop) != provider["next_hop"]
        or not isinstance(transit, ipaddress.IPv4Network)
        or not isinstance(vn_network, ipaddress.IPv4Network)
        or not transit.subnet_of(vn_network)
        or next_hop.network != transit
        or str(transit) != parent["transit"]["cidr"]
    ):
        raise NATGatewayProviderError("NATGateway transit network is invalid")
    if not network.namespace_exists(parent["namespace_name"]):
        raise NATGatewayProviderError("VirtualNetwork namespace is not present")
    host_link = network.link_details(None, provider["host_interface"])
    namespace_link = network.link_details(parent["namespace_name"], provider["namespace_interface"])
    if host_link is None or namespace_link is None:
        raise NATGatewayProviderError("VirtualNetwork transit interfaces are not present")
    if host_link.get("ifalias") != f"osac-vn:{parent['uid']}" or host_link.get("linkinfo", {}).get("info_kind") != "veth":
        raise NATGatewayProviderError("VirtualNetwork host interface ownership is invalid")
    if "UP" not in host_link.get("flags", []) or "UP" not in namespace_link.get("flags", []):
        raise NATGatewayProviderError("VirtualNetwork transit interfaces are not ready")
    external_link = network.link_details(None, provider["external_interface"])
    if external_link is None or "UP" not in external_link.get("flags", []):
        raise NATGatewayProviderError("NATGateway external interface is not ready")
    host_forwarding = _run(["sysctl", "-n", "net.ipv4.ip_forward"]).stdout.strip()
    if host_forwarding != "1":
        raise NATGatewayProviderError("network-node IPv4 forwarding is not enabled")
    expected_next_hop = parent["transit"]["namespace_ip"]
    if provider["next_hop"] != expected_next_hop:
        raise NATGatewayProviderError("NATGateway route next hop does not match saved VirtualNetwork state")


def _verify_owned_forward_chain() -> None:
    rules = _chain_rules("filter", SNAT_FORWARD_CHAIN)
    if not rules or rules[-1] != ["-j", "RETURN"] or rules.count(["-j", "RETURN"]) != 1:
        raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} must end with one RETURN")
    seen = set()
    for rule in rules[:-1]:
        comment = _rule_comment(rule)
        match = OWNER_COMMENT.fullmatch(comment)
        if match is None or match.group(2) not in {"forward-in", "forward-out"}:
            raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} contains an unowned rule")
        if comment in seen:
            raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} contains a duplicate owner rule")
        seen.add(comment)
        try:
            input_interface = _interface(rule[rule.index("-i") + 1], "NATGateway input interface")
            output_interface = _interface(rule[rule.index("-o") + 1], "NATGateway output interface")
            selector = "-s" if match.group(2) == "forward-out" else "-d"
            address_value = rule[rule.index(selector) + 1]
        except (ValueError, IndexError) as error:
            raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} rule is incomplete") from error
        if not address_value.endswith("/32"):
            raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} rule is not address scoped")
        address = _ipv4(address_value[:-3], "NATGateway forwarding address")
        if match.group(2) == "forward-out":
            expected = [
                "-i", input_interface, "-o", output_interface, "-s", f"{address}/32",
                "-m", "comment", "--comment", comment, "-j", "ACCEPT",
            ]
        else:
            expected = [
                "-i", input_interface, "-o", output_interface, "-d", f"{address}/32",
                "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED",
                "-m", "comment", "--comment", comment, "-j", "ACCEPT",
            ]
        if agentless_net_external_ip._forwarding_rule_signature(rule) != agentless_net_external_ip._forwarding_rule_signature(expected):
            raise NATGatewayProviderError(f"{SNAT_FORWARD_CHAIN} contains a broad or malformed rule")


def verify_snat_forwarding_hook() -> None:
    rules = _chain_rules("filter", "FORWARD")
    hook = ["-j", SNAT_FORWARD_CHAIN]
    if rules.count(hook) > 1:
        raise NATGatewayProviderError("duplicate NATGateway forwarding chain hooks exist")
    if hook not in rules:
        return
    _verify_owned_forward_chain()
    expected_index = 1 if ["-j", agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN] in rules else 0
    if rules.index(hook) != expected_index:
        raise NATGatewayProviderError("NATGateway forwarding chain hook is below host isolation rules")


def verify_snat_bypass_hook() -> None:
    rules = _chain_rules("nat", "POSTROUTING")
    hook = ["-j", SNAT_BYPASS_CHAIN]
    if rules.count(hook) != 1 or rules.index(hook) != 0:
        raise NATGatewayProviderError("NATGateway bypass hook must run before host POSTROUTING rules")
    _verify_bypass_chain()


def _verify_bypass_chain() -> None:
    rules = _chain_rules("nat", SNAT_BYPASS_CHAIN)
    if not rules or rules[-1] != ["-j", "RETURN"] or rules.count(["-j", "RETURN"]) != 1:
        raise NATGatewayProviderError(f"{SNAT_BYPASS_CHAIN} must end with one RETURN")
    seen = set()
    for rule in rules[:-1]:
        comment = _rule_comment(rule)
        match = OWNER_COMMENT.fullmatch(comment)
        if match is None or match.group(2) != "bypass":
            raise NATGatewayProviderError(f"{SNAT_BYPASS_CHAIN} contains an unowned rule")
        if comment in seen or "-s" not in rule or rule[-1] != "ACCEPT":
            raise NATGatewayProviderError(f"{SNAT_BYPASS_CHAIN} contains an invalid rule")
        seen.add(comment)
        address = rule[rule.index("-s") + 1]
        if not address.endswith("/32"):
            raise NATGatewayProviderError(f"{SNAT_BYPASS_CHAIN} rule is not address scoped")
        address = _ipv4(address[:-3], "NATGateway bypass address")
        expected = [
            "-s", f"{address}/32", "-m", "comment", "--comment",
            _owner(match.group(1), "bypass"), "-j", "ACCEPT",
        ]
        if rule != expected:
            raise NATGatewayProviderError(f"{SNAT_BYPASS_CHAIN} contains a broad or malformed rule")


def _cleanup_chain_if_empty(table: str, parent_chain: str, chain: str) -> bool:
    rules = _chain_rules(table, chain)
    if not rules:
        return False
    if rules != [["-j", "RETURN"]]:
        return False
    prefix = _iptables_prefix(table)
    hook = ["-j", chain]
    parent_rules = _chain_rules(table, parent_chain)
    if hook in parent_rules:
        _delete_rule(table, parent_chain, hook)
    _run([*prefix, "-F", chain])
    _run([*prefix, "-X", chain])
    return True


def _existing_namespace_nat_rules(gateway: dict[str, Any]) -> list[tuple[list[str], str]]:
    return _owned_rules(
        "nat", "POSTROUTING", gateway["uid"],
        namespace=gateway["provider"]["namespace_name"],
    )


def _reconcile_namespace_nat_rules(gateway: dict[str, Any]) -> bool:
    namespace = gateway["provider"]["namespace_name"]
    desired = (
        ("infra-bypass", _infra_bypass_rule(gateway)),
        ("snat", _snat_rule(gateway)),
    )
    expected_by_kind = dict(desired)
    existing = _existing_namespace_nat_rules(gateway)
    for rule, kind in existing:
        if kind not in expected_by_kind:
            raise NATGatewayProviderError("NATGateway namespace rule type is invalid")
        if (
            agentless_net_external_ip._forwarding_rule_signature(rule)
            != agentless_net_external_ip._forwarding_rule_signature(expected_by_kind[kind])
        ):
            raise NATGatewayProviderError(
                "NATGateway namespace rule changed under the same owner"
            )

    changed = False
    deletions = 0
    for position, (kind, expected) in enumerate(desired, start=1):
        rules = _chain_rules("nat", "POSTROUTING", namespace=namespace)
        signature = agentless_net_external_ip._forwarding_rule_signature(expected)
        matching_positions = [
            index + 1
            for index, rule in enumerate(rules)
            if agentless_net_external_ip._forwarding_rule_signature(rule) == signature
        ]
        if position not in matching_positions:
            _run([
                *_iptables_prefix("nat", namespace),
                "-I", "POSTROUTING", str(position), *expected,
            ])
            changed = True
            rules = _chain_rules("nat", "POSTROUTING", namespace=namespace)
            matching_positions = [
                index + 1
                for index, rule in enumerate(rules)
                if agentless_net_external_ip._forwarding_rule_signature(rule) == signature
            ]
        if position not in matching_positions:
            raise NATGatewayProviderError(
                f"NATGateway {kind} rule did not converge at position {position}"
            )
        for duplicate_position in sorted(
            (item for item in matching_positions if item != position), reverse=True
        ):
            if deletions >= network.MAX_RULE_DELETIONS:
                raise NATGatewayProviderError(
                    "too many duplicate owned NATGateway rules in POSTROUTING"
                )
            _run([
                *_iptables_prefix("nat", namespace),
                "-D", "POSTROUTING", str(duplicate_position),
            ])
            deletions += 1
            changed = True

    rules = _chain_rules("nat", "POSTROUTING", namespace=namespace)
    expected_rules = [rule for _kind, rule in desired]
    for index, expected in enumerate(expected_rules):
        if (
            index >= len(rules)
            or agentless_net_external_ip._forwarding_rule_signature(rules[index])
            != agentless_net_external_ip._forwarding_rule_signature(expected)
        ):
            raise NATGatewayProviderError("NATGateway namespace rule pair did not converge")
    final_owned = _existing_namespace_nat_rules(gateway)
    if len(final_owned) != 2 or {kind for _rule, kind in final_owned} != set(expected_by_kind):
        raise NATGatewayProviderError("NATGateway namespace rule pair has duplicates")
    return changed


def _nat_gateway_conntrack_matches(namespace: str, address: str) -> list[str]:
    result = _run(
        [
            "ip", "netns", "exec", namespace, "conntrack", "-L", "-f", "ipv4",
            "--reply-dst", address,
        ],
        check=False,
    )
    if result.returncode != 0:
        raise NATGatewayProviderError("could not inspect NATGateway conntrack state")
    return [line for line in result.stdout.splitlines() if line.strip()]


def _delete_nat_gateway_conntrack(namespace: str, address: str) -> bool:
    matches = _nat_gateway_conntrack_matches(namespace, address)
    if matches:
        _run(
            [
                "ip", "netns", "exec", namespace, "conntrack", "-D", "-f", "ipv4",
                "--reply-dst", address,
            ],
            check=False,
        )
    if _nat_gateway_conntrack_matches(namespace, address):
        raise NATGatewayProviderError("NATGateway conntrack state remains after cleanup")
    return bool(matches)


def reconcile_nat_gateway(parent: dict[str, Any], gateway: dict[str, Any]) -> bool:
    _validate_parent(parent, gateway)
    namespace = gateway["provider"]["namespace_name"]
    uid = gateway["uid"]
    address = _ipv4(gateway["external_address"], "NATGateway address")
    changed = False
    try:
        changed = _ensure_shared_chain("filter", "FORWARD", SNAT_FORWARD_CHAIN) or changed
        changed = _ensure_shared_chain("nat", "POSTROUTING", SNAT_BYPASS_CHAIN) or changed
        changed = _reconcile_namespace_nat_rules(gateway) or changed
        verify_snat_forwarding_hook()
        verify_snat_bypass_hook()

        outbound, inbound = _forward_rules(gateway)
        for rule, kind in ((outbound, "forward-out"), (inbound, "forward-in")):
            owned = [item for item, owner_kind in _owned_rules("filter", SNAT_FORWARD_CHAIN, uid) if owner_kind == kind]
            if len(owned) > 1 or (
                owned
                and agentless_net_external_ip._forwarding_rule_signature(owned[0])
                != agentless_net_external_ip._forwarding_rule_signature(rule)
            ):
                raise NATGatewayProviderError("NATGateway forwarding rule changed under the same owner")
            # Keep owned rules before the chain's terminal RETURN sentinel.
            changed = _ensure_rule("filter", SNAT_FORWARD_CHAIN, rule, insert=True) or changed

        bypass = _bypass_rule(gateway)
        owned_bypass = [item for item, _kind in _owned_rules("nat", SNAT_BYPASS_CHAIN, uid)]
        if len(owned_bypass) > 1 or (owned_bypass and owned_bypass[0] != bypass):
            raise NATGatewayProviderError("NATGateway bypass rule changed under the same owner")
        changed = _ensure_rule("nat", SNAT_BYPASS_CHAIN, bypass, insert=True) or changed

        verify_snat_forwarding_hook()
        verify_snat_bypass_hook()
        changed = agentless_net_external_ip.ensure_external_route(
            address, gateway["provider"]["next_hop"]
        ) or changed
        if not agentless_net_external_ip._route_uses_next_hop(
            address, gateway["provider"]["next_hop"].split("/", 1)[0]
        ):
            raise NATGatewayProviderError("NATGateway /32 route did not converge through its saved next hop")
        return changed
    except network.NetworkCommandError as error:
        raise NATGatewayProviderError(str(error)) from error


def delete_nat_gateway(parent: dict[str, Any], gateway: dict[str, Any]) -> bool:
    namespace = gateway["provider"]["namespace_name"]
    address = _ipv4(gateway["external_address"], "NATGateway address")
    next_hop = gateway["provider"]["next_hop"]
    changed = False
    try:
        # Withdraw the consumer route before deleting SNAT and established flows.
        changed = agentless_net_external_ip.withdraw_external_route(address, next_hop) or changed

        if network.namespace_exists(namespace):
            namespace_rules = _existing_namespace_nat_rules(gateway)
            for kind in ("snat", "infra-bypass"):
                for rule, rule_kind in namespace_rules:
                    if rule_kind == kind:
                        changed = _delete_rule(
                            "nat", "POSTROUTING", rule, namespace=namespace
                        ) or changed
        for chain in (SNAT_FORWARD_CHAIN, SNAT_BYPASS_CHAIN):
            table = "filter" if chain == SNAT_FORWARD_CHAIN else "nat"
            for rule, _kind in _owned_rules(table, chain, gateway["uid"]):
                changed = _delete_rule(table, chain, rule) or changed

        if network.namespace_exists(namespace):
            changed = _delete_nat_gateway_conntrack(namespace, address) or changed
        for table, parent_chain, chain in (
            ("filter", "FORWARD", SNAT_FORWARD_CHAIN),
            ("nat", "POSTROUTING", SNAT_BYPASS_CHAIN),
        ):
            changed = _cleanup_chain_if_empty(table, parent_chain, chain) or changed

        if network.namespace_exists(namespace) and _existing_namespace_nat_rules(gateway):
            raise NATGatewayProviderError("NATGateway namespace rules remain after cleanup")
        if _owned_rules("filter", SNAT_FORWARD_CHAIN, gateway["uid"]):
            raise NATGatewayProviderError("NATGateway forwarding rules remain after cleanup")
        if _owned_rules("nat", SNAT_BYPASS_CHAIN, gateway["uid"]):
            raise NATGatewayProviderError("NATGateway bypass rule remains after cleanup")
        if _chain_rules("filter", SNAT_FORWARD_CHAIN):
            _verify_owned_forward_chain()
        if _chain_rules("nat", SNAT_BYPASS_CHAIN):
            _verify_bypass_chain()
        result = agentless_net_external_ip._route_output(address)
        if result.returncode == 0 and f"{address}/32" in result.stdout:
            raise NATGatewayProviderError("NATGateway /32 route remains after withdrawal")
        return changed
    except network.NetworkCommandError as error:
        raise NATGatewayProviderError(str(error)) from error
