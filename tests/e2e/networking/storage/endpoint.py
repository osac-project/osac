"""Storage endpoint helpers for the storage-over-SNAT suite.

The storage server is outside the fabric gateway. Tenant workloads reach it
through their VirtualNetwork NATGateway, so the server must log the tenant's
NAT address as the client. These helpers read that evidence from the server;
they never accept a successful mount alone.
"""

from __future__ import annotations

import ipaddress
import re
import shlex
import subprocess
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass

DEFAULT_TIMEOUT = 60
DEFAULT_DELAY = 2

# (output, return code) for a command run on the storage server.
SshRunner = Callable[["StorageEndpoint", str, int], tuple[str, int]]


@dataclass(frozen=True)
class StorageEndpoint:
    """An IP storage endpoint reachable from the lab's NAT path."""

    server: str
    export: str
    ssh_host: str
    log_path: str
    identity: str = ""

    def mount_target(self) -> str:
        return f"{self.server}:{self.export}"


def from_env(env: Mapping[str, str]) -> StorageEndpoint:
    """Build the endpoint from the lab's OSAC_STORAGE_* variables."""
    required = ("OSAC_STORAGE_NFS_SERVER", "OSAC_STORAGE_NFS_EXPORT", "OSAC_STORAGE_SSH_HOST", "OSAC_STORAGE_LOG_PATH")
    missing = [name for name in required if not str(env.get(name, "")).strip()]
    if missing:
        raise RuntimeError(f"Missing storage endpoint environment: {', '.join(missing)}")
    return StorageEndpoint(
        server=str(env["OSAC_STORAGE_NFS_SERVER"]).strip(),
        export=str(env["OSAC_STORAGE_NFS_EXPORT"]).strip(),
        ssh_host=str(env["OSAC_STORAGE_SSH_HOST"]).strip(),
        log_path=str(env["OSAC_STORAGE_LOG_PATH"]).strip(),
        identity=str(env.get("OSAC_STORAGE_SSH_IDENTITY", "")).strip(),
    )


def ssh_argv(endpoint: StorageEndpoint, command: str) -> list[str]:
    argv = [
        "ssh",
        "-o",
        "BatchMode=yes",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-o",
        "ConnectTimeout=10",
    ]
    if endpoint.identity:
        argv.extend(["-i", endpoint.identity])
    argv.extend([endpoint.ssh_host, command])
    return argv


def default_ssh_runner(endpoint: StorageEndpoint, command: str, timeout: int = 30) -> tuple[str, int]:
    completed = subprocess.run(
        ssh_argv(endpoint, command), capture_output=True, text=True, timeout=timeout, check=False
    )
    return (completed.stdout + completed.stderr).strip(), completed.returncode


def assert_log_readable(endpoint: StorageEndpoint, *, run: SshRunner = default_ssh_runner) -> None:
    """Fail before any test if the server's client log cannot be read."""
    output, status = run(endpoint, f"sudo test -r {shlex.quote(endpoint.log_path)}", 30)
    if status != 0:
        raise RuntimeError(f"Cannot read {endpoint.log_path} on {endpoint.ssh_host}: {output!r}")


def log_line_count(endpoint: StorageEndpoint, *, run: SshRunner = default_ssh_runner) -> int:
    output, status = run(endpoint, f"sudo wc -l < {shlex.quote(endpoint.log_path)}", 30)
    if status != 0:
        raise RuntimeError(f"Cannot count {endpoint.log_path} on {endpoint.ssh_host}: {output!r}")
    numbers = re.findall(r"(?m)^\s*(\d+)\s*$", output)
    if not numbers:
        raise RuntimeError(f"Unexpected line count from {endpoint.log_path}: {output!r}")
    return max(int(numbers[-1]), 0)


_IPV4_RE = re.compile(r"(?<![\d.])(\d{1,3}(?:\.\d{1,3}){3})(?!\d)")
_TCPDUMP_ARROW_RE = re.compile(r"\s+>\s+")


def client_addresses(text: str) -> list[ipaddress.IPv4Address]:
    """Return the IPv4 client (source) addresses of a tcpdump-style log, in order.

    tcpdump prints ``198.51.100.25.43210 > 192.168.123.10.2049``, so a plain
    split on ``.`` or ``:`` cannot recover the address; match the dotted quad
    and leave the port. A tcpdump line names its source before ``>``; counting
    every address would let a destination in the expected CIDR satisfy a wait
    even though no client in that CIDR connected. A line without a tcpdump
    direction (a plain server log line) has no known destination, so all of its
    addresses count.
    """
    addresses: list[ipaddress.IPv4Address] = []
    for line in text.splitlines():
        source = _TCPDUMP_ARROW_RE.split(line, maxsplit=1)[0]
        for match in _IPV4_RE.finditer(source):
            try:
                address = ipaddress.IPv4Address(match.group(1))
            except ipaddress.AddressValueError:
                continue
            addresses.append(address)
    return addresses


