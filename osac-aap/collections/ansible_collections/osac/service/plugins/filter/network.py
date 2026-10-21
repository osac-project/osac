import ipaddress


def validate_single_ipv4_cidr(cidrs):
    """Validate an ExternalIPPool's single canonical IPv4 CIDR."""
    if not isinstance(cidrs, list) or len(cidrs) != 1 or not isinstance(cidrs[0], str):
        raise ValueError("ExternalIP allocation requires exactly one IPv4 CIDR")

    try:
        network = ipaddress.ip_network(cidrs[0], strict=True)
    except ValueError as error:
        raise ValueError("ExternalIPPool CIDR must be canonical IPv4") from error

    if not isinstance(network, ipaddress.IPv4Network) or str(network) != cidrs[0]:
        raise ValueError("ExternalIPPool CIDR must be canonical IPv4")

    return cidrs[0]


def validate_ipv4_address_in_cidr(address, cidr):
    """Validate a canonical IPv4 address against one canonical IPv4 CIDR."""
    if not isinstance(address, str):
        raise ValueError("ExternalIP address must be a canonical IPv4 address")

    try:
        parsed_address = ipaddress.ip_address(address)
        network = ipaddress.ip_network(validate_single_ipv4_cidr([cidr]), strict=True)
    except ValueError as error:
        raise ValueError("ExternalIP address or ExternalIPPool CIDR is invalid") from error

    if (
        not isinstance(parsed_address, ipaddress.IPv4Address)
        or str(parsed_address) != address
        or parsed_address.is_unspecified
    ):
        raise ValueError("ExternalIP address must be a canonical IPv4 address")
    if parsed_address not in network:
        raise ValueError("ExternalIP address is outside the selected pool")

    return address


def next_available_ip(cidrs, allocated_ips):
    """Return the first free IPv4 host from one or more CIDR ranges.

    Iterate lazily and stop as soon as a free address is found. Returns None
    when every range is exhausted. Network and broadcast addresses are never
    selected, matching IPv4 host allocation semantics.

    Args:
        cidrs: CIDR string or ordered list of CIDR strings
        allocated_ips: dict or list of already-allocated IP strings

    Example:
        ["10.0.100.0/28"] | osac.service.next_available_ip(allocated_ips)
        => "10.0.100.1"  (if 10.0.100.1 is not in allocated_ips)
    """
    cidr_list = [cidrs] if isinstance(cidrs, str) else cidrs
    used = set(allocated_ips)

    for cidr in cidr_list:
        network = ipaddress.ip_network(cidr, strict=False)
        if not isinstance(network, ipaddress.IPv4Network):
            raise ValueError(f"ExternalIP allocation requires an IPv4 CIDR, got {cidr}")

        # Preserve the allocator's existing policy of excluding the network
        # and broadcast addresses, including for /31 and /32 ranges.
        first_host = int(network.network_address) + 1
        broadcast = int(network.broadcast_address)
        for address in range(first_host, broadcast):
            candidate = str(ipaddress.IPv4Address(address))
            if candidate not in used:
                return candidate

    return None


class FilterModule:
    def filters(self):
        return {
            "validate_ipv4_address_in_cidr": validate_ipv4_address_in_cidr,
            "validate_single_ipv4_cidr": validate_single_ipv4_cidr,
            "next_available_ip": next_available_ip,
        }
