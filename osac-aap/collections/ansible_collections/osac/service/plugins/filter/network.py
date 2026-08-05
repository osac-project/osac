import ipaddress


def next_available_ip(cidr, allocated_ips):
    """Returns the first available host IP in a CIDR range.

    Iterates through the CIDR lazily and returns the first IP not present
    in allocated_ips. Returns None if the range is exhausted.

    Args:
        cidr: CIDR string (e.g., "10.0.100.0/24")
        allocated_ips: dict or list of already-allocated IP strings

    Example:
        "10.0.100.0/28" | osac.service.next_available_ip(allocated_ips)
        => "10.0.100.1"  (if 10.0.100.1 is not in allocated_ips)
    """
    network = ipaddress.ip_network(cidr, strict=False)
    used = set(allocated_ips) if isinstance(allocated_ips, list) else set(allocated_ips.keys())
    for ip in network.hosts():
        if str(ip) not in used:
            return str(ip)
    raise Exception(
        "No available IPs in %s. All %d host addresses are allocated." % (cidr, len(used))
    )


class FilterModule:
    def filters(self):
        return {
            "next_available_ip": next_available_ip,
        }
