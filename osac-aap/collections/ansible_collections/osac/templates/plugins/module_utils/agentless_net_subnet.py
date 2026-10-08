"""Linux VLAN interface and per-VirtualNetwork DHCP helpers."""

from __future__ import annotations

import hashlib
import ipaddress
import json
import os
import re
import shutil
import stat
import tempfile
import time
from pathlib import Path
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils import agentless_net_network as network


class SubnetProviderError(Exception):
    """A Subnet data-plane operation could not be verified."""


AGENTLESS_NET_CONFIG_ROOT = Path("/etc/agentless-net")
AGENTLESS_NET_STATE_ROOT = Path("/var/lib/agentless-net")
AGENTLESS_NET_RUNTIME_ROOT = Path("/run/agentless-net")
AGENTLESS_NET_LOG_ROOT = Path("/var/log/agentless-net")
DHCP_CONFIG_ROOT = AGENTLESS_NET_CONFIG_ROOT / "dhcp"
DHCP_LEASE_ROOT = AGENTLESS_NET_STATE_ROOT / "dhcp"
DHCP_RUNTIME_ROOT = AGENTLESS_NET_RUNTIME_ROOT / "dhcp"
DHCP_LOG_ROOT = AGENTLESS_NET_LOG_ROOT / "dhcp"
SYSTEMD_UNIT_ROOT = Path("/etc/systemd/system")
SUPERVISOR_PROGRAM_ROOT = Path("/etc/agentless-net/supervisor.d")
SUPERVISOR_BASE_CONFIG = Path("/etc/agentless-net/supervisord.conf")
SUPERVISOR_START_TIMEOUT_SECONDS = 5.0
SUPERVISOR_POLL_INTERVAL_SECONDS = 0.2
DHCP_INTERFACE_RE = re.compile(r"^[A-Za-z0-9_.-]{1,15}$")
DHCP_MAC_RE = re.compile(r"^(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$")
MIN_VLAN_ID = 1
MAX_VLAN_ID = 4094
SYSTEMD_RUNTIME_ROOT = Path("/run/systemd/system")


def _vlan_id(value: Any, context: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise SubnetProviderError(f"{context} must be an integer VLAN ID")
    if not MIN_VLAN_ID <= value <= MAX_VLAN_ID:
        raise SubnetProviderError(f"{context} is outside the supported VLAN range")
    return value


def _nvue_variants(value: Any, context: str) -> dict[str, Any]:
    if isinstance(value, dict):
        if not value or not set(value).issubset({"operational", "applied"}):
            raise SubnetProviderError(f"Cumulus {context} JSON is malformed")
        return value
    return {"value": value}


def _parse_vlan_range(value: Any, context: str) -> tuple[int, int]:
    if isinstance(value, int) and not isinstance(value, bool):
        first = last = _vlan_id(value, context)
        return first, last
    if not isinstance(value, str):
        raise SubnetProviderError(f"Cumulus {context} range is malformed")
    match = re.fullmatch(r"([0-9]+)-([0-9]+)", value)
    if match is None:
        raise SubnetProviderError(f"Cumulus {context} range is malformed")
    first = _vlan_id(int(match.group(1)), context)
    last = _vlan_id(int(match.group(2)), context)
    if first > last:
        raise SubnetProviderError(f"Cumulus {context} range is reversed")
    return first, last


def parse_switch_vlan_exclusions(
    bridge_vlan_output: str, reserved_vlan_output: str
) -> set[int]:
    """Parse one switch's active VLAN memberships and NVUE reserved ranges."""
    try:
        interfaces = json.loads(bridge_vlan_output)
        reserved = json.loads(reserved_vlan_output)
    except (TypeError, json.JSONDecodeError) as error:
        raise SubnetProviderError("Cumulus VLAN discovery returned invalid JSON") from error
    if not isinstance(interfaces, list) or not interfaces:
        raise SubnetProviderError("Cumulus VLAN discovery returned no interfaces")
    excluded: set[int] = set()
    seen_interfaces: set[str] = set()
    for interface in interfaces:
        if not isinstance(interface, dict):
            raise SubnetProviderError("Cumulus VLAN interface entry is malformed")
        name = interface.get("ifname")
        memberships = interface.get("vlans")
        if (
            not isinstance(name, str)
            or not name
            or name in seen_interfaces
            or not isinstance(memberships, list)
        ):
            raise SubnetProviderError("Cumulus VLAN interface entry is malformed")
        seen_interfaces.add(name)
        for membership in memberships:
            if not isinstance(membership, dict):
                raise SubnetProviderError("Cumulus VLAN membership entry is malformed")
            first = _vlan_id(membership.get("vlan"), "Cumulus VLAN membership")
            last = _vlan_id(membership.get("vlanEnd", first), "Cumulus VLAN range end")
            flags = membership.get("flags", [])
            if first > last or not isinstance(flags, list) or any(
                not isinstance(flag, str) for flag in flags
            ):
                raise SubnetProviderError("Cumulus VLAN membership entry is malformed")
            # bridge -j vlan show includes native/PVID memberships as VLAN entries.
            excluded.update(range(first, last + 1))

    if not isinstance(reserved, dict):
        raise SubnetProviderError("Cumulus reserved VLAN JSON is malformed")
    internal = reserved.get("internal")
    l3_vni = reserved.get("l3-vni-vlan")
    if not isinstance(internal, dict) or "range" not in internal:
        raise SubnetProviderError("Cumulus internal reserved VLAN range is missing")
    if not isinstance(l3_vni, dict) or "begin" not in l3_vni or "end" not in l3_vni:
        raise SubnetProviderError("Cumulus L3-VNI reserved VLAN range is missing")

    for value in _nvue_variants(internal["range"], "internal reserved VLAN").values():
        first, last = _parse_vlan_range(value, "internal reserved VLAN")
        excluded.update(range(first, last + 1))

    begins = _nvue_variants(l3_vni["begin"], "L3-VNI reserved VLAN begin")
    ends = _nvue_variants(l3_vni["end"], "L3-VNI reserved VLAN end")
    variant_names = set(begins) | set(ends)
    for name in variant_names:
        begin_value = begins.get(name, begins.get("value"))
        end_value = ends.get(name, ends.get("value"))
        if begin_value is None or end_value is None:
            raise SubnetProviderError("Cumulus L3-VNI reserved VLAN range is incomplete")
        first = _vlan_id(begin_value, "Cumulus L3-VNI reserved VLAN begin")
        last = _vlan_id(end_value, "Cumulus L3-VNI reserved VLAN end")
        if first > last:
            raise SubnetProviderError("Cumulus L3-VNI reserved VLAN range is reversed")
        excluded.update(range(first, last + 1))
    return excluded


def _run(command: list[str], check: bool = True):
    try:
        return network.run_command(command, check=check)
    except network.NetworkCommandError as error:
        raise SubnetProviderError(str(error)) from error


def _validate_parent(parent: dict[str, Any]) -> tuple[str, str]:
    namespace = parent.get("namespace_name")
    uid = parent.get("uid")
    if not isinstance(uid, str) or not re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", uid
    ):
        raise SubnetProviderError("saved VirtualNetwork UID is invalid")
    if not isinstance(namespace, str) or not re.fullmatch(r"n[0-9a-f]{14}", namespace):
        raise SubnetProviderError("saved VirtualNetwork namespace is invalid")
    return uid, namespace


