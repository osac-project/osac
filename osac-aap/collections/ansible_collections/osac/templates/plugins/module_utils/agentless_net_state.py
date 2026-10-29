"""Transactional state for AgentlessNet VirtualNetworks."""

from __future__ import annotations

import contextlib
import fcntl
import hashlib
import ipaddress
import json
import os
import re
import sqlite3
import stat
import time
import uuid as uuidlib
from pathlib import Path
from typing import Any, Callable

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_network import (
    AGENTLESS_NET_HOST_INTERFACE_PREFIX,
    _address_present,
    _default_route_present,
    configure_uplink,
    delete_uplink,
    ensure_ipv4_forwarding,
    ensure_veth_pair,
    link_details,
    NetworkCommandError,
    namespace_exists,
    parse_canonical_ipv4_network,
    parse_json_object_list,
    remove_repeated_rule,
    run_command,
    validate_ipv4_address,
)
from ansible_collections.osac.templates.plugins.module_utils import agentless_net_external_ip
from ansible_collections.osac.templates.plugins.module_utils import agentless_net_nat_gateway
from ansible_collections.osac.templates.plugins.module_utils.agentless_net_subnet import (
    DHCP_MAC_RE,
    MAX_VLAN_ID,
    MIN_VLAN_ID,
    SubnetProviderError,
    cleanup_virtual_network_dhcp,
    dhcp_lease_path,
    ensure_subnet_data_plane,
    find_active_dhcp_lease,
    preflight_dhcp_supervisor,
    prepare_subnet_delete_data_plane,
    read_lease_file_read_only,
)

SCHEMA_VERSION = 1
EXTERNAL_IP_ALLOCATOR_PROBE_LIMIT = 256
SCHEMA_TABLES = {
    "virtual_networks",
    "subnets",
    "external_ip_pools",
    "external_ips",
    "attachments",
    "nat_gateways",
}
PREVIOUS_V1_SCHEMA_TABLES = SCHEMA_TABLES - {"attachments", "nat_gateways"}
PRE_NAT_GATEWAY_V1_SCHEMA_TABLES = SCHEMA_TABLES - {"nat_gateways"}
LOCK_WAIT_SECONDS = 60
LOCK_POLL_SECONDS = 0.05
RESOURCE_LOCK_SHARDS = 256
VIRTUAL_NETWORK_KEYS = {
    "uid",
    "tenant_id",
    "virtual_network_cidr",
    "namespace_name",
    "uplink",
    "transit",
}
TRANSIT_KEYS = {"cidr", "namespace_ip", "host_ip", "gateway"}
SUBNET_KEYS = {
    "uid",
    "virtual_network_uid",
    "tenant_id",
    "ipv4_cidr",
    "vlan_id",
    "vlan_interface",
    "trunk_interface",
    "gateway_ipv4",
    "dhcp_range_start",
    "dhcp_range_end",
    "vip_cidr",
    "phase",
}
SAFE_INTERFACE_NAME = re.compile(r"^[A-Za-z0-9_.-]{1,15}$")
SUBNET_PHASES = {"provisioning", "ready", "deleting"}
ATTACHMENT_PHASES = {"provisioning", "ready", "deleting"}
ATTACHMENT_CONTEXT_KEYS = {"external_ip", "target", "subnet", "virtual_network"}
ATTACHMENT_PROVIDER_KEYS = {
    "namespace_name",
    "namespace_interface",
    "host_interface",
    "external_interface",
    "next_hop",
}
ATTACHMENT_KEYS = {
    "uid",
    "fulfillment_id",
    "tenant_id",
    "external_ip_uid",
    "virtual_network_uid",
    "subnet_uid",
    "external_address",
    "phase",
    "context",
    "provider",
}
NAT_GATEWAY_CONTEXT_KEYS = {"external_ip", "virtual_network"}
NAT_GATEWAY_PHASES = {"provisioning", "ready", "deleting"}
NAT_GATEWAY_PROVIDER_KEYS = {
    "namespace_name",
    "namespace_interface",
    "host_interface",
    "external_interface",
    "next_hop",
}
NAT_GATEWAY_KEYS = {
    "uid",
    "fulfillment_id",
    "tenant_id",
    "external_ip_uid",
    "external_address",
    "virtual_network_uid",
    "phase",
    "context",
    "provider",
}


class StateError(Exception):
    """A requested state transition is invalid."""


class StateCorrupt(StateError):
    """The state file is missing or does not contain a supported generation."""


class _LegacySchemaNeedsMigration(Exception):
    """A read-only open found the previous v1 table layout."""


def _is_canonical_uuid(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        return str(uuidlib.UUID(value)) == value
    except (ValueError, AttributeError):
        return False


def _is_valid_tenant_id(value: Any) -> bool:
    return isinstance(value, str) and bool(value) and value == value.strip()


def _parse_canonical_ipv4_network(value: Any) -> ipaddress.IPv4Network:
    return parse_canonical_ipv4_network(
        value,
        context="VirtualNetwork CIDR",
        wrong_family_message="AgentlessNet VirtualNetworks require IPv4 CIDRs",
    )


def _external_ip_pool_entry(
    uid: Any,
    fulfillment_id: Any,
    tenant_id: Any,
    ip_family: Any,
    cidrs: Any,
) -> dict[str, str]:
    if not _is_canonical_uuid(uid):
        raise StateError("ExternalIPPool UID must be a canonical UUID")
    if not _is_canonical_uuid(fulfillment_id):
        raise StateError("ExternalIPPool Fulfillment UUID must be a canonical UUID")
    if not _is_valid_tenant_id(tenant_id):
        raise StateError("ExternalIPPool tenant ID is required")
    if ip_family != "IPv4":
        raise StateError("ExternalIPPool IP family must be IPv4")
    if not isinstance(cidrs, list) or len(cidrs) != 1:
        raise StateError("ExternalIPPool must contain exactly one canonical IPv4 CIDR")
    try:
        network = parse_canonical_ipv4_network(cidrs[0], context="ExternalIPPool IPv4 CIDR")
    except (TypeError, ValueError) as error:
        raise StateError("ExternalIPPool must contain exactly one canonical IPv4 CIDR") from error
    return {
        "uid": uid,
        "fulfillment_id": fulfillment_id,
        "tenant_id": tenant_id,
        "ip_family": ip_family,
        "ipv4_cidr": str(network),
    }


def _external_ip_identity(
    uid: Any,
    fulfillment_id: Any,
    tenant_id: Any,
    pool_uid: Any,
) -> dict[str, str]:
    if not _is_canonical_uuid(uid):
        raise StateError("ExternalIP UID must be a canonical UUID")
    if not _is_canonical_uuid(fulfillment_id):
        raise StateError("ExternalIP Fulfillment UUID must be a canonical UUID")
    if not _is_valid_tenant_id(tenant_id):
        raise StateError("ExternalIP tenant ID is required")
    if not _is_canonical_uuid(pool_uid):
        raise StateError("ExternalIPPool UID must be a canonical UUID")
    return {
        "uid": uid,
        "fulfillment_id": fulfillment_id,
        "tenant_id": tenant_id,
        "pool_uid": pool_uid,
    }


def _external_ip_entry(
    uid: Any,
    fulfillment_id: Any,
    tenant_id: Any,
    pool_uid: Any,
    address: Any,
) -> dict[str, str]:
    identity = _external_ip_identity(uid, fulfillment_id, tenant_id, pool_uid)
    try:
        validate_ipv4_address(address, "ExternalIP address", error_type=StateError)
    except StateError as error:
        raise StateError("ExternalIP address must be canonical IPv4") from error
    return {**identity, "address": address}


def _external_ip_attachment_context(value: Any, tenant_id: str) -> dict[str, Any]:
    expected_keys = {
        "external_ip": {
            "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "address", "state", "phase",
        },
        "target": {
            "kind", "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "ip_address", "ready",
        },
        "subnet": {
            "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "ipv4_cidr", "virtual_network_id", "phase",
        },
        "virtual_network": {
            "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "phase",
        },
    }
    if not isinstance(value, dict) or set(value) != ATTACHMENT_CONTEXT_KEYS:
        raise StateError("ExternalIPAttachment context has an invalid shape")
    context = {}
    for section, keys in expected_keys.items():
        item = value[section]
        if not isinstance(item, dict) or set(item) != keys:
            raise StateError(f"ExternalIPAttachment {section} context has an invalid shape")
        for key in keys - {"ready"}:
            field = item[key]
            if not isinstance(field, str) or not field or field != field.strip():
                raise StateError(f"ExternalIPAttachment {section} {key} is required")
        if item["tenant_id"] != tenant_id:
            raise StateError(f"ExternalIPAttachment {section} tenant does not match")
        for key in ("fulfillment_id", "kubernetes_uid"):
            if not _is_canonical_uuid(item[key]):
                raise StateError(f"ExternalIPAttachment {section} {key} must be a canonical UUID")
        context[section] = dict(item)

    external_ip = context["external_ip"]
    target = context["target"]
    subnet = context["subnet"]
    virtual_network = context["virtual_network"]
    if external_ip["state"] != "Allocated" or external_ip["phase"] != "Ready":
        raise StateError("ExternalIPAttachment requires an allocated, Ready ExternalIP")
    if target["kind"] != "BareMetalInstance" or target["ready"] is not True:
        raise StateError("AgentlessNet ExternalIPAttachment requires a Ready BareMetalInstance")
    if subnet["phase"] != "Ready" or virtual_network["phase"] != "Ready":
        raise StateError("ExternalIPAttachment requires a Ready Subnet and VirtualNetwork")
    if subnet["virtual_network_id"] != virtual_network["fulfillment_id"]:
        raise StateError("ExternalIPAttachment Subnet does not reference the supplied VirtualNetwork")
    if (
        external_ip["namespace"] != subnet["namespace"]
        or subnet["namespace"] != virtual_network["namespace"]
    ):
        raise StateError("ExternalIPAttachment networking resources must share a namespace")
    try:
        external_address = validate_ipv4_address(
            external_ip["address"],
            "ExternalIPAttachment ExternalIP address",
            reject_unspecified=True,
            error_type=StateError,
        )
    except StateError as error:
        raise StateError(
            "ExternalIPAttachment ExternalIP address must be canonical IPv4"
        ) from error
    try:
        target_address = validate_ipv4_address(
            target["ip_address"],
            "ExternalIPAttachment target address",
            reject_unspecified=True,
            error_type=StateError,
        )
    except StateError as error:
        raise StateError(
            "ExternalIPAttachment target address must be canonical IPv4"
        ) from error
    try:
        subnet_network = parse_canonical_ipv4_network(
            subnet["ipv4_cidr"], context="ExternalIPAttachment Subnet CIDR"
        )
    except (TypeError, ValueError) as error:
        raise StateError("ExternalIPAttachment addresses or Subnet CIDR are invalid") from error
    if subnet_network.prefixlen > 30 or target_address not in subnet_network:
        raise StateError("ExternalIPAttachment target address is outside its canonical IPv4 Subnet")
    return context


def _external_ip_attachment_entry(
    uid: Any,
    fulfillment_id: Any,
    tenant_id: Any,
    context: Any,
    provider: Any,
    phase: Any,
) -> dict[str, Any]:
    if not _is_canonical_uuid(uid):
        raise StateError("ExternalIPAttachment UID must be a canonical UUID")
    if not _is_canonical_uuid(fulfillment_id):
        raise StateError("ExternalIPAttachment Fulfillment UUID must be a canonical UUID")
    if not _is_valid_tenant_id(tenant_id):
        raise StateError("ExternalIPAttachment tenant ID is required")
    if not isinstance(provider, dict) or set(provider) != ATTACHMENT_PROVIDER_KEYS:
        raise StateError("ExternalIPAttachment provider state has an invalid shape")
    for key in ("namespace_name", "namespace_interface", "host_interface", "external_interface", "next_hop"):
        if not isinstance(provider[key], str) or not provider[key] or provider[key] != provider[key].strip():
            raise StateError(f"ExternalIPAttachment provider {key} is required")
    for key in ("namespace_interface", "host_interface", "external_interface"):
        if not SAFE_INTERFACE_NAME.fullmatch(provider[key]):
            raise StateError(f"ExternalIPAttachment provider {key} is invalid")
    try:
        next_hop = ipaddress.ip_interface(provider["next_hop"])
    except ValueError as error:
        raise StateError("ExternalIPAttachment transit next hop is invalid") from error
    if not isinstance(next_hop, ipaddress.IPv4Interface) or next_hop.network.prefixlen != 31:
        raise StateError("ExternalIPAttachment transit next hop must be on an IPv4 /31")
    if phase not in ATTACHMENT_PHASES:
        raise StateError("ExternalIPAttachment provider phase is invalid")
    normalized_context = _external_ip_attachment_context(context, tenant_id)
    entry = {
        "uid": uid,
        "fulfillment_id": fulfillment_id,
        "tenant_id": tenant_id,
        "external_ip_uid": normalized_context["external_ip"]["kubernetes_uid"],
        "virtual_network_uid": normalized_context["virtual_network"]["kubernetes_uid"],
        "subnet_uid": normalized_context["subnet"]["kubernetes_uid"],
        "external_address": normalized_context["external_ip"]["address"],
        "phase": phase,
        "context": normalized_context,
        "provider": dict(provider),
    }
    return entry


def _validate_external_ip_attachment_entry(entry: Any) -> None:
    if not isinstance(entry, dict) or set(entry) != ATTACHMENT_KEYS:
        raise StateCorrupt("ExternalIPAttachment state entry has an invalid shape")
    for key in ("uid", "fulfillment_id", "external_ip_uid", "virtual_network_uid", "subnet_uid"):
        if not _is_canonical_uuid(entry[key]):
            raise StateCorrupt(f"ExternalIPAttachment {key} must be a canonical UUID")
    if not _is_valid_tenant_id(entry["tenant_id"]):
        raise StateCorrupt("ExternalIPAttachment tenant ID is invalid")
    if entry["phase"] not in ATTACHMENT_PHASES:
        raise StateCorrupt("ExternalIPAttachment provider phase is invalid")
    try:
        validate_ipv4_address(
            entry["external_address"],
            "ExternalIPAttachment external address",
            error_type=StateCorrupt,
        )
    except StateCorrupt as error:
        raise StateCorrupt(
            "ExternalIPAttachment external address must be canonical IPv4"
        ) from error
    try:
        context = _external_ip_attachment_context(entry["context"], entry["tenant_id"])
    except StateError as error:
        raise StateCorrupt("ExternalIPAttachment context is malformed") from error
    if (
        entry["external_ip_uid"] != context["external_ip"]["kubernetes_uid"]
        or entry["virtual_network_uid"] != context["virtual_network"]["kubernetes_uid"]
        or entry["subnet_uid"] != context["subnet"]["kubernetes_uid"]
        or entry["external_address"] != context["external_ip"]["address"]
    ):
        raise StateCorrupt("ExternalIPAttachment indexed identity does not match its context")
    provider = entry["provider"]
    if not isinstance(provider, dict) or set(provider) != ATTACHMENT_PROVIDER_KEYS:
        raise StateCorrupt("ExternalIPAttachment provider state is malformed")
    if (
        not all(isinstance(value, str) and value for value in provider.values())
        or any(not SAFE_INTERFACE_NAME.fullmatch(provider[key]) for key in (
            "namespace_interface", "host_interface", "external_interface"
        ))
    ):
        raise StateCorrupt("ExternalIPAttachment provider interface state is invalid")
    try:
        next_hop = ipaddress.ip_interface(provider["next_hop"])
    except (TypeError, ValueError) as error:
        raise StateCorrupt("ExternalIPAttachment transit next hop is invalid") from error
    if not isinstance(next_hop, ipaddress.IPv4Interface) or next_hop.network.prefixlen != 31:
        raise StateCorrupt("ExternalIPAttachment transit next hop must use an IPv4 /31")


def _nat_gateway_context(value: Any, tenant_id: str) -> dict[str, Any]:
    expected_keys = {
        "external_ip": {
            "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "address", "state", "phase",
        },
        "virtual_network": {
            "name", "namespace", "fulfillment_id", "kubernetes_uid",
            "tenant_id", "ipv4_cidr", "phase",
        },
    }
    if not isinstance(value, dict) or set(value) != NAT_GATEWAY_CONTEXT_KEYS:
        raise StateError("NATGateway context has an invalid shape")
    context: dict[str, Any] = {}
    for section, keys in expected_keys.items():
        item = value[section]
        if not isinstance(item, dict) or set(item) != keys:
            raise StateError(f"NATGateway {section} context has an invalid shape")
        for key, field in item.items():
            if not isinstance(field, str) or not field or field != field.strip():
                raise StateError(f"NATGateway {section} {key} is required")
        if item["tenant_id"] != tenant_id:
            raise StateError(f"NATGateway {section} tenant does not match")
        for key in ("fulfillment_id", "kubernetes_uid"):
            if not _is_canonical_uuid(item[key]):
                raise StateError(f"NATGateway {section} {key} must be a canonical UUID")
        if item["phase"] != "Ready":
            raise StateError(f"NATGateway {section} must be Ready")
        if section == "external_ip":
            if item["state"] != "Allocated":
                raise StateError("NATGateway ExternalIP must be Allocated")
            try:
                validate_ipv4_address(
                    item["address"],
                    "NATGateway ExternalIP address",
                    error_type=StateError,
                )
            except StateError as error:
                raise StateError(
                    "NATGateway ExternalIP address must be canonical IPv4"
                ) from error
        else:
            try:
                parse_canonical_ipv4_network(
                    item["ipv4_cidr"], context="NATGateway VirtualNetwork CIDR"
                )
            except (TypeError, ValueError) as error:
                raise StateError("NATGateway VirtualNetwork CIDR must be IPv4") from error
        context[section] = dict(item)
    if context["external_ip"]["namespace"] != context["virtual_network"]["namespace"]:
        raise StateError("NATGateway dependencies must share a namespace")
    return context


def _validate_nat_gateway_entry(entry: Any) -> None:
    if not isinstance(entry, dict) or set(entry) != NAT_GATEWAY_KEYS:
        raise StateCorrupt("NATGateway state entry has an invalid shape")
    for key in ("uid", "fulfillment_id", "external_ip_uid", "virtual_network_uid"):
        if not _is_canonical_uuid(entry[key]):
            raise StateCorrupt(f"NATGateway {key} must be a canonical UUID")
    if not _is_valid_tenant_id(entry["tenant_id"]):
        raise StateCorrupt("NATGateway tenant ID is invalid")
    if entry["phase"] not in NAT_GATEWAY_PHASES:
        raise StateCorrupt("NATGateway provider phase is invalid")
    try:
        validate_ipv4_address(
            entry["external_address"],
            "NATGateway external address",
            error_type=StateCorrupt,
        )
    except StateCorrupt as error:
        raise StateCorrupt("NATGateway external address must be canonical IPv4") from error
    try:
        context = _nat_gateway_context(entry["context"], entry["tenant_id"])
    except StateError as error:
        raise StateCorrupt("NATGateway context is malformed") from error
    if any((
        entry["external_ip_uid"] != context["external_ip"]["kubernetes_uid"],
        entry["virtual_network_uid"] != context["virtual_network"]["kubernetes_uid"],
        entry["external_address"] != context["external_ip"]["address"],
    )):
        raise StateCorrupt("NATGateway indexed identity does not match its context")
    provider = entry["provider"]
    if not isinstance(provider, dict) or set(provider) != NAT_GATEWAY_PROVIDER_KEYS:
        raise StateCorrupt("NATGateway provider state is malformed")
    if any(not isinstance(value, str) or not value for value in provider.values()):
        raise StateCorrupt("NATGateway provider state is incomplete")
    if any(not SAFE_INTERFACE_NAME.fullmatch(provider[key]) for key in (
        "namespace_interface", "host_interface", "external_interface"
    )):
        raise StateCorrupt("NATGateway provider interface state is invalid")
    try:
        next_hop = ipaddress.ip_interface(provider["next_hop"])
    except (TypeError, ValueError) as error:
        raise StateCorrupt("NATGateway transit next hop is invalid") from error
    if (
        not isinstance(next_hop, ipaddress.IPv4Interface)
        or next_hop.network.prefixlen != 31
        or str(next_hop) != provider["next_hop"]
    ):
        raise StateCorrupt("NATGateway transit next hop must use an IPv4 /31")
    try:
        virtual_network = ipaddress.ip_network(
            context["virtual_network"]["ipv4_cidr"], strict=True
        )
    except ValueError as error:
        raise StateCorrupt("NATGateway VirtualNetwork CIDR is invalid") from error
    if not next_hop.network.subnet_of(virtual_network):
        raise StateCorrupt("NATGateway transit next hop is outside its VirtualNetwork")


def _validate_dhcp_supervisor(value: Any) -> str:
    if not isinstance(value, str) or value not in {"systemd", "supervisor"}:
        raise StateError("AgentlessNet DHCP supervisor must be systemd or supervisor")
    return value


def _subnet_payload(
    uid: str,
    virtual_network_uid: str,
    tenant_id: str,
    cidr: str,
    trunk_interface: str,
    vlan_id: int,
    vip_cidr: str,
) -> dict[str, Any]:
    if not _is_canonical_uuid(uid):
        raise StateError("Subnet UID must be a canonical UUID")
    if not _is_canonical_uuid(virtual_network_uid):
        raise StateError("Subnet parent UID must be a canonical UUID")
    if not _is_valid_tenant_id(tenant_id):
        raise StateError("Subnet tenant ID is required")
    try:
        network = parse_canonical_ipv4_network(cidr, context="Subnet CIDR")
    except (TypeError, ValueError) as error:
        raise StateError(
            "Subnet CIDR must be a canonical IPv4 prefix of /30 or shorter for gateway and DHCP support"
        ) from error
    if network.prefixlen > 30:
        raise StateError(
            "Subnet CIDR must be a canonical IPv4 prefix of /30 or shorter for gateway and DHCP support"
        )
    if not isinstance(trunk_interface, str) or not SAFE_INTERFACE_NAME.fullmatch(
        trunk_interface
    ):
        raise StateError("Subnet trunk interface name is invalid")
    if (
        isinstance(vlan_id, bool)
        or not isinstance(vlan_id, int)
        or not MIN_VLAN_ID <= vlan_id <= MAX_VLAN_ID
    ):
        raise StateError("Subnet VLAN ID is outside the supported range")

    gateway = network.network_address + 1
    dhcp_start = network.network_address + 2
    dhcp_end = network.broadcast_address - 1
    if vip_cidr:
        try:
            vip_network = parse_canonical_ipv4_network(vip_cidr, context="Subnet VIP CIDR")
        except (TypeError, ValueError) as error:
            raise StateError("Subnet VIP CIDR must be a canonical upper-end IPv4 block") from error
        if (
            vip_network.prefixlen <= network.prefixlen
            or not vip_network.subnet_of(network)
            or vip_network.broadcast_address != network.broadcast_address
        ):
            raise StateError("Subnet VIP CIDR must be a canonical upper-end IPv4 block")
        dhcp_end = vip_network.network_address - 1
    if dhcp_start > dhcp_end:
        raise StateError("Subnet VIP reservation leaves no DHCP address")

    return {
        "uid": uid,
        "virtual_network_uid": virtual_network_uid,
        "tenant_id": tenant_id,
        "ipv4_cidr": str(network),
        "vlan_id": vlan_id,
        "vlan_interface": f"s{hashlib.sha256(uid.encode()).hexdigest()[:12]}",
        "trunk_interface": trunk_interface,
        "gateway_ipv4": str(gateway),
        "dhcp_range_start": str(dhcp_start),
        "dhcp_range_end": str(dhcp_end),
        "vip_cidr": vip_cidr,
        "phase": "provisioning",
    }


@contextlib.contextmanager
def _locked_path(path: Path):
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        lock_fd = os.open(
            path,
            os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0),
            0o600,
        )
    except OSError as error:
        raise StateError(f"could not open AgentlessNet lock {path.name}: {error}") from error
    try:
        lock_stat = os.fstat(lock_fd)
        if not stat.S_ISREG(lock_stat.st_mode):
            raise StateError(f"AgentlessNet lock {path.name} is not a regular file")
        if lock_stat.st_uid != os.geteuid() or lock_stat.st_mode & 0o077:
            raise StateError(f"AgentlessNet lock {path.name} has unsafe owner or mode")
        deadline = time.monotonic() + LOCK_WAIT_SECONDS
        while True:
            try:
                fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise StateError(f"timed out waiting for AgentlessNet lock {path.name}")
                time.sleep(LOCK_POLL_SECONDS)
        try:
            yield
        finally:
            fcntl.flock(lock_fd, fcntl.LOCK_UN)
    finally:
        os.close(lock_fd)


