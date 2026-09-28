"""Transactional state for AgentlessNet VirtualNetworks."""

from __future__ import annotations

import contextlib
import copy
import fcntl
import hashlib
import ipaddress
import json
import os
import re
import tempfile
from pathlib import Path
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_network import (
    configure_uplink,
    delete_uplink,
    ensure_ipv4_forwarding,
    ensure_veth_pair,
    NetworkCommandError,
    run_command,
)

SCHEMA_VERSION = 3
LEGACY_SCHEMA_VERSION = 2
STATE_KEYS = {"schema_version", "virtual_networks"}
VIRTUAL_NETWORK_KEYS = {
    "uid",
    "namespace_name",
    "uplink",
    "transit",
    "external_reachability",
    "default_forward_policy",
}
TRANSIT_KEYS = {"cidr", "namespace_ip", "host_ip", "next_hop", "gateway"}
LEGACY_TRANSIT_KEYS = TRANSIT_KEYS | {"external_interface"}


class StateError(Exception):
    """A requested state transition is invalid."""


class StateCorrupt(StateError):
    """The state file is missing or does not contain a supported generation."""


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


class StateStore:
    def __init__(self, path: str | os.PathLike[str]) -> None:
        self.path = Path(path)
        self.lock_path = Path(f"{self.path}.lock")
        self.backup_path = Path(f"{self.path}.bak")

    @staticmethod
    def _empty_state() -> dict[str, Any]:
        return {"schema_version": SCHEMA_VERSION, "virtual_networks": []}

    @contextlib.contextmanager
    def _locked(self):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        lock_fd = os.open(self.lock_path, os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(lock_fd, fcntl.LOCK_EX)
            yield
        finally:
            fcntl.flock(lock_fd, fcntl.LOCK_UN)
            os.close(lock_fd)

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
            previous = self.path.read_bytes()
        except FileNotFoundError:
            if self.backup_path.exists():
                raise StateCorrupt("state file is missing while a backup exists")
            return self._empty_state(), None

        try:
            state = json.loads(previous, object_pairs_hook=self._reject_duplicate_keys)
        except (json.JSONDecodeError, UnicodeDecodeError) as error:
            raise StateCorrupt(f"malformed state JSON: {error}") from error
        self._validate(state)
        return state, previous

    @staticmethod
    def _validate(state: Any) -> None:
        if not isinstance(state, dict):
            raise StateCorrupt("state root must be an object")
        schema_version = state.get("schema_version")
        if type(schema_version) is not int or schema_version not in (
            LEGACY_SCHEMA_VERSION,
            SCHEMA_VERSION,
        ):
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
            allowed_entry_keys = (
                (VIRTUAL_NETWORK_KEYS,)
                if schema_version == LEGACY_SCHEMA_VERSION
                else (VIRTUAL_NETWORK_KEYS, VIRTUAL_NETWORK_KEYS | {"virtual_network_cidr"})
            )
            if not isinstance(entry, dict) or set(entry) not in allowed_entry_keys:
                raise StateCorrupt("VirtualNetwork state entry has an invalid shape")
            uid = entry["uid"]
            namespace = entry["namespace_name"]
            uplink = entry["uplink"]
            transit = entry["transit"]
            if not isinstance(uid, str) or not uid or uid in seen_uids:
                raise StateCorrupt("duplicate or empty VirtualNetwork UID")
            if not isinstance(namespace, str) or not namespace or namespace in seen_namespaces:
                raise StateCorrupt("duplicate or invalid namespace name")
            digest = hashlib.sha256(uid.encode()).hexdigest()
            if namespace != f"n{digest[:14]}":
                raise StateCorrupt("VirtualNetwork namespace does not match its UID")
            if (
                not isinstance(uplink, dict)
                or set(uplink) != {"namespace_interface", "host_interface"}
                or not all(isinstance(name, str) and name for name in uplink.values())
            ):
                raise StateCorrupt("VirtualNetwork uplink state is invalid")
            if uplink != {
                "namespace_interface": f"v{digest[:11]}n",
                "host_interface": f"v{digest[:11]}h",
            }:
                raise StateCorrupt("VirtualNetwork uplink does not match its UID")
            allowed_transit_keys = (
                LEGACY_TRANSIT_KEYS
                if schema_version == LEGACY_SCHEMA_VERSION
                else TRANSIT_KEYS
            )
            if not isinstance(transit, dict) or set(transit) != allowed_transit_keys:
                raise StateCorrupt("VirtualNetwork transit state is invalid")
            try:
                transit_cidr = ipaddress.ip_network(transit["cidr"], strict=True)
            except (TypeError, ValueError) as error:
                raise StateCorrupt("VirtualNetwork transit CIDR is invalid") from error
            if not isinstance(transit_cidr, ipaddress.IPv4Network) or transit_cidr.prefixlen != 30:
                raise StateCorrupt("VirtualNetwork transit CIDR must be an IPv4 /30")
            if "virtual_network_cidr" in entry:
                try:
                    virtual_network_cidr = ipaddress.ip_network(
                        entry["virtual_network_cidr"], strict=True
                    )
                except (TypeError, ValueError) as error:
                    raise StateCorrupt("VirtualNetwork CIDR is invalid") from error
                if not isinstance(virtual_network_cidr, ipaddress.IPv4Network):
                    raise StateCorrupt("VirtualNetwork CIDR must be IPv4")
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
            if transit["next_hop"] != transit["namespace_ip"].split("/", 1)[0]:
                raise StateCorrupt("VirtualNetwork next hop does not match namespace IP")
            if transit["gateway"] != transit["host_ip"].split("/", 1)[0]:
                raise StateCorrupt("VirtualNetwork gateway does not match host IP")
            if transit["namespace_ip"] != f"{transit_cidr.network_address + 1}/{transit_cidr.prefixlen}":
                raise StateCorrupt("VirtualNetwork namespace IP is invalid")
            if transit["host_ip"] != f"{transit_cidr.network_address + 2}/{transit_cidr.prefixlen}":
                raise StateCorrupt("VirtualNetwork host IP is invalid")
            if "external_interface" in transit and (
                not isinstance(transit["external_interface"], str)
                or not re.fullmatch(
                    r"[A-Za-z0-9_.:-]{1,15}", transit["external_interface"]
                )
            ):
                raise StateCorrupt("VirtualNetwork external interface is invalid")
            if entry["external_reachability"] != {
                "mode": "bgp",
                "route_prefix_length": 32,
            }:
                raise StateCorrupt("VirtualNetwork external reachability is invalid")
            if entry["default_forward_policy"] != "permit_all":
                raise StateCorrupt("VirtualNetwork forwarding policy is invalid")

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

    def _transact(self, update, *, before_write=None, after_write=None):
        with self._locked():
            state, previous = self._read_locked()
            next_state = copy.deepcopy(state)
            migrated = state["schema_version"] != SCHEMA_VERSION
            if migrated:
                next_state["schema_version"] = SCHEMA_VERSION
                for entry in next_state["virtual_networks"]:
                    entry["transit"].pop("external_interface", None)

            result, changed = update(next_state)
            state_changed = changed or migrated
            if state_changed:
                self._validate(next_state)
            if before_write is not None:
                before_write(copy.deepcopy(result))
            if state_changed:
                self._write_locked(next_state, previous)
            effect_result = None
            if after_write is not None:
                effect_result = after_write(copy.deepcopy(result))
            return copy.deepcopy(result), state_changed, effect_result

    def _ensure_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        *,
        after_write=None,
    ) -> tuple[dict[str, Any], bool, Any]:
        if not isinstance(uid, str) or not uid.strip():
            raise StateError("VirtualNetwork UID is required")
        try:
            network = ipaddress.ip_network(virtual_network_cidr, strict=True)
        except (TypeError, ValueError) as error:
            raise StateError(f"invalid VirtualNetwork IPv4 CIDR: {error}") from error
        if not isinstance(network, ipaddress.IPv4Network):
            raise StateError("AgentlessNet VirtualNetworks require IPv4 CIDRs")
        network_cidr = str(network)
        provider_entry_to_delete: dict[str, Any] | None = None

        def allocate_transit(state: dict[str, Any], excluded_uid: str | None = None):
            slot_count = network.num_addresses // 4
            if slot_count < 1:
                raise StateError("VirtualNetwork CIDR is too small for a /30 transit link")
            used_transit = {
                ipaddress.ip_network(entry["transit"]["cidr"])
                for entry in state["virtual_networks"]
                if entry["uid"] != excluded_uid
            }
            seed = int.from_bytes(
                hashlib.sha256(f"{uid}:{network_cidr}".encode()).digest()[:8],
                "big",
            ) % slot_count
            for offset in range(slot_count):
                slot = (seed + offset) % slot_count
                address = network.network_address + (slot * 4)
                candidate = ipaddress.ip_network((address, 30))
                if candidate in used_transit:
                    continue
                return candidate
            raise StateError("no free /30 transit block remains in the VirtualNetwork CIDR")

        def transit_state(transit_network: ipaddress.IPv4Network) -> dict[str, str]:
            namespace_ip = transit_network.network_address + 1
            host_ip = transit_network.network_address + 2
            return {
                "cidr": str(transit_network),
                "namespace_ip": f"{namespace_ip}/{transit_network.prefixlen}",
                "host_ip": f"{host_ip}/{transit_network.prefixlen}",
                "next_hop": str(namespace_ip),
                "gateway": str(host_ip),
            }

        def update(state: dict[str, Any]):
            nonlocal provider_entry_to_delete
            for entry in state["virtual_networks"]:
                if entry["uid"] != uid:
                    continue

                saved_cidr = entry.get("virtual_network_cidr")
                if saved_cidr is not None and saved_cidr != network_cidr:
                    raise StateError("VirtualNetwork CIDR does not match saved state")

                old_transit = ipaddress.ip_network(entry["transit"]["cidr"])
                changed = False
                if not old_transit.subnet_of(network):
                    provider_entry_to_delete = copy.deepcopy(entry)
                    entry["transit"] = transit_state(allocate_transit(state, excluded_uid=uid))
                    changed = True
                if saved_cidr is None:
                    entry["virtual_network_cidr"] = network_cidr
                    changed = True
                if "external_interface" in entry["transit"]:
                    del entry["transit"]["external_interface"]
                    changed = True
                return entry, changed

            transit_network = allocate_transit(state)
            digest = hashlib.sha256(uid.encode()).hexdigest()
            namespace_name = f"n{digest[:14]}"
            namespace_interface = f"v{digest[:11]}n"
            host_interface = f"v{digest[:11]}h"
            entry = {
                "uid": uid,
                "virtual_network_cidr": network_cidr,
                "namespace_name": namespace_name,
                "uplink": {
                    "namespace_interface": namespace_interface,
                    "host_interface": host_interface,
                },
                "transit": transit_state(transit_network),
                "external_reachability": {
                    "mode": "bgp",
                    "route_prefix_length": 32,
                },
                "default_forward_policy": "permit_all",
            }
            state["virtual_networks"].append(entry)
            return entry, True

        def prepare_provider_state(entry):
            if provider_entry_to_delete is not None and after_write is not None:
                delete_virtual_network(provider_entry_to_delete)
            if after_write is not None:
                _assert_transit_route_available(entry)

        return self._transact(
            update,
            before_write=prepare_provider_state,
            after_write=after_write,
        )

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
        return self._ensure_virtual_network(
            uid,
            virtual_network_cidr,
            after_write=reconcile_virtual_network,
        )

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
        def update(state: dict[str, Any]):
            entries = state["virtual_networks"]
            entry = next((item for item in entries if item["uid"] == uid), None)
            if entry is None:
                return None, False
            state["virtual_networks"] = [item for item in entries if item["uid"] != uid]
            return entry, True

        def remove_provider_state(entry):
            if entry is not None:
                delete_virtual_network(entry)

        _, changed, _ = self._transact(update, before_write=remove_provider_state)
        return changed


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
        route_entries = json.loads(routes or "[]")
    except (json.JSONDecodeError, TypeError) as error:
        raise StateError(f"could not inspect network-node IPv4 routes: {error}") from error
    if not isinstance(route_entries, list):
        raise StateError("network-node IPv4 route output is not a list")

    for route in route_entries:
        if not isinstance(route, dict):
            raise StateError("network-node IPv4 route entry is invalid")
        if route.get("dev") == host_interface:
            continue

        destination = route.get("dst", "default")
        if destination in ("default", "0.0.0.0/0"):
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
            while _run(check_rule, check=False).returncode == 0:
                _run(delete_rule)
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


