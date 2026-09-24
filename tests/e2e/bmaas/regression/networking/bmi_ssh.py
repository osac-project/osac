from __future__ import annotations

import json
import logging
import re
import shlex
import subprocess
from collections.abc import Mapping

log = logging.getLogger(__name__)

_SSH_OPTS = [
    "-o",
    "ConnectTimeout=10",
    "-o",
    "StrictHostKeyChecking=no",
    "-o",
    "UserKnownHostsFile=/dev/null",
    "-i",
    "/root/.ssh/id_rsa",
]
_PROBE_EXIT_SENTINEL = "__OSAC_PROBE_EXIT_STATUS__"
_PROBE_EXIT_RE = re.compile(rf"(?m)^{_PROBE_EXIT_SENTINEL}=(\d+)$")


def _as_text(value: str | bytes | None) -> str:
    if isinstance(value, bytes):
        return value.decode(errors="replace")
    return value or ""


def _combined_output(stdout: str | bytes | None, stderr: str | bytes | None) -> str:
    return (_as_text(stdout).strip() + "\n" + _as_text(stderr).strip()).strip()


def _timeout_output(exc: subprocess.TimeoutExpired, timeout: int) -> str:
    output = _combined_output(exc.stdout, exc.stderr)
    return f"{output}\nssh timed out after {timeout}s".strip()


def _log_command(ssh_host: str, command: str, output: str, rc: int) -> None:
    log.info("BMI command (%s): %s; ssh_rc=%d; output=%r", ssh_host, command, rc, output)


def parse_ssh_hosts(raw: str) -> dict[str, str]:
    try:
        parsed: object = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("OSAC_BMH_SSH_HOSTS must be valid JSON") from exc

    if not isinstance(parsed, dict):
        raise RuntimeError("OSAC_BMH_SSH_HOSTS must be a JSON object")

    hosts: dict[str, str] = {}
    for bmh_name, ssh_host in parsed.items():
        if not isinstance(bmh_name, str) or not bmh_name.strip() or not isinstance(ssh_host, str) or not ssh_host:
            raise RuntimeError("OSAC_BMH_SSH_HOSTS must map non-empty BMH names to SSH hosts")
        hosts[bmh_name] = ssh_host
    return hosts


def get_ssh_host(bmh_name: str, hosts: Mapping[str, str]) -> str:
    try:
        return hosts[bmh_name]
    except KeyError as exc:
        raise RuntimeError(f"No SSH host configured for BMH {bmh_name}") from exc


def ssh_bmi(ssh_host: str, command: str, timeout: int = 30) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(
            ["ssh", *_SSH_OPTS, f"fedora@{ssh_host}", command],
            capture_output=True,
            text=True,
            timeout=timeout,
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        _log_command(ssh_host, command, _combined_output(exc.stdout or "", exc.stderr or ""), exc.returncode)
        raise
    except subprocess.TimeoutExpired as exc:
        _log_command(ssh_host, command, _timeout_output(exc, timeout), 255)
        raise
    _log_command(ssh_host, command, _combined_output(result.stdout, result.stderr), result.returncode)
    return result


def ssh_bmi_unchecked(ssh_host: str, command: str, timeout: int = 30) -> tuple[str, int]:
    try:
        result = subprocess.run(
            ["ssh", *_SSH_OPTS, f"fedora@{ssh_host}", command],
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        output = _timeout_output(exc, timeout)
        _log_command(ssh_host, command, output, 255)
        return output, 255
    output = _combined_output(result.stdout, result.stderr)
    _log_command(ssh_host, command, output, result.returncode)
    return output, result.returncode


def _probe_connectivity(ssh_host: str, command: str, timeout: int = 30) -> bool:
    wrapped_command = (
        f'{command}; probe_rc=$?; printf \'\\n{_PROBE_EXIT_SENTINEL}=%s\\n\' "$probe_rc"; exit "$probe_rc"'
    )
    output, ssh_rc = ssh_bmi_unchecked(ssh_host, wrapped_command, timeout=timeout)
    matches = _PROBE_EXIT_RE.findall(output)
    if len(matches) != 1:
        raise RuntimeError(
            f"SSH connectivity probe from {ssh_host} did not complete (ssh_rc={ssh_rc}, output={output!r})"
        )

    probe_rc = int(matches[0])
    if ssh_rc != probe_rc:
        raise RuntimeError(
            f"SSH connectivity probe from {ssh_host} reported command exit {probe_rc}, "
            f"but SSH exited {ssh_rc} (output={output!r})"
        )
    if probe_rc not in (0, 1):
        raise RuntimeError(
            f"SSH connectivity probe from {ssh_host} failed with unexpected command exit {probe_rc} (output={output!r})"
        )
    return probe_rc == 0


def arping(ssh_host: str, target_ip: str, count: int = 3) -> bool:
    return _probe_connectivity(ssh_host, f"arping -c {count} {shlex.quote(target_ip)}", timeout=30)


def ping(ssh_host: str, target_ip: str, count: int = 3, wait: int = 3) -> bool:
    return _probe_connectivity(ssh_host, f"ping -c {count} -W {wait} {shlex.quote(target_ip)}", timeout=30)


def curl_status(ssh_host: str, url: str, timeout: int = 15) -> int:
    output, _ = ssh_bmi_unchecked(
        ssh_host, f"curl -s -o /dev/null -w '%{{http_code}}' --connect-timeout {timeout} {url}", timeout=timeout + 30
    )
    for line in output.strip().splitlines():
        line = line.strip()
        if line.isdigit() and len(line) == 3:
            return int(line)
    log.warning("curl_status: no HTTP status code found in output, returning 0")
    return 0


def ssh_via_external_ip(external_ip: str, command: str = "hostname", timeout: int = 15) -> str:
    args = [
        "ssh",
        "-o",
        f"ConnectTimeout={timeout}",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-i",
        "/root/.ssh/id_rsa",
        f"fedora@{external_ip}",
        command,
    ]
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout + 10, check=True)
    except subprocess.CalledProcessError as exc:
        _log_command(external_ip, command, _combined_output(exc.stdout or "", exc.stderr or ""), exc.returncode)
        raise
    except subprocess.TimeoutExpired as exc:
        _log_command(external_ip, command, _timeout_output(exc, timeout + 10), 255)
        raise
    _log_command(external_ip, command, _combined_output(result.stdout, result.stderr), result.returncode)
    return result.stdout.strip()
