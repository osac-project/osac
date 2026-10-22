from __future__ import annotations

import ipaddress
import logging
import os
import re
import shlex
import subprocess
import tempfile

from tests.e2e.core.ssh_probe import probe_connectivity

log = logging.getLogger(__name__)

_HTTP_STATUS_SENTINEL = "__OSAC_HTTP_STATUS__"
_HTTP_STATUS_RE = re.compile(rf"(?m)^{_HTTP_STATUS_SENTINEL}(\d{{3}})$")
_ED25519_HOST_KEY_RE = re.compile(r"^ssh-ed25519 ([A-Za-z0-9+/]+={0,2})(?:\s+.*)?$")


def _combined_output(stdout: str | bytes | None, stderr: str | bytes | None) -> str:
    def as_text(value: str | bytes | None) -> str:
        if isinstance(value, bytes):
            return value.decode(errors="replace")
        return value or ""

    return (as_text(stdout).strip() + "\n" + as_text(stderr).strip()).strip()


def _ssh_user() -> str:
    return os.environ.get("OSAC_VM_SSH_USER", "fedora").strip() or "fedora"


def ssh_guest_unchecked(
    *, vm_namespace: str, vm_name: str, identity_file: str, command: str, timeout: int = 30
) -> tuple[str, int]:
    """Run a command through KubeVirt's SSH port-forward and return output/status."""
    vm_kubeconfig = os.environ.get("OSAC_VM_KUBECONFIG", "").strip()
    if not vm_kubeconfig:
        raise RuntimeError("OSAC_VM_KUBECONFIG must identify the VM cluster kubeconfig")

    target = f"{_ssh_user()}@vm/{vm_name}/{vm_namespace}"
    args = ["virtctl", "ssh", "--identity-file", identity_file, "--command", command, target]
    child_env = os.environ.copy()
    child_env["KUBECONFIG"] = vm_kubeconfig
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False, env=child_env)
    except subprocess.TimeoutExpired as exc:
        output = _combined_output(exc.stdout, exc.stderr)
        raise RuntimeError(f"virtctl SSH to {vm_namespace}/{vm_name} timed out after {timeout}s: {output}") from exc
    except OSError as exc:
        raise RuntimeError(f"failed to start virtctl SSH to {vm_namespace}/{vm_name}: {exc}") from exc

    output = _combined_output(result.stdout, result.stderr)
    return output, result.returncode


def _probe_connectivity(vm_namespace: str, vm_name: str, identity_file: str, command: str, timeout: int = 30) -> bool:
    source = f"{vm_namespace}/{vm_name}"
    return probe_connectivity(
        source,
        command,
        lambda wrapped, call_timeout: ssh_guest_unchecked(
            vm_namespace=vm_namespace,
            vm_name=vm_name,
            identity_file=identity_file,
            command=wrapped,
            timeout=call_timeout,
        ),
        transport="virtctl",
        timeout=timeout,
    )


def _ipv4_target(target_ip: str) -> str:
    try:
        return str(ipaddress.IPv4Address(target_ip))
    except ipaddress.AddressValueError as exc:
        raise ValueError(f"{target_ip!r} is not a valid IPv4 address") from exc


def arping(vm_namespace: str, vm_name: str, identity_file: str, target_ip: str, count: int = 3) -> bool:
    ip = _ipv4_target(target_ip)
    return _probe_connectivity(vm_namespace, vm_name, identity_file, f"arping -c {count} {shlex.quote(ip)}", timeout=30)


def ping(vm_namespace: str, vm_name: str, identity_file: str, target_ip: str, count: int = 3, wait: int = 3) -> bool:
    ip = _ipv4_target(target_ip)
    return _probe_connectivity(
        vm_namespace, vm_name, identity_file, f"ping -c {count} -W {wait} {shlex.quote(ip)}", timeout=30
    )


def curl_status(vm_namespace: str, vm_name: str, identity_file: str, timeout: int = 15) -> int:
    command = (
        f"curl -sS -o /dev/null -w '\\n{_HTTP_STATUS_SENTINEL}%{{http_code}}\\n' "
        f"--connect-timeout {timeout} https://quay.io"
    )
    output, virtctl_rc = ssh_guest_unchecked(
        vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command, timeout=timeout + 30
    )
    if virtctl_rc != 0:
        log.warning("VM guest curl via %s/%s exited %d", vm_namespace, vm_name, virtctl_rc)
        return 0
    matches = _HTTP_STATUS_RE.findall(output)
    if len(matches) != 1:
        log.warning("VM guest curl via %s/%s returned no unique HTTP status", vm_namespace, vm_name)
        return 0
    return int(matches[0])


def guest_host_key(vm_namespace: str, vm_name: str, identity_file: str) -> str:
    """Read and validate the VM's ED25519 host key through trusted virtctl SSH."""
    output, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace,
        vm_name=vm_name,
        identity_file=identity_file,
        command="cat /etc/ssh/ssh_host_ed25519_key.pub",
    )
    if status != 0:
        raise RuntimeError(f"failed to read SSH host key from VM {vm_namespace}/{vm_name}: {output!r}")

    matches = [_ED25519_HOST_KEY_RE.fullmatch(line.strip()) for line in output.splitlines()]
    host_keys = [f"ssh-ed25519 {match.group(1)}" for match in matches if match is not None]
    if len(host_keys) != 1:
        raise RuntimeError(f"VM {vm_namespace}/{vm_name} did not return exactly one ED25519 SSH host key")
    return host_keys[0]


def _validated_ed25519_host_key(expected_host_key: str) -> str:
    lines = [line.strip() for line in expected_host_key.splitlines() if line.strip()]
    if len(lines) != 1:
        raise ValueError("expected_host_key must contain exactly one ED25519 public key")
    match = _ED25519_HOST_KEY_RE.fullmatch(lines[0])
    if match is None:
        raise ValueError("expected_host_key must contain one valid ED25519 public key")
    return f"ssh-ed25519 {match.group(1)}"


def ssh_via_external_ip(
    external_ip: str, identity_file: str, *, expected_host_key: str, command: str = "hostname", timeout: int = 15
) -> str:
    host = _ipv4_target(external_ip)
    host_key = _validated_ed25519_host_key(expected_host_key)
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8") as known_hosts:
        known_hosts.write(f"{host} {host_key}\n")
        known_hosts.flush()
        os.fchmod(known_hosts.fileno(), 0o600)
        args = [
            "ssh",
            "-o",
            f"ConnectTimeout={timeout}",
            "-o",
            "BatchMode=yes",
            "-o",
            "IdentitiesOnly=yes",
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            f"UserKnownHostsFile={known_hosts.name}",
            "-o",
            "GlobalKnownHostsFile=/dev/null",
            "-i",
            identity_file,
            f"{_ssh_user()}@{host}",
            command,
        ]
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout + 10, check=True)
        log.info("VM ExternalIP SSH (%s) completed with ssh_rc=%d", host, result.returncode)
        return result.stdout.strip()
