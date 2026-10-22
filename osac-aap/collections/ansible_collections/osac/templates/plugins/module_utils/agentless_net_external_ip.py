"""AgentlessNet ExternalIPAttachment Linux networking operations."""

from __future__ import annotations

import ipaddress
import re
import shlex
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils import agentless_net_network as network


class ExternalIPProviderError(network.NetworkCommandError):
    """An ExternalIPAttachment provider operation could not be verified."""


EXTERNAL_FORWARD_CHAIN = "OSAC-EXTERNAL"
SAFE_INTERFACE_NAME = re.compile(r"^[A-Za-z0-9_.-]{1,15}$")
OWNER_COMMENT = re.compile(
    r"^osac-eia:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}):(in|out)$"
)


def _run(command: list[str], *, check: bool = True):
    try:
        return network.run_command(command, check=check)
    except network.NetworkCommandError as error:
        raise ExternalIPProviderError(str(error)) from error


def _ipv4(value: Any, context: str) -> str:
    if not isinstance(value, str):
        raise ExternalIPProviderError(f"{context} must be IPv4")
    try:
        address = ipaddress.ip_address(value)
    except ValueError as error:
        raise ExternalIPProviderError(f"{context} must be IPv4") from error
    if not isinstance(address, ipaddress.IPv4Address) or address.is_unspecified or str(address) != value:
        raise ExternalIPProviderError(f"{context} must be canonical IPv4")
    return value


def _interface(value: Any, context: str) -> str:
    if not isinstance(value, str) or SAFE_INTERFACE_NAME.fullmatch(value) is None:
        raise ExternalIPProviderError(f"{context} is invalid")
    return value


def _owner(uid: str) -> str:
    if not isinstance(uid, str) or OWNER_COMMENT.fullmatch(f"osac-eia:{uid}:in") is None:
        raise ExternalIPProviderError("ExternalIPAttachment UID is invalid")
    return f"osac-eia:{uid}"


def _ensure_iptables_rule(table: str, chain: str, rule: list[str], *, insert: bool) -> bool:
    prefix = ["iptables", "-w", "-t", table]
    check = _run([*prefix, "-C", chain, *rule], check=False)
    if check.returncode == 0:
        return False
    action = "-I" if insert else "-A"
    command = [*prefix, action, chain]
    if insert:
        command.append("1")
    _run([*command, *rule])
    if _run([*prefix, "-C", chain, *rule], check=False).returncode != 0:
        raise ExternalIPProviderError(f"owned iptables rule did not converge in {chain}")
    return True


def _delete_iptables_rule(
    table: str,
    chain: str,
    rule: list[str],
    *,
    namespace: str | None = None,
) -> bool:
    prefix = ["iptables", "-w", "-t", table]
    if namespace is not None:
        prefix = ["ip", "netns", "exec", namespace, *prefix]
    return network.remove_repeated_rule(
        _run,
        [*prefix, "-C", chain, *rule],
        [*prefix, "-D", chain, *rule],
        error_message="too many duplicate owned iptables rules",
        error_type=ExternalIPProviderError,
    )


def _chain_rules() -> list[list[str]]:
    result = _run(["iptables", "-w", "-t", "filter", "-S", EXTERNAL_FORWARD_CHAIN], check=False)
    if result.returncode != 0:
        return []
    rules: list[list[str]] = []
    for line in result.stdout.splitlines():
        parts = shlex.split(line)
        if parts[:2] == ["-A", EXTERNAL_FORWARD_CHAIN]:
            rules.append(parts[2:])
    return rules


def _forwarding_rule_signature(rule: list[str]) -> tuple[list[tuple[str, str]], list[str]]:
    """Compare selector semantics without relying on iptables output ordering."""
    selectors: list[tuple[str, str]] = []
    remainder: list[str] = []
    index = 0
    while index < len(rule):
        token = rule[index]
        if token in {"-d", "-i", "-o", "-s"}:
            if index + 1 >= len(rule):
                raise ExternalIPProviderError("ExternalIP forwarding rule has an incomplete selector")
            selectors.append((token, rule[index + 1]))
            index += 2
        elif token == "--ctstate":
            if index + 1 >= len(rule):
                raise ExternalIPProviderError("ExternalIP forwarding rule has incomplete conntrack state")
            states = rule[index + 1].split(",")
            remainder.extend((token, ",".join(sorted(states))))
            index += 2
        else:
            remainder.append(token)
            index += 1
    return sorted(selectors), remainder


