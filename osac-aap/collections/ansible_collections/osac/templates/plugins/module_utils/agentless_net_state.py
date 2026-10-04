"""Transactional state for AgentlessNet VirtualNetworks."""

from __future__ import annotations

import contextlib
import copy
import fcntl
import hashlib
import ipaddress
import json
import os
import stat
import tempfile
import time
import uuid as uuidlib
from pathlib import Path
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_network import (
    configure_uplink,
    delete_uplink,
    ensure_ipv4_forwarding,
    ensure_veth_pair,
    link_details,
    NetworkCommandError,
    parse_json_object_list,
    run_command,
)

SCHEMA_VERSION = 1
LOCK_WAIT_SECONDS = 60
LOCK_POLL_SECONDS = 0.05
MAX_RULE_DELETIONS = 64
RESOURCE_LOCK_SHARDS = 256
STATE_KEYS = {"schema_version", "virtual_networks"}
VIRTUAL_NETWORK_KEYS = {
    "uid",
    "virtual_network_cidr",
    "namespace_name",
    "uplink",
    "transit",
}
TRANSIT_KEYS = {"cidr", "namespace_ip", "host_ip", "gateway"}


class StateError(Exception):
    """A requested state transition is invalid."""


class StateCorrupt(StateError):
    """The state file is missing or does not contain a supported generation."""


