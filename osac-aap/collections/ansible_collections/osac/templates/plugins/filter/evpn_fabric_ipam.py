"""Helpers for reserving Netris EVPN gateway space in a CUDN."""

from __future__ import annotations

import ipaddress
from typing import Any

from ansible.errors import AnsibleFilterError


def evpn_fabric_ipam(value: Any) -> dict[str, Any]:
    """Split the Netris reserved pool around its first usable gateway address.

    Netris uses the first usable address in the reserved pool as its gateway.
    OVN-Kubernetes reserves the same address for the CUDN gateway, so it must
    be declared as infrastructure and removed from reservedSubnets to avoid
    allocating the same address twice.
    """
    if not isinstance(value, str):
        raise AnsibleFilterError(
            "fabric_reserved_range must be a canonical IPv4 CIDR; No Kubernetes resources were changed"
        )

    try:
        network = ipaddress.ip_network(value.strip(), strict=True)
    except ValueError as exc:
        raise AnsibleFilterError(
            f"fabric_reserved_range {value!r} must be a canonical IPv4 CIDR; "
            "No Kubernetes resources were changed"
        ) from exc

    if network.version != 4 or network.prefixlen > 30:
        raise AnsibleFilterError(
            "fabric_reserved_range must be an IPv4 CIDR with room for a gateway and DHCP address; "
            "No Kubernetes resources were changed"
        )

    gateway = ipaddress.IPv4Address(int(network.network_address) + 1)
    gateway_network = ipaddress.ip_network(f"{gateway}/32")
    reserved_subnets = sorted(
        network.address_exclude(gateway_network), key=lambda subnet: int(subnet.network_address)
    )
    if len(reserved_subnets) > 25:
        raise AnsibleFilterError(
            f"fabric_reserved_range {network} requires {len(reserved_subnets)} reservedSubnets entries "
            "after separating the gateway; OpenShift permits at most 25. No Kubernetes resources were changed"
        )

    return {
        "gateway_ip": str(gateway),
        "gateway_infrastructure_subnet": f"{gateway}/32",
        "reserved_subnets": [str(subnet) for subnet in reserved_subnets],
    }


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {"evpn_fabric_ipam": evpn_fabric_ipam}
