import ipaddress


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
            "next_available_ip": next_available_ip,
        }