def _is_canonical_uuid(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        return str(uuidlib.UUID(value)) == value
    except (ValueError, AttributeError):
        return False


def _parse_canonical_ipv4_network(value: Any) -> ipaddress.IPv4Network:
    if not isinstance(value, str):
        raise TypeError("VirtualNetwork CIDR must be a string")
    network = ipaddress.ip_network(value, strict=True)
    if not isinstance(network, ipaddress.IPv4Network):
        raise ValueError("AgentlessNet VirtualNetworks require IPv4 CIDRs")
    if str(network) != value:
        raise ValueError("VirtualNetwork CIDR must be canonical")
    return network


def _fsync_directory(directory: Path) -> None:
    directory_fd = os.open(directory, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)


def _atomic_write(path: Path, payload: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    state_fd, state_tmp_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=path.parent
    )
    try:
        os.fchmod(state_fd, 0o600)
        with os.fdopen(state_fd, "wb", closefd=True) as state_file:
            state_file.write(payload)
            state_file.flush()
            os.fsync(state_file.fileno())
        os.replace(state_tmp_name, path)
        _fsync_directory(path.parent)
    except BaseException:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(state_tmp_name)
        raise


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
        self.backup_path = Path(f"{self.path}.bak")

    @staticmethod
    def _empty_state() -> dict[str, Any]:
        return {"schema_version": SCHEMA_VERSION, "virtual_networks": []}

    @contextlib.contextmanager
    def _locked(self):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with _locked_path(self.lock_path):
            yield

    @contextlib.contextmanager
    def _resource_locked(self, uid: str):
        digest = hashlib.sha256(uid.encode()).digest()
        shard = int.from_bytes(digest[:4], "big") % RESOURCE_LOCK_SHARDS
        resource_lock_path = Path(f"{self.path}.uid-lock-{shard:03d}.lock")
        with _locked_path(resource_lock_path):
            yield

    @staticmethod
    def _reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise StateCorrupt(f"duplicate state key: {key}")
            result[key] = value
        return result

    def _read_locked(self) -> tuple[dict[str, Any], bytes | None]:
        try:
            state_fd = os.open(
                self.path,
                os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
            )
        except FileNotFoundError:
            if self.backup_path.exists():
                raise StateCorrupt("state file is missing while a backup exists")
            return self._empty_state(), None
        except OSError as error:
            raise StateCorrupt(f"could not safely open AgentlessNet state: {error}") from error

        with os.fdopen(state_fd, "rb", closefd=True) as state_file:
            file_stat = os.fstat(state_file.fileno())
            if not stat.S_ISREG(file_stat.st_mode):
                raise StateCorrupt("AgentlessNet state path is not a regular file")
            if file_stat.st_uid != os.geteuid() or file_stat.st_mode & 0o077:
                raise StateCorrupt("AgentlessNet state file has unsafe owner or mode")
            previous = state_file.read()

        try:
            state = json.loads(previous, object_pairs_hook=self._reject_duplicate_keys)
        except (json.JSONDecodeError, UnicodeDecodeError) as error:
            raise StateCorrupt(f"malformed state JSON: {error}") from error
        self._validate(state)
        return state, previous

    def _validate(self, state: Any) -> None:
        if not isinstance(state, dict):
            raise StateCorrupt("state root must be an object")
        schema_version = state.get("schema_version")
        if type(schema_version) is not int or schema_version != SCHEMA_VERSION:
            raise StateCorrupt(f"unsupported state schema: {schema_version!r}")
        if set(state) != STATE_KEYS:
            raise StateCorrupt("state must contain only schema_version and virtual_networks")
        entries = state["virtual_networks"]
        if not isinstance(entries, list):
            raise StateCorrupt("virtual_networks state section must be a list")

        seen_uids: set[str] = set()
        seen_namespaces: set[str] = set()
        seen_interfaces: set[str] = set()
        seen_transit: set[str] = set()
        for entry in entries:
            if not isinstance(entry, dict) or set(entry) != VIRTUAL_NETWORK_KEYS:
                raise StateCorrupt("VirtualNetwork state entry has an invalid shape")
            uid = entry["uid"]
            namespace = entry["namespace_name"]
            uplink = entry["uplink"]
            transit = entry["transit"]
            if not isinstance(uid, str) or uid in seen_uids:
                raise StateCorrupt("duplicate or empty VirtualNetwork UID")
            if not _is_canonical_uuid(uid):
                raise StateCorrupt("VirtualNetwork UID must be a canonical UUID")
            identity = self._identity_entry(uid)
            if not isinstance(namespace, str) or not namespace or namespace in seen_namespaces:
                raise StateCorrupt("duplicate or invalid namespace name")
            if namespace != identity["namespace_name"]:
                raise StateCorrupt("VirtualNetwork namespace does not match its UID")
            if (
                not isinstance(uplink, dict)
                or set(uplink) != {"namespace_interface", "host_interface"}
                or not all(isinstance(name, str) and name for name in uplink.values())
            ):
                raise StateCorrupt("VirtualNetwork uplink state is invalid")
            if uplink != identity["uplink"]:
                raise StateCorrupt("VirtualNetwork uplink does not match its UID")
            if not isinstance(transit, dict) or set(transit) != TRANSIT_KEYS:
                raise StateCorrupt("VirtualNetwork transit state is invalid")
            try:
                if not isinstance(transit["cidr"], str):
                    raise ValueError("not a string")
                transit_cidr = ipaddress.ip_network(transit["cidr"], strict=True)
            except (TypeError, ValueError) as error:
                raise StateCorrupt("VirtualNetwork transit CIDR is invalid") from error
            if (
                not isinstance(transit_cidr, ipaddress.IPv4Network)
                or transit_cidr.prefixlen != 31
                or str(transit_cidr) != transit["cidr"]
            ):
                raise StateCorrupt("VirtualNetwork transit CIDR must be an IPv4 /31")
            try:
                virtual_network_cidr = _parse_canonical_ipv4_network(
                    entry["virtual_network_cidr"]
                )
            except (TypeError, ValueError) as error:
                raise StateCorrupt(f"VirtualNetwork CIDR is invalid: {error}") from error
            if not transit_cidr.subnet_of(virtual_network_cidr):
                raise StateCorrupt("VirtualNetwork transit CIDR is outside its CR CIDR")
            if transit["cidr"] in seen_transit:
                raise StateCorrupt("duplicate VirtualNetwork transit CIDR")
            for key in ("namespace_ip", "host_ip"):
                try:
                    address = ipaddress.ip_interface(transit[key])
                except (TypeError, ValueError) as error:
                    raise StateCorrupt(f"VirtualNetwork transit {key} is invalid") from error
                if address.network != transit_cidr:
                    raise StateCorrupt(f"VirtualNetwork transit {key} is outside its CIDR")
            if transit["gateway"] != transit["host_ip"].split("/", 1)[0]:
                raise StateCorrupt("VirtualNetwork gateway does not match host IP")
            if transit["namespace_ip"] != f"{transit_cidr.network_address + 1}/{transit_cidr.prefixlen}":
                raise StateCorrupt("VirtualNetwork namespace IP is invalid")
            if transit["host_ip"] != f"{transit_cidr.network_address}/{transit_cidr.prefixlen}":
                raise StateCorrupt("VirtualNetwork host IP is invalid")
            seen_uids.add(uid)
            seen_namespaces.add(namespace)
            seen_interfaces.update(uplink.values())
            seen_transit.add(transit["cidr"])
        if len(seen_interfaces) != sum(len(entry["uplink"]) for entry in entries):
            raise StateCorrupt("duplicate VirtualNetwork uplink interface")

    def _write_locked(self, state: dict[str, Any], previous: bytes | None) -> None:
        payload = (json.dumps(state, indent=2, sort_keys=True) + "\n").encode()
        if previous is not None:
            _atomic_write(self.backup_path, previous)
        _atomic_write(self.path, payload)

    @staticmethod
    def _validate_uid(uid: str) -> None:
        if not _is_canonical_uuid(uid):
            raise StateError("VirtualNetwork UID must be a canonical UUID")

    @staticmethod
    def _identity_entry(uid: str) -> dict[str, Any]:
        digest = hashlib.sha256(uid.encode()).hexdigest()
        return {
            "uid": uid,
            "namespace_name": f"n{digest[:14]}",
            "uplink": {
                "namespace_interface": f"v{digest[:11]}n",
                "host_interface": f"v{digest[:11]}h",
            },
        }

    def _ensure_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        *,
        reconcile: bool = False,
    ) -> tuple[dict[str, Any], bool, bool]:
        self._validate_uid(uid)
        try:
            network = _parse_canonical_ipv4_network(virtual_network_cidr)
        except (TypeError, ValueError) as error:
            raise StateError(f"invalid VirtualNetwork IPv4 CIDR: {error}") from error
        network_cidr = str(network)

        with self._resource_locked(uid):
            with self._locked():
                state, previous = self._read_locked()
                entry = next(
                    (item for item in state["virtual_networks"] if item["uid"] == uid),
                    None,
                )
                if entry is not None:
                    if entry["virtual_network_cidr"] != network_cidr:
                        raise StateError("VirtualNetwork CIDR does not match saved state")
                    state_changed = False
                else:
                    identity = self._identity_entry(uid)
                    if any(
                        item["namespace_name"] == identity["namespace_name"]
                        or set(item["uplink"].values()) & set(identity["uplink"].values())
                        for item in state["virtual_networks"]
                    ):
                        raise StateError("VirtualNetwork UID collides with an existing provider identity")

                    slot_count = network.num_addresses // 2
                    if slot_count < 1:
                        raise StateError("VirtualNetwork CIDR is too small for a /31 transit link")
                    used_transit = {
                        ipaddress.ip_network(item["transit"]["cidr"])
                        for item in state["virtual_networks"]
                    }
                    seed = int.from_bytes(
                        hashlib.sha256(f"{uid}:{network_cidr}".encode()).digest()[:8],
                        "big",
                    ) % slot_count
                    transit_network = None
                    for offset in range(slot_count):
                        slot = (seed + offset) % slot_count
                        address = network.network_address + (slot * 2)
                        candidate = ipaddress.ip_network((address, 31))
                        if candidate not in used_transit:
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
                        "virtual_network_cidr": network_cidr,
                        "transit": {
                            "cidr": str(transit_network),
                            "namespace_ip": f"{namespace_ip}/{transit_network.prefixlen}",
                            "host_ip": f"{host_ip}/{transit_network.prefixlen}",
                            "gateway": str(host_ip),
                        },
                    }
                    state["virtual_networks"].append(entry)
                    self._write_locked(state, previous)
                    state_changed = True

            network_changed = False
            if reconcile:
                # Reserve first so a route or provider failure is retryable with the
                # same UID-to-/31 mapping. No provider command runs under the JSON lock.
                _assert_transit_route_available(entry)
                network_changed = reconcile_virtual_network(
                    entry,
                    firewall_lock_path=self.firewall_lock_path,
                )
            return copy.deepcopy(entry), state_changed, network_changed

    def ensure_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
    ) -> dict[str, Any]:
        entry, _, _ = self._ensure_virtual_network(uid, virtual_network_cidr)
        return entry

    def ensure_and_reconcile_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
    ) -> tuple[dict[str, Any], bool, bool]:
        return self._ensure_virtual_network(uid, virtual_network_cidr, reconcile=True)

    def get_virtual_network(self, uid: str) -> dict[str, Any] | None:
        with self._locked():
            state, _ = self._read_locked()
            return next(
                (
                    copy.deepcopy(entry)
                    for entry in state["virtual_networks"]
                    if entry["uid"] == uid
                ),
                None,
            )

    def delete_and_remove_virtual_network(self, uid: str) -> bool:
        self._validate_uid(uid)
        with self._resource_locked(uid):
            with self._locked():
                state, _ = self._read_locked()
                entry = next(
                    (item for item in state["virtual_networks"] if item["uid"] == uid),
                    None,
                )
                provider_entry = copy.deepcopy(entry) if entry is not None else self._identity_entry(uid)

            provider_changed = delete_virtual_network(
                provider_entry,
                firewall_lock_path=self.firewall_lock_path,
                require_alias=entry is None,
            )
            if entry is None:
                return provider_changed

            with self._locked():
                state, previous = self._read_locked()
                current = next(
                    (item for item in state["virtual_networks"] if item["uid"] == uid),
                    None,
                )
                if current != entry:
                    raise StateError("VirtualNetwork state changed while provider cleanup was running")
                state["virtual_networks"] = [
                    item for item in state["virtual_networks"] if item["uid"] != uid
                ]
                self._write_locked(state, previous)
            return True


