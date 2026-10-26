"""Netris cluster ingress reservation contracts for the CaaS guest VIP."""

from __future__ import annotations

import ipaddress
import time

from ansible_collections.osac.templates.plugins.module_utils.netris_external_ip import (
    NetrisExternalIPError as NetrisIpamError,
    NetrisSubnetClient,
)

OPERATIONS = ("cluster_ingress.reserve", "cluster_ingress.release")

RESERVATION_NAME_PREFIX = "osac-ingress-"
RESERVATION_PURPOSE = "load-balancer"

MAX_RESERVE_ATTEMPTS = 5
MAX_RELEASE_OBSERVATIONS = 5
RETRY_DELAY_SECONDS = 1.0

RETRYABLE_CATEGORIES = frozenset(
    {
        "transport_error",
        "provider_error",
        "malformed_response",
        "uncertain_state",
        "exhausted_attempts",
    }
)


class NetrisClusterIngressError(NetrisIpamError):
    """A cluster ingress reservation failed or could not be verified."""


def is_retryable(category: str) -> bool:
    """Return whether OSAC should retry a failure of this category."""
    return category in RETRYABLE_CATEGORIES


def _required_text(value, context: str) -> str:
    """Return the value, or raise ValueError unless it is nonempty stripped text."""
    if not isinstance(value, str) or not value or value != value.strip():
        raise ValueError(f"{context} must be a nonempty string")
    return value


def _canonical_ipv4(value, context: str) -> ipaddress.IPv4Network:
    """Parse a strict canonical IPv4 CIDR string, or raise ValueError."""
    if not isinstance(value, str) or "/" not in value:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR string")
    try:
        network = ipaddress.ip_network(value, strict=True)
    except ValueError as error:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR") from error
    if not isinstance(network, ipaddress.IPv4Network):
        raise ValueError(f"{context} must be an IPv4 CIDR")
    return network


def validate_request(
    operation: str, cluster_id: str, subnet: dict, ipam: dict
) -> dict:
    """Normalize and validate the reservation input before any provider I/O."""
    if operation not in OPERATIONS:
        raise ValueError(f"unsupported cluster ingress operation {operation!r}")
    if not isinstance(subnet, dict):
        raise ValueError("Subnet snapshot must be a dictionary")
    if not isinstance(ipam, dict):
        raise ValueError("ipam must be a dictionary")

    cluster_id = _required_text(cluster_id, "cluster_id")

    spec = subnet.get("spec")
    if not isinstance(spec, dict):
        raise ValueError("Subnet snapshot omits spec")
    subnet_cidr = _canonical_ipv4(spec.get("ipv4Cidr"), "Subnet spec.ipv4Cidr")

    ranges = ipam.get("lb_vip_ipv4_cidrs")
    if not isinstance(ranges, list) or not ranges:
        raise ValueError("ipam.lb_vip_ipv4_cidrs must be a non-empty list")
    networks = [
        _canonical_ipv4(value, "ipam.lb_vip_ipv4_cidrs entry") for value in ranges
    ]
    for network in networks:
        if not network.subnet_of(subnet_cidr):
            raise ValueError(
                f"ipam.lb_vip_ipv4_cidrs entry {network} is outside the Subnet CIDR"
            )

    gateway_value = ipam.get("gateway_ipv4")
    gateway = None
    if gateway_value is not None:
        if not isinstance(gateway_value, str) or "/" in gateway_value:
            raise ValueError("ipam.gateway_ipv4 must be a bare IPv4 address")
        try:
            gateway = ipaddress.IPv4Address(gateway_value)
        except ValueError as error:
            raise ValueError(
                f"ipam.gateway_ipv4 {gateway_value!r} is not an IPv4 address"
            ) from error

    return {
        "operation": operation,
        "cluster_id": cluster_id,
        "reservation_name": f"{RESERVATION_NAME_PREFIX}{cluster_id}",
        "subnet_cidr": str(subnet_cidr),
        "ranges": [str(network) for network in networks],
        "gateway_ipv4": str(gateway) if gateway is not None else "",
    }


def select_ingress_address(
    ranges: list[str], subnet_cidr: str, gateway_ipv4: str, occupied_prefixes: list[str]
) -> str:
    """Return the lowest free eligible address across the supplied ranges."""
    subnet = _canonical_ipv4(subnet_cidr, "Subnet CIDR")
    gateway = ipaddress.IPv4Address(gateway_ipv4) if gateway_ipv4 else None
    occupied = [
        _canonical_ipv4(prefix, "provider subnet prefix")
        for prefix in occupied_prefixes
    ]

    for cidr in ranges:
        network = _canonical_ipv4(cidr, "ipam.lb_vip_ipv4_cidrs entry")
        # A prefix that contains the whole range is its parent allocation, not
        # a reservation inside it; only deletions inside the range block
        # addresses.
        blockers = [prefix for prefix in occupied if not network.subnet_of(prefix)]
        if network.prefixlen >= 31:
            first = int(network.network_address)
            last = int(network.broadcast_address)
        else:
            first = int(network.network_address) + 1
            last = int(network.broadcast_address) - 1
        for value in range(first, last + 1):
            address = ipaddress.IPv4Address(value)
            if address in (subnet.network_address, subnet.broadcast_address, gateway):
                continue
            if any(address in prefix for prefix in blockers):
                continue
            return str(address)

    raise NetrisClusterIngressError(
        f"the load-balancer VIP range {ranges} is full", category="exhausted"
    )