def verify_external_forwarding_chain() -> None:
    """Reject broad rules in the shared chain used before VN host isolation drops."""
    rules = _chain_rules()
    if not rules:
        return
    returns = [index for index, rule in enumerate(rules) if rule == ["-j", "RETURN"]]
    if len(returns) != 1 or returns[0] != len(rules) - 1:
        raise ExternalIPProviderError("ExternalIP forwarding chain must end with one RETURN")
    for rule in rules[:-1]:
        if not rule or rule[-1] != "ACCEPT" or "-j" not in rule:
            raise ExternalIPProviderError("ExternalIP forwarding chain has an unsupported rule")
        try:
            comment_index = rule.index("--comment")
            comment = rule[comment_index + 1]
        except (ValueError, IndexError) as error:
            raise ExternalIPProviderError("ExternalIP forwarding rule has no owner comment") from error
        match = OWNER_COMMENT.fullmatch(comment)
        if match is None:
            raise ExternalIPProviderError("ExternalIP forwarding rule owner is invalid")
        if "-i" not in rule or "-o" not in rule:
            raise ExternalIPProviderError("ExternalIP forwarding rule interfaces are missing")
        external_ip = None
        for option in ("-d", "-s"):
            if option in rule:
                index = rule.index(option)
                if index + 1 < len(rule) and rule[index + 1].endswith("/32"):
                    external_ip = rule[index + 1][:-3]
                    _ipv4(external_ip, "ExternalIP forwarding address")
        if external_ip is None:
            raise ExternalIPProviderError("ExternalIP forwarding rule is not address-scoped")
        if match.group(2) == "in":
            expected = [
                "-i", _interface(rule[rule.index("-i") + 1], "ExternalIP uplink"),
                "-o", _interface(rule[rule.index("-o") + 1], "VirtualNetwork interface"),
                "-d", f"{external_ip}/32", "-m", "comment", "--comment", comment,
                "-j", "ACCEPT",
            ]
        else:
            expected = [
                "-i", _interface(rule[rule.index("-i") + 1], "VirtualNetwork interface"),
                "-o", _interface(rule[rule.index("-o") + 1], "ExternalIP uplink"),
                "-s", f"{external_ip}/32", "-m", "conntrack", "--ctstate",
                "ESTABLISHED,RELATED", "-m", "comment", "--comment", comment,
                "-j", "ACCEPT",
            ]
        if _forwarding_rule_signature(rule) != _forwarding_rule_signature(expected):
            raise ExternalIPProviderError("ExternalIP forwarding rule is broader than its contract")


def _ensure_external_forwarding_chain() -> bool:
    changed = False
    chain = _run(["iptables", "-w", "-t", "filter", "-S", EXTERNAL_FORWARD_CHAIN], check=False)
    if chain.returncode != 0:
        _run(["iptables", "-w", "-t", "filter", "-N", EXTERNAL_FORWARD_CHAIN])
        changed = True

    forward = _run(["iptables", "-w", "-t", "filter", "-S", "FORWARD"]).stdout.splitlines()
    hook = f"-A FORWARD -j {EXTERNAL_FORWARD_CHAIN}"
    if forward.count(hook) > 1:
        raise ExternalIPProviderError("duplicate ExternalIP forwarding hooks exist")
    if hook not in forward:
        _run(["iptables", "-w", "-t", "filter", "-I", "FORWARD", "1", "-j", EXTERNAL_FORWARD_CHAIN])
        changed = True
    elif forward[0] != hook:
        _run(["iptables", "-w", "-t", "filter", "-D", "FORWARD", "-j", EXTERNAL_FORWARD_CHAIN])
        _run(["iptables", "-w", "-t", "filter", "-I", "FORWARD", "1", "-j", EXTERNAL_FORWARD_CHAIN])
        changed = True

    rules = _chain_rules()
    if ["-j", "RETURN"] not in rules:
        _run(["iptables", "-w", "-t", "filter", "-A", EXTERNAL_FORWARD_CHAIN, "-j", "RETURN"])
        changed = True
    verify_external_forwarding_chain()
    return changed


def _forwarding_rules(attachment: dict[str, Any]) -> tuple[list[str], list[str]]:
    provider = attachment["provider"]
    address = f"{attachment['context']['external_ip']['address']}/32"
    owner = _owner(attachment["uid"])
    inbound = [
        "-i", provider["external_interface"], "-o", provider["host_interface"],
        "-d", address, "-m", "comment", "--comment", f"{owner}:in", "-j", "ACCEPT",
    ]
    outbound = [
        "-i", provider["host_interface"], "-o", provider["external_interface"],
        "-s", address, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED",
        "-m", "comment", "--comment", f"{owner}:out", "-j", "ACCEPT",
    ]
    return inbound, outbound


