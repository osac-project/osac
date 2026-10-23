"""Helpers for reserving Netris EVPN infrastructure space in a CUDN."""

from __future__ import annotations

import ipaddress
from typing import Any

from ansible.errors import AnsibleFilterError


def evpn_fabric_ipam(value: Any) -> dict[str, Any]:
    """Split the Netris reserved pool around the gateway and OVN management IP.

    The first usable address is the Netris gateway and the second is the OVN
    management address. Declare both as infrastructure. Leave the third and
    later addresses in reservedSubnets so OVN cannot allocate Netris DHCP IPs.
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
            "fabric_reserved_range must be an IPv4 CIDR with room for a gateway, "
            "OVN management address, and DHCP address; "
            "No Kubernetes resources were changed"
        )

    gateway = ipaddress.IPv4Address(int(network.network_address) + 1)
    management_ip = ipaddress.IPv4Address(int(network.network_address) + 2)
    infrastructure_subnets = [ipaddress.ip_network(f"{address}/32") for address in (gateway, management_ip)]
    reserved_subnets = [network]
    for infrastructure_subnet in infrastructure_subnets:
        remaining_subnets = []
        for subnet in reserved_subnets:
            if infrastructure_subnet.subnet_of(subnet):
                remaining_subnets.extend(subnet.address_exclude(infrastructure_subnet))
            else:
                remaining_subnets.append(subnet)
        reserved_subnets = remaining_subnets
    reserved_subnets.sort(key=lambda subnet: int(subnet.network_address))
    if len(reserved_subnets) > 25:
        raise AnsibleFilterError(
            f"fabric_reserved_range {network} requires {len(reserved_subnets)} reservedSubnets entries "
            "after separating the gateway; OpenShift permits at most 25. No Kubernetes resources were changed"
        )

    return {
        "gateway_ip": str(gateway),
        "gateway_infrastructure_subnet": f"{gateway}/32",
        "management_infrastructure_subnet": f"{management_ip}/32",
        "reserved_subnets": [str(subnet) for subnet in reserved_subnets],
    }


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {"evpn_fabric_ipam": evpn_fabric_ipam}
