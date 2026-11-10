"""Netris reservation of one ClusterOrder ingress address."""

from __future__ import annotations

import ipaddress
import time

from ansible_collections.osac.templates.plugins.module_utils import netris_external_ip

OPERATIONS = ("cluster_ingress.allocate", "cluster_ingress.release")
NAME_PREFIX = "osac-ci-"
MAX_ALLOCATE_ATTEMPTS = 5
MAX_RELEASE_OBSERVATIONS = 5
RETRY_DELAY_SECONDS = 1.0


def validate_request(
    resource: dict,
    virtual_network: dict,
    subnet: dict,
    ipam: dict,
    operation: str,
    reservation_name: str,
    cluster_id: str,
    vpc_id: int | None,
) -> dict:
    """Validate the ClusterOrder, VirtualNetwork, Subnet and LB VIP ranges."""
    if operation not in OPERATIONS:
        raise ValueError(f"unsupported ClusterOrder ingress operation {operation!r}")
    if not isinstance(resource, dict) or resource.get("kind") != "ClusterOrder":
        raise ValueError("resource must be a ClusterOrder snapshot")
    resource_id = netris_external_ip._api_id(resource.get("id"), "ClusterOrder resource.id")
    cluster_id = netris_external_ip._api_id(cluster_id, "cluster_id")
    if not isinstance(virtual_network, dict) or virtual_network.get("kind") != "VirtualNetwork":
        raise ValueError("virtual_network must be a VirtualNetwork snapshot")
    virtual_network_id = netris_external_ip._api_id(
        virtual_network.get("id"), "VirtualNetwork id"
    )
    if vpc_id is not None and (
        isinstance(vpc_id, bool) or not isinstance(vpc_id, int) or vpc_id <= 0
    ):
        raise ValueError("vpc_id must be a positive Netris VPC ID")
    if operation == "cluster_ingress.allocate" and vpc_id is None:
        raise ValueError("vpc_id is required to allocate a ClusterOrder ingress reservation")
    if not isinstance(subnet, dict) or subnet.get("kind") != "Subnet":
        raise ValueError("subnet must be a Subnet snapshot")
    netris_external_ip._api_id(subnet.get("id"), "Subnet id")
    subnet_spec = subnet.get("spec")
    subnet_network = netris_external_ip._canonical_ipv4(
        subnet_spec.get("ipv4Cidr") if isinstance(subnet_spec, dict) else None,
        "subnet.spec.ipv4Cidr",
    )
    subnet_name = (subnet.get("metadata") or {}).get("name")
    attachment = (resource.get("spec") or {}).get("networkAttachment")
    if not isinstance(subnet_name, str) or not subnet_name:
        raise ValueError("Subnet metadata.name must be set")
    if not isinstance(attachment, dict) or attachment.get("subnetRef") != subnet_name:
        raise ValueError("ClusterOrder networkAttachment.subnetRef must match the Subnet snapshot")
    expected_name = f"{NAME_PREFIX}{cluster_id}"
    if reservation_name != expected_name:
        raise ValueError("reservation_name must be derived from the Cluster API ID")
    if not isinstance(ipam, dict):
        raise ValueError("ipam must be a mapping")
    raw_gateway = ipam.get("gateway_ipv4")
    if not isinstance(raw_gateway, str):
        raise ValueError("ipam.gateway_ipv4 must be a canonical IPv4 address")
    try:
        gateway = ipaddress.IPv4Address(raw_gateway)
    except ipaddress.AddressValueError as error:
        raise ValueError("ipam.gateway_ipv4 must be a canonical IPv4 address") from error
    if str(gateway) != raw_gateway:
        raise ValueError("ipam.gateway_ipv4 must be a canonical IPv4 address")
    if gateway not in subnet_network:
        raise ValueError("ipam.gateway_ipv4 must be inside subnet.spec.ipv4Cidr")
    raw_cidrs = ipam.get("lb_vip_ipv4_cidrs")
    if not isinstance(raw_cidrs, list):
        raise ValueError("ipam.lb_vip_ipv4_cidrs must be a list")
    if operation == "cluster_ingress.allocate" and not raw_cidrs:
        raise netris_external_ip.NetrisExternalIPError(
            "Subnet has no lb_vip_ipv4_cidrs address range", category="exhausted"
        )
    cidrs = []
    for index, value in enumerate(raw_cidrs):
        cidr = netris_external_ip._canonical_ipv4(value, f"ipam.lb_vip_ipv4_cidrs[{index}]")
        if any(cidr.overlaps(existing) for existing in cidrs):
            raise ValueError("ipam.lb_vip_ipv4_cidrs ranges must not overlap")
        if not cidr.subnet_of(subnet_network):
            raise ValueError("ipam.lb_vip_ipv4_cidrs ranges must be inside subnet.spec.ipv4Cidr")
        cidrs.append(cidr)
    return {
        "operation": operation,
        "reservation_name": reservation_name,
        "resource_id": resource_id,
        "cluster_id": cluster_id,
        "virtual_network_id": virtual_network_id,
        "subnet_id": subnet["id"],
        "vpc_id": vpc_id,
        "gateway_ipv4": str(gateway),
        "subnet_network": str(subnet_network.network_address),
        "subnet_broadcast": str(subnet_network.broadcast_address),
        "cidrs": [str(cidr) for cidr in cidrs],
    }