def _remove_host_forwarding_isolation(host_interface: str) -> None:
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
        while _run(check_rule, check=False).returncode == 0:
            _run(delete_rule)


def reconcile_virtual_network(entry: dict[str, Any]) -> bool:
    namespace = entry["namespace_name"]
    namespace_interface = entry["uplink"]["namespace_interface"]
    host_interface = entry["uplink"]["host_interface"]
    transit = entry["transit"]
    try:
        changed = ensure_veth_pair(namespace, namespace_interface, host_interface)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error

    # Keep this namespace isolated even when the node already has forwarding
    # enabled for other workloads. Do not change the host-wide forwarding sysctl.
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
        established_rule.insert(9, "1")
        _run(established_rule)

    _verify_virtual_network(entry)
    return changed


def _verify_virtual_network(entry: dict[str, Any]) -> None:
    namespace = entry["namespace_name"]
    namespace_interface = entry["uplink"]["namespace_interface"]
    host_interface = entry["uplink"]["host_interface"]
    transit = entry["transit"]
    namespaces = _run(["ip", "netns", "list"]).stdout.splitlines()
    if namespace not in {line.split()[0] for line in namespaces if line.split()}:
        raise StateError("VirtualNetwork namespace is not present after reconciliation")
    host_addresses = _run(["ip", "-o", "-4", "address", "show", "dev", host_interface]).stdout
    namespace_addresses = _run(
        ["ip", "netns", "exec", namespace, "ip", "-o", "-4", "address", "show", "dev", namespace_interface]
    ).stdout
    if transit["host_ip"] not in host_addresses or transit["namespace_ip"] not in namespace_addresses:
        raise StateError("VirtualNetwork uplink addresses did not converge")
    route = _run(["ip", "netns", "exec", namespace, "ip", "-4", "route", "show", "default"]).stdout
    if f"via {transit['gateway']}" not in route:
        raise StateError("VirtualNetwork default route did not converge")
    policy = _run(
        ["ip", "netns", "exec", namespace, "iptables", "-w", "-t", "filter", "-S", "FORWARD"]
    ).stdout
    if "-P FORWARD ACCEPT" not in policy:
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


def delete_virtual_network(entry: dict[str, Any]) -> None:
    namespace = entry["namespace_name"]
    host_interface = entry["uplink"]["host_interface"]
    try:
        delete_uplink(namespace, host_interface)
    except NetworkCommandError as error:
        raise StateError(str(error)) from error
    _remove_host_forwarding_isolation(host_interface)
