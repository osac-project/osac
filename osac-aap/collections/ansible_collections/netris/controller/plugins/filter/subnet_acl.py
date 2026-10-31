"""Select provider ACL rows owned by one subnet without relying on subnet indexes."""

import ipaddress
import re
from collections.abc import Callable, Mapping

from ansible.errors import AnsibleFilterError


def _positive_id(value: object) -> int:
    if isinstance(value, bool) or not re.fullmatch(r"[1-9][0-9]*", str(value)):
        raise AnsibleFilterError("Subnet ACL cleanup requires positive numeric resource IDs")
    return int(value)


def subnet_acl_ids(rules: object, vpc_id: object, subnet_cidr: str, security_group_names: object) -> list[int]:
    """Match exact SG names, VPC and directional subnet prefix, including old ACL names."""
    expected_vpc = _positive_id(vpc_id)
    try:
        subnet = ipaddress.IPv4Network(subnet_cidr)
    except (ValueError, TypeError) as exc:
        raise AnsibleFilterError("Subnet ACL cleanup requires a canonical IPv4 subnet") from exc
    if not isinstance(rules, list) or not all(isinstance(rule, Mapping) for rule in rules):
        raise AnsibleFilterError("Netris ACL response must contain a list of rule objects")
    if not isinstance(security_group_names, list) or not all(
        isinstance(name, str) and name for name in security_group_names
    ):
        raise AnsibleFilterError("Subnet ACL cleanup requires SecurityGroup names")
    if not security_group_names:
        return []
    names = "|".join(re.escape(name) for name in security_group_names)
    pattern = re.compile(rf"(?:{names})-(ingress|egress)-[0-9]+(?:-[0-9]+)?")
    selected = set()
    for rule in rules:
        name = rule.get("name")
        match = pattern.fullmatch(name) if isinstance(name, str) else None
        if not match:
            continue
        vpc = rule.get("vpc")
        if not isinstance(vpc, Mapping):
            raise AnsibleFilterError("Cannot establish VPC ownership of a SecurityGroup ACL")
        if _positive_id(vpc.get("id")) != expected_vpc:
            continue
        prefix = rule.get("dst_prefix" if match[1] == "ingress" else "src_prefix")
        try:
            network = ipaddress.IPv4Network(prefix)
        except (ValueError, TypeError) as exc:
            raise AnsibleFilterError("Cannot establish subnet ownership of a SecurityGroup ACL") from exc
        if network == subnet:
            selected.add(_positive_id(rule.get("id")))
    return sorted(selected)


class FilterModule:
    def filters(self) -> dict[str, Callable[..., list[int]]]:
        return {"subnet_acl_ids": subnet_acl_ids}
