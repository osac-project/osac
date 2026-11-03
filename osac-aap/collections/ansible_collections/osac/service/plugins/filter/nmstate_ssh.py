"""Construct shell-safe local SSH/SCP arguments for nmstate_config."""

from __future__ import annotations

import ipaddress
import re
import shlex
from collections.abc import Mapping
from typing import Any

from ansible.errors import AnsibleFilterError


_SSH_OPTIONS = (
    "-o",
    "StrictHostKeyChecking=no",
    "-o",
    "UserKnownHostsFile=/dev/null",
    "-o",
    "ConnectTimeout=30",
)
_REMOTE_CONFIG_PATH = "/tmp/nmstate-config.yaml"
_REMOTE_APPLY_COMMAND = (
    "nohup sudo nmstatectl apply /tmp/nmstate-config.yaml "
    "> /tmp/nmstate-apply.log 2>&1 &"
)
_DNS_LABEL = re.compile(r"[A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\Z")
_SSH_USER = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.+-]*\$?\Z")


def _single_line(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value or any(
        ord(character) < 0x20 or ord(character) == 0x7F for character in value
    ):
        raise AnsibleFilterError(
            f"nmstate SSH {label} must be a non-empty single-line string"
        )
    return value


def _target_ipv4(value: Any) -> str:
    raw = _single_line(value, "target host")
    try:
        return str(ipaddress.IPv4Address(raw))
    except ipaddress.AddressValueError:
        raise AnsibleFilterError(
            "nmstate SSH target host must be an IPv4 address"
        ) from None


def _ssh_host(value: Any) -> str:
    host = _single_line(value, "bastion host")
    try:
        return str(ipaddress.ip_address(host))
    except ValueError:
        # Keep provider DNS names (including single-label and underscore names)
        # while rejecting option-like values and shell/SSH token syntax.
        if host.startswith("-") or len(host) > 253 or "@" in host:
            raise AnsibleFilterError("nmstate SSH bastion host is invalid") from None
        dns_name = host[:-1] if host.endswith(".") else host
        labels = dns_name.split(".")
        if not dns_name or any(not _DNS_LABEL.fullmatch(label) for label in labels):
            raise AnsibleFilterError("nmstate SSH bastion host is invalid") from None
        return host


def _ssh_user(value: Any, label: str) -> str:
    user = _single_line(value, label)
    if not _SSH_USER.fullmatch(user):
        raise AnsibleFilterError(f"nmstate SSH {label} is invalid")
    return user


def _key_path(value: Any, label: str) -> str:
    path = _single_line(value, label)
    if path.startswith("-"):
        raise AnsibleFilterError(f"nmstate SSH {label} must not look like an option")
    return path


def _connection(value: Any) -> dict[str, str]:
    if not isinstance(value, Mapping):
        raise AnsibleFilterError("nmstate SSH connection settings must be a mapping")

    target_host = _target_ipv4(value.get("host"))
    target_user = _ssh_user(value.get("user"), "target user")
    target_key = _key_path(value.get("key"), "target key path")

    bastion_host = value.get("bastion_host") or ""
    if not bastion_host:
        return {
            "host": target_host,
            "user": target_user,
            "key": target_key,
            "bastion_host": "",
            "bastion_user": "",
            "bastion_key": "",
        }

    return {
        "host": target_host,
        "user": target_user,
        "key": target_key,
        "bastion_host": _ssh_host(bastion_host),
        "bastion_user": _ssh_user(value.get("bastion_user"), "bastion user"),
        "bastion_key": _key_path(value.get("bastion_key"), "bastion key path"),
    }


def _proxy_command(connection: Mapping[str, str]) -> str:
    # OpenSSH expands % tokens in ProxyCommand before invoking its shell.
    # Escape literal percent signs in configured values while preserving the
    # intentional %h/%p destination and port substitutions below.
    bastion_key = connection["bastion_key"].replace("%", "%%")
    bastion_target = f"{connection['bastion_user']}@{connection['bastion_host']}".replace(
        "%", "%%"
    )
    return shlex.join(
        [
            "ssh",
            *_SSH_OPTIONS,
            "-i",
            bastion_key,
            "-W",
            "%h:%p",
            bastion_target,
        ]
    )


def _common_argv(binary: str, connection: Mapping[str, str]) -> list[str]:
    argv = [binary, *_SSH_OPTIONS]
    if connection["bastion_host"]:
        argv.extend(["-o", f"ProxyCommand={_proxy_command(connection)}"])
    argv.extend(["-i", connection["key"]])
    return argv


def nmstate_ssh_argv(
    value: Any,
    operation: str,
    management_interface: str | None = None,
) -> list[str]:
    """Build an argv list for one of the role's fixed remote SSH operations."""
    connection = _connection(value)
    if operation == "disable_rp_filter":
        interface = _single_line(management_interface, "management interface")
        if (
            len(interface.encode("utf-8")) > 15
            or interface in {".", ".."}
            or any(character.isspace() for character in interface)
            or "/" in interface
            or ":" in interface
        ):
            raise AnsibleFilterError("nmstate SSH management interface is invalid")
        remote_command = shlex.join(
            [
                "sudo",
                "sysctl",
                "-qw",
                "net.ipv4.conf.all.rp_filter=0",
                f"net.ipv4.conf.{interface}.rp_filter=0",
            ]
        )
    elif operation == "apply_nmstate" and management_interface is None:
        # This fixed command deliberately uses remote shell redirection and
        # backgrounding because NMState may interrupt the SSH connection.
        remote_command = _REMOTE_APPLY_COMMAND
    else:
        raise AnsibleFilterError("nmstate SSH operation is invalid")

    argv = _common_argv("ssh", connection)
    argv.extend([f"{connection['user']}@{connection['host']}", remote_command])
    return argv


def nmstate_scp_argv(value: Any, source_path: str) -> list[str]:
    """Build an argv list to copy the role's config to a managed agent."""
    connection = _connection(value)
    source = _single_line(source_path, "SCP source path")
    if not source.startswith("/") or any(character in source for character in "*?[]"):
        raise AnsibleFilterError("nmstate SCP source path must be an absolute file path")

    argv = _common_argv("scp", connection)
    argv.extend(
        [
            source,
            f"{connection['user']}@{connection['host']}:{_REMOTE_CONFIG_PATH}",
        ]
    )
    return argv


class FilterModule:
    def filters(self):
        return {
            "nmstate_ssh_argv": nmstate_ssh_argv,
            "nmstate_scp_argv": nmstate_scp_argv,
        }
