"""Transactional state for AgentlessNet VirtualNetworks."""

from __future__ import annotations

import contextlib
import fcntl
import hashlib
import ipaddress
import json
import os
import sqlite3
import stat
import time
import uuid as uuidlib
from pathlib import Path
from typing import Any

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_network import (
    AGENTLESS_NET_HOST_INTERFACE_PREFIX,
    configure_uplink,
    delete_uplink,
    ensure_ipv4_forwarding,
    ensure_veth_pair,
    link_details,
    NetworkCommandError,
    namespace_exists,
    parse_json_object_list,
    run_command,
)

SCHEMA_VERSION = 1
LOCK_WAIT_SECONDS = 60
LOCK_POLL_SECONDS = 0.05
MAX_RULE_DELETIONS = 64
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


def _is_valid_tenant_id(value: Any) -> bool:
    return isinstance(value, str) and bool(value) and value == value.strip()


def _parse_canonical_ipv4_network(value: Any) -> ipaddress.IPv4Network:
    if not isinstance(value, str):
        raise TypeError("VirtualNetwork CIDR must be a string")
    network = ipaddress.ip_network(value, strict=True)
    if not isinstance(network, ipaddress.IPv4Network):
        raise ValueError("AgentlessNet VirtualNetworks require IPv4 CIDRs")
    if str(network) != value:
        raise ValueError("VirtualNetwork CIDR must be canonical")
    return network


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
    def _locked_database(self, *, create: bool):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with _locked_path(self.lock_path):
            connection = self._open_database(create=create)
            try:
                yield connection
            except sqlite3.DatabaseError as error:
                raise StateCorrupt(f"AgentlessNet state database is corrupt: {error}") from error
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

    def _open_database(self, *, create: bool) -> sqlite3.Connection | None:
        if not create and not self.path.exists():
            return None
        flags = os.O_RDWR | getattr(os, "O_NOFOLLOW", 0)
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
            tables = {
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'table'"
                )
            }
            version = connection.execute("PRAGMA user_version").fetchone()[0]
            if not tables and version == 0 and create:
                connection.executescript(
                    """
                    BEGIN IMMEDIATE;
                    CREATE TABLE virtual_networks (
                        uid TEXT PRIMARY KEY,
                        tenant_id TEXT NOT NULL,
                        virtual_network_cidr TEXT NOT NULL,
                        namespace_name TEXT NOT NULL UNIQUE,
                        namespace_interface TEXT NOT NULL UNIQUE,
                        host_interface TEXT NOT NULL UNIQUE,
                        transit_cidr TEXT NOT NULL,
                        transit_start INTEGER NOT NULL UNIQUE,
                        payload TEXT NOT NULL
                    );
                    CREATE TABLE node_capacity (
                        id INTEGER PRIMARY KEY CHECK (id = 1),
                        active_virtual_networks INTEGER NOT NULL
                            CHECK (active_virtual_networks >= 0)
                    );
                    INSERT INTO node_capacity (id, active_virtual_networks)
                        VALUES (1, 0);
                    CREATE TABLE tenant_capacity (
                        tenant_id TEXT PRIMARY KEY,
                        active_virtual_networks INTEGER NOT NULL
                            CHECK (active_virtual_networks > 0)
                    );
                    CREATE TRIGGER virtual_networks_after_insert
                    AFTER INSERT ON virtual_networks
                    BEGIN
                        UPDATE node_capacity
                            SET active_virtual_networks = active_virtual_networks + 1
                            WHERE id = 1;
                        INSERT INTO tenant_capacity (
                            tenant_id, active_virtual_networks
                        ) VALUES (NEW.tenant_id, 1)
                        ON CONFLICT (tenant_id) DO UPDATE SET
                            active_virtual_networks = active_virtual_networks + 1;
                    END;
                    CREATE TRIGGER virtual_networks_after_delete
                    AFTER DELETE ON virtual_networks
                    BEGIN
                        UPDATE node_capacity
                            SET active_virtual_networks = active_virtual_networks - 1
                            WHERE id = 1;
                        DELETE FROM tenant_capacity
                            WHERE tenant_id = OLD.tenant_id
                              AND active_virtual_networks = 1;
                        UPDATE tenant_capacity
                            SET active_virtual_networks = active_virtual_networks - 1
                            WHERE tenant_id = OLD.tenant_id;
                    END;
                    PRAGMA user_version = 1;
                    COMMIT;
                    """
                )
                tables = {"virtual_networks", "node_capacity", "tenant_capacity"}
                version = SCHEMA_VERSION
            if version != SCHEMA_VERSION:
                raise StateCorrupt(f"unsupported state schema: {version!r}")
            if tables != {"virtual_networks", "node_capacity", "tenant_capacity"}:
                raise StateCorrupt("AgentlessNet state database has an invalid schema")
            columns = {
                row[1]
                for row in connection.execute("PRAGMA table_info(virtual_networks)")
            }
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
            triggers = {
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'trigger'"
                )
            }
            if triggers != {
                "virtual_networks_after_insert",
                "virtual_networks_after_delete",
            }:
                raise StateCorrupt("AgentlessNet state database has an invalid schema")
            return connection
        except (sqlite3.DatabaseError, OSError) as error:
            connection.close()
            raise StateCorrupt(f"AgentlessNet state database is corrupt: {error}") from error
        except BaseException:
            connection.close()
            raise

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
        if transit["gateway"] != transit["host_ip"].split("/", 1)[0]:
            raise StateCorrupt("VirtualNetwork gateway does not match host IP")
        if transit["namespace_ip"] != f"{transit_cidr.network_address + 1}/{transit_cidr.prefixlen}":
            raise StateCorrupt("VirtualNetwork namespace IP is invalid")
        if transit["host_ip"] != f"{transit_cidr.network_address}/{transit_cidr.prefixlen}":
            raise StateCorrupt("VirtualNetwork host IP is invalid")

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

    def _snapshot_locked(self, connection: sqlite3.Connection | None):
        if connection is None:
            entries = []
        else:
            rows = connection.execute(
                "SELECT uid FROM virtual_networks ORDER BY uid"
            ).fetchall()
            entries = [self._entry_for_uid(connection, row[0]) for row in rows]
        return {"schema_version": SCHEMA_VERSION, "virtual_networks": entries}

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
        max_per_tenant: int,
        max_per_node: int,
        *,
        reconcile: bool = False,
    ) -> tuple[dict[str, Any], bool, bool]:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        if type(max_per_tenant) is not int or max_per_tenant < 1:
            raise StateError("AgentlessNet per-tenant VirtualNetwork limit must be positive")
        if type(max_per_node) is not int or max_per_node < 1:
            raise StateError("AgentlessNet network-node VirtualNetwork limit must be positive")
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
                        node_capacity = connection.execute(
                            "SELECT active_virtual_networks FROM node_capacity WHERE id = 1"
                        ).fetchone()
                        tenant_capacity = connection.execute(
                            """
                            SELECT active_virtual_networks FROM tenant_capacity
                            WHERE tenant_id = ?
                            """,
                            (tenant_id,),
                        ).fetchone()
                        if node_capacity is None:
                            raise StateCorrupt("AgentlessNet node capacity state is missing")
                        if (tenant_capacity[0] if tenant_capacity else 0) >= max_per_tenant:
                            raise StateError("AgentlessNet tenant VirtualNetwork quota is exhausted")
                        if node_capacity[0] >= max_per_node:
                            raise StateError("AgentlessNet network-node VirtualNetwork capacity is exhausted")

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
                # same UID-to-/31 mapping. No provider command runs under the JSON lock.
                _assert_transit_route_available(entry)
                network_changed = reconcile_virtual_network(
                    entry,
                    firewall_lock_path=self.firewall_lock_path,
                )
            return entry, state_changed, network_changed

    def ensure_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        tenant_id: str,
        max_per_tenant: int,
        max_per_node: int,
    ) -> dict[str, Any]:
        entry, _, _ = self._ensure_virtual_network(
            uid,
            virtual_network_cidr,
            tenant_id,
            max_per_tenant,
            max_per_node,
        )
        return entry

    def ensure_and_reconcile_virtual_network(
        self,
        uid: str,
        virtual_network_cidr: str,
        tenant_id: str,
        max_per_tenant: int,
        max_per_node: int,
    ) -> tuple[dict[str, Any], bool, bool]:
        return self._ensure_virtual_network(
            uid,
            virtual_network_cidr,
            tenant_id,
            max_per_tenant,
            max_per_node,
            reconcile=True,
        )

    def get_virtual_network(self, uid: str, tenant_id: str) -> dict[str, Any] | None:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        with self._locked_database(create=False) as connection:
            if connection is None:
                return None
            entry = self._entry_for_uid(connection, uid)
            self._require_tenant_ownership(entry, tenant_id)
            return entry

    def snapshot(self) -> dict[str, Any]:
        with self._locked_database(create=False) as connection:
            return self._snapshot_locked(connection)

    def delete_and_remove_virtual_network(self, uid: str, tenant_id: str) -> bool:
        self._validate_uid(uid)
        self._validate_tenant_id(tenant_id)
        with self._resource_locked(uid):
            with self._locked_database(create=False) as connection:
                entry = (
                    self._entry_for_uid(connection, uid)
                    if connection is not None
                    else None
                )
                self._require_tenant_ownership(entry, tenant_id)
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

            provider_changed = delete_virtual_network(
                provider_entry,
                firewall_lock_path=self.firewall_lock_path,
                require_alias=entry is None,
                remaining_virtual_network=remaining_virtual_network,
            )
            if entry is None:
                return provider_changed

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