def _run(command: list[str], check: bool = True):
    try:
        return run_command(command, check=check)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error


def _assert_transit_route_available(entry: dict[str, Any]) -> None:
    transit = ipaddress.ip_network(entry["transit"]["cidr"])
    host_interface = entry["uplink"]["host_interface"]
    routes = _run(["ip", "-j", "-4", "route", "show", "table", "all"]).stdout
    try:
        route_entries = parse_json_object_list(routes, description="network-node IPv4 routes")
    except NetworkCommandError as error:
        raise StateError(f"could not inspect network-node IPv4 routes: {error}") from error

    for route in route_entries:
        if route.get("dev") == host_interface:
            continue

        destination = route.get("dst", "default")
        if destination in ("default", "0.0.0.0/0"):
            # The VN-owned transit /31 intentionally takes precedence over the
            # host's catch-all route. More-specific existing routes still conflict.
            continue
        try:
            route_network = ipaddress.ip_network(destination, strict=False)
        except (TypeError, ValueError):
            source = route.get("prefsrc")
            if not isinstance(source, str):
                continue
            try:
                route_network = ipaddress.ip_network(source, strict=False)
            except ValueError as error:
                raise StateError("network-node IPv4 route has an invalid source address") from error
        if route_network.version == 4 and transit.overlaps(route_network):
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