def _validate_subnet(entry: dict[str, Any]) -> tuple[str, str, int, str]:
    uid = entry.get("uid")
    interface = entry.get("vlan_interface")
    trunk = entry.get("trunk_interface")
    vlan_id = entry.get("vlan_id")
    if not isinstance(uid, str) or not re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", uid
    ):
        raise SubnetProviderError("saved Subnet UID is invalid")
    expected = f"s{hashlib.sha256(uid.encode()).hexdigest()[:12]}"
    if interface != expected:
        raise SubnetProviderError("saved Subnet VLAN interface is invalid")
    if not isinstance(trunk, str) or not DHCP_INTERFACE_RE.fullmatch(trunk):
        raise SubnetProviderError("saved Subnet trunk interface is invalid")
    if isinstance(vlan_id, bool) or not isinstance(vlan_id, int) or not 1 <= vlan_id <= 4094:
        raise SubnetProviderError("saved Subnet VLAN ID is invalid")
    try:
        subnet = ipaddress.ip_network(entry.get("ipv4_cidr"), strict=True)
        gateway = ipaddress.IPv4Address(entry.get("gateway_ipv4"))
        dhcp_start = ipaddress.IPv4Address(entry.get("dhcp_range_start"))
        dhcp_end = ipaddress.IPv4Address(entry.get("dhcp_range_end"))
    except (TypeError, ValueError) as error:
        raise SubnetProviderError("saved Subnet address state is invalid") from error
    if (
        not isinstance(subnet, ipaddress.IPv4Network)
        or subnet.prefixlen > 30
        or gateway != subnet.network_address + 1
        or dhcp_start != subnet.network_address + 2
        or dhcp_start > dhcp_end
        or dhcp_end >= subnet.broadcast_address
    ):
        raise SubnetProviderError("saved Subnet gateway or DHCP range is invalid")
    return uid, interface, vlan_id, trunk


def _expected_alias(uid: str) -> str:
    return f"osac-subnet:{uid}"


def _verify_vlan_link(
    details: dict[str, Any] | None,
    *,
    uid: str,
    interface: str,
    vlan_id: int,
    trunk: str,
    allow_unclaimed: bool = False,
) -> bool:
    if details is None:
        return False
    linkinfo = details.get("linkinfo")
    info_data = linkinfo.get("info_data") if isinstance(linkinfo, dict) else None
    if (
        not isinstance(linkinfo, dict)
        or linkinfo.get("info_kind") != "vlan"
        or not isinstance(info_data, dict)
        or str(info_data.get("id")) != str(vlan_id)
    ):
        raise SubnetProviderError(
            f"interface {interface} exists but is not the saved Subnet VLAN"
        )
    alias = details.get("ifalias", "")
    if alias not in (_expected_alias(uid), "" if allow_unclaimed else _expected_alias(uid)):
        raise SubnetProviderError(f"interface {interface} is owned by another resource")
    parent = details.get("link")
    if parent and parent != trunk:
        raise SubnetProviderError(f"interface {interface} is attached to another trunk")
    return alias == _expected_alias(uid)