def _validate_parent(parent: dict[str, Any], subnet: dict[str, Any], attachment: dict[str, Any]) -> None:
    provider = attachment["provider"]
    if not network.namespace_exists(parent["namespace_name"]):
        raise ExternalIPProviderError("VirtualNetwork namespace is not present")
    host_link = network.link_details(None, provider["host_interface"])
    namespace_link = network.link_details(parent["namespace_name"], provider["namespace_interface"])
    if host_link is None or namespace_link is None:
        raise ExternalIPProviderError("VirtualNetwork transit interfaces are not present")
    if host_link.get("ifalias") != f"osac-vn:{parent['uid']}" or host_link.get("linkinfo", {}).get("info_kind") != "veth":
        raise ExternalIPProviderError("VirtualNetwork host interface ownership is invalid")
    subnet_link = network.link_details(parent["namespace_name"], subnet["vlan_interface"])
    if subnet_link is None or "UP" not in subnet_link.get("flags", []):
        raise ExternalIPProviderError("Subnet VLAN interface is not ready")
    uplink = network.link_details(None, provider["external_interface"])
    if uplink is None or "UP" not in uplink.get("flags", []):
        raise ExternalIPProviderError("ExternalIP provider uplink is not ready")
    if subnet["virtual_network_uid"] != parent["uid"] or subnet["phase"] != "ready":
        raise ExternalIPProviderError("Subnet is not Ready under the saved VirtualNetwork")


def _external_route_lines(address: str) -> list[list[str]]:
    prefix = f"{_ipv4(address, 'ExternalIP address')}/32"
    pattern = f"^ip route {re.escape(prefix)}[[:space:]]"
    output = _run(["vtysh", "-c", f"show running-config | include {pattern}"]).stdout
    return [
        shlex.split(line.strip())
        for line in output.splitlines()
        if line.strip()
    ]


def _route_output(address: str):
    prefix = f"{_ipv4(address, 'ExternalIP address')}/32"
    return _run(["vtysh", "-c", f"show ip route {prefix}"], check=False)


def _route_uses_next_hop(address: str, next_hop: str) -> bool:
    result = _route_output(address)
    if result.returncode != 0:
        return False
    lines = result.stdout.splitlines()
    exact_route = any(f"{address}/32" in line for line in lines)
    return exact_route and any(next_hop in line for line in lines)


def _configured_external_route(
    address: str, next_hop: str, *, mismatch_message: str
) -> tuple[str, bool]:
    prefix = f"{address}/32"
    configured = _external_route_lines(address)
    if any(len(line) < 4 or line[:3] != ["ip", "route", prefix] for line in configured):
        raise ExternalIPProviderError("ExternalIP route configuration is malformed")
    if any(line[3] != next_hop for line in configured):
        raise ExternalIPProviderError(mismatch_message)
    return prefix, bool(configured)


def ensure_external_route(address: str, next_hop: str) -> bool:
    address = _ipv4(address, "ExternalIP address")
    next_hop = _ipv4(next_hop.split("/", 1)[0], "VirtualNetwork transit next hop")
    prefix, configured = _configured_external_route(
        address,
        next_hop,
        mismatch_message="ExternalIP route is configured with a different next hop",
    )
    changed = False
    if not configured:
        _run(["vtysh", "-c", "configure terminal", "-c", f"ip route {prefix} {next_hop}", "-c", "end"])
        changed = True
    if not _route_uses_next_hop(address, next_hop):
        raise ExternalIPProviderError("ExternalIP /32 route did not converge through its saved next hop")
    return changed


def withdraw_external_route(address: str, next_hop: str) -> bool:
    address = _ipv4(address, "ExternalIP address")
    next_hop = _ipv4(next_hop.split("/", 1)[0], "VirtualNetwork transit next hop")
    prefix, configured = _configured_external_route(
        address,
        next_hop,
        mismatch_message="ExternalIP route changed next hop; refusing to remove it",
    )
    changed = configured
    if configured:
        _run(["vtysh", "-c", "configure terminal", "-c", f"no ip route {prefix} {next_hop}", "-c", "end"])
    if _route_uses_next_hop(address, next_hop):
        raise ExternalIPProviderError("ExternalIP /32 route remains after withdrawal")
    result = _route_output(address)
    if result.returncode == 0 and f"{address}/32" in result.stdout:
        raise ExternalIPProviderError("an ExternalIP /32 route remains after withdrawal")
    return changed


def _conntrack_matches(namespace: str, address: str) -> list[str]:
    needle = {f"src={address}", f"dst={address}"}
    matches: list[str] = []
    for selector in ("-s", "-d"):
        result = _run(
            [
                "ip", "netns", "exec", namespace, "conntrack", "-L", "-f", "ipv4",
                selector, address,
            ],
            check=False,
        )
        if result.returncode != 0:
            raise ExternalIPProviderError("could not inspect VirtualNetwork conntrack state")
        matches.extend(
            line for line in result.stdout.splitlines() if needle.intersection(line.split())
        )
    return matches


