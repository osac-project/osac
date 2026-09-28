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
import subprocess
import tempfile
from pathlib import Path
from typing import Any

SCHEMA_VERSION = 2
STATE_KEYS = {"schema_version", "virtual_networks"}
VIRTUAL_NETWORK_KEYS = {
    "uid",
    "namespace_name",
    "uplink",
    "transit",
    "external_reachability",
    "default_forward_policy",
}


class StateError(Exception):
    """A requested state transition is invalid."""


class StateCorrupt(StateError):
    """The state file is missing or does not contain a supported generation."""


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
        if type(state.get("schema_version")) is not int or state["schema_version"] != SCHEMA_VERSION:
            raise StateCorrupt(f"unsupported state schema: {state.get('schema_version')!r}")
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
            if not isinstance(transit, dict) or set(transit) != {
                "cidr",
                "namespace_ip",
                "host_ip",
                "next_hop",
                "gateway",
                "external_interface",
            }:
                raise StateCorrupt("VirtualNetwork transit state is invalid")
            try:
                transit_cidr = ipaddress.ip_network(transit["cidr"], strict=True)
            except (TypeError, ValueError) as error:
                raise StateCorrupt("VirtualNetwork transit CIDR is invalid") from error
            if not isinstance(transit_cidr, ipaddress.IPv4Network) or transit_cidr.prefixlen != 30:
                raise StateCorrupt("VirtualNetwork transit CIDR must be an IPv4 /30")
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
            if not isinstance(transit["external_interface"], str) or not re.fullmatch(
                r"[A-Za-z0-9_.:-]{1,15}", transit["external_interface"]
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

    def _fsync_directory(self) -> None:
        directory_fd = os.open(self.path.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)

    def _write_locked(self, state: dict[str, Any], previous: bytes | None) -> None:
        payload = (json.dumps(state, indent=2, sort_keys=True) + "\n").encode()
        self.path.parent.mkdir(parents=True, exist_ok=True)

        if previous is not None:
            backup_fd, backup_tmp_name = tempfile.mkstemp(
                prefix=f".{self.backup_path.name}.", dir=self.path.parent
            )
            try:
                os.fchmod(backup_fd, 0o600)
                with os.fdopen(backup_fd, "wb", closefd=True) as backup_file:
                    backup_file.write(previous)
                    backup_file.flush()
                    os.fsync(backup_file.fileno())
                os.replace(backup_tmp_name, self.backup_path)
                self._fsync_directory()
            except BaseException:
                with contextlib.suppress(FileNotFoundError):
                    os.unlink(backup_tmp_name)
                raise

        state_fd, state_tmp_name = tempfile.mkstemp(
            prefix=f".{self.path.name}.", dir=self.path.parent
        )
        try:
            os.fchmod(state_fd, 0o600)
            with os.fdopen(state_fd, "wb", closefd=True) as state_file:
                state_file.write(payload)
                state_file.flush()
                os.fsync(state_file.fileno())
            os.replace(state_tmp_name, self.path)
            self._fsync_directory()
        except BaseException:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(state_tmp_name)
            raise

    def _transact(self, update, *, before_write=None, after_write=None):
        with self._locked():
            state, previous = self._read_locked()
            next_state = copy.deepcopy(state)
            result, changed = update(next_state)
            if changed:
                self._validate(next_state)
            if before_write is not None:
                before_write(copy.deepcopy(result))
            if changed:
                self._write_locked(next_state, previous)
            effect_result = None
            if after_write is not None:
                effect_result = after_write(copy.deepcopy(result))
            return copy.deepcopy(result), changed, effect_result

    def _ensure_virtual_network(
        self,
        uid: str,
        transit_cidr_pool: str,
        external_interface: str,
        virtual_network_cidr: str,
        *,
        after_write=None,
    ) -> tuple[dict[str, Any], bool, Any]:
        if not isinstance(uid, str) or not uid.strip():
            raise StateError("VirtualNetwork UID is required")

        def update(state: dict[str, Any]):
            for entry in state["virtual_networks"]:
                if entry["uid"] == uid:
                    return entry, False

            if (
                not isinstance(external_interface, str)
                or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,15}", external_interface)
            ):
                raise StateError("external interface name is invalid")
            try:
                pool = ipaddress.ip_network(transit_cidr_pool, strict=True)
                network = ipaddress.ip_network(virtual_network_cidr, strict=True)
            except (TypeError, ValueError) as error:
                raise StateError(f"invalid IPv4 CIDR: {error}") from error
            if not isinstance(pool, ipaddress.IPv4Network) or not isinstance(
                network, ipaddress.IPv4Network
            ):
                raise StateError("AgentlessNet VirtualNetworks require IPv4 CIDRs")
            if pool.prefixlen > 30:
                raise StateError("transit CIDR pool must contain at least one /30")
            if pool.overlaps(network):
                raise StateError("transit pool overlaps the VirtualNetwork CIDR")

            digest = hashlib.sha256(uid.encode()).hexdigest()
            namespace_name = f"n{digest[:14]}"
            namespace_interface = f"v{digest[:11]}n"
            host_interface = f"v{digest[:11]}h"
            used = [
                ipaddress.ip_network(entry["transit"]["cidr"])
                for entry in state["virtual_networks"]
            ]
            transit_network = next(
                (
                    candidate
                    for candidate in pool.subnets(new_prefix=30)
                    if not any(candidate.overlaps(existing) for existing in used)
                    and not candidate.overlaps(network)
                ),
                None,
            )
            if transit_network is None:
                raise StateError("transit CIDR pool is exhausted")

            namespace_ip = transit_network.network_address + 1
            host_ip = transit_network.network_address + 2
            entry = {
                "uid": uid,
                "namespace_name": namespace_name,
                "uplink": {
                    "namespace_interface": namespace_interface,
                    "host_interface": host_interface,
                },
                "transit": {
                    "cidr": str(transit_network),
                    "namespace_ip": f"{namespace_ip}/{transit_network.prefixlen}",
                    "host_ip": f"{host_ip}/{transit_network.prefixlen}",
                    "next_hop": str(namespace_ip),
                    "gateway": str(host_ip),
                    "external_interface": external_interface,
                },
                "external_reachability": {
                    "mode": "bgp",
                    "route_prefix_length": 32,
                },
                "default_forward_policy": "permit_all",
            }
            state["virtual_networks"].append(entry)
            return entry, True

        return self._transact(update, after_write=after_write)

    def ensure_virtual_network(
        self,
        uid: str,
        transit_cidr_pool: str,
        external_interface: str,
        virtual_network_cidr: str,
    ) -> dict[str, Any]:
        entry, _, _ = self._ensure_virtual_network(
            uid, transit_cidr_pool, external_interface, virtual_network_cidr
        )
        return entry

    def ensure_and_reconcile_virtual_network(
        self,
        uid: str,
        transit_cidr_pool: str,
        external_interface: str,
        virtual_network_cidr: str,
    ) -> tuple[dict[str, Any], bool, bool]:
        return self._ensure_virtual_network(
            uid,
            transit_cidr_pool,
            external_interface,
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


def _run(command: list[str], check: bool = True) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=False)
    except OSError as error:
        raise StateError(f"{command[0]} could not run: {error}") from error
    if check and result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "command failed"
        raise StateError(f"{command[0]} failed: {message}")
    return result


