"""Normalize a SecurityGroup rule set into the Netris ACL intent.

The contract maps ``PROTOCOL_*`` values to ``tcp``/``udp``/``icmp``/``all``
before dispatch, requires a remote IPv4 CIDR per rule, and carries ports only
for ``tcp`` and ``udp`` (contract 4.10). This filter applies that mapping and
reports the first rule Netris cannot enforce exactly as a plain message.

It returns the message instead of raising so the task can publish a sanitized
``osac_error`` without copying ``ansible_failed_result`` into it.
"""

from __future__ import annotations

import ipaddress
from typing import Any

PROTOCOLS = {
    "PROTOCOL_TCP": "tcp",
    "PROTOCOL_UDP": "udp",
    "PROTOCOL_ICMP": "icmp",
    "PROTOCOL_ALL": "all",
}
PORT_PROTOCOLS = ("tcp", "udp")


def _protocol(rule: dict[str, Any], where: str) -> tuple[str, str]:
    value = rule.get("protocol")
    if not isinstance(value, str):
        return "", f"{where}: protocol must be a string"
    name = PROTOCOLS.get(value, value.lower())
    if name not in PROTOCOLS.values():
        return "", (
            f"{where}: protocol {value!r} is not one of PROTOCOL_TCP, "
            "PROTOCOL_UDP, PROTOCOL_ICMP, PROTOCOL_ALL"
        )
    return name, ""


def _remote_cidr(rule: dict[str, Any], direction: str, where: str) -> tuple[str, str]:
    wanted, unwanted = (
        ("sourceCidr", "destinationCidr")
        if direction == "ingress"
        else ("destinationCidr", "sourceCidr")
    )
    if rule.get(unwanted) not in (None, ""):
        return "", f"{where}: {unwanted} is not valid for an {direction} rule"
    value = rule.get(wanted)
    if not isinstance(value, str) or not value.strip():
        return "", f"{where}: {wanted} must be a non-empty IPv4 CIDR"
    try:
        network = ipaddress.ip_network(value.strip(), strict=True)
    except ValueError:
        return "", f"{where}: {wanted} {value!r} must be a canonical IPv4 CIDR"
    if network.version != 4:
        return "", f"{where}: {wanted} {value!r} must be IPv4"
    return str(network), ""


def _ports(rule: dict[str, Any], protocol: str, where: str) -> tuple[tuple[int | None, int | None], str]:
    present = [key for key in ("portFrom", "portTo") if rule.get(key) is not None]
    if protocol not in PORT_PROTOCOLS:
        if present:
            return (None, None), f"{where}: ports are only valid for tcp and udp rules"
        return (None, None), ""
    if len(present) != 2:
        return (None, None), f"{where}: portFrom and portTo are both required"
    ports: list[int] = []
    for key in ("portFrom", "portTo"):
        value = rule[key]
        if isinstance(value, bool) or not isinstance(value, int):
            return (None, None), f"{where}: {key} must be an integer from 1 to 65535"
        if not 1 <= value <= 65535:
            return (None, None), f"{where}: {key} must be an integer from 1 to 65535"
        ports.append(value)
    if ports[0] > ports[1]:
        return (None, None), f"{where}: portFrom must not exceed portTo"
    return (ports[0], ports[1]), ""


def _direction_rules(
    spec: dict[str, Any], direction: str, group_id: str
) -> tuple[list[dict[str, Any]], str]:
    key = f"{direction}Rules"
    rules = spec.get(key, [])
    if rules is None:
        rules = []
    if not isinstance(rules, list):
        return [], f"{key} must be a list"

    plan: list[dict[str, Any]] = []
    for index, rule in enumerate(rules):
        where = f"SecurityGroup {direction} rule {index}"
        if not isinstance(rule, dict):
            return [], f"{where}: rule must be a mapping"
        protocol, error = _protocol(rule, where)
        if error:
            return [], error
        remote_cidr, error = _remote_cidr(rule, direction, where)
        if error:
            return [], error
        (port_from, port_to), error = _ports(rule, protocol, where)
        if error:
            return [], error
        plan.append(
            {
                "direction": direction,
                "protocol": protocol,
                "remote_cidr": remote_cidr,
                "port_from": port_from,
                "port_to": port_to,
                "name": f"{group_id}-{direction}-{index}" if group_id else f"{direction}-{index}",
            }
        )
    return plan, ""


def security_group_plan(spec: Any, group_id: str = "") -> dict[str, Any]:
    """Return the exact ACL intent for a SecurityGroup snapshot spec."""
    if not isinstance(spec, dict):
        return {"ingress": [], "egress": [], "error": "SecurityGroup spec must be a mapping"}
    if not isinstance(group_id, str):
        return {"ingress": [], "egress": [], "error": "SecurityGroup id must be a string"}

    ingress, error = _direction_rules(spec, "ingress", group_id)
    if error:
        return {"ingress": [], "egress": [], "error": error}
    egress, error = _direction_rules(spec, "egress", group_id)
    if error:
        return {"ingress": [], "egress": [], "error": error}
    return {"ingress": ingress, "egress": egress, "error": ""}


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {"security_group_plan": security_group_plan}