def _owned_record(request: dict, subnets: list[dict], site_id: int, tenant_id: int) -> dict | None:
    """Return the matching reservation after checking its recorded ownership."""
    matches = [record for record in subnets if record.get("name") == request["reservation_name"]]
    if not matches:
        return None
    if len(matches) != 1:
        raise netris_external_ip.NetrisExternalIPError(
            "Netris has ambiguous ClusterOrder ingress reservations",
            category="foreign_object",
        )
    record = matches[0]
    try:
        prefix = netris_external_ip._canonical_ipv4(record.get("prefix"), "provider reservation prefix")
    except ValueError as error:
        raise netris_external_ip.NetrisExternalIPError(
            "Netris ClusterOrder ingress reservation is malformed", category="foreign_object"
        ) from error
    # Release can run after VPC deletion. The API-ID-derived name must be
    # unique, and the record must still match the configured site, tenant,
    # purpose, and the ClusterOrder's immutable Subnet range.
    if (
        prefix.prefixlen != 32
        or not any(prefix.subnet_of(ipaddress.ip_network(cidr)) for cidr in request["cidrs"])
        or record.get("purpose") != netris_external_ip.RESERVATION_PURPOSE
        or record.get("site_id") != site_id
        or record.get("tenant_id") != tenant_id
        or (
            request["vpc_id"] is not None
            and record.get("vpc_id") != request["vpc_id"]
        )
    ):
        raise netris_external_ip.NetrisExternalIPError(
            "Netris ClusterOrder ingress reservation is outside its recorded Subnet range or ownership",
            category="foreign_object",
        )
    return record


def _address(record: dict) -> str:
    """Return the IPv4 host address represented by a Netris reservation."""
    return str(ipaddress.ip_network(record["prefix"]).network_address)


def _select_address(request: dict, subnets: list[dict]) -> str:
    """Choose a free host address from the Subnet's configured LB VIP ranges."""
    for cidr in request["cidrs"]:
        network = ipaddress.ip_network(cidr)
        occupied = []
        for record in subnets:
            prefix = record.get("prefix")
            purpose = record.get("purpose")
            record_vpc_id = record.get("vpc_id")
            if record_vpc_id is not None and record_vpc_id != request["vpc_id"]:
                continue
            try:
                other = ipaddress.ip_network(prefix)
            except (TypeError, ValueError) as error:
                if purpose == netris_external_ip.RESERVATION_PURPOSE:
                    raise netris_external_ip.NetrisExternalIPError(
                        "Netris reservation has a malformed prefix",
                        category="malformed_response",
                    ) from error
                continue
            if other.version != 4:
                continue
            if record.get("vpc_id") is None and other.overlaps(network):
                if (
                    purpose != netris_external_ip.RESERVATION_PURPOSE
                    and network.subnet_of(other)
                ):
                    continue
                raise netris_external_ip.NetrisExternalIPError(
                    "Netris IPAM record with an overlapping prefix omits its VPC",
                    category="malformed_response",
                )
            if (
                purpose == netris_external_ip.RESERVATION_PURPOSE
                or not network.subnet_of(other)
            ):
                occupied.append(str(other))
        try:
            occupied.append(f"{request['subnet_network']}/32")
            occupied.append(f"{request['subnet_broadcast']}/32")
            occupied.append(f"{request['gateway_ipv4']}/32")
            return netris_external_ip.select_address(
                cidr, occupied, exclude_network_broadcast=False
            )
        except ValueError:
            continue
    raise netris_external_ip.NetrisExternalIPError(
        "Subnet lb_vip_ipv4_cidrs has no free host address", category="exhausted"
    )


