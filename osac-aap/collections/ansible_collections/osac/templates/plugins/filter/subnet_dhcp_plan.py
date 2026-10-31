"""Derive the Netris Fabric gateway and DHCP pool from the OSAC address layout."""

from __future__ import annotations

import ipaddress
from typing import Any

from ansible.errors import AnsibleFilterError


def _canonical_cidr(value: Any) -> ipaddress.IPv4Network:
    if not isinstance(value, str):
        raise AnsibleFilterError("ipam CIDR entries must be IPv4 CIDR strings")
    try:
        network = ipaddress.ip_network(value.strip(), strict=True)
    except ValueError as exc:
        raise AnsibleFilterError(f"ipam CIDR {value!r} must be a canonical IPv4 CIDR") from exc
    if network.version != 4:
        raise AnsibleFilterError(f"ipam CIDR {value!r} must be IPv4")
    return network


def subnet_dhcp_plan(ipam: Any) -> dict[str, Any]:
    """Return the gateway and DHCP pool the Fabric serves for a Subnet.

    The Fabric serves only the gateway and the physical range. Overlay and
    load-balancer ranges belong to other managers and are never handed out.
    """
    if not isinstance(ipam, dict):
        raise AnsibleFilterError("ipam must be a mapping")

    gateway_value = ipam.get("gateway_ipv4")
    if not isinstance(gateway_value, str) or "/" in gateway_value:
        raise AnsibleFilterError("ipam.gateway_ipv4 must be a bare IPv4 address")
    try:
        gateway = ipaddress.IPv4Address(gateway_value.strip())
    except ValueError as exc:
        raise AnsibleFilterError(
            f"ipam.gateway_ipv4 {gateway_value!r} is not an IPv4 address"
        ) from exc

    physical = ipam.get("physical_ipv4_cidrs", [])
    if not isinstance(physical, list):
        raise AnsibleFilterError("ipam.physical_ipv4_cidrs must be a list")
    if not physical:
        return {
            "gateway_ipv4": str(gateway),
            "dhcp_enabled": False,
            "dhcp_start": None,
            "dhcp_end": None,
        }

    networks = sorted(
        (_canonical_cidr(item) for item in physical),
        key=lambda network: int(network.network_address),
    )
    for previous, current in zip(networks, networks[1:]):
        if int(current.network_address) != int(previous.broadcast_address) + 1:
            raise AnsibleFilterError(
                f"ipam.physical_ipv4_cidrs {physical!r} must be contiguous; "
                "Netris serves one DHCP range per Subnet"
            )

    gateway_int = int(gateway)
    start = int(networks[0].network_address) + 1
    end = int(networks[-1].broadcast_address) - 1
    if gateway_int == start:
        start += 1
    if gateway_int == end:
        end -= 1
    if start > end:
        raise AnsibleFilterError(
            f"ipam.physical_ipv4_cidrs {physical!r} has no usable address for DHCP"
        )

    return {
        "gateway_ipv4": str(gateway),
        "dhcp_enabled": True,
        "dhcp_start": str(ipaddress.IPv4Address(start)),
        "dhcp_end": str(ipaddress.IPv4Address(end)),
    }


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {"subnet_dhcp_plan": subnet_dhcp_plan}