def _interface_locations(namespace: str, interface: str):
    return network.link_details(None, interface), network.link_details(namespace, interface)


def _ensure_subnet_interface(namespace: str, entry: dict[str, Any]) -> bool:
    uid, interface, vlan_id, trunk = _validate_subnet(entry)
    if network.link_details(None, trunk) is None:
        raise SubnetProviderError(f"saved Subnet trunk interface {trunk} is missing")
    changed = False
    host_link, namespace_link = _interface_locations(namespace, interface)
    if host_link is not None and namespace_link is not None:
        raise SubnetProviderError(f"Subnet VLAN interface {interface} exists in two locations")
    if namespace_link is not None:
        owned = _verify_vlan_link(
            namespace_link,
            uid=uid,
            interface=interface,
            vlan_id=vlan_id,
            trunk=trunk,
        )
        if not owned:
            raise SubnetProviderError(f"Subnet VLAN interface {interface} has no ownership alias")
    elif host_link is not None:
        _verify_vlan_link(
            host_link,
            uid=uid,
            interface=interface,
            vlan_id=vlan_id,
            trunk=trunk,
            allow_unclaimed=True,
        )
        if host_link.get("ifalias", "") == "":
            _run(["ip", "link", "set", "dev", interface, "alias", _expected_alias(uid)])
            changed = True
        _run(["ip", "link", "set", "dev", interface, "netns", namespace])
        changed = True
    else:
        _run(["ip", "link", "add", "link", trunk, "name", interface, "type", "vlan", "id", str(vlan_id)])
        _run(["ip", "link", "set", "dev", interface, "alias", _expected_alias(uid)])
        _run(["ip", "link", "set", "dev", interface, "netns", namespace])
        changed = True

    if not network._link_is_up(namespace, interface):
        _run(["ip", "netns", "exec", namespace, "ip", "link", "set", "dev", interface, "up"])
        changed = True
    prefix = ipaddress.ip_network(entry["ipv4_cidr"]).prefixlen
    gateway = f"{entry['gateway_ipv4']}/{prefix}"
    if not network._address_present(namespace, interface, gateway):
        _run(
            [
                "ip",
                "netns",
                "exec",
                namespace,
                "ip",
                "address",
                "replace",
                gateway,
                "dev",
                interface,
            ]
        )
        changed = True
    verify_subnet_interface(namespace, entry)
    return changed


def verify_subnet_interface(namespace: str, entry: dict[str, Any]) -> None:
    uid, interface, vlan_id, trunk = _validate_subnet(entry)
    details = network.link_details(namespace, interface)
    if details is None:
        raise SubnetProviderError(f"Subnet VLAN interface {interface} is absent")
    if not _verify_vlan_link(
        details,
        uid=uid,
        interface=interface,
        vlan_id=vlan_id,
        trunk=trunk,
    ):
        raise SubnetProviderError(f"Subnet VLAN interface {interface} has no ownership alias")
    if not network._link_is_up(namespace, interface):
        raise SubnetProviderError(f"Subnet VLAN interface {interface} is not up")
    prefix = ipaddress.ip_network(entry["ipv4_cidr"]).prefixlen
    if not network._address_present(
        namespace, interface, f"{entry['gateway_ipv4']}/{prefix}"
    ):
        raise SubnetProviderError("Subnet gateway address did not converge")


def remove_subnet_interface(namespace: str, entry: dict[str, Any]) -> bool:
    uid, interface, vlan_id, trunk = _validate_subnet(entry)
    host_link, namespace_link = _interface_locations(namespace, interface)
    if host_link is not None and namespace_link is not None:
        raise SubnetProviderError(f"Subnet VLAN interface {interface} exists in two locations")
    changed = False
    if namespace_link is not None:
        _verify_vlan_link(
            namespace_link,
            uid=uid,
            interface=interface,
            vlan_id=vlan_id,
            trunk=trunk,
            allow_unclaimed=True,
        )
        _run(["ip", "netns", "exec", namespace, "ip", "link", "delete", "dev", interface])
        changed = True
    elif host_link is not None:
        _verify_vlan_link(
            host_link,
            uid=uid,
            interface=interface,
            vlan_id=vlan_id,
            trunk=trunk,
            allow_unclaimed=True,
        )
        _run(["ip", "link", "delete", "dev", interface])
        changed = True
    host_after, namespace_after = _interface_locations(namespace, interface)
    if host_after is not None or namespace_after is not None:
        raise SubnetProviderError(f"Subnet VLAN interface {interface} remains after deletion")
    return changed