def mount_command(endpoint: StorageEndpoint, mountpoint: str = "/mnt/osac-storage") -> str:
    """Mount the export as NFSv4.1 with noresvport, so SNAT keeps working.

    ``noresvport`` keeps the client off the privileged source-port range; the
    lab export is also ``insecure`` for the same reason.
    """
    return (
        f"sudo mkdir -p {shlex.quote(mountpoint)}"
        f" && sudo mount -t nfs -o vers=4.1,noresvport"
        f" {shlex.quote(endpoint.mount_target())} {shlex.quote(mountpoint)}"
    )


def timeout_mount_command(endpoint: StorageEndpoint, mountpoint: str = "/mnt/osac-storage", timeout: int = 30) -> str:
    """Try the mount under coreutils ``timeout`` so a missing NAT path is a 124, not a hang."""
    mount = f"sudo mount -t nfs -o vers=4.1,noresvport {shlex.quote(endpoint.mount_target())} {shlex.quote(mountpoint)}"
    return f"sudo mkdir -p {shlex.quote(mountpoint)} && timeout {int(timeout)} sh -c {shlex.quote(mount)}"


def ensure_nfs_client_command() -> str:
    """Install the NFS client only when the guest image lacks mount.nfs."""
    return (
        "command -v mount.nfs >/dev/null"
        " || { command -v dnf >/dev/null && sudo dnf -y --setopt=ip_resolve=4 install nfs-utils; }"
        " || { command -v apt-get >/dev/null && sudo apt-get update"
        " && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y nfs-common; }"
        " || true"
    )


def write_probe_file_command(mountpoint: str, marker: str) -> str:
    path = f"{mountpoint.rstrip('/')}/{marker}"
    return f"printf '%s' {shlex.quote(marker)} | sudo tee {shlex.quote(path)} >/dev/null && cat {shlex.quote(path)}"


def marker_on_server(endpoint: StorageEndpoint, marker: str, *, run: SshRunner = default_ssh_runner) -> str:
    """Read the probe file from the export; a local guest copy alone would not prove the mount."""
    path = shlex.quote(f"{endpoint.export.rstrip('/')}/{marker}")
    last_output = ""
    for command in (f"cat {path}", f"sudo cat {path}"):
        last_output, status = run(endpoint, command, 30)
        if status == 0:
            return last_output
    raise AssertionError(
        f"marker {marker} is not readable on {endpoint.ssh_host} at {endpoint.export}: {last_output!r}"
    )


def expected_network(expected: str) -> ipaddress.IPv4Network:
    if "/" in expected:
        return ipaddress.ip_network(expected, strict=False)
    return ipaddress.ip_network(f"{expected}/32")


def new_log_lines(endpoint: StorageEndpoint, since: int, *, run: SshRunner = default_ssh_runner) -> list[str]:
    """Return log lines after `since`; the caller records the count before the action."""
    total = log_line_count(endpoint, run=run)
    if total <= since:
        return []
    request = max(total - since, 0)
    output, status = run(endpoint, f"sudo tail -n {int(request)} {shlex.quote(endpoint.log_path)}", 30)
    if status != 0:
        raise RuntimeError(f"Cannot read {endpoint.log_path} on {endpoint.ssh_host}: {output!r}")
    return output.splitlines()


def wait_for_client(
    endpoint: StorageEndpoint,
    expected: str,
    *,
    since: int = 0,
    run: SshRunner = default_ssh_runner,
    timeout: int = DEFAULT_TIMEOUT,
    delay: int = DEFAULT_DELAY,
) -> ipaddress.IPv4Address:
    """Wait until the server logs a client inside `expected`, returning that address.

    Only new lines (after `since`) are considered, so an earlier mount by the
    same tenant cannot make a later negative or positive case pass.
    """
    network = expected_network(expected)
    deadline = time.monotonic() + max(timeout, 0)
    last: list[str] = []
    while True:
        for line in new_log_lines(endpoint, since, run=run):
            last.append(line)
            for address in client_addresses(line):
                if address in network:
                    return address
        if time.monotonic() >= deadline:
            detail = last[-1] if last else "no new log line"
            raise TimeoutError(
                f"{endpoint.ssh_host} logged no client in {network} after the mount; last line: {detail}"
            )
        time.sleep(delay)