def reconcile_virtual_network(entry: dict[str, Any]) -> bool:
    namespace = entry["namespace_name"]
    namespace_interface = entry["uplink"]["namespace_interface"]
    host_interface = entry["uplink"]["host_interface"]
    transit = entry["transit"]
    changed = False

    namespace_list = _run(["ip", "netns", "list"]).stdout.splitlines()
    namespace_names = {line.split()[0] for line in namespace_list if line.split()}
    if namespace not in namespace_names:
        _run(["ip", "netns", "add", namespace])
        changed = True

    host_link = _run(["ip", "-o", "link", "show", "dev", host_interface], check=False)
    namespace_link = _run(
        ["ip", "netns", "exec", namespace, "ip", "-o", "link", "show", "dev", namespace_interface],
        check=False,
    )
    if host_link.returncode == 0 and namespace_link.returncode != 0:
        _run(["ip", "link", "delete", "dev", host_interface])
        host_link = _run(["ip", "-o", "link", "show", "dev", host_interface], check=False)
        changed = True
    if host_link.returncode != 0 and namespace_link.returncode == 0:
        raise StateError("namespace uplink exists without its host peer")
    if host_link.returncode != 0:
        _run(["ip", "link", "add", host_interface, "type", "veth", "peer", "name", namespace_interface])
        _run(["ip", "link", "set", namespace_interface, "netns", namespace])
        changed = True

    host_address = transit["host_ip"]
    namespace_address = transit["namespace_ip"]
    host_addresses = _run(["ip", "-o", "-4", "address", "show", "dev", host_interface]).stdout
    namespace_addresses = _run(
        ["ip", "netns", "exec", namespace, "ip", "-o", "-4", "address", "show", "dev", namespace_interface]
    ).stdout
    host_link = _run(["ip", "-o", "link", "show", "dev", host_interface]).stdout
    namespace_link = _run(
        ["ip", "netns", "exec", namespace, "ip", "-o", "link", "show", "dev", namespace_interface]
    ).stdout
    if (
        host_address not in host_addresses
        or namespace_address not in namespace_addresses
        or "UP" not in host_link
        or "UP" not in namespace_link
    ):
        changed = True
    _run(["ip", "address", "replace", host_address, "dev", host_interface])
    _run(["ip", "link", "set", "dev", host_interface, "up"])
    _run(["ip", "netns", "exec", namespace, "ip", "link", "set", "dev", "lo", "up"])
    _run(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "address",
            "replace",
            namespace_address,
            "dev",
            namespace_interface,
        ]
    )
    _run(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "link",
            "set",
            "dev",
            namespace_interface,
            "up",
        ]
    )

    route = _run(["ip", "netns", "exec", namespace, "ip", "-4", "route", "show", "default"]).stdout
    if f"via {transit['gateway']}" not in route:
        changed = True
    _run(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "route",
            "replace",
            "default",
            "via",
            transit["gateway"],
            "dev",
            namespace_interface,
        ]
    )

    host_forwarding = _run(["sysctl", "-n", "net.ipv4.ip_forward"]).stdout.strip()
    namespace_forwarding = _run(
        ["ip", "netns", "exec", namespace, "sysctl", "-n", "net.ipv4.ip_forward"]
    ).stdout.strip()
    if host_forwarding != "1" or namespace_forwarding != "1":
        changed = True
    _run(["sysctl", "-w", "net.ipv4.ip_forward=1"])
    _run(["ip", "netns", "exec", namespace, "sysctl", "-w", "net.ipv4.ip_forward=1"])

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


def delete_virtual_network(entry: dict[str, Any]) -> None:
    namespace = entry["namespace_name"]
    host_interface = entry["uplink"]["host_interface"]
    namespaces = _run(["ip", "netns", "list"]).stdout.splitlines()
    if namespace in {line.split()[0] for line in namespaces if line.split()}:
        _run(["ip", "netns", "delete", namespace])

    host_link = _run(["ip", "-o", "link", "show", "dev", host_interface], check=False)
    if host_link.returncode == 0:
        _run(["ip", "link", "delete", "dev", host_interface])

    namespaces = _run(["ip", "netns", "list"]).stdout.splitlines()
    host_link = _run(["ip", "-o", "link", "show", "dev", host_interface], check=False)
    if namespace in {line.split()[0] for line in namespaces if line.split()} or host_link.returncode == 0:
        raise StateError("VirtualNetwork namespace or uplink remains after deletion")