def _ensure_subnet_interfaces(
    parent: dict[str, Any], entries: list[dict[str, Any]]
) -> bool:
    _, namespace = _validate_parent(parent)
    if not network.namespace_exists(namespace):
        raise SubnetProviderError("VirtualNetwork namespace is absent during Subnet reconciliation")
    changed = False
    for entry in sorted(entries, key=lambda value: value["uid"]):
        _, _, _, trunk = _validate_subnet(entry)
        if not network._link_is_up(None, trunk):
            _run(["ip", "link", "set", "dev", trunk, "up"])
            changed = True
        changed = _ensure_subnet_interface(namespace, entry) or changed
    return changed


def _state_paths(uid: str) -> dict[str, Path]:
    if not isinstance(uid, str) or not re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", uid
    ):
        raise SubnetProviderError("saved VirtualNetwork UID is invalid")
    return {
        "config_dir": DHCP_CONFIG_ROOT / uid,
        "config": DHCP_CONFIG_ROOT / uid / "dnsmasq.conf",
        "lease_dir": DHCP_LEASE_ROOT / uid,
        "leases": DHCP_LEASE_ROOT / uid / "dnsmasq.leases",
        "marker": DHCP_LEASE_ROOT / uid / ".initialized",
        "pid": DHCP_RUNTIME_ROOT / f"{uid}.pid",
        "systemd_unit": SYSTEMD_UNIT_ROOT / f"agentless-dhcp@{uid}.service",
        "supervisor_program": SUPERVISOR_PROGRAM_ROOT / f"{uid}.ini",
        "log_dir": DHCP_LOG_ROOT,
        "log": DHCP_LOG_ROOT / f"{uid}.log",
    }


def _owned_service_file(path: Path) -> bool:
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False
    except OSError as error:
        raise SubnetProviderError("could not inspect AgentlessNet DHCP service file") from error
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        raise SubnetProviderError("AgentlessNet DHCP service file has unsafe type or owner")
    return True


def _systemd_available() -> bool:
    return SYSTEMD_RUNTIME_ROOT.is_dir() and shutil.which("systemctl") is not None


def _supervisor_available() -> bool:
    if not SUPERVISOR_BASE_CONFIG.is_file() or shutil.which("supervisorctl") is None:
        return False
    result = _run(
        ["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "pid"],
        check=False,
    )
    return result.returncode == 0 and result.stdout.strip().isdigit()


def resolve_dhcp_supervisor(uid: str) -> str:
    """Select the managed node's DHCP manager, preserving an owned existing service."""
    if not isinstance(uid, str) or not re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", uid
    ):
        raise SubnetProviderError("VirtualNetwork UID is invalid for DHCP manager selection")
    paths = _state_paths(uid)
    systemd_file = _owned_service_file(paths["systemd_unit"])
    supervisor_file = _owned_service_file(paths["supervisor_program"])
    if systemd_file and supervisor_file:
        raise SubnetProviderError(
            "AgentlessNet DHCP has both systemd and Supervisor service files"
        )
    if systemd_file:
        if not _systemd_available():
            raise SubnetProviderError(
                "saved AgentlessNet DHCP systemd unit exists but systemd is unavailable"
            )
        return "systemd"
    if supervisor_file:
        if not _supervisor_available():
            raise SubnetProviderError(
                "saved AgentlessNet DHCP Supervisor program exists but Supervisor is unavailable"
            )
        return "supervisor"
    if _systemd_available():
        return "systemd"
    if _supervisor_available():
        return "supervisor"
    raise SubnetProviderError(
        "managed node has neither a running systemd manager nor a reachable AgentlessNet Supervisor"
    )


def preflight_dhcp_supervisor(uid: str) -> str:
    """Resolve the DHCP manager before Subnet provisioning changes switch state."""
    return resolve_dhcp_supervisor(uid)


def _ensure_private_directory(path: Path) -> bool:
    changed = False
    try:
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        info = path.lstat()
    except OSError as error:
        raise SubnetProviderError("could not prepare private AgentlessNet DHCP directory") from error
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid():
        raise SubnetProviderError("AgentlessNet DHCP directory has unsafe type or owner")
    if stat.S_IMODE(info.st_mode) != 0o700:
        try:
            os.chmod(path, 0o700)
        except OSError as error:
            raise SubnetProviderError("could not secure AgentlessNet DHCP directory") from error
        changed = True
    return changed


def _atomic_write(path: Path, content: bytes, *, mode: int = 0o600) -> bool:
    _ensure_private_directory(path.parent)
    try:
        info = path.lstat()
    except FileNotFoundError:
        info = None
    except OSError as error:
        raise SubnetProviderError("could not inspect AgentlessNet DHCP file") from error
    if info is not None:
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
            raise SubnetProviderError("AgentlessNet DHCP file has unsafe type or owner")
        try:
            current = path.read_bytes()
        except OSError as error:
            raise SubnetProviderError("could not read AgentlessNet DHCP file") from error
        if current == content:
            if stat.S_IMODE(info.st_mode) != mode:
                try:
                    os.chmod(path, mode)
                except OSError as error:
                    raise SubnetProviderError("could not secure AgentlessNet DHCP file") from error
                return True
            return False
    temporary = None
    try:
        fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
        os.fchmod(fd, mode)
        with os.fdopen(fd, "wb") as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    except OSError as error:
        if temporary is not None:
            try:
                os.unlink(temporary)
            except OSError:
                pass
        raise SubnetProviderError("could not atomically install AgentlessNet DHCP file") from error
    return True