def _remove_repeated_rule(
    check_rule: list[str], delete_rule: list[str], *, error_message: str
) -> bool:
    removed = False
    deletions = 0
    while _run(check_rule, check=False).returncode == 0:
        if deletions >= MAX_RULE_DELETIONS:
            raise StateError(error_message)
        _run(delete_rule)
        deletions += 1
        removed = True
    return removed


def _verify_host_forwarding_isolation() -> None:
    rules = _host_forwarding_rules()
    for direction in ("-i", "-o"):
        expected = _host_forwarding_isolation_rule(direction)
        try:
            index = rules.index(expected)
        except ValueError as error:
            raise StateError("host VirtualNetwork forwarding isolation rule is absent") from error
        if any(not _is_unconditional_forward_drop(rule) for rule in rules[:index]):
            raise StateError("host VirtualNetwork forwarding isolation rule is below an allow rule")


def _ensure_host_forwarding_isolation() -> bool:
    changed = False
    rules = _host_forwarding_rules()
    for direction in ("-i", "-o"):
        expected = _host_forwarding_isolation_rule(direction)
        needs_reorder = expected not in rules or rules.count(expected) > 1
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
                f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+",
                "-j",
                "DROP",
            ]
            delete_rule = check_rule.copy()
            delete_rule[4] = "-D"
            _remove_repeated_rule(
                check_rule,
                delete_rule,
                error_message="too many duplicate host forwarding isolation rules",
            )
            insert_rule = [
                "iptables",
                "-w",
                "-t",
                "filter",
                "-I",
                "FORWARD",
                "1",
                direction,
                f"{AGENTLESS_NET_HOST_INTERFACE_PREFIX}+",
                "-j",
                "DROP",
            ]
            _run(insert_rule)
            changed = True
            rules = [rule for rule in rules if rule != expected]
            rules.insert(0, expected)
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
            _remove_repeated_rule(
                check_rule,
                delete_rule,
                error_message=(
                    "too many duplicate host forwarding isolation rules during deletion"
                ),
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
