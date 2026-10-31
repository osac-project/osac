"""Tenant VirtualNetwork and Subnet CIDRs for the networking E2E suites.

Tenant CIDRs stay inside 172.16.0.0/12. The Netris lab excludes all of 10.0.0.0/8 (its private allocation) through
``global.networking.excludedIpv4Cidrs``, and the API rejects a VirtualNetwork or Subnet that overlaps an excluded
range. 172.27.0.0/16 is left out because the ExternalIP test pools live there and the API rejects a pool that overlaps a
VirtualNetwork; 172.30.0.0/16 is left out because OpenShift uses it for the service network. That leaves fourteen
/16 networks.
"""

from __future__ import annotations

TENANT_SECOND_OCTETS: tuple[int, ...] = (16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 28, 29, 31)


def tenant_vn_cidr(index: int, *, offset: int = 0) -> str:
    """Return the /16 for network ``index``; indexes below ``len(TENANT_SECOND_OCTETS)`` never collide.

    ``offset`` rotates the starting point (for example by a run id) without breaking that guarantee, because the
    result depends only on ``(index + offset) % len(TENANT_SECOND_OCTETS)``.
    """
    if index < 0 or index >= len(TENANT_SECOND_OCTETS):
        raise ValueError(f"network index {index} is outside the {len(TENANT_SECOND_OCTETS)} reserved tenant networks")
    return f"172.{TENANT_SECOND_OCTETS[(index + offset) % len(TENANT_SECOND_OCTETS)]}.0.0/16"


def run_offset(run_id: str) -> int:
    """Return a stable offset derived from a hexadecimal run id."""
    return int(run_id[:4], 16) % len(TENANT_SECOND_OCTETS)


def subnet_cidr(vn_cidr: str, number: int) -> str:
    """Return the ``number``-th /24 of a tenant /16, for example 172.20.0.0/16 and 1 give 172.20.1.0/24."""
    base = vn_cidr.split("/")[0].rsplit(".", 2)[0]
    return f"{base}.{number}.0/24"