def _ensure_lease_file(paths: dict[str, Path]) -> bytes:
    _ensure_private_directory(paths["lease_dir"])
    lease_path = paths["leases"]
    marker_path = paths["marker"]
    marker_exists = marker_path.exists()
    lease_exists = lease_path.exists()
    if marker_exists:
        try:
            marker_info = marker_path.lstat()
        except OSError as error:
            raise SubnetProviderError("could not inspect AgentlessNet DHCP initialization marker") from error
        if not stat.S_ISREG(marker_info.st_mode) or marker_info.st_uid != os.geteuid():
            raise SubnetProviderError("AgentlessNet DHCP initialization marker has unsafe type or owner")
        if stat.S_IMODE(marker_info.st_mode) != 0o600:
            try:
                os.chmod(marker_path, 0o600)
            except OSError as error:
                raise SubnetProviderError("could not secure AgentlessNet DHCP initialization marker") from error
    if marker_exists and not lease_exists:
        raise SubnetProviderError(
            "initialized AgentlessNet DHCP lease file is missing; restore it before retrying"
        )
    if not lease_exists:
        _atomic_write(lease_path, b"")
    try:
        lease_info = lease_path.lstat()
        if not stat.S_ISREG(lease_info.st_mode) or lease_info.st_uid != os.geteuid():
            raise SubnetProviderError("AgentlessNet DHCP lease file has unsafe type or owner")
        os.chmod(lease_path, 0o600)
        contents = lease_path.read_bytes()
    except SubnetProviderError:
        raise
    except OSError as error:
        raise SubnetProviderError("could not read AgentlessNet DHCP lease file") from error
    _validate_lease_file(contents)
    if not marker_exists:
        _atomic_write(marker_path, b"initialized\n")
    return contents


def _validate_lease_file(contents: bytes) -> None:
    try:
        text = contents.decode("ascii")
    except UnicodeDecodeError as error:
        raise SubnetProviderError(
            "AgentlessNet DHCP lease file is malformed; preserve and repair it before retrying"
        ) from error
    for line in text.splitlines():
        fields = line.split()
        if len(fields) != 5:
            raise SubnetProviderError(
                "AgentlessNet DHCP lease file is malformed; preserve and repair it before retrying"
            )
        try:
            expiry = int(fields[0], 10)
            ipaddress.IPv4Address(fields[2])
        except (ValueError, ipaddress.AddressValueError) as error:
            raise SubnetProviderError(
                "AgentlessNet DHCP lease file is malformed; preserve and repair it before retrying"
            ) from error
        if expiry < 0 or not DHCP_MAC_RE.fullmatch(fields[1]):
            raise SubnetProviderError(
                "AgentlessNet DHCP lease file is malformed; preserve and repair it before retrying"
            )


def _retain_active_subnet_leases(contents: bytes, entries: list[dict[str, Any]]) -> bytes:
    """Keep lease records only while their addresses remain in active DHCP ranges."""
    _validate_lease_file(contents)
    ranges = []
    for entry in entries:
        _validate_subnet(entry)
        ranges.append(
            (
                ipaddress.IPv4Address(entry["dhcp_range_start"]),
                ipaddress.IPv4Address(entry["dhcp_range_end"]),
            )
        )
    retained = []
    for line in contents.splitlines(keepends=True):
        address = ipaddress.IPv4Address(line.split()[2].decode("ascii"))
        if any(start <= address <= end for start, end in ranges):
            retained.append(line)
    return b"".join(retained)


def _render_dhcp_config(
    parent: dict[str, Any], entries: list[dict[str, Any]], paths: dict[str, Path]
) -> bytes:
    _, namespace = _validate_parent(parent)
    del namespace
    lines = [
        "port=0",
        "no-hosts",
        "no-resolv",
        "bind-interfaces",
        "dhcp-authoritative",
        "user=root",
        "log-facility=-",
        "dhcp-option=option:dns-server",
        f"dhcp-leasefile={paths['leases']}",
        f"pid-file={paths['pid']}",
    ]
    for entry in sorted(entries, key=lambda value: value["uid"]):
        _validate_subnet(entry)
        subnet = ipaddress.ip_network(entry["ipv4_cidr"])
        tag = entry["vlan_interface"]
        lines.extend(
            [
                f"interface={tag}",
                f"dhcp-range=set:{tag},{entry['dhcp_range_start']},{entry['dhcp_range_end']},{subnet.netmask},12h",
                f"dhcp-option=tag:{tag},option:router,{entry['gateway_ipv4']}",
            ]
        )
    return ("\n".join(lines) + "\n").encode("ascii")


