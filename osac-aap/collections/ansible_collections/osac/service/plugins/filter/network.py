import ipaddress


def dhcp_range_excluding_cidr(subnet_cidr, reserved_cidr=""):
    """Return the Netris DHCP start/end addresses, excluding a reserved CIDR.

    The first usable subnet address is the gateway and the DHCP range starts at
    the second usable address. A reserved VIP range must be contained in the
    subnet and end at the subnet's broadcast address. Restricting the range to
    the addresses before that CIDR prevents DHCP from handing out MetalLB VIPs.
    """
    subnet = ipaddress.ip_network(subnet_cidr, strict=False)
    if subnet.version != 4:
        raise ValueError(f"Netris DHCP range requires an IPv4 subnet, got {subnet_cidr}")

    usable = iter(subnet.hosts())
    try:
        next(usable)  # The first usable address is the gateway.
        dhcp_start = next(usable)
    except StopIteration as exc:
        raise ValueError(f"subnet {subnet_cidr} has no DHCP host range after its gateway") from exc

    dhcp_end = subnet.broadcast_address - 1
    if reserved_cidr:
        reserved = ipaddress.ip_network(reserved_cidr, strict=False)
        if reserved.version != subnet.version or not reserved.subnet_of(subnet):
            raise ValueError(f"reserved CIDR {reserved_cidr} is not contained in subnet {subnet_cidr}")
        if reserved.broadcast_address != subnet.broadcast_address:
            raise ValueError(f"reserved CIDR {reserved_cidr} must end at subnet {subnet_cidr}'s broadcast address")
        dhcp_end = reserved.network_address - 1

    if dhcp_end < dhcp_start:
        raise ValueError(f"subnet {subnet_cidr} has no DHCP host range before reserved CIDR {reserved_cidr}")

    return str(dhcp_start), str(dhcp_end)


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
            "dhcp_range_excluding_cidr": dhcp_range_excluding_cidr,
            "next_available_ip": next_available_ip,
        }
