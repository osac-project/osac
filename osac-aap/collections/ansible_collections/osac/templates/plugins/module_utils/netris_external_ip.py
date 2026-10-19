"""Netris ExternalIP reservation contracts for the v1 job-result path."""

from __future__ import annotations

import ipaddress

OPERATIONS = ("external_ip.allocate", "external_ip.release")

CONTRACT_VERSION = "v1"
TENANT_ANNOTATION = "osac.openshift.io/tenant"
EXTERNAL_IP_ID_LABEL = "osac.openshift.io/externalip-uuid"
CONTRACT_VERSION_ANNOTATION = "osac.openshift.io/externalip-contract-version"
RESERVATION_NAME_PREFIX = "osac-eip-"

RESERVATION_PURPOSE = "nat"

POOL_SNAPSHOT_KEYS = ("fulfillment_id", "kubernetes_uid", "tenant_id", "cidr", "phase")


def _required_text(value, context: str) -> str:
    if not isinstance(value, str) or not value or value != value.strip():
        raise ValueError(f"{context} must be a nonempty string")
    return value


def _positive_int(value, context: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise ValueError(f"{context} must be a positive integer")
    return value


def _canonical_ipv4(value, context: str) -> ipaddress.IPv4Network:
    if not isinstance(value, str) or "/" not in value:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR string")
    try:
        network = ipaddress.ip_network(value, strict=True)
    except ValueError as error:
        raise ValueError(f"{context} must be a canonical IPv4 CIDR") from error
    if not isinstance(network, ipaddress.IPv4Network):
        raise ValueError(f"{context} must be an IPv4 CIDR")
    return network


def validate_request(resource: dict, pool: dict, operation: str) -> dict:
    """Normalize and validate resource + pool snapshots before any provider I/O."""
    if operation not in OPERATIONS:
        raise ValueError(f"unsupported ExternalIP operation {operation!r}")
    if not isinstance(resource, dict):
        raise ValueError("ExternalIP resource snapshot must be a dictionary")
    if not isinstance(pool, dict):
        raise ValueError("ExternalIP pool snapshot must be a dictionary")

    metadata = resource.get("metadata")
    if not isinstance(metadata, dict):
        raise ValueError("ExternalIP resource snapshot omits metadata")
    uid = _required_text(metadata.get("uid"), "ExternalIP resource UID")
    generation = _positive_int(metadata.get("generation"), "ExternalIP generation")
    labels = metadata.get("labels")
    if not isinstance(labels, dict):
        raise ValueError("ExternalIP resource snapshot omits metadata labels")
    resource_id = _required_text(
        labels.get(EXTERNAL_IP_ID_LABEL), "ExternalIP UUID label"
    )
    annotations = metadata.get("annotations")
    if not isinstance(annotations, dict):
        raise ValueError("ExternalIP resource snapshot omits metadata annotations")
    version = annotations.get(CONTRACT_VERSION_ANNOTATION)
    if version != CONTRACT_VERSION:
        raise ValueError(f"unsupported ExternalIP contract version {version!r}")
    tenant_id = _required_text(
        annotations.get(TENANT_ANNOTATION), "ExternalIP tenant annotation"
    )

    deleting = metadata.get("deletionTimestamp")
    if deleting is not None and not isinstance(deleting, str):
        raise ValueError("ExternalIP deletionTimestamp must be a timestamp string or null")
    if operation == "external_ip.allocate" and deleting:
        raise ValueError("ExternalIP allocation rejects a deleting resource")
    if operation == "external_ip.release" and not deleting:
        raise ValueError("ExternalIP release requires a deleting resource")

    spec = resource.get("spec")
    if not isinstance(spec, dict):
        raise ValueError("ExternalIP resource snapshot omits spec")
    pool_reference = _required_text(spec.get("pool"), "ExternalIP pool reference")

    for key in POOL_SNAPSHOT_KEYS:
        if key not in pool:
            raise ValueError(f"ExternalIP pool snapshot omits {key}")
    pool_id = _required_text(pool["fulfillment_id"], "ExternalIPPool fulfillment ID")
    pool_uid = _required_text(pool["kubernetes_uid"], "ExternalIPPool Kubernetes UID")
    pool_tenant_id = _required_text(pool["tenant_id"], "ExternalIPPool tenant ID")
    pool_phase = _required_text(pool["phase"], "ExternalIPPool phase")
    if pool_id != pool_reference:
        raise ValueError("ExternalIP pool reference does not match the pool snapshot")
    if tenant_id != pool_tenant_id and pool_tenant_id != "shared":
        raise ValueError("ExternalIP tenant does not match its pool")

    pool_network = _canonical_ipv4(pool["cidr"], "ExternalIPPool CIDR")
    if operation == "external_ip.allocate" and pool_phase != "Ready":
        raise ValueError("ExternalIP allocation requires a Ready pool")

    return {
        "operation": operation,
        "uid": uid,
        "generation": generation,
        "reservation_name": f"{RESERVATION_NAME_PREFIX}{uid}",
        "resource_id": resource_id,
        "tenant_id": tenant_id,
        "pool_id": pool_id,
        "pool_uid": pool_uid,
        "pool_tenant_id": pool_tenant_id,
        "cidr": str(pool_network),
        "phase": pool_phase,
    }


def _occupied_intervals(cidr: ipaddress.IPv4Network, reserved_prefixes) -> list[tuple[int, int]]:
    intervals: list[tuple[int, int]] = []
    for prefix in reserved_prefixes:
        network = _canonical_ipv4(prefix, "provider subnet prefix")
        intervals.append(
            (int(network.network_address), int(network.broadcast_address))
        )
    intervals.sort()
    merged: list[tuple[int, int]] = []
    for start, end in intervals:
        if merged and start <= merged[-1][1] + 1:
            merged[-1] = (merged[-1][0], max(merged[-1][1], end))
        else:
            merged.append((start, end))
    return merged


def select_address(cidr: str, reserved_prefixes: list[str]) -> str:
    """Return the first unused eligible IPv4 host, or raise ValueError on exhaustion."""
    network = _canonical_ipv4(cidr, "pool CIDR")
    merged = _occupied_intervals(network, reserved_prefixes)

    first_host = int(network.network_address)
    last_host = int(network.broadcast_address)
    if network.prefixlen <= 30:
        first_host += 1
        last_host -= 1

    current = first_host
    for start, end in merged:
        if end < current:
            continue
        if start > last_host:
            break
        if start > current:
            break
        if end >= last_host:
            raise ValueError("pool CIDR has no free host address")
        current = end + 1
    if current > last_host:
        raise ValueError("pool CIDR has no free host address")
    return str(ipaddress.IPv4Address(current))


def owned_address(
    request: dict, subnets: list[dict], site_id: int, tenant_id: int
) -> str | None:
    """Validate/reuse one UID reservation, or return None if it is absent."""
    matches = []
    for record in subnets:
        if not isinstance(record, dict):
            raise ValueError("provider subnet record must be a dictionary")
        name = record.get("name")
        if not isinstance(name, str) or not name:
            raise ValueError("provider subnet record omits its name")
        if name == request["reservation_name"]:
            matches.append(record)

    if not matches:
        return None
    if len(matches) > 1:
        raise ValueError(
            f"provider has {len(matches)} reservations named"
            f" {request['reservation_name']}"
        )

    record = matches[0]
    prefix = record.get("prefix")
    if not isinstance(prefix, str):
        raise ValueError("provider reservation prefix must be a string")
    network = _canonical_ipv4(prefix, "provider reservation prefix")
    if network.prefixlen != 32:
        raise ValueError("provider reservation must be a canonical /32")
    if not network.subnet_of(ipaddress.ip_network(request["cidr"])):
        raise ValueError("provider reservation is outside the pool CIDR")
    if record.get("purpose") != RESERVATION_PURPOSE:
        raise ValueError("provider reservation purpose must be nat")
    if record.get("site_id") != site_id or record.get("tenant_id") != tenant_id:
        raise ValueError("provider reservation does not match the configured site or tenant")
    return str(network.network_address)


def reconcile(request: dict, client, site_id: int, tenant_id: int) -> dict:
    """Run allocation/release against the Netris API; return changed + address."""
    raise NotImplementedError