def _owned_record(
    request: dict, subnets: list[dict], site_id: int, tenant_id: int
) -> dict | None:
    """Return the validated cluster_id reservation record, or None if absent."""
    matches = []
    for record in subnets:
        if not isinstance(record, dict):
            raise NetrisClusterIngressError(
                "provider subnet record must be a dictionary",
                category="malformed_response",
            )
        if record.get("name") == request["reservation_name"]:
            matches.append(record)
    if not matches:
        return None
    if len(matches) > 1:
        raise NetrisClusterIngressError(
            f"provider has {len(matches)} reservations named"
            f" {request['reservation_name']}",
            category="uncertain_state",
        )

    record = matches[0]
    prefix = record.get("prefix")
    try:
        network = _canonical_ipv4(prefix, "provider reservation prefix")
    except ValueError as error:
        raise NetrisClusterIngressError(
            str(error), category="malformed_response"
        ) from error
    if network.prefixlen != 32:
        raise NetrisClusterIngressError(
            "provider reservation must be a canonical /32",
            category="malformed_response",
        )
    ranges = [ipaddress.ip_network(cidr) for cidr in request["ranges"]]
    if not any(network.subnet_of(cidr) for cidr in ranges):
        raise NetrisClusterIngressError(
            "provider reservation is outside the load-balancer VIP range",
            category="malformed_response",
        )
    if record.get("purpose") != RESERVATION_PURPOSE:
        raise NetrisClusterIngressError(
            f"provider reservation purpose must be {RESERVATION_PURPOSE}",
            category="malformed_response",
        )
    if record.get("site_id") != site_id or record.get("tenant_id") != tenant_id:
        raise NetrisClusterIngressError(
            "provider reservation does not match the configured site or tenant",
            category="malformed_response",
        )
    return record


def _reservation_address(record: dict) -> str:
    """Return the network address of a reservation's /32 prefix."""
    return str(ipaddress.ip_network(record["prefix"]).network_address)


def _pause(delay_seconds: float, attempt: int, max_attempts: int) -> None:
    """Sleep between attempts until the attempt bound is reached."""
    if attempt < max_attempts and delay_seconds > 0:
        time.sleep(delay_seconds)


def _reconcile_reserve(request, client, site_id, tenant_id, delay_seconds) -> dict:
    """Reserve or reuse the cluster_id address; return changed + address."""
    wrote = False
    last_error = None
    for attempt in range(1, MAX_RESERVE_ATTEMPTS + 1):
        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is not None:
            return {"changed": wrote, "address": _reservation_address(owned)}
        try:
            address = select_ingress_address(
                request["ranges"],
                request["subnet_cidr"],
                request["gateway_ipv4"],
                [record["prefix"] for record in subnets],
            )
        except NetrisClusterIngressError:
            raise
        except ValueError as error:
            raise NetrisClusterIngressError(
                str(error), category="exhausted"
            ) from error

        wrote = True
        try:
            client.create_subnet(
                request["reservation_name"],
                f"{address}/32",
                site_id,
                tenant_id,
                purpose=RESERVATION_PURPOSE,
            )
        except NetrisIpamError as error:
            last_error = NetrisClusterIngressError(
                str(error),
                category=getattr(error, "category", "provider_error"),
                http_status=getattr(error, "http_status", None),
            )
            _pause(delay_seconds, attempt, MAX_RESERVE_ATTEMPTS)
            continue

        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is not None:
            if _reservation_address(owned) != address:
                raise NetrisClusterIngressError(
                    "Netris returned the cluster reservation at an unexpected"
                    " address",
                    category="uncertain_state",
                )
            return {"changed": True, "address": address}
        _pause(delay_seconds, attempt, MAX_RESERVE_ATTEMPTS)

    if last_error is not None:
        raise last_error
    raise NetrisClusterIngressError(
        "Netris cluster ingress reservation did not converge within the"
        " attempt bound",
        category="exhausted_attempts",
    )


def _reconcile_release(request, client, site_id, tenant_id, delay_seconds) -> dict:
    """Delete the cluster_id reservation and confirm absence within the bound."""
    changed = False
    for observation in range(1, MAX_RELEASE_OBSERVATIONS + 1):
        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is None:
            return {"changed": changed, "address": ""}
        changed = True
        try:
            client.delete_subnet(owned["id"])
        except NetrisIpamError as error:
            if getattr(error, "http_status", None) != 404:
                raise NetrisClusterIngressError(
                    str(error),
                    category=getattr(error, "category", "provider_error"),
                ) from error
        _pause(delay_seconds, observation, MAX_RELEASE_OBSERVATIONS)
    raise NetrisClusterIngressError(
        "Netris cluster ingress reservation persists after the observation"
        " bound",
        category="exhausted_attempts",
    )


def reconcile(
    request: dict,
    client,
    site_id: int,
    tenant_id: int,
    delay_seconds: float = RETRY_DELAY_SECONDS,
) -> dict:
    """Run reserve/release against the Netris API; return changed + address."""
    if request["operation"] == "cluster_ingress.reserve":
        return _reconcile_reserve(
            request, client, site_id, tenant_id, delay_seconds
        )
    return _reconcile_release(request, client, site_id, tenant_id, delay_seconds)
