"""Shared Linux network-namespace and veth operations for AgentlessNet."""

from __future__ import annotations

import subprocess


class NetworkCommandError(Exception):
    """A Linux networking command could not complete."""


def run_command(command: list[str], check: bool = True) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=False)
    except OSError as error:
        raise NetworkCommandError(f"{command[0]} could not run: {error}") from error
    if check and result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "command failed"
        raise NetworkCommandError(f"{command[0]} failed: {message}")
    return result


def ensure_namespace(namespace: str) -> bool:
    namespaces = run_command(["ip", "netns", "list"]).stdout.splitlines()
    if namespace in {line.split()[0] for line in namespaces if line.split()}:
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


def ensure_veth_pair(
    namespace: str, namespace_interface: str, host_interface: str
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
        changed = True
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
    host_addresses = run_command(
        ["ip", "-o", "-4", "address", "show", "dev", host_interface]
    ).stdout
    namespace_addresses = run_command(
        [
            "ip",
            "netns",
            "exec",
            namespace,
            "ip",
            "-o",
            "-4",
            "address",
            "show",
            "dev",
            namespace_interface,
        ]
    ).stdout
    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface]
    ).stdout
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
        ]
    ).stdout
    if (
        host_ip not in host_addresses
        or namespace_ip not in namespace_addresses
        or "UP" not in host_link
        or "UP" not in namespace_link
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

    route = run_command(
        ["ip", "netns", "exec", namespace, "ip", "-4", "route", "show", "default"]
    ).stdout
    if f"via {gateway}" not in route:
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
    namespaces = run_command(["ip", "netns", "list"]).stdout.splitlines()
    if namespace in {line.split()[0] for line in namespaces if line.split()}:
        run_command(["ip", "netns", "delete", namespace])
        changed = True

    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface], check=False
    )
    if host_link.returncode == 0:
        run_command(["ip", "link", "delete", "dev", host_interface])
        changed = True

    namespaces = run_command(["ip", "netns", "list"]).stdout.splitlines()
    host_link = run_command(
        ["ip", "-o", "link", "show", "dev", host_interface], check=False
    )
    if namespace in {line.split()[0] for line in namespaces if line.split()} or host_link.returncode == 0:
        raise NetworkCommandError("network namespace or host uplink remains after deletion")
    return changed