def _binary(name: str) -> str:
    resolved = shutil.which(name)
    if resolved is None:
        raise SubnetProviderError(f"required network-node binary {name} is unavailable")
    return resolved


def _validated_candidate(path: Path, content: bytes, dnsmasq: str) -> tuple[Path, bool]:
    _ensure_private_directory(path.parent)
    try:
        fd, candidate_name = tempfile.mkstemp(prefix=f".{path.name}.candidate.", dir=path.parent)
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as candidate:
            candidate.write(content)
            candidate.flush()
            os.fsync(candidate.fileno())
        candidate_path = Path(candidate_name)
        _run([dnsmasq, "--test", f"--conf-file={candidate_path}"])
        return candidate_path, True
    except SubnetProviderError:
        if "candidate_path" in locals():
            candidate_path.unlink(missing_ok=True)
        raise SubnetProviderError(
            "dnsmasq rejected the generated AgentlessNet DHCP configuration"
        )
    except OSError as error:
        if "candidate_name" in locals():
            Path(candidate_name).unlink(missing_ok=True)
        raise SubnetProviderError("could not validate AgentlessNet DHCP configuration") from error


def _install_validated_config(path: Path, candidate: Path, content: bytes) -> bool:
    try:
        info = path.lstat()
    except FileNotFoundError:
        info = None
    except OSError as error:
        candidate.unlink(missing_ok=True)
        raise SubnetProviderError("could not inspect AgentlessNet DHCP configuration") from error
    if info is not None:
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
            candidate.unlink(missing_ok=True)
            raise SubnetProviderError("AgentlessNet DHCP configuration has unsafe type or owner")
        try:
            current = path.read_bytes()
        except OSError as error:
            candidate.unlink(missing_ok=True)
            raise SubnetProviderError("could not read AgentlessNet DHCP configuration") from error
    else:
        current = None
    if current == content:
        candidate.unlink(missing_ok=True)
        return False
    try:
        os.replace(candidate, path)
        directory_fd = os.open(path.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    except OSError as error:
        candidate.unlink(missing_ok=True)
        raise SubnetProviderError("could not install AgentlessNet DHCP configuration") from error
    return True


def _managed_file_matches(
    path: Path, content: bytes, *, mode: int | None = None
) -> bool:
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False
    except OSError as error:
        raise SubnetProviderError("could not inspect AgentlessNet DHCP file") from error
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        raise SubnetProviderError("AgentlessNet DHCP file has unsafe type or owner")
    try:
        current = path.read_bytes()
    except OSError as error:
        raise SubnetProviderError("could not read AgentlessNet DHCP file") from error
    return current == content and (mode is None or stat.S_IMODE(info.st_mode) == mode)


def _service_socket_present(namespace: str, ip: str) -> bool:
    ss = _binary("ss")
    result = _run([ip, "netns", "exec", namespace, ss, "-H", "-lun"])
    return re.search(r":67(?:\s|$)", result.stdout) is not None


def _systemd_running(unit: str) -> bool:
    return _run(["systemctl", "is-active", "--quiet", unit], check=False).returncode == 0


def _supervisor_running(program: str) -> bool:
    result = _run(
        ["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "status", program],
        check=False,
    )
    return result.returncode == 0 and " RUNNING " in result.stdout


def _wait_for_supervisor_running(program: str) -> bool:
    """Wait through Supervisor's STARTING state after starting or updating a program."""
    deadline = time.monotonic() + SUPERVISOR_START_TIMEOUT_SECONDS
    while True:
        if _supervisor_running(program):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(SUPERVISOR_POLL_INTERVAL_SECONDS, remaining))


def _stop_dhcp_service(supervisor: str, service_name: str) -> None:
    if supervisor == "systemd":
        _run(["systemctl", "stop", service_name])
        if _systemd_running(service_name):
            raise SubnetProviderError("AgentlessNet DHCP systemd unit did not stop")
        return
    _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "stop", service_name])
    if _supervisor_running(service_name):
        raise SubnetProviderError("AgentlessNet DHCP Supervisor program did not stop")