def _pause(delay_seconds: float, attempt: int, max_attempts: int) -> None:
    """Wait between bounded provider retries or convergence observations."""
    if attempt < max_attempts and delay_seconds > 0:
        time.sleep(delay_seconds)


def _allocate(request: dict, client, site_id: int, tenant_id: int, delay_seconds: float) -> dict:
    """Create or reuse the ClusterOrder's reservation and return its address."""
    wrote = False
    last_error = None
    pending_address = None
    conflicted_address = None
    for attempt in range(1, MAX_ALLOCATE_ATTEMPTS + 1):
        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is not None:
            if pending_address is not None and _address(owned) != pending_address:
                raise netris_external_ip.NetrisExternalIPError(
                    "Netris returned a different address for the pending ClusterOrder ingress reservation",
                    category="uncertain_state",
                )
            if _address(owned) in {
                request["subnet_network"],
                request["subnet_broadcast"],
                request["gateway_ipv4"],
            }:
                try:
                    client.delete_subnet(owned["id"])
                except netris_external_ip.NetrisExternalIPError as error:
                    if error.http_status != 404:
                        raise
                wrote = True
                _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
                continue
            return {"changed": wrote, "address": _address(owned)}
        if pending_address is not None:
            # A successful or ambiguous write may already exist in IPAM. Keep
            # observing this reservation; never submit another create.
            _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
            continue
        address = _select_address(request, subnets)
        if conflicted_address == address:
            # A second create after 409 is allowed only after the listing shows
            # the conflict and selection advances to a different address.
            _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
            continue
        conflicted_address = None
        wrote = True
        try:
            client.create_subnet(
                request["reservation_name"], f"{address}/32", site_id, tenant_id,
                request["vpc_id"],
            )
        except netris_external_ip.NetrisExternalIPError as error:
            last_error = error
            if error.http_status == 409:
                # Do not assume whether Netris saved the request. The next
                # listing must show our reservation or the conflicting address
                # before another create can be considered.
                conflicted_address = address
            elif error.http_status is not None and 400 <= error.http_status < 500:
                # A client error other than 409 is a definitive rejection;
                # retrying a different address would hide the provider error.
                raise
            else:
                # A transport failure, malformed response, or server error
                # may follow a committed write. Observe this reservation before
                # considering another create.
                pending_address = address
            _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
            continue
        pending_address = address
        last_error = None
        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is not None:
            if _address(owned) != address:
                raise netris_external_ip.NetrisExternalIPError(
                    "Netris returned a different address for the ClusterOrder ingress reservation",
                    category="uncertain_state",
                )
            return {"changed": True, "address": address}
        _pause(delay_seconds, attempt, MAX_ALLOCATE_ATTEMPTS)
    if pending_address is not None:
        raise netris_external_ip.NetrisExternalIPError(
            "Netris ClusterOrder ingress reservation may have been created but was not observed",
            category="uncertain_state",
        ) from last_error
    if conflicted_address is not None:
        raise netris_external_ip.NetrisExternalIPError(
            "Netris rejected the ClusterOrder ingress reservation but its conflicting address was not observed",
            category="uncertain_state",
        ) from last_error
    if last_error is not None:
        raise last_error
    raise netris_external_ip.NetrisExternalIPError(
        "Netris ClusterOrder ingress allocation did not converge within the attempt bound",
        category="exhausted_attempts",
    )


def _release(request: dict, client, site_id: int, tenant_id: int, delay_seconds: float) -> dict:
    """Delete the owned reservation and wait until it is absent from Netris."""
    changed = False
    for attempt in range(1, MAX_RELEASE_OBSERVATIONS + 1):
        subnets = client.list_subnets()
        owned = _owned_record(request, subnets, site_id, tenant_id)
        if owned is None:
            return {"changed": changed, "address": ""}
        changed = True
        try:
            client.delete_subnet(owned["id"])
        except netris_external_ip.NetrisExternalIPError as error:
            if error.http_status != 404:
                raise
        _pause(delay_seconds, attempt, MAX_RELEASE_OBSERVATIONS)
    raise netris_external_ip.NetrisExternalIPError(
        "Netris ClusterOrder ingress reservation persists after the observation bound",
        category="exhausted_observations",
    )


def reconcile(request: dict, client, site_id: int, tenant_id: int,
              delay_seconds: float = RETRY_DELAY_SECONDS) -> dict:
    """Allocate or release a durable ClusterOrder ingress reservation."""
    if request["operation"] == "cluster_ingress.allocate":
        return _allocate(request, client, site_id, tenant_id, delay_seconds)
    return _release(request, client, site_id, tenant_id, delay_seconds)
