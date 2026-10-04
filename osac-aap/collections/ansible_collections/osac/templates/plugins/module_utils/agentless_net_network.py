"""Shared Linux network-namespace and veth operations for AgentlessNet."""

from __future__ import annotations

import ipaddress
import json
import subprocess
from typing import Any


class NetworkCommandError(Exception):
    """A Linux networking command could not complete."""


COMMAND_TIMEOUT_SECONDS = 30
AGENTLESS_NET_HOST_INTERFACE_PREFIX = "osacvn"


def run_command(command: list[str], check: bool = True) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            check=False,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired as error:
        raise NetworkCommandError(
            f"{command[0]} timed out after {COMMAND_TIMEOUT_SECONDS:g} seconds"
        ) from error
    except OSError as error:
        raise NetworkCommandError(f"{command[0]} could not run: {error}") from error
    if check and result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "command failed"
        raise NetworkCommandError(f"{command[0]} failed: {message}")
    return result


def parse_json_object_list(output: str, *, description: str) -> list[dict[str, Any]]:
    try:
        entries = json.loads(output or "[]")
    except (json.JSONDecodeError, TypeError) as error:
        raise NetworkCommandError(f"could not parse {description} JSON: {error}") from error
    if not isinstance(entries, list) or any(not isinstance(item, dict) for item in entries):
        raise NetworkCommandError(f"{description} JSON must be a list of objects")
    return entries


def _json_ip_output(command: list[str], *, description: str) -> list[dict[str, Any]]:
    return parse_json_object_list(run_command(command).stdout, description=description)


def link_details(namespace: str | None, interface: str) -> dict[str, Any] | None:
    command = ["ip", "-j", "-d", "link", "show", "dev", interface]
    if namespace is not None:
        command = ["ip", "netns", "exec", namespace, *command]
    result = run_command(command, check=False)
    if result.returncode != 0:
        return None
    links = parse_json_object_list(result.stdout, description="link details")
    if len(links) != 1 or links[0].get("ifname") != interface:
        raise NetworkCommandError(f"unexpected link details for interface {interface}")
    if not isinstance(links[0].get("flags"), list) or any(
        not isinstance(flag, str) for flag in links[0]["flags"]
    ):
        raise NetworkCommandError(f"interface {interface} has invalid link flags")
    if not isinstance(links[0].get("ifalias", ""), str):
        raise NetworkCommandError(f"interface {interface} has invalid ownership alias")
    if "linkinfo" in links[0] and not isinstance(links[0]["linkinfo"], dict):
        raise NetworkCommandError(f"interface {interface} has invalid link details")
    return links[0]


def _address_present(namespace: str | None, interface: str, expected: str) -> bool:
    command = ["ip", "-j", "-4", "address", "show", "dev", interface]
    if namespace is not None:
        command = ["ip", "netns", "exec", namespace, *command]
    entries = _json_ip_output(command, description="IPv4 address")
    expected_interface = ipaddress.ip_interface(expected)
    if not isinstance(expected_interface, ipaddress.IPv4Interface):
        raise NetworkCommandError("expected uplink address must be IPv4")
    for entry in entries:
        if entry.get("ifname") != interface:
            raise NetworkCommandError(f"unexpected IPv4 address interface for {interface}")
        address_info = entry.get("addr_info")
        if not isinstance(address_info, list):
            raise NetworkCommandError("IPv4 address JSON has no addr_info list")
        for address in address_info:
            if not isinstance(address, dict):
                raise NetworkCommandError("IPv4 address entry is invalid")
            if (
                address.get("family") == "inet"
                and address.get("local") == str(expected_interface.ip)
                and address.get("prefixlen") == expected_interface.network.prefixlen
            ):
                return True
    return False


def _link_is_up(namespace: str | None, interface: str) -> bool:
    details = link_details(namespace, interface)
    if details is None:
        raise NetworkCommandError(f"interface {interface} is absent")
    flags = details.get("flags")
    if not isinstance(flags, list) or any(not isinstance(flag, str) for flag in flags):
        raise NetworkCommandError(f"interface {interface} has invalid link flags")
    return "UP" in flags


def _default_route_present(namespace: str, gateway: str, interface: str) -> bool:
    command = ["ip", "-j", "-4", "route", "show", "default"]
    command = ["ip", "netns", "exec", namespace, *command]
    routes = _json_ip_output(command, description="IPv4 default route")
    return any(
        route.get("dst") == "default"
        and route.get("gateway") == gateway
        and route.get("dev") == interface
        for route in routes
    )


def namespace_exists(namespace: str) -> bool:
    return run_command(
        ["ip", "netns", "exec", namespace, "true"], check=False
    ).returncode == 0