class StateStore:
    def __init__(self, path: str | os.PathLike[str]) -> None:
        self.path = Path(path)
        self.lock_path = Path(f"{self.path}.lock")
        self.firewall_lock_path = Path(f"{self.path}.firewall.lock")

    @contextlib.contextmanager
    def _locked_database(
        self, *, create: bool, read_only: bool = False
    ):
        if create:
            self.path.parent.mkdir(parents=True, exist_ok=True)
        if read_only:
            try:
                connection = self._open_database(create=False, read_only=True)
            except _LegacySchemaNeedsMigration:
                with _locked_path(self.lock_path):
                    migrated = self._open_database(create=False)
                    if migrated is not None:
                        migrated.close()
                connection = self._open_database(create=False, read_only=True)
            try:
                yield connection
            except sqlite3.DatabaseError as error:
                raise StateCorrupt(
                    f"AgentlessNet state database is corrupt: {error}"
                ) from error
            finally:
                if connection is not None:
                    connection.close()
            return

        with _locked_path(self.lock_path):
            connection = self._open_database(create=create)
            try:
                yield connection
            except sqlite3.DatabaseError as error:
                raise StateCorrupt(
                    f"AgentlessNet state database is corrupt: {error}"
                ) from error
            finally:
                if connection is not None:
                    connection.close()

    @contextlib.contextmanager
    def _resource_locked(self, uid: str):
        digest = hashlib.sha256(uid.encode()).digest()
        shard = int.from_bytes(digest[:4], "big") % RESOURCE_LOCK_SHARDS
        resource_lock_path = Path(f"{self.path}.uid-lock-{shard:03d}.lock")
        with _locked_path(resource_lock_path):
            yield

    def _open_database(
        self, *, create: bool, read_only: bool = False
    ) -> sqlite3.Connection | None:
        if not create and not self.path.exists():
            return None
        flags = (os.O_RDONLY if read_only else os.O_RDWR) | getattr(
            os, "O_NOFOLLOW", 0
        )
        if create:
            flags |= os.O_CREAT
        try:
            state_fd = os.open(self.path, flags, 0o600)
        except FileNotFoundError:
            return None
        except OSError as error:
            raise StateCorrupt(f"could not safely open AgentlessNet state: {error}") from error

        try:
            file_stat = os.fstat(state_fd)
            if not stat.S_ISREG(file_stat.st_mode):
                raise StateCorrupt("AgentlessNet state path is not a regular file")
            if file_stat.st_uid != os.geteuid() or file_stat.st_mode & 0o077:
                raise StateCorrupt("AgentlessNet state file has unsafe owner or mode")
            if read_only:
                connection = sqlite3.connect(
                    f"{self.path.resolve().as_uri()}?mode=ro",
                    uri=True,
                    timeout=LOCK_WAIT_SECONDS,
                    isolation_level=None,
                )
            else:
                connection = sqlite3.connect(
                    self.path,
                    timeout=LOCK_WAIT_SECONDS,
                    isolation_level=None,
                )
        except sqlite3.DatabaseError as error:
            raise StateCorrupt(f"AgentlessNet state database is corrupt: {error}") from error
        finally:
            os.close(state_fd)

        try:
            connection.execute("PRAGMA foreign_keys = ON")
            tables = {
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'table'"
                )
            }
            version = connection.execute("PRAGMA user_version").fetchone()[0]
            if not tables and version == 0 and create:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """CREATE TABLE virtual_networks (
                        uid TEXT PRIMARY KEY,
                        tenant_id TEXT NOT NULL,
                        virtual_network_cidr TEXT NOT NULL,
                        namespace_name TEXT NOT NULL UNIQUE,
                        namespace_interface TEXT NOT NULL UNIQUE,
                        host_interface TEXT NOT NULL UNIQUE,
                        transit_cidr TEXT NOT NULL,
                        transit_start INTEGER NOT NULL UNIQUE,
                        payload TEXT NOT NULL
                    )"""
                    )
                    self._create_subnet_schema(connection)
                    self._create_external_ip_pool_schema(connection)
                    self._create_external_ip_schema(connection)
                    self._create_external_ip_attachment_schema(connection)
                    self._create_nat_gateway_schema(connection)
                    connection.execute(f"PRAGMA user_version = {SCHEMA_VERSION}")
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
                tables = set(SCHEMA_TABLES)
                version = SCHEMA_VERSION

            if version == SCHEMA_VERSION and tables in (
                PREVIOUS_V1_SCHEMA_TABLES,
                PRE_NAT_GATEWAY_V1_SCHEMA_TABLES,
            ):
                if read_only:
                    raise _LegacySchemaNeedsMigration
                connection.execute("BEGIN IMMEDIATE")
                try:
                    tables = {
                        row[0]
                        for row in connection.execute(
                            "SELECT name FROM sqlite_master WHERE type = 'table'"
                        )
                    }
                    version = connection.execute("PRAGMA user_version").fetchone()[0]
                    if version == SCHEMA_VERSION and tables in (
                        PREVIOUS_V1_SCHEMA_TABLES,
                        PRE_NAT_GATEWAY_V1_SCHEMA_TABLES,
                    ):
                        self._validate_virtual_network_table(connection)
                        self._validate_subnet_table(connection)
                        self._validate_external_ip_pool_schema(connection)
                        self._validate_external_ip_schema(connection)
                        if "attachments" not in tables:
                            self._create_external_ip_attachment_schema(connection)
                        else:
                            self._validate_external_ip_attachment_schema(connection)
                        self._create_nat_gateway_schema(connection)
                    elif version != SCHEMA_VERSION or tables != SCHEMA_TABLES:
                        raise StateCorrupt(
                            "AgentlessNet state database changed during schema migration"
                        )
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
                tables = {
                    row[0]
                    for row in connection.execute(
                        "SELECT name FROM sqlite_master WHERE type = 'table'"
                    )
                }
                version = connection.execute("PRAGMA user_version").fetchone()[0]

            if version != SCHEMA_VERSION:
                raise StateCorrupt(f"unsupported state schema: {version!r}")
            if tables != SCHEMA_TABLES:
                raise StateCorrupt("AgentlessNet state database has an invalid schema")
            self._validate_virtual_network_table(connection)
            self._validate_subnet_table(connection)
            self._validate_external_ip_pool_schema(connection)
            self._validate_external_ip_schema(connection)
            self._validate_external_ip_attachment_schema(connection)
            self._validate_nat_gateway_schema(connection)
            if not read_only:
                connection.execute(
                    "CREATE INDEX IF NOT EXISTS external_ips_by_pool "
                    "ON external_ips(pool_uid)"
                )
                address_value_sql = self._external_ip_address_value_sql()
                connection.execute(
                    "CREATE INDEX IF NOT EXISTS external_ips_by_pool_address_value "
                    f"ON external_ips(pool_uid, {address_value_sql})"
                )
            return connection
        except (sqlite3.DatabaseError, OSError) as error:
            connection.close()
            raise StateCorrupt(f"AgentlessNet state database is corrupt: {error}") from error
        except BaseException:
            connection.close()
            raise

    @staticmethod
    def _create_subnet_schema(connection: sqlite3.Connection) -> None:
        connection.execute(
            """CREATE TABLE subnets (
                uid TEXT PRIMARY KEY,
                virtual_network_uid TEXT NOT NULL
                    REFERENCES virtual_networks(uid),
                tenant_id TEXT NOT NULL,
                vlan_id INTEGER NOT NULL UNIQUE,
                vlan_interface TEXT NOT NULL UNIQUE,
                ipv4_cidr TEXT NOT NULL,
                phase TEXT NOT NULL,
                payload TEXT NOT NULL
            )"""
        )
        connection.execute(
            "CREATE INDEX subnets_by_vn ON subnets(virtual_network_uid)"
        )

    @staticmethod
    def _create_external_ip_pool_schema(connection: sqlite3.Connection) -> None:
        connection.execute(
            """CREATE TABLE external_ip_pools (
                uid TEXT NOT NULL PRIMARY KEY,
                fulfillment_id TEXT NOT NULL UNIQUE,
                tenant_id TEXT NOT NULL,
                ip_family TEXT NOT NULL,
                ipv4_cidr TEXT NOT NULL
            )"""
        )

    @staticmethod
    def _create_external_ip_schema(connection: sqlite3.Connection) -> None:
        connection.execute(
            """CREATE TABLE external_ips (
                uid TEXT NOT NULL PRIMARY KEY,
                fulfillment_id TEXT NOT NULL UNIQUE,
                tenant_id TEXT NOT NULL,
                pool_uid TEXT NOT NULL REFERENCES external_ip_pools(uid),
                address TEXT NOT NULL,
                UNIQUE (pool_uid, address)
            )"""
        )

    @staticmethod
    def _create_external_ip_attachment_schema(connection: sqlite3.Connection) -> None:
        connection.execute(
            """CREATE TABLE attachments (
                uid TEXT NOT NULL PRIMARY KEY,
                fulfillment_id TEXT NOT NULL UNIQUE,
                tenant_id TEXT NOT NULL,
                external_ip_uid TEXT NOT NULL UNIQUE REFERENCES external_ips(uid),
                virtual_network_uid TEXT NOT NULL REFERENCES virtual_networks(uid),
                subnet_uid TEXT NOT NULL REFERENCES subnets(uid),
                external_address TEXT NOT NULL UNIQUE,
                phase TEXT NOT NULL,
                payload TEXT NOT NULL
            )"""
        )
        connection.execute(
            "CREATE INDEX attachments_by_vn ON attachments(virtual_network_uid)"
        )
        connection.execute(
            "CREATE INDEX attachments_by_subnet ON attachments(subnet_uid)"
        )

    @staticmethod
    def _create_nat_gateway_schema(connection: sqlite3.Connection) -> None:
        connection.execute(
            """CREATE TABLE nat_gateways (
                uid TEXT NOT NULL PRIMARY KEY,
                fulfillment_id TEXT NOT NULL UNIQUE,
                tenant_id TEXT NOT NULL,
                external_ip_uid TEXT NOT NULL UNIQUE REFERENCES external_ips(uid),
                virtual_network_uid TEXT NOT NULL REFERENCES virtual_networks(uid),
                external_address TEXT NOT NULL UNIQUE,
                phase TEXT NOT NULL,
                payload TEXT NOT NULL
            )"""
        )
        connection.execute(
            "CREATE INDEX nat_gateways_by_vn ON nat_gateways(virtual_network_uid)"
        )

    @staticmethod
    def _external_ip_address_value_sql() -> str:
        first_tail = "substr(address, instr(address, '.') + 1)"
        second_tail = f"substr({first_tail}, instr({first_tail}, '.') + 1)"
        third_tail = f"substr({second_tail}, instr({second_tail}, '.') + 1)"
        first_octet = "substr(address, 1, instr(address, '.') - 1)"
        second_octet = f"substr({first_tail}, 1, instr({first_tail}, '.') - 1)"
        third_octet = f"substr({second_tail}, 1, instr({second_tail}, '.') - 1)"
        return (
            f"(CAST({first_octet} AS INTEGER) * 16777216 + "
            f"CAST({second_octet} AS INTEGER) * 65536 + "
            f"CAST({third_octet} AS INTEGER) * 256 + "
            f"CAST({third_tail} AS INTEGER))"
        )

    @staticmethod
    def _validate_external_ip_identity_table_schema(
        connection: sqlite3.Connection,
        table_name: str,
        resource_name: str,
        expected_columns: set[str],
        required_unique_indexes: set[tuple[str, ...]],
    ) -> None:
        column_info = {
            row[1]: row
            for row in connection.execute(f"PRAGMA table_info({table_name})")
        }
        if set(column_info) != expected_columns:
            raise StateCorrupt(
                f"AgentlessNet {table_name} table has an invalid schema"
            )
        if any(
            column_info[name][2].upper() != "TEXT" or column_info[name][3] != 1
            for name in expected_columns
        ):
            raise StateCorrupt(
                f"AgentlessNet {resource_name} constraints are missing"
            )
        if column_info["uid"][5] != 1:
            raise StateCorrupt(
                f"AgentlessNet {resource_name} UID primary key is missing"
            )

        unique_columns = set()
        for index_row in connection.execute(f"PRAGMA index_list({table_name})"):
            if not index_row[2] or index_row[4]:
                continue
            quoted_index = "'" + index_row[1].replace("'", "''") + "'"
            columns = tuple(
                row[2]
                for row in connection.execute(
                    f"PRAGMA index_info({quoted_index})"
                ).fetchall()
            )
            unique_columns.add(columns)
        if not required_unique_indexes.issubset(unique_columns):
            raise StateCorrupt(
                f"AgentlessNet {resource_name} uniqueness constraints are missing"
            )

    @staticmethod
    def _validate_external_ip_schema(connection: sqlite3.Connection) -> None:
        StateStore._validate_external_ip_identity_table_schema(
            connection,
            "external_ips",
            "ExternalIP",
            {"uid", "fulfillment_id", "tenant_id", "pool_uid", "address"},
            {("fulfillment_id",), ("pool_uid", "address")},
        )

        foreign_keys = {
            (row[2], row[3], row[4])
            for row in connection.execute("PRAGMA foreign_key_list(external_ips)")
        }
        if ("external_ip_pools", "pool_uid", "uid") not in foreign_keys:
            raise StateCorrupt("AgentlessNet ExternalIP pool foreign key is missing")

    @staticmethod
    def _validate_external_ip_pool_schema(connection: sqlite3.Connection) -> None:
        StateStore._validate_external_ip_identity_table_schema(
            connection,
            "external_ip_pools",
            "ExternalIPPool",
            {"uid", "fulfillment_id", "tenant_id", "ip_family", "ipv4_cidr"},
            {("fulfillment_id",)},
        )

    @staticmethod
    def _validate_external_ip_attachment_schema(connection: sqlite3.Connection) -> None:
        StateStore._validate_external_ip_identity_table_schema(
            connection,
            "attachments",
            "ExternalIPAttachment",
            {
                "uid",
                "fulfillment_id",
                "tenant_id",
                "external_ip_uid",
                "virtual_network_uid",
                "subnet_uid",
                "external_address",
                "phase",
                "payload",
            },
            {("fulfillment_id",), ("external_ip_uid",), ("external_address",)},
        )
        foreign_keys = {
            (row[2], row[3], row[4])
            for row in connection.execute("PRAGMA foreign_key_list(attachments)")
        }
        if not {
            ("external_ips", "external_ip_uid", "uid"),
            ("virtual_networks", "virtual_network_uid", "uid"),
            ("subnets", "subnet_uid", "uid"),
        }.issubset(foreign_keys):
            raise StateCorrupt("AgentlessNet ExternalIPAttachment foreign keys are missing")
        for query, column_index, column in (
            (
                "PRAGMA index_info(attachments_by_vn)",
                4,
                "virtual_network_uid",
            ),
            (
                "PRAGMA index_info(attachments_by_subnet)",
                5,
                "subnet_uid",
            ),
        ):
            rows = connection.execute(query).fetchall()
            if rows != [(0, column_index, column)]:
                raise StateCorrupt("AgentlessNet ExternalIPAttachment indexes are invalid")

    @staticmethod
    def _validate_nat_gateway_schema(connection: sqlite3.Connection) -> None:
        StateStore._validate_external_ip_identity_table_schema(
            connection,
            "nat_gateways",
            "NATGateway",
            {
                "uid",
                "fulfillment_id",
                "tenant_id",
                "external_ip_uid",
                "virtual_network_uid",
                "external_address",
                "phase",
                "payload",
            },
            {("fulfillment_id",), ("external_ip_uid",), ("external_address",)},
        )
        foreign_keys = {
            (row[2], row[3], row[4])
            for row in connection.execute("PRAGMA foreign_key_list(nat_gateways)")
        }
        if not {
            ("external_ips", "external_ip_uid", "uid"),
            ("virtual_networks", "virtual_network_uid", "uid"),
        }.issubset(foreign_keys):
            raise StateCorrupt("AgentlessNet NATGateway foreign keys are missing")
        if connection.execute("PRAGMA index_info(nat_gateways_by_vn)").fetchall() != [
            (0, 4, "virtual_network_uid")
        ]:
            raise StateCorrupt("AgentlessNet NATGateway index is invalid")

    @staticmethod
    def _validate_virtual_network_table(connection: sqlite3.Connection) -> None:
        column_info = {
            row[1]: row
            for row in connection.execute("PRAGMA table_info(virtual_networks)")
        }
        columns = set(column_info)
        expected_columns = {
            "uid",
            "tenant_id",
            "virtual_network_cidr",
            "namespace_name",
            "namespace_interface",
            "host_interface",
            "transit_cidr",
            "transit_start",
            "payload",
        }
        if columns != expected_columns:
            raise StateCorrupt("AgentlessNet state database has an invalid schema")
        if column_info["uid"][5] != 1 or any(
            column_info[name][3] != 1
            for name in expected_columns - {"uid"}
        ):
            raise StateCorrupt("AgentlessNet VirtualNetwork constraints are missing")
        unique_columns = set()
        for index_row in connection.execute("PRAGMA index_list(virtual_networks)"):
            if index_row[2]:
                index_info = connection.execute(
                    f"PRAGMA index_info('{index_row[1]}')"
                ).fetchone()
                if index_info is not None:
                    unique_columns.add(index_info[2])
        if not {
            "uid",
            "namespace_name",
            "namespace_interface",
            "host_interface",
            "transit_start",
        }.issubset(unique_columns):
            raise StateCorrupt("AgentlessNet VirtualNetwork uniqueness constraints are missing")

    @staticmethod
    def _validate_subnet_table(connection: sqlite3.Connection) -> None:
        column_info = {
            row[1]: row for row in connection.execute("PRAGMA table_info(subnets)")
        }
        columns = set(column_info)
        if columns != {
            "uid",
            "virtual_network_uid",
            "tenant_id",
            "vlan_id",
            "vlan_interface",
            "ipv4_cidr",
            "phase",
            "payload",
        }:
            raise StateCorrupt("AgentlessNet Subnet table has an invalid schema")
        if column_info["uid"][5] != 1 or any(
            column_info[name][3] != 1
            for name in columns - {"uid"}
        ):
            raise StateCorrupt("AgentlessNet Subnet constraints are missing")
        index = connection.execute(
            """SELECT sql FROM sqlite_master
               WHERE type = 'index' AND name = 'subnets_by_vn'"""
        ).fetchone()
        if index is None or connection.execute(
            "PRAGMA index_info(subnets_by_vn)"
        ).fetchall() != [(0, 1, "virtual_network_uid")]:
            raise StateCorrupt("AgentlessNet Subnet index has an invalid schema")
        foreign_keys = connection.execute(
            "PRAGMA foreign_key_list(subnets)"
        ).fetchall()
        if not any(
            row[2] == "virtual_networks"
            and row[3] == "virtual_network_uid"
            and row[4] == "uid"
            and row[6] == "NO ACTION"
            for row in foreign_keys
        ):
            raise StateCorrupt("AgentlessNet Subnet parent constraint is missing")
        unique_columns = set()
        for index_row in connection.execute("PRAGMA index_list(subnets)"):
            if index_row[2]:
                index_info = connection.execute(
                    f"PRAGMA index_info('{index_row[1]}')"
                ).fetchone()
                if index_info is not None:
                    unique_columns.add(index_info[2])
        if not {"uid", "vlan_id", "vlan_interface"}.issubset(unique_columns):
            raise StateCorrupt("AgentlessNet Subnet uniqueness constraints are missing")

    @staticmethod
    def _validate_entry(entry: Any) -> None:
        if not isinstance(entry, dict) or set(entry) != VIRTUAL_NETWORK_KEYS:
            raise StateCorrupt("VirtualNetwork state entry has an invalid shape")
        uid = entry["uid"]
        tenant_id = entry["tenant_id"]
        namespace = entry["namespace_name"]
        uplink = entry["uplink"]
        transit = entry["transit"]
        if not _is_canonical_uuid(uid):
            raise StateCorrupt("VirtualNetwork UID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateCorrupt("VirtualNetwork tenant ID is invalid")
        identity = StateStore._identity_entry(uid)
        if namespace != identity["namespace_name"]:
            raise StateCorrupt("VirtualNetwork namespace does not match its UID")
        if not isinstance(uplink, dict) or set(uplink) != {
            "namespace_interface",
            "host_interface",
        }:
            raise StateCorrupt("VirtualNetwork uplink state is invalid")
        if uplink != identity["uplink"]:
            raise StateCorrupt("VirtualNetwork uplink does not match its UID")
        if not isinstance(transit, dict) or set(transit) != TRANSIT_KEYS:
            raise StateCorrupt("VirtualNetwork transit state is invalid")
        if not all(
            isinstance(transit[key], str)
            for key in ("namespace_ip", "host_ip", "gateway")
        ):
            raise StateCorrupt("VirtualNetwork transit endpoint is invalid")
        try:
            transit_cidr = parse_canonical_ipv4_network(
                transit["cidr"], context="VirtualNetwork transit CIDR"
            )
        except (TypeError, ValueError) as error:
            raise StateCorrupt("VirtualNetwork transit CIDR is invalid") from error
        if transit_cidr.prefixlen != 31:
            raise StateCorrupt("VirtualNetwork transit CIDR must be an IPv4 /31")
        try:
            virtual_network_cidr = _parse_canonical_ipv4_network(
                entry["virtual_network_cidr"]
            )
        except (TypeError, ValueError) as error:
            raise StateCorrupt(f"VirtualNetwork CIDR is invalid: {error}") from error
        if not transit_cidr.subnet_of(virtual_network_cidr):
            raise StateCorrupt("VirtualNetwork transit CIDR is outside its CR CIDR")
        if transit["gateway"] != transit["host_ip"].split("/", 1)[0]:
            raise StateCorrupt("VirtualNetwork gateway does not match host IP")
        if transit["namespace_ip"] != f"{transit_cidr.network_address + 1}/{transit_cidr.prefixlen}":
            raise StateCorrupt("VirtualNetwork namespace IP is invalid")
        if transit["host_ip"] != f"{transit_cidr.network_address}/{transit_cidr.prefixlen}":
            raise StateCorrupt("VirtualNetwork host IP is invalid")

    @staticmethod
    def _validate_subnet_entry(entry: Any) -> None:
        if not isinstance(entry, dict) or set(entry) != SUBNET_KEYS:
            raise StateCorrupt("Subnet state entry has an invalid shape")
        if not _is_canonical_uuid(entry["uid"]):
            raise StateCorrupt("Subnet UID must be a canonical UUID")
        if not _is_canonical_uuid(entry["virtual_network_uid"]):
            raise StateCorrupt("Subnet parent UID must be a canonical UUID")
        if not _is_valid_tenant_id(entry["tenant_id"]):
            raise StateCorrupt("Subnet tenant ID is invalid")
        try:
            network = parse_canonical_ipv4_network(
                entry["ipv4_cidr"], context="Subnet IPv4 CIDR"
            )
        except (TypeError, ValueError) as error:
            raise StateCorrupt("Subnet IPv4 CIDR is invalid") from error
        if network.prefixlen > 30:
            raise StateCorrupt("Subnet IPv4 CIDR must be canonical and no smaller than /30")
        if isinstance(entry["vlan_id"], bool) or not isinstance(entry["vlan_id"], int):
            raise StateCorrupt("Subnet VLAN ID is invalid")
        if not MIN_VLAN_ID <= entry["vlan_id"] <= MAX_VLAN_ID:
            raise StateCorrupt("Subnet VLAN ID is outside the supported range")
        expected_interface = f"s{hashlib.sha256(entry['uid'].encode()).hexdigest()[:12]}"
        if entry["vlan_interface"] != expected_interface:
            raise StateCorrupt("Subnet VLAN interface does not match its UID")
        if not isinstance(entry["trunk_interface"], str) or not SAFE_INTERFACE_NAME.fullmatch(
            entry["trunk_interface"]
        ):
            raise StateCorrupt("Subnet trunk interface is invalid")
        if not isinstance(entry["phase"], str) or entry["phase"] not in SUBNET_PHASES:
            raise StateCorrupt("Subnet provider phase is invalid")

        gateway = network.network_address + 1
        dhcp_start = network.network_address + 2
        dhcp_end = network.broadcast_address - 1
        vip_cidr = entry["vip_cidr"]
        if not isinstance(vip_cidr, str):
            raise StateCorrupt("Subnet VIP CIDR is invalid")
        if vip_cidr:
            try:
                vip_network = parse_canonical_ipv4_network(
                    vip_cidr, context="Subnet VIP CIDR"
                )
            except (TypeError, ValueError) as error:
                raise StateCorrupt("Subnet VIP CIDR is invalid") from error
            if (
                vip_network.prefixlen <= network.prefixlen
                or not vip_network.subnet_of(network)
                or vip_network.broadcast_address != network.broadcast_address
            ):
                raise StateCorrupt("Subnet VIP CIDR must be a canonical upper-end block")
            dhcp_end = vip_network.network_address - 1
        if dhcp_start > dhcp_end:
            raise StateCorrupt("Subnet VIP reservation leaves no DHCP address")
        expected = {
            "gateway_ipv4": str(gateway),
            "dhcp_range_start": str(dhcp_start),
            "dhcp_range_end": str(dhcp_end),
        }
        if any(entry[key] != value for key, value in expected.items()):
            raise StateCorrupt("Subnet gateway or DHCP range does not match its CIDR")

    @staticmethod
    def _row_for_entry(entry: dict[str, Any]) -> tuple[Any, ...]:
        transit = entry["transit"]
        return (
            entry["uid"],
            entry["tenant_id"],
            entry["virtual_network_cidr"],
            entry["namespace_name"],
            entry["uplink"]["namespace_interface"],
            entry["uplink"]["host_interface"],
            transit["cidr"],
            int(ipaddress.ip_network(transit["cidr"]).network_address),
            json.dumps(entry, sort_keys=True, separators=(",", ":")),
        )

    @staticmethod
    def _row_for_subnet_entry(entry: dict[str, Any]) -> tuple[Any, ...]:
        return (
            entry["uid"],
            entry["virtual_network_uid"],
            entry["tenant_id"],
            entry["vlan_id"],
            entry["vlan_interface"],
            entry["ipv4_cidr"],
            entry["phase"],
            json.dumps(entry, sort_keys=True, separators=(",", ":")),
        )

    @staticmethod
    def _row_for_attachment_entry(entry: dict[str, Any]) -> tuple[Any, ...]:
        return (
            entry["uid"],
            entry["fulfillment_id"],
            entry["tenant_id"],
            entry["external_ip_uid"],
            entry["virtual_network_uid"],
            entry["subnet_uid"],
            entry["external_address"],
            entry["phase"],
            json.dumps(entry, sort_keys=True, separators=(",", ":")),
        )

    @staticmethod
    def _row_for_nat_gateway_entry(entry: dict[str, Any]) -> tuple[Any, ...]:
        return (
            entry["uid"],
            entry["fulfillment_id"],
            entry["tenant_id"],
            entry["external_ip_uid"],
            entry["virtual_network_uid"],
            entry["external_address"],
            entry["phase"],
            json.dumps(entry, sort_keys=True, separators=(",", ":")),
        )

    def _entry_for_uid(self, connection: sqlite3.Connection, uid: str):
        row = connection.execute(
            """
            SELECT uid, tenant_id, virtual_network_cidr, namespace_name,
                   namespace_interface, host_interface, transit_cidr,
                   transit_start, payload
            FROM virtual_networks WHERE uid = ?
            """,
            (uid,),
        ).fetchone()
        if row is None:
            return None
        try:
            entry = json.loads(row[8])
        except (json.JSONDecodeError, TypeError) as error:
            raise StateCorrupt("malformed VirtualNetwork state payload") from error
        self._validate_entry(entry)
        if row[:8] != self._row_for_entry(entry)[:8] or entry["uid"] != uid:
            raise StateCorrupt("VirtualNetwork indexed state does not match its payload")
        return entry

    @staticmethod
    def _subnet_entry_for_row(row: tuple[Any, ...]) -> dict[str, Any]:
        try:
            entry = json.loads(row[7])
        except (json.JSONDecodeError, TypeError) as error:
            raise StateCorrupt("malformed Subnet state payload") from error
        StateStore._validate_subnet_entry(entry)
        if row != StateStore._row_for_subnet_entry(entry) or entry["uid"] != row[0]:
            raise StateCorrupt("Subnet indexed state does not match its payload")
        return entry

    def _subnet_for_uid(self, connection: sqlite3.Connection, uid: str):
        row = connection.execute(
            """SELECT uid, virtual_network_uid, tenant_id, vlan_id,
                      vlan_interface, ipv4_cidr, phase, payload
               FROM subnets WHERE uid = ?""",
            (uid,),
        ).fetchone()
        return None if row is None else self._subnet_entry_for_row(row)

    @staticmethod
    def _attachment_for_uid(connection: sqlite3.Connection, uid: str):
        row = connection.execute(
            """SELECT uid, fulfillment_id, tenant_id, external_ip_uid,
                      virtual_network_uid, subnet_uid, external_address, phase, payload
               FROM attachments WHERE uid = ?""",
            (uid,),
        ).fetchone()
        if row is None:
            return None
        try:
            entry = json.loads(row[8])
        except (json.JSONDecodeError, TypeError) as error:
            raise StateCorrupt("malformed ExternalIPAttachment state payload") from error
        _validate_external_ip_attachment_entry(entry)
        if row != StateStore._row_for_attachment_entry(entry) or entry["uid"] != uid:
            raise StateCorrupt("ExternalIPAttachment indexed state does not match its payload")
        return entry

    @staticmethod
    def _nat_gateway_for_uid(connection: sqlite3.Connection, uid: str):
        row = connection.execute(
            """SELECT uid, fulfillment_id, tenant_id, external_ip_uid,
                      virtual_network_uid, external_address, phase, payload
               FROM nat_gateways WHERE uid = ?""",
            (uid,),
        ).fetchone()
        if row is None:
            return None
        try:
            entry = json.loads(row[7])
        except (json.JSONDecodeError, TypeError) as error:
            raise StateCorrupt("malformed NATGateway state payload") from error
        _validate_nat_gateway_entry(entry)
        if row != StateStore._row_for_nat_gateway_entry(entry) or entry["uid"] != uid:
            raise StateCorrupt("NATGateway indexed state does not match its payload")
        return entry

    def _validate_attachment_dependencies(
        self,
        connection: sqlite3.Connection,
        entry: dict[str, Any],
        *,
        require_ready: bool,
    ) -> tuple[dict[str, Any], dict[str, Any], dict[str, str]]:
        context = entry["context"]
        virtual_network = self._entry_for_uid(connection, entry["virtual_network_uid"])
        subnet = self._subnet_for_uid(connection, entry["subnet_uid"])
        external_ip = self._external_ip_for_uid(connection, entry["external_ip_uid"])
        if virtual_network is None or subnet is None or external_ip is None:
            raise StateError("ExternalIPAttachment provider dependency state is missing")
        if any(value["tenant_id"] != entry["tenant_id"] for value in (virtual_network, subnet, external_ip)):
            raise StateError("ExternalIPAttachment tenant does not match saved dependencies")
        if subnet["virtual_network_uid"] != virtual_network["uid"]:
            raise StateError("ExternalIPAttachment Subnet belongs to another VirtualNetwork")
        if require_ready and subnet["phase"] != "ready":
            raise StateError("ExternalIPAttachment Subnet provider state is not Ready")

        eip_context = context["external_ip"]
        subnet_context = context["subnet"]
        vn_context = context["virtual_network"]
        if any(
            (
                eip_context["kubernetes_uid"] != external_ip["uid"],
                eip_context["fulfillment_id"] != external_ip["fulfillment_id"],
                eip_context["tenant_id"] != external_ip["tenant_id"],
                eip_context["address"] != external_ip["address"],
                subnet_context["kubernetes_uid"] != subnet["uid"],
                subnet_context["tenant_id"] != subnet["tenant_id"],
                subnet_context["ipv4_cidr"] != subnet["ipv4_cidr"],
                subnet_context["virtual_network_id"] != vn_context["fulfillment_id"],
                vn_context["kubernetes_uid"] != virtual_network["uid"],
                vn_context["tenant_id"] != virtual_network["tenant_id"],
            )
        ):
            raise StateError("ExternalIPAttachment context does not match saved provider dependencies")
        provider = entry["provider"]
        expected_provider = {
            "namespace_name": virtual_network["namespace_name"],
            "namespace_interface": virtual_network["uplink"]["namespace_interface"],
            "host_interface": virtual_network["uplink"]["host_interface"],
            "next_hop": virtual_network["transit"]["namespace_ip"],
        }
        if any(provider[key] != value for key, value in expected_provider.items()):
            raise StateError("ExternalIPAttachment provider path does not match saved VirtualNetwork state")
        return virtual_network, subnet, external_ip

    def _subnets_for_virtual_network(
        self, connection: sqlite3.Connection, virtual_network_uid: str
    ) -> list[dict[str, Any]]:
        rows = connection.execute(
            """SELECT uid, virtual_network_uid, tenant_id, vlan_id,
                      vlan_interface, ipv4_cidr, phase, payload
               FROM subnets WHERE virtual_network_uid = ? ORDER BY uid""",
            (virtual_network_uid,),
        ).fetchall()
        return [self._subnet_entry_for_row(row) for row in rows]

    def _parent_uid_for_subnet(
        self, uid: str, tenant_id: str, *, check_mode: bool = False
    ) -> str | None:
        if not _is_canonical_uuid(uid):
            raise StateError("Subnet UID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("Subnet tenant ID is required")
        with self._locked_database(
            create=False,
            read_only=True,
        ) as connection:
            if connection is None:
                return None
            row = connection.execute(
                "SELECT virtual_network_uid, tenant_id FROM subnets WHERE uid = ?",
                (uid,),
            ).fetchone()
            if row is None:
                return None
            if row[1] != tenant_id:
                raise StateError("Subnet tenant does not match saved state")
            return row[0]

    def get_subnet(
        self, uid: str, tenant_id: str, *, check_mode: bool = False
    ) -> dict[str, Any] | None:
        if not _is_canonical_uuid(uid):
            raise StateError("Subnet UID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("Subnet tenant ID is required")
        with self._locked_database(
            create=False,
            read_only=True,
        ) as connection:
            if connection is None:
                return None
            entry = self._subnet_for_uid(connection, uid)
            if entry is not None and entry["tenant_id"] != tenant_id:
                raise StateError("Subnet tenant does not match saved state")
            return entry

    def query_dhcp_lease(
        self,
        uid: str,
        tenant_id: str,
        mac_address: str,
        *,
        now: int | None = None,
    ) -> dict[str, str] | None:
        """Read the active DHCP lease for one ready Subnet and NIC MAC."""
        if not _is_canonical_uuid(uid):
            raise StateError("Subnet UID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("Subnet tenant ID is required")
        if not isinstance(mac_address, str) or not DHCP_MAC_RE.fullmatch(mac_address):
            raise StateError("DHCP lease query requires a valid NIC MAC address")

        with self._locked_database(create=False, read_only=True) as connection:
            if connection is None:
                return None
            initial_subnet = self._subnet_for_uid(connection, uid)
            if initial_subnet is None:
                return None
            if initial_subnet["tenant_id"] != tenant_id:
                raise StateError("Subnet tenant does not match saved state")
            parent_uid = initial_subnet["virtual_network_uid"]

        # Subnet reconciliation and deletion hold this same parent lock while
        # changing DHCP state and the lease file. Re-read the allocation after
        # acquiring it so a concurrent delete cannot turn a stale snapshot into
        # a result for a removed Subnet.
        with self._resource_locked(parent_uid):
            with self._locked_database(create=False, read_only=True) as connection:
                if connection is None:
                    return None
                subnet = self._subnet_for_uid(connection, uid)
                if subnet is None:
                    return None
                if subnet["tenant_id"] != tenant_id:
                    raise StateError("Subnet tenant does not match saved state")
                if subnet["phase"] != "ready":
                    raise StateError("DHCP lease lookup requires a ready Subnet")
                parent = self._entry_for_uid(connection, parent_uid)
                if parent is None or parent["tenant_id"] != tenant_id:
                    raise StateError("Subnet parent tenant does not match saved state")
            contents = read_lease_file_read_only(dhcp_lease_path(parent_uid))
            return find_active_dhcp_lease(contents, subnet, mac_address, now=now)

    def reserve_subnet(
        self,
        uid: str,
        virtual_network_uid: str,
        tenant_id: str,
        subnet_cidr: str,
        trunk_interface: str,
        observed_vlan_ids: set[int] | None = None,
        vip_cidr: str = "",
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any], bool]:
        if observed_vlan_ids is None:
            observed_vlan_ids = set()
        if not isinstance(observed_vlan_ids, (set, list, tuple)):
            raise StateError("Observed Cumulus VLAN IDs must be a collection of integers")
        for vlan_id in observed_vlan_ids:
            if (
                isinstance(vlan_id, bool)
                or not isinstance(vlan_id, int)
                or not MIN_VLAN_ID <= vlan_id <= MAX_VLAN_ID
            ):
                raise StateError("Observed Cumulus VLAN ID is outside the supported range")
        observed_vlan_ids = set(observed_vlan_ids)
        if not isinstance(vip_cidr, str):
            raise StateError("Subnet VIP CIDR must be text")
        # Validate all caller-controlled fields before opening state.
        requested = _subnet_payload(
            uid,
            virtual_network_uid,
            tenant_id,
            subnet_cidr,
            trunk_interface,
            MIN_VLAN_ID,
            vip_cidr,
        )
        operation_lock = (
            contextlib.nullcontext()
            if check_mode
            else self._resource_locked(virtual_network_uid)
        )
        with operation_lock:
            with self._locked_database(
                create=not check_mode,
                read_only=check_mode,
            ) as connection:
                if connection is None:
                    raise StateError("AgentlessNet parent VirtualNetwork state is missing")
                parent = self._entry_for_uid(connection, virtual_network_uid)
                if parent is None:
                    raise StateError("AgentlessNet parent VirtualNetwork state is missing")
                if parent["tenant_id"] != tenant_id:
                    raise StateError("Subnet tenant does not match saved VirtualNetwork state")
                try:
                    parent_network = _parse_canonical_ipv4_network(
                        parent["virtual_network_cidr"]
                    )
                    subnet_network = parse_canonical_ipv4_network(
                        subnet_cidr, context="Subnet CIDR"
                    )
                    transit_network = ipaddress.ip_network(parent["transit"]["cidr"])
                except (TypeError, ValueError) as error:
                    raise StateCorrupt("saved VirtualNetwork CIDR state is invalid") from error
                if not subnet_network.subnet_of(parent_network):
                    raise StateError("Subnet CIDR must be contained by its parent VirtualNetwork")
                if subnet_network.overlaps(transit_network):
                    raise StateError("Subnet CIDR overlaps its parent VirtualNetwork transit link")

                current = self._subnet_for_uid(connection, uid)
                existing_subnets = self._subnets_for_virtual_network(
                    connection, virtual_network_uid
                )
                if current is not None:
                    if current["phase"] == "deleting":
                        raise StateError("Subnet is already deleting and cannot be recreated")
                    immutable = (
                        "virtual_network_uid",
                        "tenant_id",
                        "ipv4_cidr",
                        "trunk_interface",
                        "vip_cidr",
                    )
                    if any(current[key] != requested[key] for key in immutable):
                        raise StateError("Subnet request does not match its saved allocation")
                    return current, False

                for sibling in existing_subnets:
                    if ipaddress.ip_network(sibling["ipv4_cidr"]).overlaps(subnet_network):
                        raise StateError("Subnet CIDR overlaps a saved sibling Subnet")
                used_vlans = {
                    row[0]
                    for row in connection.execute("SELECT vlan_id FROM subnets")
                }
                vlan_id = next(
                    (
                        candidate
                        for candidate in range(MIN_VLAN_ID, MAX_VLAN_ID + 1)
                        if candidate not in used_vlans
                        and candidate not in observed_vlan_ids
                    ),
                    None,
                )
                if vlan_id is None:
                    raise StateError("AgentlessNet Subnet VLAN range is exhausted")
                entry = _subnet_payload(
                    uid,
                    virtual_network_uid,
                    tenant_id,
                    subnet_cidr,
                    trunk_interface,
                    vlan_id,
                    vip_cidr,
                )
                self._validate_subnet_entry(entry)
                if check_mode:
                    return entry, True

                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """INSERT INTO subnets (
                            uid, virtual_network_uid, tenant_id, vlan_id,
                            vlan_interface, ipv4_cidr, phase, payload
                        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                        self._row_for_subnet_entry(entry),
                    )
                    connection.commit()
                except sqlite3.IntegrityError as error:
                    connection.rollback()
                    raise StateError(
                        "AgentlessNet Subnet VLAN allocation conflicts with saved state"
                    ) from error
                except BaseException:
                    connection.rollback()
                    raise
                return entry, True

    def ensure_and_reconcile_subnet(
        self,
        uid: str,
        tenant_id: str,
        dhcp_supervisor: str | None = None,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any], bool, bool]:
        if dhcp_supervisor is not None:
            _validate_dhcp_supervisor(dhcp_supervisor)
        if check_mode:
            entry = self.get_subnet(uid, tenant_id, check_mode=True)
            if entry is None:
                raise StateError("AgentlessNet Subnet reservation is missing")
            if entry["phase"] == "deleting":
                raise StateError("AgentlessNet Subnet is deleting and cannot be ensured")
            return entry, entry["phase"] != "ready", False

        virtual_network_uid = self._parent_uid_for_subnet(uid, tenant_id)
        if virtual_network_uid is None:
            raise StateError("AgentlessNet Subnet reservation is missing")
        with self._resource_locked(virtual_network_uid):
            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet Subnet state database disappeared")
                entry = self._subnet_for_uid(connection, uid)
                if entry is None:
                    raise StateError("AgentlessNet Subnet reservation is missing")
                if entry["tenant_id"] != tenant_id:
                    raise StateError("Subnet tenant does not match saved state")
                if entry["phase"] == "deleting":
                    raise StateError("AgentlessNet Subnet is deleting and cannot be ensured")
                parent = self._entry_for_uid(connection, virtual_network_uid)
                if parent is None or parent["tenant_id"] != tenant_id:
                    raise StateError("AgentlessNet Subnet parent state does not match its reservation")
                siblings = self._subnets_for_virtual_network(
                    connection, virtual_network_uid
                )
                active_subnets = [
                    child for child in siblings if child["phase"] != "deleting"
                ]

            if dhcp_supervisor is None:
                try:
                    dhcp_supervisor = preflight_dhcp_supervisor(virtual_network_uid)
                except SubnetProviderError as error:
                    raise StateError(str(error)) from error

            try:
                parent_changed = reconcile_virtual_network(
                    parent, firewall_lock_path=self.firewall_lock_path
                )
                subnet_changed = ensure_subnet_data_plane(
                    parent, active_subnets, dhcp_supervisor
                )
            except (SubnetProviderError, NetworkCommandError) as error:
                raise StateError(str(error)) from error

            state_changed = False
            if entry["phase"] != "ready":
                ready_entry = {**entry, "phase": "ready"}
                with self._locked_database(create=False) as connection:
                    if connection is None:
                        raise StateCorrupt("AgentlessNet Subnet state database disappeared")
                    connection.execute("BEGIN IMMEDIATE")
                    try:
                        current = self._subnet_for_uid(connection, uid)
                        if current != entry:
                            raise StateError(
                                "Subnet state changed while provider reconciliation was running"
                            )
                        connection.execute(
                            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
                            (
                                ready_entry["phase"],
                                json.dumps(ready_entry, sort_keys=True, separators=(",", ":")),
                                uid,
                            ),
                        )
                        connection.commit()
                    except BaseException:
                        connection.rollback()
                        raise
                entry = ready_entry
                state_changed = True
            return entry, state_changed, parent_changed or subnet_changed

    def prepare_delete_subnet(
        self,
        uid: str,
        tenant_id: str,
        dhcp_supervisor: str | None = None,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any] | None, bool]:
        if dhcp_supervisor is not None:
            _validate_dhcp_supervisor(dhcp_supervisor)
        virtual_network_uid = self._parent_uid_for_subnet(
            uid, tenant_id, check_mode=check_mode
        )
        if virtual_network_uid is None:
            return None, False
        if check_mode:
            entry = self.get_subnet(uid, tenant_id, check_mode=True)
            if entry is None:
                return None, False
            with self._locked_database(create=False, read_only=True) as connection:
                if connection is not None:
                    if connection.execute(
                        "SELECT 1 FROM attachments WHERE subnet_uid = ? LIMIT 1", (uid,)
                    ).fetchone() is not None:
                        raise StateError("Subnet still has an ExternalIPAttachment")
            if entry["phase"] != "deleting":
                entry = {**entry, "phase": "deleting"}
            return entry, True

        with self._resource_locked(virtual_network_uid):
            with self._locked_database(create=False) as connection:
                if connection is None:
                    return None, False
                entry = self._subnet_for_uid(connection, uid)
                if entry is None:
                    return None, False
                if entry["tenant_id"] != tenant_id:
                    raise StateError("Subnet tenant does not match saved state")
                if connection.execute(
                    "SELECT 1 FROM attachments WHERE subnet_uid = ? LIMIT 1", (uid,)
                ).fetchone() is not None:
                    raise StateError("Subnet still has an ExternalIPAttachment")
                parent = self._entry_for_uid(connection, virtual_network_uid)
                if parent is None or parent["tenant_id"] != tenant_id:
                    raise StateError("AgentlessNet Subnet parent state does not match its reservation")
                if dhcp_supervisor is None:
                    try:
                        dhcp_supervisor = preflight_dhcp_supervisor(virtual_network_uid)
                    except SubnetProviderError as error:
                        raise StateError(str(error)) from error
                state_changed = entry["phase"] != "deleting"
                deleting_entry = {**entry, "phase": "deleting"}
                if state_changed:
                    connection.execute("BEGIN IMMEDIATE")
                    try:
                        connection.execute(
                            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
                            (
                                deleting_entry["phase"],
                                json.dumps(
                                    deleting_entry,
                                    sort_keys=True,
                                    separators=(",", ":"),
                                ),
                                uid,
                            ),
                        )
                        connection.commit()
                    except BaseException:
                        connection.rollback()
                        raise
                siblings = self._subnets_for_virtual_network(
                    connection, virtual_network_uid
                )
                remaining = [
                    child
                    for child in siblings
                    if child["uid"] != uid and child["phase"] != "deleting"
                ]

            try:
                provider_changed = prepare_subnet_delete_data_plane(
                    parent, deleting_entry, remaining, dhcp_supervisor
                )
            except (SubnetProviderError, NetworkCommandError) as error:
                raise StateError(str(error)) from error
            return deleting_entry, state_changed or provider_changed

    def release_subnet(
        self, uid: str, tenant_id: str, *, check_mode: bool = False
    ) -> tuple[dict[str, Any] | None, bool]:
        virtual_network_uid = self._parent_uid_for_subnet(
            uid, tenant_id, check_mode=check_mode
        )
        if virtual_network_uid is None:
            return None, False
        if check_mode:
            entry = self.get_subnet(uid, tenant_id, check_mode=True)
            if entry is None:
                return None, False
            with self._locked_database(create=False, read_only=True) as connection:
                if connection is not None:
                    if connection.execute(
                        "SELECT 1 FROM attachments WHERE subnet_uid = ? LIMIT 1", (uid,)
                    ).fetchone() is not None:
                        raise StateError("Subnet still has an ExternalIPAttachment")
            if entry["phase"] != "deleting":
                raise StateError("AgentlessNet Subnet must be deleting before VLAN release")
            return entry, True

        with self._resource_locked(virtual_network_uid):
            with self._locked_database(create=False) as connection:
                if connection is None:
                    return None, False
                entry = self._subnet_for_uid(connection, uid)
                if entry is None:
                    return None, False
                if entry["tenant_id"] != tenant_id:
                    raise StateError("Subnet tenant does not match saved state")
                if connection.execute(
                    "SELECT 1 FROM attachments WHERE subnet_uid = ? LIMIT 1", (uid,)
                ).fetchone() is not None:
                    raise StateError("Subnet still has an ExternalIPAttachment")
                if entry["phase"] != "deleting":
                    raise StateError("AgentlessNet Subnet must be deleting before VLAN release")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute("DELETE FROM subnets WHERE uid = ?", (uid,))
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
                return entry, True

    @staticmethod
    def _external_ip_pool_for_uid(
        connection: sqlite3.Connection, uid: str
    ) -> dict[str, str] | None:
        row = connection.execute(
            """SELECT uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr
               FROM external_ip_pools WHERE uid = ?""",
            (uid,),
        ).fetchone()
        if row is None:
            return None
        try:
            return _external_ip_pool_entry(row[0], row[1], row[2], row[3], [row[4]])
        except StateError as error:
            raise StateCorrupt("malformed ExternalIPPool state") from error

    @staticmethod
    def _external_ip_for_uid(
        connection: sqlite3.Connection, uid: str
    ) -> dict[str, str] | None:
        row = connection.execute(
            """SELECT uid, fulfillment_id, tenant_id, pool_uid, address
               FROM external_ips WHERE uid = ?""",
            (uid,),
        ).fetchone()
        if row is None:
            return None
        try:
            entry = _external_ip_entry(row[0], row[1], row[2], row[3], row[4])
        except StateError as error:
            raise StateCorrupt("malformed ExternalIP reservation state") from error
        pool = StateStore._external_ip_pool_for_uid(connection, entry["pool_uid"])
        if pool is None:
            raise StateCorrupt("ExternalIP reservation references a missing pool")
        if entry["tenant_id"] != pool["tenant_id"] and pool["tenant_id"] != "shared":
            raise StateCorrupt("ExternalIP reservation tenant does not match its pool")
        if ipaddress.ip_address(entry["address"]) not in ipaddress.ip_network(
            pool["ipv4_cidr"], strict=True
        ):
            raise StateCorrupt("ExternalIP reservation address is outside its pool")
        return entry

    @staticmethod
    def _require_unique_fulfillment_id(
        connection: sqlite3.Connection,
        table_name: str,
        uid: str,
        fulfillment_id: str,
        validate_conflicting_row: Callable[[sqlite3.Connection, str], Any],
        error_message: str,
    ) -> None:
        row = connection.execute(
            f"""SELECT uid FROM {table_name}
               WHERE fulfillment_id = ? AND uid <> ? LIMIT 1""",
            (fulfillment_id, uid),
        ).fetchone()
        if row is None:
            return
        validate_conflicting_row(connection, row[0])
        raise StateError(error_message)

    @staticmethod
    def _require_unique_external_ip_fulfillment_id(
        connection: sqlite3.Connection,
        uid: str,
        fulfillment_id: str,
    ) -> None:
        StateStore._require_unique_fulfillment_id(
            connection,
            "external_ips",
            uid,
            fulfillment_id,
            StateStore._external_ip_for_uid,
            "ExternalIP Fulfillment identity is already reserved",
        )

    @staticmethod
    def _require_unique_external_ip_pool_fulfillment_id(
        connection: sqlite3.Connection,
        uid: str,
        fulfillment_id: str,
    ) -> None:
        StateStore._require_unique_fulfillment_id(
            connection,
            "external_ip_pools",
            uid,
            fulfillment_id,
            StateStore._external_ip_pool_for_uid,
            "ExternalIPPool Fulfillment identity is already registered",
        )

    @staticmethod
    def _require_unique_attachment_fulfillment_id(
        connection: sqlite3.Connection,
        uid: str,
        fulfillment_id: str,
    ) -> None:
        StateStore._require_unique_fulfillment_id(
            connection,
            "attachments",
            uid,
            fulfillment_id,
            StateStore._attachment_for_uid,
            "ExternalIPAttachment Fulfillment identity is already reserved",
        )

    def _provider_state(
        self,
        virtual_network: dict[str, Any],
        external_interface: str,
    ) -> dict[str, str]:
        if not isinstance(external_interface, str) or not SAFE_INTERFACE_NAME.fullmatch(external_interface):
            raise StateError("AgentlessNet external interface is invalid")
        return {
            "namespace_name": virtual_network["namespace_name"],
            "namespace_interface": virtual_network["uplink"]["namespace_interface"],
            "host_interface": virtual_network["uplink"]["host_interface"],
            "external_interface": external_interface,
            "next_hop": virtual_network["transit"]["namespace_ip"],
        }

    def _validate_nat_gateway_dependencies(
        self,
        connection: sqlite3.Connection,
        entry: dict[str, Any],
    ) -> tuple[dict[str, Any], dict[str, str]]:
        virtual_network = self._entry_for_uid(connection, entry["virtual_network_uid"])
        external_ip = self._external_ip_for_uid(connection, entry["external_ip_uid"])
        if virtual_network is None or external_ip is None:
            raise StateError("NATGateway provider dependency state is missing")
        if virtual_network["tenant_id"] != entry["tenant_id"] or external_ip["tenant_id"] != entry["tenant_id"]:
            raise StateError("NATGateway tenant does not match saved dependencies")
        context = entry["context"]
        if any((
            context["virtual_network"]["kubernetes_uid"] != virtual_network["uid"],
            context["virtual_network"]["tenant_id"] != virtual_network["tenant_id"],
            context["virtual_network"]["ipv4_cidr"] != virtual_network["virtual_network_cidr"],
            context["external_ip"]["kubernetes_uid"] != external_ip["uid"],
            context["external_ip"]["fulfillment_id"] != external_ip["fulfillment_id"],
            context["external_ip"]["tenant_id"] != external_ip["tenant_id"],
            context["external_ip"]["address"] != external_ip["address"],
        )):
            raise StateError("NATGateway context does not match saved provider dependencies")
        expected_provider = {
            "namespace_name": virtual_network["namespace_name"],
            "namespace_interface": virtual_network["uplink"]["namespace_interface"],
            "host_interface": virtual_network["uplink"]["host_interface"],
            "next_hop": virtual_network["transit"]["namespace_ip"],
        }
        if any(entry["provider"][key] != value for key, value in expected_provider.items()):
            raise StateError("NATGateway provider path does not match saved VirtualNetwork state")
        return virtual_network, external_ip

    @staticmethod
    def _require_nat_gateway_external_ip_exclusive(
        connection: sqlite3.Connection,
        external_ip_uid: str,
        external_address: str,
        nat_gateway_uid: str,
    ) -> None:
        attachment = connection.execute(
            """SELECT uid FROM attachments
               WHERE uid <> ? AND (external_ip_uid = ? OR external_address = ?)
               LIMIT 1""",
            (nat_gateway_uid, external_ip_uid, external_address),
        ).fetchone()
        if attachment is not None:
            raise StateError("ExternalIP is already reserved by an ExternalIPAttachment")
        other_gateway = connection.execute(
            """SELECT uid FROM nat_gateways
               WHERE uid <> ? AND (external_ip_uid = ? OR external_address = ?)
               LIMIT 1""",
            (nat_gateway_uid, external_ip_uid, external_address),
        ).fetchone()
        if other_gateway is not None:
            raise StateError("ExternalIP is already reserved by another NATGateway")

    @staticmethod
    def _require_unique_nat_gateway_fulfillment_id(
        connection: sqlite3.Connection,
        uid: str,
        fulfillment_id: str,
    ) -> None:
        StateStore._require_unique_fulfillment_id(
            connection,
            "nat_gateways",
            uid,
            fulfillment_id,
            StateStore._nat_gateway_for_uid,
            "NATGateway Fulfillment identity is already reserved",
        )

    @staticmethod
    def _nat_gateway_identity_matches(
        existing: dict[str, Any], requested: dict[str, Any]
    ) -> bool:
        identity_keys = (
            "uid", "fulfillment_id", "tenant_id", "external_ip_uid",
            "external_address", "virtual_network_uid", "provider",
        )
        return all(existing[key] == requested[key] for key in identity_keys) and all(
            existing["context"][section] == requested["context"][section]
            for section in ("external_ip", "virtual_network")
        )

    @staticmethod
    def _attachment_identity_matches(
        existing: dict[str, Any], requested: dict[str, Any]
    ) -> bool:
        identity_keys = (
            "uid",
            "fulfillment_id",
            "tenant_id",
            "external_ip_uid",
            "virtual_network_uid",
            "subnet_uid",
            "external_address",
            "provider",
        )
        if any(existing[key] != requested[key] for key in identity_keys):
            return False
        old_context = existing["context"]
        new_context = requested["context"]
        old_target = dict(old_context["target"])
        new_target = dict(new_context["target"])
        old_target.pop("ip_address")
        new_target.pop("ip_address")
        return (
            old_context["external_ip"] == new_context["external_ip"]
            and old_target == new_target
            and old_context["subnet"] == new_context["subnet"]
            and old_context["virtual_network"] == new_context["virtual_network"]
        )

    def ensure_external_ip_attachment(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        context: dict[str, Any],
        external_interface: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any], bool]:
        if not _is_canonical_uuid(uid):
            raise StateError("ExternalIPAttachment UID must be a canonical UUID")
        if not _is_canonical_uuid(fulfillment_id):
            raise StateError("ExternalIPAttachment Fulfillment UUID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("ExternalIPAttachment tenant ID is required")
        normalized_context = _external_ip_attachment_context(context, tenant_id)
        virtual_network_uid = normalized_context["virtual_network"]["kubernetes_uid"]

        with self._resource_locked(virtual_network_uid):
            with self._locked_database(
                create=False,
                read_only=check_mode,
            ) as connection:
                if connection is None:
                    raise StateError("AgentlessNet VirtualNetwork state is missing")
                virtual_network = self._entry_for_uid(connection, virtual_network_uid)
                subnet = self._subnet_for_uid(
                    connection, normalized_context["subnet"]["kubernetes_uid"]
                )
                external_ip = self._external_ip_for_uid(
                    connection, normalized_context["external_ip"]["kubernetes_uid"]
                )
                if virtual_network is None or subnet is None or external_ip is None:
                    raise StateError("AgentlessNet attachment dependency state is missing")
                provider = self._provider_state(
                    virtual_network, external_interface
                )
                requested = _external_ip_attachment_entry(
                    uid,
                    fulfillment_id,
                    tenant_id,
                    normalized_context,
                    provider,
                    "provisioning",
                )
                self._validate_attachment_dependencies(
                    connection, requested, require_ready=True
                )
                self._require_nat_gateway_external_ip_exclusive(
                    connection,
                    requested["external_ip_uid"],
                    requested["external_address"],
                    uid,
                )
                existing = self._attachment_for_uid(connection, uid)
                self._require_unique_attachment_fulfillment_id(
                    connection, uid, fulfillment_id
                )
                if existing is not None:
                    if not self._attachment_identity_matches(existing, requested):
                        raise StateError(
                            "ExternalIPAttachment identity does not match saved state"
                        )
                    if existing["phase"] == "deleting":
                        raise StateError("ExternalIPAttachment is already deleting")
                if check_mode:
                    return requested, existing is None or existing["context"] != normalized_context

            if existing is not None and existing["context"] != normalized_context:
                try:
                    with _with_firewall_lock(self.firewall_lock_path):
                        agentless_net_external_ip.delete_external_ip_attachment(existing)
                except NetworkCommandError as error:
                    raise StateError(str(error)) from error

            if existing is None or existing["context"] != normalized_context:
                with self._locked_database(create=False) as connection:
                    if connection is None:
                        raise StateCorrupt("AgentlessNet state disappeared before attachment reservation")
                    connection.execute("BEGIN IMMEDIATE")
                    try:
                        current = self._attachment_for_uid(connection, uid)
                        if current != existing:
                            raise StateError("ExternalIPAttachment state changed during reservation")
                        self._require_nat_gateway_external_ip_exclusive(
                            connection,
                            requested["external_ip_uid"],
                            requested["external_address"],
                            uid,
                        )
                        if current is None:
                            connection.execute(
                                """INSERT INTO attachments
                                   (uid, fulfillment_id, tenant_id, external_ip_uid,
                                    virtual_network_uid, subnet_uid, external_address,
                                    phase, payload)
                                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                                self._row_for_attachment_entry(requested),
                            )
                        else:
                            connection.execute(
                                """UPDATE attachments SET external_address = ?, phase = ?, payload = ?
                                   WHERE uid = ?""",
                                (
                                    requested["external_address"],
                                    requested["phase"],
                                    json.dumps(requested, sort_keys=True, separators=(",", ":")),
                                    uid,
                                ),
                            )
                        connection.commit()
                    except sqlite3.IntegrityError as error:
                        connection.rollback()
                        raise StateError("ExternalIP is already reserved by an attachment") from error
                    except BaseException:
                        connection.rollback()
                        raise

            try:
                with _with_firewall_lock(self.firewall_lock_path):
                    network_changed = reconcile_virtual_network(virtual_network)
                    provider_changed = agentless_net_external_ip.reconcile_external_ip_attachment(
                        virtual_network, subnet, requested
                    )
            except NetworkCommandError as error:
                raise StateError(str(error)) from error

            ready = {**requested, "phase": "ready"}
            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet state disappeared after attachment setup")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._attachment_for_uid(connection, uid)
                    if current is None or current["context"] != normalized_context:
                        raise StateError("ExternalIPAttachment state changed during provider reconciliation")
                    connection.execute(
                        "UPDATE attachments SET phase = ?, payload = ? WHERE uid = ?",
                        (
                            ready["phase"],
                            json.dumps(ready, sort_keys=True, separators=(",", ":")),
                            uid,
                        ),
                    )
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
            return ready, existing is None or existing["context"] != normalized_context or network_changed or provider_changed

    def delete_external_ip_attachment(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any] | None, bool]:
        if not _is_canonical_uuid(uid):
            raise StateError("ExternalIPAttachment UID must be a canonical UUID")
        if not _is_canonical_uuid(fulfillment_id):
            raise StateError("ExternalIPAttachment Fulfillment UUID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("ExternalIPAttachment tenant ID is required")
        with self._locked_database(create=False, read_only=True) as connection:
            if connection is None:
                return None, False
            existing = self._attachment_for_uid(connection, uid)
            if existing is None:
                return None, False
            if any(
                existing[key] != value
                for key, value in (
                    ("fulfillment_id", fulfillment_id),
                    ("tenant_id", tenant_id),
                )
            ):
                raise StateError("ExternalIPAttachment identity does not match saved state")
        if check_mode:
            return existing, True

        virtual_network_uid = existing["virtual_network_uid"]
        with self._resource_locked(virtual_network_uid):
            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet attachment state disappeared")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._attachment_for_uid(connection, uid)
                    if current is None:
                        connection.rollback()
                        return None, False
                    if any(
                        current[key] != value
                        for key, value in (
                            ("fulfillment_id", fulfillment_id),
                            ("tenant_id", tenant_id),
                        )
                    ):
                        raise StateError("ExternalIPAttachment identity does not match saved state")
                    deleting = {**current, "phase": "deleting"}
                    connection.execute(
                        "UPDATE attachments SET phase = ?, payload = ? WHERE uid = ?",
                        (
                            deleting["phase"],
                            json.dumps(deleting, sort_keys=True, separators=(",", ":")),
                            uid,
                        ),
                    )
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise

            try:
                with _with_firewall_lock(self.firewall_lock_path):
                    agentless_net_external_ip.delete_external_ip_attachment(deleting)
            except NetworkCommandError as error:
                raise StateError(str(error)) from error

            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet state disappeared after attachment cleanup")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._attachment_for_uid(connection, uid)
                    if current != deleting:
                        raise StateError("ExternalIPAttachment state changed during cleanup")
                    connection.execute("DELETE FROM attachments WHERE uid = ?", (uid,))
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
            return deleting, True

    def _write_nat_gateway_entry(
        self,
        connection: sqlite3.Connection,
        entry: dict[str, Any],
        *,
        existing: dict[str, Any] | None,
    ) -> None:
        row = self._row_for_nat_gateway_entry(entry)
        if existing is None:
            connection.execute(
                """INSERT INTO nat_gateways
                   (uid, fulfillment_id, tenant_id, external_ip_uid,
                    virtual_network_uid, external_address, phase, payload)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                row,
            )
        else:
            connection.execute(
                """UPDATE nat_gateways SET fulfillment_id = ?, tenant_id = ?,
                   external_ip_uid = ?, virtual_network_uid = ?, external_address = ?,
                   phase = ?, payload = ? WHERE uid = ?""",
                (*row[1:], row[0]),
            )

    def ensure_nat_gateway(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        context: Any,
        external_interface: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any], bool, bool]:
        if not _is_canonical_uuid(uid):
            raise StateError("NATGateway UID must be a canonical UUID")
        if not _is_canonical_uuid(fulfillment_id):
            raise StateError("NATGateway Fulfillment UUID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("NATGateway tenant ID is required")
        normalized_context = _nat_gateway_context(context, tenant_id)
        virtual_network_uid = normalized_context["virtual_network"]["kubernetes_uid"]
        external_ip_uid = normalized_context["external_ip"]["kubernetes_uid"]
        external_address = normalized_context["external_ip"]["address"]
        lock = contextlib.nullcontext() if check_mode else self._resource_locked(virtual_network_uid)

        with lock:
            with self._locked_database(create=False, read_only=check_mode) as connection:
                if connection is None:
                    raise StateError("NATGateway provider dependencies are not registered")
                if not check_mode:
                    connection.execute("BEGIN IMMEDIATE")
                try:
                    virtual_network = self._entry_for_uid(connection, virtual_network_uid)
                    external_ip = self._external_ip_for_uid(connection, external_ip_uid)
                    if virtual_network is None or external_ip is None:
                        raise StateError("NATGateway provider dependency state is missing")
                    if virtual_network["tenant_id"] != tenant_id or external_ip["tenant_id"] != tenant_id:
                        raise StateError("NATGateway tenant does not match saved dependencies")
                    if any((
                        virtual_network["virtual_network_cidr"] != normalized_context["virtual_network"]["ipv4_cidr"],
                        external_ip["fulfillment_id"] != normalized_context["external_ip"]["fulfillment_id"],
                        external_ip["address"] != external_address,
                    )):
                        raise StateError("NATGateway context does not match saved provider dependencies")

                    provider = self._provider_state(virtual_network, external_interface)
                    requested = {
                        "uid": uid,
                        "fulfillment_id": fulfillment_id,
                        "tenant_id": tenant_id,
                        "external_ip_uid": external_ip_uid,
                        "external_address": external_address,
                        "virtual_network_uid": virtual_network_uid,
                        "phase": "provisioning",
                        "context": normalized_context,
                        "provider": provider,
                    }
                    existing = self._nat_gateway_for_uid(connection, uid)
                    if existing is not None and not self._nat_gateway_identity_matches(existing, requested):
                        raise StateError("NATGateway identity does not match saved provider state")
                    if existing is not None and existing["phase"] == "deleting":
                        raise StateError("NATGateway is already deleting")
                    self._require_unique_nat_gateway_fulfillment_id(
                        connection, uid, fulfillment_id
                    )
                    self._require_nat_gateway_external_ip_exclusive(
                        connection, external_ip_uid, external_address, uid
                    )
                    if check_mode:
                        return requested, existing is None or existing["phase"] != "ready", False

                    entry = {**(existing or requested), "phase": "provisioning"}
                    entry.update({"context": normalized_context, "provider": provider})
                    _validate_nat_gateway_entry(entry)
                    if existing == entry:
                        # A retry still runs provider reconciliation to repair partial changes.
                        pass
                    self._write_nat_gateway_entry(connection, entry, existing=existing)
                    connection.commit()
                except sqlite3.IntegrityError as error:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise StateError("NATGateway identity or ExternalIP is already reserved") from error
                except BaseException:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise

            try:
                with _with_firewall_lock(self.firewall_lock_path):
                    provider_changed = agentless_net_nat_gateway.reconcile_nat_gateway(
                        virtual_network, entry
                    )
            except (agentless_net_nat_gateway.NATGatewayProviderError, NetworkCommandError) as error:
                raise StateError(str(error)) from error

            ready = {
                **entry,
                "phase": "ready",
            }
            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet state disappeared after NATGateway setup")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._nat_gateway_for_uid(connection, uid)
                    if current != entry:
                        raise StateError("NATGateway state changed during provider reconciliation")
                    self._write_nat_gateway_entry(connection, ready, existing=current)
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
            return ready, existing is None or existing != ready, provider_changed

    def delete_nat_gateway(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, Any] | None, bool]:
        if not _is_canonical_uuid(uid):
            raise StateError("NATGateway UID must be a canonical UUID")
        if not _is_canonical_uuid(fulfillment_id):
            raise StateError("NATGateway Fulfillment UUID must be a canonical UUID")
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("NATGateway tenant ID is required")
        with self._locked_database(create=False, read_only=True) as connection:
            if connection is None:
                return None, False
            existing = self._nat_gateway_for_uid(connection, uid)
            if existing is None:
                return None, False
            if (existing["fulfillment_id"], existing["tenant_id"]) != (fulfillment_id, tenant_id):
                raise StateError("NATGateway identity does not match saved provider state")
        if check_mode:
            return existing, True

        virtual_network_uid = existing["virtual_network_uid"]
        with self._resource_locked(virtual_network_uid):
            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet NATGateway state disappeared")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._nat_gateway_for_uid(connection, uid)
                    if current is None:
                        connection.rollback()
                        return None, False
                    if (current["fulfillment_id"], current["tenant_id"]) != (fulfillment_id, tenant_id):
                        raise StateError("NATGateway identity does not match saved provider state")
                    deleting = {
                        **current,
                        "phase": "deleting",
                    }
                    self._write_nat_gateway_entry(connection, deleting, existing=current)
                    connection.commit()
                except BaseException:
                    if connection.in_transaction:
                        connection.rollback()
                    raise

            with self._locked_database(create=False, read_only=True) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet VirtualNetwork state disappeared")
                parent = self._entry_for_uid(connection, virtual_network_uid)
                if parent is None or parent["tenant_id"] != tenant_id:
                    raise StateError("NATGateway VirtualNetwork state does not match saved identity")
                self._validate_nat_gateway_dependencies(connection, deleting)
            try:
                with _with_firewall_lock(self.firewall_lock_path):
                    agentless_net_nat_gateway.delete_nat_gateway(parent, deleting)
            except (agentless_net_nat_gateway.NATGatewayProviderError, NetworkCommandError) as error:
                raise StateError(str(error)) from error

            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet state disappeared after NATGateway cleanup")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._nat_gateway_for_uid(connection, uid)
                    if current != deleting:
                        raise StateError("NATGateway state changed during provider cleanup")
                    connection.execute("DELETE FROM nat_gateways WHERE uid = ?", (uid,))
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
            return deleting, True


    def ensure_external_ip_pool(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        ip_family: str,
        cidrs: list[str],
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, str], bool]:
        requested = _external_ip_pool_entry(
            uid, fulfillment_id, tenant_id, ip_family, cidrs
        )
        operation_lock = (
            contextlib.nullcontext()
            if check_mode
            else self._resource_locked(uid)
        )
        with operation_lock:
            with self._locked_database(
                create=not check_mode,
                read_only=check_mode,
            ) as connection:
                if connection is None:
                    return requested, True
                if not check_mode:
                    connection.execute("BEGIN IMMEDIATE")
                try:
                    existing = self._external_ip_pool_for_uid(connection, uid)
                    if existing is not None:
                        if existing != requested:
                            raise StateError(
                                "ExternalIPPool identity does not match saved state"
                            )
                        if not check_mode:
                            connection.rollback()
                        return existing, False

                    self._require_unique_external_ip_pool_fulfillment_id(
                        connection, uid, fulfillment_id
                    )
                    if check_mode:
                        return requested, True

                    connection.execute(
                        """INSERT INTO external_ip_pools
                           (uid, fulfillment_id, tenant_id, ip_family, ipv4_cidr)
                           VALUES (?, ?, ?, ?, ?)""",
                        (
                            requested["uid"],
                            requested["fulfillment_id"],
                            requested["tenant_id"],
                            requested["ip_family"],
                            requested["ipv4_cidr"],
                        ),
                    )
                    connection.commit()
                    return requested, True
                except BaseException:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise

    def delete_external_ip_pool(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        ip_family: str,
        cidrs: list[str],
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, str] | None, bool]:
        requested = _external_ip_pool_entry(
            uid, fulfillment_id, tenant_id, ip_family, cidrs
        )
        operation_lock = (
            contextlib.nullcontext()
            if check_mode
            else self._resource_locked(uid)
        )
        with operation_lock:
            with self._locked_database(
                create=False,
                read_only=check_mode,
            ) as connection:
                if connection is None:
                    return None, False
                if not check_mode:
                    connection.execute("BEGIN IMMEDIATE")
                try:
                    existing = self._external_ip_pool_for_uid(connection, uid)
                    if existing is not None and existing != requested:
                        raise StateError(
                            "ExternalIPPool identity does not match saved state"
                        )

                    self._require_unique_external_ip_pool_fulfillment_id(
                        connection, uid, fulfillment_id
                    )
                    if existing is None:
                        if not check_mode:
                            connection.rollback()
                        return None, False
                    if connection.execute(
                        "SELECT 1 FROM external_ips WHERE pool_uid = ? LIMIT 1",
                        (uid,),
                    ).fetchone() is not None:
                        raise StateError("ExternalIP reservation still references this pool")
                    if check_mode:
                        return existing, True

                    connection.execute(
                        "DELETE FROM external_ip_pools WHERE uid = ?", (uid,)
                    )
                    connection.commit()
                    return existing, True
                except BaseException:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise

    def reserve_external_ip(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        pool_uid: str,
        pool_fulfillment_id: str,
        pool_tenant_id: str,
        pool_cidr: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, str], bool]:
        requested = _external_ip_identity(uid, fulfillment_id, tenant_id, pool_uid)
        pool = _external_ip_pool_entry(
            pool_uid, pool_fulfillment_id, pool_tenant_id, "IPv4", [pool_cidr]
        )
        if tenant_id != pool_tenant_id and pool_tenant_id != "shared":
            raise StateError("ExternalIP tenant does not match its pool")

        operation_lock = contextlib.nullcontext() if check_mode else self._resource_locked(uid)
        with operation_lock:
            with self._locked_database(create=False, read_only=check_mode) as connection:
                if connection is None:
                    raise StateError("ExternalIPPool is not registered")
                if not check_mode:
                    connection.execute("BEGIN IMMEDIATE")
                try:
                    saved_pool = self._external_ip_pool_for_uid(connection, pool_uid)
                    if saved_pool != pool:
                        raise StateError("ExternalIPPool identity does not match saved state")

                    existing = self._external_ip_for_uid(connection, uid)
                    if existing is not None:
                        if any(
                            existing[key] != requested[key]
                            for key in ("uid", "fulfillment_id", "tenant_id", "pool_uid")
                        ):
                            raise StateError("ExternalIP identity does not match saved reservation")
                        if not check_mode:
                            connection.rollback()
                        return existing, False

                    self._require_unique_external_ip_fulfillment_id(
                        connection, uid, fulfillment_id
                    )
                    network = ipaddress.ip_network(pool["ipv4_cidr"], strict=True)
                    first_host = int(network.network_address) + (
                        1 if network.prefixlen < 31 else 0
                    )
                    host_count = network.num_addresses - (
                        2 if network.prefixlen < 31 else 0
                    )
                    last_reservation = connection.execute(
                        "SELECT uid FROM external_ips WHERE pool_uid = ? "
                        "ORDER BY rowid DESC LIMIT 1",
                        (pool_uid,),
                    ).fetchone()
                    if last_reservation is None:
                        start = 0
                    else:
                        last_entry = self._external_ip_for_uid(
                            connection, last_reservation[0]
                        )
                        if last_entry is None:
                            raise StateCorrupt("ExternalIP reservation disappeared")
                        start = (
                            int(ipaddress.ip_address(last_entry["address"]))
                            - first_host
                            + 1
                        ) % host_count
                    address = None
                    # Keep the common path to a small number of indexed checks.
                    # For fragmented, nearly full pools, find a gap from the
                    # reservation set instead of querying every candidate address.
                    probe_count = min(host_count, EXTERNAL_IP_ALLOCATOR_PROBE_LIMIT)
                    for offset in range(probe_count):
                        candidate = str(
                            ipaddress.IPv4Address(
                                first_host + ((start + offset) % host_count)
                            )
                        )
                        if connection.execute(
                            "SELECT 1 FROM external_ips "
                            "WHERE pool_uid = ? AND address = ?",
                            (pool_uid, candidate),
                        ).fetchone() is None:
                            address = candidate
                            break
                    if address is None:
                        reservation_count = connection.execute(
                            "SELECT COUNT(*) FROM external_ips WHERE pool_uid = ?",
                            (pool_uid,),
                        ).fetchone()[0]
                        if reservation_count >= host_count:
                            raise StateError(
                                "ExternalIPPool has no available addresses"
                            )

                        pool_end = first_host + host_count
                        start_address = first_host + (
                            (start + probe_count) % host_count
                        )

                        def first_gap(lower: int, upper: int) -> int | None:
                            if lower >= upper:
                                return None
                            address_value_sql = self._external_ip_address_value_sql()
                            reservations = connection.execute(
                                f"""SELECT {address_value_sql}
                                    FROM external_ips
                                    WHERE pool_uid = ?
                                      AND {address_value_sql} >= ?
                                      AND {address_value_sql} < ?
                                    ORDER BY {address_value_sql}""",
                                (pool_uid, lower, upper),
                            )
                            candidate = lower
                            for (reserved,) in reservations:
                                if reserved > candidate:
                                    return candidate
                                if reserved == candidate:
                                    candidate += 1
                            return candidate if candidate < upper else None

                        available_address = first_gap(start_address, pool_end)
                        if available_address is None and start_address > first_host:
                            available_address = first_gap(first_host, start_address)
                        if available_address is None:
                            raise StateError(
                                "ExternalIPPool has no available addresses"
                            )
                        address = str(ipaddress.IPv4Address(available_address))
                    reservation = _external_ip_entry(
                        uid, fulfillment_id, tenant_id, pool_uid, address
                    )
                    if check_mode:
                        return reservation, True

                    connection.execute(
                        """INSERT INTO external_ips
                           (uid, fulfillment_id, tenant_id, pool_uid, address)
                           VALUES (?, ?, ?, ?, ?)""",
                        (
                            reservation["uid"],
                            reservation["fulfillment_id"],
                            reservation["tenant_id"],
                            reservation["pool_uid"],
                            reservation["address"],
                        ),
                    )
                    connection.commit()
                    return reservation, True
                except BaseException:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise

    def release_external_ip(
        self,
        uid: str,
        fulfillment_id: str,
        tenant_id: str,
        pool_uid: str,
        *,
        check_mode: bool = False,
    ) -> tuple[dict[str, str] | None, bool]:
        _external_ip_identity(uid, fulfillment_id, tenant_id, pool_uid)
        operation_lock = contextlib.nullcontext() if check_mode else self._resource_locked(uid)
        with operation_lock:
            with self._locked_database(create=False, read_only=check_mode) as connection:
                if connection is None:
                    return None, False
                if not check_mode:
                    connection.execute("BEGIN IMMEDIATE")
                try:
                    existing = self._external_ip_for_uid(connection, uid)
                    if existing is None:
                        if not check_mode:
                            connection.rollback()
                        return None, False
                    if any(
                        existing[key] != value
                        for key, value in (
                            ("fulfillment_id", fulfillment_id),
                            ("tenant_id", tenant_id),
                            ("pool_uid", pool_uid),
                        )
                    ):
                        raise StateError("ExternalIP identity does not match saved reservation")
                    if connection.execute(
                        """SELECT 1 FROM attachments
                           WHERE external_ip_uid = ? OR external_address = ? LIMIT 1""",
                        (uid, existing["address"]),
                    ).fetchone() is not None:
                        raise StateError("ExternalIP still has an ExternalIPAttachment")
                    if connection.execute(
                        """SELECT 1 FROM nat_gateways
                           WHERE external_ip_uid = ? OR external_address = ? LIMIT 1""",
                        (uid, existing["address"]),
                    ).fetchone() is not None:
                        raise StateError("ExternalIP still has an AgentlessNet NATGateway")
                    if check_mode:
                        return existing, True

                    connection.execute("DELETE FROM external_ips WHERE uid = ?", (uid,))
                    connection.commit()
                    return existing, True
                except BaseException:
                    if not check_mode and connection.in_transaction:
                        connection.rollback()
                    raise

    @staticmethod
    def _validate_uid(uid: str) -> None:
        if not _is_canonical_uuid(uid):
            raise StateError("VirtualNetwork UID must be a canonical UUID")

    @staticmethod
    def _validate_tenant_id(tenant_id: str) -> None:
        if not _is_valid_tenant_id(tenant_id):
            raise StateError("VirtualNetwork tenant ID is required")

    @staticmethod
    def _require_tenant_ownership(
        entry: dict[str, Any] | None, tenant_id: str
    ) -> None:
        if entry is not None and entry["tenant_id"] != tenant_id:
            raise StateError("VirtualNetwork tenant does not match saved state")

    @staticmethod
    def _identity_entry(uid: str) -> dict[str, Any]:
        digest = hashlib.sha256(uid.encode()).hexdigest()
        return {
            "uid": uid,
            "namespace_name": f"n{digest[:14]}",
            "uplink": {
                "namespace_interface": f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}{digest[:8]}n",
                "host_interface": f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}{digest[:8]}h",
            },
        }

    def _ensure_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        tenant_id: str,
        *,
        reconcile: bool = False,
    ) -> tuple[dict[str, Any], bool, bool]:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        try:
            network = _parse_canonical_ipv4_network(virtual_network_cidr)
        except (TypeError, ValueError) as error:
            raise StateError(f"invalid VirtualNetwork IPv4 CIDR: {error}") from error
        network_cidr = str(network)
        slot_count = network.num_addresses // 2
        if slot_count < 1:
            raise StateError("VirtualNetwork CIDR is too small for a /31 transit link")

        with self._resource_locked(uid):
            with self._locked_database(create=True) as connection:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    entry = self._entry_for_uid(connection, uid)
                    if entry is not None:
                        self._require_tenant_ownership(entry, tenant_id)
                        if entry["virtual_network_cidr"] != network_cidr:
                            raise StateError("VirtualNetwork CIDR does not match saved state")
                        state_changed = False
                    else:
                        identity = self._identity_entry(uid)
                        collision = connection.execute(
                            """
                            SELECT 1 FROM virtual_networks
                            WHERE namespace_name = ? OR namespace_interface = ?
                               OR host_interface = ?
                            LIMIT 1
                            """,
                            (
                                identity["namespace_name"],
                                identity["uplink"]["namespace_interface"],
                                identity["uplink"]["host_interface"],
                            ),
                        ).fetchone()
                        if collision:
                            raise StateError(
                                "VirtualNetwork UID collides with an existing provider identity"
                            )

                        seed = int.from_bytes(
                            hashlib.sha256(f"{uid}:{network_cidr}".encode()).digest()[:8],
                            "big",
                        ) % slot_count
                        transit_network = None
                        for offset in range(slot_count):
                            slot = (seed + offset) % slot_count
                            address = network.network_address + (slot * 2)
                            candidate = ipaddress.ip_network((address, 31))
                            occupied = connection.execute(
                                "SELECT 1 FROM virtual_networks WHERE transit_start = ?",
                                (int(candidate.network_address),),
                            ).fetchone()
                            if occupied is None:
                                transit_network = candidate
                                break
                        if transit_network is None:
                            raise StateError(
                                "no free /31 transit block remains in the VirtualNetwork CIDR"
                            )

                        namespace_ip = transit_network.network_address + 1
                        host_ip = transit_network.network_address
                        entry = {
                            **identity,
                            "tenant_id": tenant_id,
                            "virtual_network_cidr": network_cidr,
                            "transit": {
                                "cidr": str(transit_network),
                                "namespace_ip": f"{namespace_ip}/{transit_network.prefixlen}",
                                "host_ip": f"{host_ip}/{transit_network.prefixlen}",
                                "gateway": str(host_ip),
                            },
                        }
                        self._validate_entry(entry)
                        connection.execute(
                            """
                            INSERT INTO virtual_networks (
                                uid, tenant_id, virtual_network_cidr, namespace_name,
                                namespace_interface, host_interface, transit_cidr,
                                transit_start, payload
                            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                            """,
                            self._row_for_entry(entry),
                        )
                        state_changed = True
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise

            network_changed = False
            if reconcile:
                # Reserve first so a route or provider failure is retryable with the
                # same UID-to-/31 mapping. No provider command runs under the database state lock.
                _assert_transit_route_available(entry)
                network_changed = reconcile_virtual_network(
                    entry,
                    firewall_lock_path=self.firewall_lock_path,
                )
            return entry, state_changed, network_changed

    def ensure_and_reconcile_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        tenant_id: str,
    ) -> tuple[dict[str, Any], bool, bool]:
        return self._ensure_virtual_network(
            uid,
            virtual_network_cidr,
            tenant_id,
            reconcile=True,
        )

    def get_virtual_network(
        self, uid: str, tenant_id: str, *, check_mode: bool = False
    ) -> dict[str, Any] | None:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        with self._locked_database(
            create=False,
            read_only=check_mode,
        ) as connection:
            if connection is None:
                return None
            entry = self._entry_for_uid(connection, uid)
            self._require_tenant_ownership(entry, tenant_id)
            return entry

    def delete_and_remove_virtual_network(
        self, uid: str, tenant_id: str, dhcp_supervisor: str | None = None
    ) -> bool:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        if dhcp_supervisor is not None:
            _validate_dhcp_supervisor(dhcp_supervisor)
        with self._resource_locked(uid):
            with self._locked_database(create=False) as connection:
                entry = (
                    self._entry_for_uid(connection, uid)
                    if connection is not None
                    else None
                )
                self._require_tenant_ownership(entry, tenant_id)
                if connection is not None:
                    has_attachments = connection.execute(
                        "SELECT 1 FROM attachments WHERE virtual_network_uid = ? LIMIT 1",
                        (uid,),
                    ).fetchone()
                    if has_attachments is not None:
                        raise StateError(
                            "VirtualNetwork cannot be deleted while saved ExternalIPAttachments remain"
                        )
                    has_nat_gateways = connection.execute(
                        "SELECT 1 FROM nat_gateways WHERE virtual_network_uid = ? LIMIT 1",
                        (uid,),
                    ).fetchone()
                    if has_nat_gateways is not None:
                        raise StateError(
                            "VirtualNetwork cannot be deleted while saved NATGateways remain"
                        )
                    has_children = connection.execute(
                        "SELECT 1 FROM subnets WHERE virtual_network_uid = ? LIMIT 1",
                        (uid,),
                    ).fetchone()
                    if has_children is not None:
                        raise StateError(
                            "VirtualNetwork cannot be deleted while saved Subnets remain"
                        )
                provider_entry = (
                    entry if entry is not None else self._identity_entry(uid)
                )
                remaining_virtual_network = False
                if connection is not None:
                    remaining_virtual_network = (
                        connection.execute(
                            "SELECT 1 FROM virtual_networks WHERE uid <> ? LIMIT 1",
                            (uid,),
                        ).fetchone()
                        is not None
                    )

            try:
                dhcp_changed = cleanup_virtual_network_dhcp(
                    uid,
                    provider_entry["namespace_name"],
                    dhcp_supervisor,
                    remove_leases=True,
                )
            except SubnetProviderError as error:
                raise StateError(str(error)) from error

            provider_changed = delete_virtual_network(
                provider_entry,
                firewall_lock_path=self.firewall_lock_path,
                require_alias=entry is None,
                remaining_virtual_network=remaining_virtual_network,
            )
            if entry is None:
                return dhcp_changed or provider_changed

            with self._locked_database(create=False) as connection:
                if connection is None:
                    raise StateCorrupt("AgentlessNet state disappeared during provider cleanup")
                connection.execute("BEGIN IMMEDIATE")
                try:
                    current = self._entry_for_uid(connection, uid)
                    if current != entry:
                        raise StateError(
                            "VirtualNetwork state changed while provider cleanup was running"
                        )
                    connection.execute(
                        "DELETE FROM virtual_networks WHERE uid = ?", (uid,)
                    )
                    connection.commit()
                except BaseException:
                    connection.rollback()
                    raise
            return True


def _run(command: list[str], check: bool = True):
    try:
        return run_command(command, check=check)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error


def _assert_transit_route_available(entry: dict[str, Any]) -> None:
    transit = ipaddress.ip_network(entry["transit"]["cidr"])
    host_interface = entry["uplink"]["host_interface"]
    for address in (transit.network_address, transit.network_address + 1):
        routes = _run(
            ["ip", "-j", "-4", "route", "get", "fibmatch", str(address)]
        ).stdout
        try:
            route_entries = parse_json_object_list(
                routes,
                description="network-node IPv4 route lookup",
            )
        except NetworkCommandError as error:
            raise StateError(f"could not inspect network-node IPv4 route: {error}") from error
        if len(route_entries) != 1:
            raise StateError("network-node IPv4 route lookup returned an unexpected result")
        route = route_entries[0]
        if route.get("dev") == host_interface:
            continue

        destination = route.get("dst", "default")
        if destination in ("default", "0.0.0.0/0"):
            continue
        try:
            route_network = ipaddress.ip_network(destination, strict=False)
        except (TypeError, ValueError) as error:
            raise StateError("network-node IPv4 route has an invalid destination") from error
        if route_network.version == 4 and route_network.prefixlen >= transit.prefixlen:
            raise StateError(
                f"VirtualNetwork transit CIDR {transit} overlaps existing host route "
                f"{route_network}"
            )


def _host_forwarding_rules() -> list[str]:
    output = _run(
        ["iptables", "-w", "-t", "filter", "-S", "FORWARD"]
    ).stdout.splitlines()
    return [line.strip() for line in output if line.startswith("-A FORWARD ")]


def _is_unconditional_forward_drop(rule: str) -> bool:
    parts = rule.split()
    return parts == ["-A", "FORWARD", "-j", "DROP"] or (
        len(parts) == 6
        and parts[:2] == ["-A", "FORWARD"]
        and parts[2] in ("-i", "-o")
        and parts[4:] == ["-j", "DROP"]
    )


def _host_forwarding_isolation_rule(direction: str) -> str:
    interface_pattern = f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+"
    return f"-A FORWARD {direction} {interface_pattern} -j DROP"


def _external_forwarding_chain_hook() -> str:
    return f"-A FORWARD -j {agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN}"


def _nat_gateway_forwarding_chain_hook() -> str:
    return f"-A FORWARD -j {agentless_net_nat_gateway.SNAT_FORWARD_CHAIN}"


def _valid_host_isolation_prefix(rule: str) -> bool:
    return rule in {
        _external_forwarding_chain_hook(),
        _nat_gateway_forwarding_chain_hook(),
    } or _is_unconditional_forward_drop(rule)


def _verify_host_forwarding_isolation() -> None:
    rules = _host_forwarding_rules()
    hook = _external_forwarding_chain_hook()
    nat_gateway_hook = _nat_gateway_forwarding_chain_hook()
    hook_count = rules.count(hook)
    if hook_count > 1:
        raise StateError("duplicate ExternalIP forwarding chain hooks exist")
    nat_gateway_hook_count = rules.count(nat_gateway_hook)
    if nat_gateway_hook_count > 1:
        raise StateError("duplicate NATGateway forwarding chain hooks exist")
    if hook_count == 1:
        if rules[0] != hook:
            raise StateError("ExternalIP forwarding chain hook is below host isolation rules")
        try:
            agentless_net_external_ip.verify_external_forwarding_chain()
        except NetworkCommandError as error:
            raise StateError(str(error)) from error
    if nat_gateway_hook_count == 1:
        try:
            agentless_net_nat_gateway.verify_snat_forwarding_hook()
        except NetworkCommandError as error:
            raise StateError(str(error)) from error
    for direction in ("-i", "-o"):
        expected = _host_forwarding_isolation_rule(direction)
        try:
            index = rules.index(expected)
        except ValueError as error:
            raise StateError("host VirtualNetwork forwarding isolation rule is absent") from error
        if any(not _valid_host_isolation_prefix(rule) for rule in rules[:index]):
            raise StateError("host VirtualNetwork forwarding isolation rule is below an allow rule")


def _ensure_host_forwarding_isolation() -> bool:
    changed = False
    rules = _host_forwarding_rules()
    hook = _external_forwarding_chain_hook()
    nat_gateway_hook = _nat_gateway_forwarding_chain_hook()
    if rules.count(hook) > 1:
        raise StateError("duplicate ExternalIP forwarding chain hooks exist")
    if hook in rules:
        try:
            agentless_net_external_ip.verify_external_forwarding_chain()
        except NetworkCommandError as error:
            raise StateError(str(error)) from error
        if rules[0] != hook:
            _run(["iptables", "-w", "-t", "filter", "-D", "FORWARD", "-j", agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN])
            _run(["iptables", "-w", "-t", "filter", "-I", "FORWARD", "1", "-j", agentless_net_external_ip.EXTERNAL_FORWARD_CHAIN])
            rules.remove(hook)
            rules.insert(0, hook)
            changed = True
    if nat_gateway_hook in rules:
        try:
            agentless_net_nat_gateway.verify_snat_forwarding_hook()
        except NetworkCommandError as error:
            raise StateError(str(error)) from error
        expected_index = 1 if hook in rules else 0
        if rules.index(nat_gateway_hook) != expected_index:
            _run([
                "iptables", "-w", "-t", "filter", "-D", "FORWARD",
                "-j", agentless_net_nat_gateway.SNAT_FORWARD_CHAIN,
            ])
            _run([
                "iptables", "-w", "-t", "filter", "-I", "FORWARD",
                str(expected_index + 1), "-j", agentless_net_nat_gateway.SNAT_FORWARD_CHAIN,
            ])
            rules.remove(nat_gateway_hook)
            rules.insert(expected_index, nat_gateway_hook)
            changed = True
    for direction in ("-i", "-o"):
        expected = _host_forwarding_isolation_rule(direction)
        needs_reorder = expected not in rules or rules.count(expected) > 1
        if not needs_reorder:
            index = rules.index(expected)
            needs_reorder = any(
                not _valid_host_isolation_prefix(rule) for rule in rules[:index]
            )
        if needs_reorder:
            check_rule = [
                "iptables",
                "-w",
                "-t",
                "filter",
                "-C",
                "FORWARD",
                direction,
                f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+",
                "-j",
                "DROP",
            ]
            delete_rule = check_rule.copy()
            delete_rule[4] = "-D"
            remove_repeated_rule(
                _run,
                check_rule,
                delete_rule,
                error_message="too many duplicate host forwarding isolation rules",
                error_type=StateError,
            )
            insert_index = "2" if hook in rules else "1"
            insert_rule = [
                "iptables",
                "-w",
                "-t",
                "filter",
                "-I",
                "FORWARD",
                insert_index,
                direction,
                f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+",
                "-j",
                "DROP",
            ]
            _run(insert_rule)
            changed = True
            rules = [rule for rule in rules if rule != expected]
            rules.insert(1 if hook in rules else 0, expected)
    _verify_host_forwarding_isolation()
    return changed


def _agentless_host_veth_present() -> bool:
    links = parse_json_object_list(
        _run(["ip", "-j", "link", "show"]).stdout,
        description="network-node link",
    )
    return any(
        isinstance(link.get("ifname"), str)
        and link["ifname"].startswith(AGENTLESS_NET_HOST_INTERFACE_PREFIX)
        for link in links
    )


def _remove_host_forwarding_isolation(
    *, remaining_virtual_network: bool = False
) -> bool:
    if remaining_virtual_network or _agentless_host_veth_present():
        return False

    changed = False
    for direction in ("-i", "-o"):
        check_rule = [
            "iptables",
            "-w",
            "-t",
            "filter",
            "-C",
            "FORWARD",
            direction,
            f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+",
            "-j",
            "DROP",
        ]
        delete_rule = check_rule.copy()
        delete_rule[4] = "-D"
        changed = (
            remove_repeated_rule(
                _run,
                check_rule,
                delete_rule,
                error_message=(
                    "too many duplicate host forwarding isolation rules during deletion"
                ),
                error_type=StateError,
            )
            or changed
        )
    return changed


def _with_firewall_lock(lock_path: Path | None):
    return _locked_path(lock_path) if lock_path is not None else contextlib.nullcontext()


def reconcile_virtual_network(
    entry: dict[str, Any], *, firewall_lock_path: Path | None = None
) -> bool:
    namespace = entry["namespace_name"]
    namespace_interface = entry["uplink"]["namespace_interface"]
    host_interface = entry["uplink"]["host_interface"]
    transit = entry["transit"]
    try:
        with _with_firewall_lock(firewall_lock_path):
            changed = _ensure_host_forwarding_isolation()
            try:
                changed = (
                    ensure_veth_pair(
                        namespace,
                        namespace_interface,
                        host_interface,
                        owner_alias=f"osac-vn:{entry['uid']}",
                    )
                    or changed
                )
            except NetworkCommandError:
                _remove_host_forwarding_isolation()
                raise
    except NetworkCommandError as error:
        raise StateError(str(error)) from error

    # Keep this namespace isolated even when the node already has forwarding
    # enabled for other workloads. Do not change the host-wide forwarding sysctl.
    try:
        changed = (
            configure_uplink(
                namespace,
                namespace_interface,
                host_interface,
                transit["namespace_ip"],
                transit["host_ip"],
                transit["gateway"],
            )
            or changed
        )
        changed = ensure_ipv4_forwarding(namespace) or changed
    except NetworkCommandError as error:
        raise StateError(str(error)) from error

    forward_policy = _run(
        ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "filter", "-S", "FORWARD"]
    ).stdout
    if "-P FORWARD ACCEPT" not in forward_policy:
        changed = True
    _run(
        ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "filter", "-P", "FORWARD", "ACCEPT"]
    )
    established_rule = [
        "ip",
        "netns",
        "exec",
        namespace,
        "iptables",
        "-w",
        "-t",
        "filter",
        "-C",
        "FORWARD",
        "-m",
        "conntrack",
        "--ctstate",
        "ESTABLISHED,RELATED",
        "-j",
        "ACCEPT",
    ]
    if _run(established_rule, check=False).returncode != 0:
        changed = True
        established_rule[8] = "-I"
        established_rule.insert(10, "1")
        # The check command is `-C FORWARD ...`; insertion syntax is `-I FORWARD 1 ...`.
        _run(established_rule)

    try:
        _verify_virtual_network(entry)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error
    return changed


def _verify_virtual_network(entry: dict[str, Any]) -> None:
    namespace = entry["namespace_name"]
    namespace_interface = entry["uplink"]["namespace_interface"]
    host_interface = entry["uplink"]["host_interface"]
    transit = entry["transit"]
    if not namespace_exists(namespace):
        raise StateError("VirtualNetwork namespace is not present after reconciliation")
    host_link = link_details(None, host_interface)
    namespace_link = link_details(namespace, namespace_interface)
    if host_link is None or namespace_link is None:
        raise StateError("VirtualNetwork uplink interface is absent")
    if host_link.get("ifalias") != f"osac-vn:{entry['uid']}":
        raise StateError("VirtualNetwork host uplink ownership alias is invalid")
    if not _address_present(None, host_interface, transit["host_ip"]):
        raise StateError("VirtualNetwork host uplink address did not converge")
    if not _address_present(namespace, namespace_interface, transit["namespace_ip"]):
        raise StateError("VirtualNetwork namespace uplink address did not converge")
    if "UP" not in host_link.get("flags", []) or "UP" not in namespace_link.get("flags", []):
        raise StateError("VirtualNetwork uplink interfaces are not up")
    if not _default_route_present(namespace, transit["gateway"], namespace_interface):
        raise StateError("VirtualNetwork default route did not converge on its uplink")
    forwarding = _run(
        ["ip", "netns", "exec", namespace, "sysctl", "-n", "net.ipv4.ip_forward"]
    ).stdout.strip()
    if forwarding != "1":
        raise StateError("VirtualNetwork IPv4 forwarding is not enabled")

    policy = _run(
        ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "filter", "-S", "FORWARD"]
    ).stdout.splitlines()
    if "-P FORWARD ACCEPT" not in {line.strip() for line in policy}:
        raise StateError("VirtualNetwork FORWARD policy is not permit-all")
    rule = _run(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "iptables",
            "-w",
            "-t",
            "filter",
            "-C",
            "FORWARD",
            "-m",
            "conntrack",
            "--ctstate",
            "ESTABLISHED,RELATED",
            "-j",
            "ACCEPT",
        ],
        check=False,
    )
    if rule.returncode != 0:
        raise StateError("VirtualNetwork established/related forwarding rule is absent")
    _verify_host_forwarding_isolation()


def delete_virtual_network(
    entry: dict[str, Any],
    *,
    firewall_lock_path: Path | None = None,
    require_alias: bool = False,
    remaining_virtual_network: bool = False,
) -> bool:
    namespace = entry["namespace_name"]
    host_interface = entry["uplink"]["host_interface"]
    owner_alias = f"osac-vn:{entry['uid']}"
    try:
        with _with_firewall_lock(firewall_lock_path):
            host_link = link_details(None, host_interface)
            if host_link is not None:
                if host_link.get("linkinfo", {}).get("info_kind") != "veth":
                    raise StateError("deterministic host uplink exists but is not a veth")
                alias = host_link.get("ifalias", "")
                if alias != owner_alias and (require_alias or alias):
                    raise StateError("deterministic host uplink is not owned by this VirtualNetwork UID")
            uplink_changed = delete_uplink(namespace, host_interface)
            firewall_changed = _remove_host_forwarding_isolation(
                remaining_virtual_network=remaining_virtual_network
            )
    except NetworkCommandError as error:
        raise StateError(str(error)) from error
    return uplink_changed or firewall_changed