def _verify_host_forwarding_isolation(host_interface: str) -> None:
    rules = _host_forwarding_rules()
    for direction in ("-i", "-o"):
        expected = f"-A FORWARD {direction} {host_interface} -j DROP"
        try:
            index = rules.index(expected)
        except ValueError as error:
            raise StateError("host VirtualNetwork forwarding isolation rule is absent") from error
        if any(not _is_unconditional_forward_drop(rule) for rule in rules[:index]):
            raise StateError("host VirtualNetwork forwarding isolation rule is below an allow rule")


def _ensure_host_forwarding_isolation(host_interface: str) -> bool:
    changed = False
    rules = _host_forwarding_rules()
    for direction in ("-i", "-o"):
        expected = f"-A FORWARD {direction} {host_interface} -j DROP"
        needs_reorder = expected not in rules
        if not needs_reorder:
            index = rules.index(expected)
            needs_reorder = any(
                not _is_unconditional_forward_drop(rule) for rule in rules[:index]
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
                host_interface,
                "-j",
                "DROP",
            ]
            delete_rule = check_rule.copy()
            delete_rule[4] = "-D"
            deletions = 0
            while _run(check_rule, check=False).returncode == 0:
                if deletions >= MAX_RULE_DELETIONS:
                    raise StateError("too many duplicate host forwarding isolation rules")
                _run(delete_rule)
                deletions += 1
            insert_rule = [
                "iptables",
                "-w",
                "-t",
                "filter",
                "-I",
                "FORWARD",
                "1",
                direction,
                host_interface,
                "-j",
                "DROP",
            ]
            _run(insert_rule)
            changed = True
            rules = [rule for rule in rules if rule != expected]
            rules.insert(0, expected)
    _verify_host_forwarding_isolation(host_interface)
    return changed