def _ensure_dhcp_service(
    parent: dict[str, Any],
    entries: list[dict[str, Any]],
    dhcp_supervisor: str,
    paths: dict[str, Path],
    *,
    interface_changed: bool,
) -> bool:
    if dhcp_supervisor not in {"systemd", "supervisor"}:
        raise SubnetProviderError("AgentlessNet DHCP supervisor must be systemd or supervisor")
    _, namespace = _validate_parent(parent)
    dnsmasq = _binary("dnsmasq")
    ip = _binary("ip")
    _binary("ss")
    _ensure_private_directory(paths["config_dir"])
    _ensure_private_directory(AGENTLESS_NET_CONFIG_ROOT)
    _ensure_private_directory(DHCP_CONFIG_ROOT)
    _ensure_private_directory(paths["lease_dir"])
    _ensure_private_directory(AGENTLESS_NET_STATE_ROOT)
    _ensure_private_directory(DHCP_LEASE_ROOT)
    _ensure_private_directory(paths["log_dir"])
    _ensure_private_directory(AGENTLESS_NET_LOG_ROOT)
    _ensure_private_directory(DHCP_RUNTIME_ROOT)
    _ensure_private_directory(AGENTLESS_NET_RUNTIME_ROOT)
    if dhcp_supervisor == "supervisor":
        _ensure_private_directory(SUPERVISOR_PROGRAM_ROOT)

    for entry in entries:
        verify_subnet_interface(namespace, entry)
    desired_config = _render_dhcp_config(parent, entries, paths)
    config_unchanged = _managed_file_matches(paths["config"], desired_config)
    lease_file_exists = _path_present(paths["leases"])
    marker_exists = _path_present(paths["marker"])
    if marker_exists and not lease_file_exists:
        raise SubnetProviderError(
            "initialized AgentlessNet DHCP lease file is missing; restore it before retrying"
        )

    if dhcp_supervisor == "systemd":
        unit = f"agentless-dhcp@{parent['uid']}.service"
        unit_content = (
            "[Unit]\n"
            "Description=AgentlessNet DHCP for VirtualNetwork %i\n"
            "After=network.target\n\n"
            "[Service]\n"
            "Type=simple\n"
            f"ExecStart={ip} netns exec {namespace} {dnsmasq} --keep-in-foreground --conf-file={paths['config']}\n"
            "Restart=on-failure\n"
            "RestartSec=1\n\n"
            "[Install]\n"
            "WantedBy=multi-user.target\n"
        ).encode("ascii")
        active = _systemd_running(unit)
        service_file_unchanged = _managed_file_matches(
            paths["systemd_unit"], unit_content, mode=0o600
        )
    elif dhcp_supervisor == "supervisor":
        program = f"agentless-dhcp-{parent['uid']}"
        program_content = (
            f"[program:{program}]\n"
            f"command={ip} netns exec {namespace} {dnsmasq} --keep-in-foreground --conf-file={paths['config']}\n"
            "autostart=true\n"
            "autorestart=true\n"
            "startsecs=1\n"
            "stopasgroup=true\n"
            "killasgroup=true\n"
            "redirect_stderr=true\n"
            f"stdout_logfile={paths['log']}\n"
            "stdout_logfile_maxbytes=0\n"
        ).encode("ascii")
        active = _supervisor_running(program)
        service_file_unchanged = _managed_file_matches(
            paths["supervisor_program"], program_content, mode=0o600
        )
    else:
        raise SubnetProviderError("AgentlessNet DHCP supervisor must be systemd or supervisor")

    candidate, _ = _validated_candidate(paths["config"], desired_config, dnsmasq)
    if (
        active
        and config_unchanged
        and service_file_unchanged
        and not interface_changed
        and lease_file_exists
    ):
        _install_validated_config(paths["config"], candidate, desired_config)
        _ensure_lease_file(paths)
        if not _service_socket_present(namespace, ip):
            raise SubnetProviderError(
                "AgentlessNet DHCP UDP socket is absent from the VirtualNetwork namespace"
            )
        return False

    try:
        if active:
            service_name = unit if dhcp_supervisor == "systemd" else program
            _stop_dhcp_service(dhcp_supervisor, service_name)

        # dnsmasq keeps this file open while it runs. Read and replace it only
        # after stopping that process, so its final lease flush cannot land on
        # an old inode after stale leases have been pruned.
        lease_bytes = _ensure_lease_file(paths)
        desired_leases = _retain_active_subnet_leases(lease_bytes, entries)
        config_changed = _install_validated_config(
            paths["config"], candidate, desired_config
        )
        lease_changed = (
            _atomic_write(paths["leases"], desired_leases)
            if desired_leases != lease_bytes
            else False
        )

        changed = config_changed or lease_changed or interface_changed
        if dhcp_supervisor == "systemd":
            unit_changed = _atomic_write(paths["systemd_unit"], unit_content)
            changed = changed or unit_changed
            if unit_changed:
                _run(["systemctl", "daemon-reload"])
            if active:
                _run(["systemctl", "start", unit])
            else:
                _run(["systemctl", "enable", "--now", unit])
            changed = True
            if not _systemd_running(unit):
                raise SubnetProviderError("AgentlessNet DHCP systemd unit is not active")
        else:
            program_changed = _atomic_write(paths["supervisor_program"], program_content)
            changed = changed or program_changed
            if program_changed:
                _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "reread"])
                _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "update", program])
            else:
                _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "start", program])
            changed = True
            if not _wait_for_supervisor_running(program):
                raise SubnetProviderError("AgentlessNet DHCP Supervisor program is not running")

        if not _service_socket_present(namespace, ip):
            raise SubnetProviderError(
                "AgentlessNet DHCP UDP socket is absent from the VirtualNetwork namespace"
            )
        return changed
    except Exception:
        candidate.unlink(missing_ok=True)
        raise