def ensure_namespace(namespace: str) -> bool:
    if namespace_exists(namespace):
        return False
    run_command(["ip", "netns", "add", namespace])
    return True


def ensure_uplink(
    namespace: str,
    namespace_interface: str,
    host_interface: str,
    namespace_ip: str,
    host_ip: str,
    gateway: str,
) -> bool:
    changed = ensure_veth_pair(namespace, namespace_interface, host_interface)
    return configure_uplink(
        namespace, namespace_interface, host_interface, namespace_ip, host_ip, gateway
    ) or changed


def _ensure_host_veth_owner_alias(host_interface: str, owner_alias: str) -> bool:
    details = link_details(None, host_interface)
    if details is None or details.get("linkinfo", {}).get("info_kind") != "veth":
        raise NetworkCommandError("host uplink exists but is not an owned veth")
    existing_alias = details.get("ifalias", "")
    if existing_alias not in ("", owner_alias):
        raise NetworkCommandError("host uplink alias does not match its VirtualNetwork UID")
    if existing_alias == "":
        run_command(["ip", "link", "set", "dev", host_interface, "alias", owner_alias])
        return True
    return False


def ensure_veth_pair(
    namespace: str,
    namespace_interface: str,
    host_interface: str,
    *,
    owner_alias: str | None = None,
) -> bool:
    changed = ensure_namespace(namespace)
    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface], check=False
    )
    namespace_link = run_command(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "-o",
            "link",
            "show",
            "dev",
            namespace_interface,
        ],
        check=False,
    )
    if host_link.returncode == 0 and namespace_link.returncode != 0:
        if owner_alias is not None:
            _ensure_host_veth_owner_alias(host_interface, owner_alias)
        run_command(["ip", "link", "delete", "dev", host_interface])
        host_link = run_command(
            ["ip", "-o", "link", "show", "dev", host_interface], check=False
        )
        changed = True
    if host_link.returncode != 0 and namespace_link.returncode == 0:
        raise NetworkCommandError("namespace uplink exists without its host peer")
    if host_link.returncode != 0:
        run_command(
            [
                "ip",
                "link",
                "add",
                host_interface,
                "type",
                "veth",
                "peer",
                "name",
                namespace_interface,
            ]
        )
        run_command(["ip", "link", "set", namespace_interface, "netns", namespace])
        if owner_alias is not None:
            run_command(["ip", "link", "set", "dev", host_interface, "alias", owner_alias])
        changed = True
    elif owner_alias is not None:
        changed = _ensure_host_veth_owner_alias(host_interface, owner_alias) or changed
    return changed


def configure_uplink(
    namespace: str,
    namespace_interface: str,
    host_interface: str,
    namespace_ip: str,
    host_ip: str,
    gateway: str,
) -> bool:
    changed = False
    if (
        not _address_present(None, host_interface, host_ip)
        or not _address_present(namespace, namespace_interface, namespace_ip)
        or not _link_is_up(None, host_interface)
        or not _link_is_up(namespace, namespace_interface)
    ):
        changed = True

    run_command(["ip", "address", "replace", host_ip, "dev", host_interface])
    run_command(["ip", "link", "set", "dev", host_interface, "up"])
    run_command(["ip", "netns", "exec", namespace, "ip", "link", "set", "dev", "lo", "up"])
    run_command(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "address",
            "replace",
            namespace_ip,
            "dev",
            namespace_interface,
        ]
    )
    run_command(
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

    if not _default_route_present(namespace, gateway, namespace_interface):
        changed = True
    run_command(
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
            gateway,
            "dev",
            namespace_interface,
        ]
    )
    return changed


def ensure_ipv4_forwarding(namespace: str) -> bool:
    namespace_forwarding = run_command(
        ["ip", "netns", "exec", namespace, "sysctl", "-n", "net.ipv4.ip_forward"]
    ).stdout.strip()
    if namespace_forwarding == "1":
        return False
    run_command(
        ["ip", "netns", "exec", namespace, "sysctl", "-w", "net.ipv4.ip_forward=1"]
    )
    return True


def delete_uplink(namespace: str, host_interface: str) -> bool:
    changed = False
    if namespace_exists(namespace):
        run_command(["ip", "netns", "delete", namespace])
        changed = True

    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface], check=False
    )
    if host_link.returncode == 0:
        run_command(["ip", "link", "delete", "dev", host_interface])
        changed = True

    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface], check=False
    )
    if namespace_exists(namespace) or host_link.returncode == 0:
        raise NetworkCommandError("network namespace or host uplink remains after deletion")
    return changed