def _remove_host_forwarding_isolation(host_interface: str) -> bool:
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
            host_interface,
            "-j",
            "DROP",
        ]
        delete_rule = [
            "iptables",
            "-w",
            "-t",
            "filter",
            "-D",
            "FORWARD",
            direction,
            host_interface,
            "-j",
            "DROP",
        ]
        deletions = 0
        while _run(check_rule, check=False).returncode == 0:
            if deletions >= MAX_RULE_DELETIONS:
                raise StateError("too many duplicate host forwarding isolation rules during deletion")
            _run(delete_rule)
            changed = True
            deletions += 1
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
        changed = ensure_veth_pair(
            namespace,
            namespace_interface,
            host_interface,
            owner_alias=f"osac-vn:{entry['uid']}",
        )
    except NetworkCommandError as error:
        raise StateError(str(error)) from error

    # Keep this namespace isolated even when the node already has forwarding
    # enabled for other workloads. Do not change the host-wide forwarding sysctl.
    with _with_firewall_lock(firewall_lock_path):
        changed = _ensure_host_forwarding_isolation(host_interface) or changed
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
    namespaces = _run(["ip", "netns", "list"]).stdout.splitlines()
    if namespace not in {line.split()[0] for line in namespaces if line.split()}:
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
    _verify_host_forwarding_isolation(host_interface)


def delete_virtual_network(
    entry: dict[str, Any],
    *,
    firewall_lock_path: Path | None = None,
    require_alias: bool = False,
) -> bool:
    namespace = entry["namespace_name"]
    host_interface = entry["uplink"]["host_interface"]
    owner_alias = f"osac-vn:{entry['uid']}"
    try:
        host_link = link_details(None, host_interface)
        if host_link is not None:
            if host_link.get("linkinfo", {}).get("info_kind") != "veth":
                raise StateError("deterministic host uplink exists but is not a veth")
            alias = host_link.get("ifalias", "")
            if alias != owner_alias and (require_alias or alias):
                raise StateError("deterministic host uplink is not owned by this VirtualNetwork UID")
        uplink_changed = delete_uplink(namespace, host_interface)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error
    with _with_firewall_lock(firewall_lock_path):
        firewall_changed = _remove_host_forwarding_isolation(host_interface)
    return uplink_changed or firewall_changed