def _unlink_owned(path: Path) -> bool:
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False
    except OSError as error:
        raise SubnetProviderError("could not inspect AgentlessNet DHCP file") from error
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        raise SubnetProviderError("AgentlessNet DHCP file has unsafe type or owner")
    try:
        path.unlink()
    except OSError as error:
        raise SubnetProviderError("could not remove AgentlessNet DHCP file") from error
    return True


def _path_present(path: Path) -> bool:
    try:
        path.lstat()
        return True
    except FileNotFoundError:
        return False
    except OSError as error:
        raise SubnetProviderError("could not inspect AgentlessNet DHCP path") from error


def _remove_dhcp_service(
    uid: str, supervisor: str, paths: dict[str, Path]
) -> bool:
    changed = False
    if supervisor == "systemd":
        unit = f"agentless-dhcp@{uid}.service"
        if _systemd_running(unit):
            _run(["systemctl", "stop", unit])
            changed = True
        if _path_present(paths["systemd_unit"]):
            _run(["systemctl", "disable", unit], check=False)
            _unlink_owned(paths["systemd_unit"])
            _run(["systemctl", "daemon-reload"])
            changed = True
    elif supervisor == "supervisor":
        program = f"agentless-dhcp-{uid}"
        status = _run(
            ["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "status", program],
            check=False,
        )
        if status.returncode == 0 and any(
            state in status.stdout for state in (" RUNNING ", " STARTING ", " STOPPING ")
        ):
            _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "stop", program])
            changed = True
        file_removed = _unlink_owned(paths["supervisor_program"])
        if file_removed or status.returncode == 0:
            _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "reread"])
            _run(["supervisorctl", "-c", str(SUPERVISOR_BASE_CONFIG), "update", program])
            changed = changed or file_removed
    else:
        raise SubnetProviderError("AgentlessNet DHCP supervisor must be systemd or supervisor")
    return changed


def cleanup_virtual_network_dhcp(
    uid: str,
    namespace: str,
    dhcp_supervisor: str | None = None,
    *,
    remove_leases: bool,
) -> bool:
    paths = _state_paths(uid)
    if not isinstance(namespace, str) or not re.fullmatch(r"n[0-9a-f]{14}", namespace):
        raise SubnetProviderError("saved VirtualNetwork namespace is invalid")
    systemd_file = _owned_service_file(paths["systemd_unit"])
    supervisor_file = _owned_service_file(paths["supervisor_program"])
    has_service_state = systemd_file or supervisor_file or _path_present(paths["config"])
    if dhcp_supervisor is None and has_service_state:
        dhcp_supervisor = resolve_dhcp_supervisor(uid)
    if dhcp_supervisor is not None and dhcp_supervisor not in {"systemd", "supervisor"}:
        raise SubnetProviderError("AgentlessNet DHCP supervisor must be systemd or supervisor")
    changed = (
        _remove_dhcp_service(uid, dhcp_supervisor, paths)
        if has_service_state and dhcp_supervisor is not None
        else False
    )
    changed = _unlink_owned(paths["config"]) or changed
    if remove_leases:
        changed = _unlink_owned(paths["leases"]) or changed
        changed = _unlink_owned(paths["marker"]) or changed
        for directory in (paths["lease_dir"], paths["config_dir"]):
            try:
                directory.rmdir()
                changed = True
            except FileNotFoundError:
                pass
            except OSError:
                # Unknown files are preserved; never recursively erase node state.
                pass
    return changed


def _reconcile_dhcp(
    parent: dict[str, Any], entries: list[dict[str, Any]], dhcp_supervisor: str | None,
    *, interface_changed: bool,
) -> bool:
    uid, _ = _validate_parent(parent)
    if dhcp_supervisor is None:
        dhcp_supervisor = resolve_dhcp_supervisor(uid)
    paths = _state_paths(uid)
    if not entries:
        return cleanup_virtual_network_dhcp(
            uid,
            parent["namespace_name"],
            dhcp_supervisor,
            remove_leases=False,
        )
    return _ensure_dhcp_service(
        parent,
        entries,
        dhcp_supervisor,
        paths,
        interface_changed=interface_changed,
    )


def ensure_subnet_data_plane(
    parent: dict[str, Any], entries: list[dict[str, Any]], dhcp_supervisor: str | None = None
) -> bool:
    changed = _ensure_subnet_interfaces(parent, entries)
    return _reconcile_dhcp(
        parent, entries, dhcp_supervisor, interface_changed=changed
    ) or changed


def prepare_subnet_delete_data_plane(
    parent: dict[str, Any],
    target: dict[str, Any],
    remaining: list[dict[str, Any]],
    dhcp_supervisor: str | None = None,
) -> bool:
    changed = _ensure_subnet_interfaces(parent, remaining)
    changed = _reconcile_dhcp(
        parent, remaining, dhcp_supervisor, interface_changed=changed
    ) or changed
    _, namespace = _validate_parent(parent)
    return remove_subnet_interface(namespace, target) or changed