def _delete_external_conntrack(namespace: str, address: str) -> bool:
    changed = False
    matches = _conntrack_matches(namespace, address)
    if matches:
        _run(["ip", "netns", "exec", namespace, "conntrack", "-D", "-f", "ipv4", "-s", address], check=False)
        _run(["ip", "netns", "exec", namespace, "conntrack", "-D", "-f", "ipv4", "-d", address], check=False)
        changed = True
    if _conntrack_matches(namespace, address):
        raise ExternalIPProviderError("ExternalIP conntrack state remains after cleanup")
    return changed


def _cleanup_empty_external_chain() -> bool:
    rules = _chain_rules()
    if not rules:
        return False
    verify_external_forwarding_chain()
    if rules != [["-j", "RETURN"]]:
        return False
    hook = ["iptables", "-w", "-t", "filter", "-C", "FORWARD", "-j", EXTERNAL_FORWARD_CHAIN]
    if _run(hook, check=False).returncode == 0:
        _run(["iptables", "-w", "-t", "filter", "-D", "FORWARD", "-j", EXTERNAL_FORWARD_CHAIN])
    _run(["iptables", "-w", "-t", "filter", "-F", EXTERNAL_FORWARD_CHAIN])
    _run(["iptables", "-w", "-t", "filter", "-X", EXTERNAL_FORWARD_CHAIN])
    return True


def reconcile_external_ip_attachment(
    parent: dict[str, Any], subnet: dict[str, Any], attachment: dict[str, Any]
) -> bool:
    _validate_parent(parent, subnet, attachment)
    context = attachment["context"]
    namespace = parent["namespace_name"]
    external_ip = _ipv4(context["external_ip"]["address"], "ExternalIP address")
    target_ip = _ipv4(context["target"]["ip_address"], "BareMetalInstance address")
    provider = attachment["provider"]
    _interface(provider["namespace_interface"], "VirtualNetwork namespace interface")
    _interface(provider["host_interface"], "VirtualNetwork host interface")
    _interface(provider["external_interface"], "ExternalIP uplink")
    owner = _owner(attachment["uid"])

    nat_rule = [
        "-d", f"{external_ip}/32", "-m", "comment", "--comment", f"{owner}:dnat",
        "-j", "DNAT", "--to-destination", target_ip,
    ]
    changed = False
    prefix = ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "nat"]
    if _run([*prefix, "-C", "PREROUTING", *nat_rule], check=False).returncode != 0:
        _run([*prefix, "-A", "PREROUTING", *nat_rule])
        changed = True
    if _run([*prefix, "-C", "PREROUTING", *nat_rule], check=False).returncode != 0:
        raise ExternalIPProviderError("ExternalIP DNAT rule did not converge")

    changed = _ensure_external_forwarding_chain() or changed
    inbound, outbound = _forwarding_rules(attachment)
    changed = _ensure_iptables_rule("filter", EXTERNAL_FORWARD_CHAIN, inbound, insert=True) or changed
    changed = _ensure_iptables_rule("filter", EXTERNAL_FORWARD_CHAIN, outbound, insert=True) or changed
    verify_external_forwarding_chain()

    forwarding = _run(["sysctl", "-n", "net.ipv4.ip_forward"]).stdout.strip()
    if forwarding != "1":
        _run(["sysctl", "-w", "net.ipv4.ip_forward=1"])
        changed = True

    changed = ensure_external_route(external_ip, provider["next_hop"]) or changed
    if _run([*prefix, "-C", "PREROUTING", *nat_rule], check=False).returncode != 0:
        raise ExternalIPProviderError("ExternalIP DNAT rule is absent after route installation")
    if not _route_uses_next_hop(external_ip, provider["next_hop"].split("/", 1)[0]):
        raise ExternalIPProviderError("ExternalIP route is absent after installation")
    return changed


def delete_external_ip_attachment(attachment: dict[str, Any]) -> bool:
    context = attachment["context"]
    provider = attachment["provider"]
    external_ip = _ipv4(context["external_ip"]["address"], "ExternalIP address")
    namespace = provider["namespace_name"]
    owner = _owner(attachment["uid"])
    changed = withdraw_external_route(external_ip, provider["next_hop"])

    prefix = ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "nat"]
    nat_rule = [
        "-d", f"{external_ip}/32", "-m", "comment", "--comment", f"{owner}:dnat",
        "-j", "DNAT", "--to-destination", context["target"]["ip_address"],
    ]
    if network.namespace_exists(namespace):
        changed = _delete_iptables_rule(
            "nat", "PREROUTING", nat_rule, namespace=namespace
        ) or changed

    inbound, outbound = _forwarding_rules(attachment)
    changed = _delete_iptables_rule("filter", EXTERNAL_FORWARD_CHAIN, inbound) or changed
    changed = _delete_iptables_rule("filter", EXTERNAL_FORWARD_CHAIN, outbound) or changed
    if network.namespace_exists(namespace):
        changed = _delete_external_conntrack(namespace, external_ip) or changed
    changed = _cleanup_empty_external_chain() or changed
    return changed
