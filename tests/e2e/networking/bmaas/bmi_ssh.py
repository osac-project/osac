from __future__ import annotations

import json
import logging
import shlex
import subprocess
from collections.abc import Mapping, Sequence

from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.runner import poll_until
from tests.e2e.core.ssh_probe import probe_connectivity
from tests.e2e.core.wait_kinds import WaitResourceKind

log = logging.getLogger(__name__)

_SSH_OPTS = [
    "-o",
    "ConnectTimeout=10",
    # Key only: a rejected key fails at once instead of waiting on a password prompt.
    "-o",
    "BatchMode=yes",
    "-o",
    "StrictHostKeyChecking=no",
    "-o",
    "UserKnownHostsFile=/dev/null",
    "-i",
    "/root/.ssh/id_rsa",
]


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
    return probe_connectivity(
        ssh_host,
        command,
        lambda wrapped, call_timeout: ssh_bmi_unchecked(ssh_host, wrapped, timeout=call_timeout),
        transport="SSH",
        timeout=timeout,
    )


# A host that is still booting: sshd is not listening yet or the network is not up.
_NOT_READY_MARKERS = (
    "connection refused",
    "connection timed out",
    "ssh timed out",
    "no route to host",
    "network is unreachable",
    "connection reset",
    "connection closed",
    "kex_exchange_identification",
    "banner exchange",
)
# A rejected login. On a booting image cloud-init may still be writing the key, so a few in a row are allowed.
_AUTH_MARKERS = ("permission denied", "too many authentication failures")
_AUTH_FAILURES_ALLOWED = 3


def wait_for_ssh_ready(bmis: Sequence[Mapping[str, str]], *, retries: int = 60, delay: int = 10) -> None:
    """Wait until every BMI accepts SSH on its management address.

    RUNNING does not mean the OS is up: after provisioning the operator power-cycles the host onto the tenant
    network and reports RUNNING once the host is powered on, a minute or more before sshd listens. Every test that
    probes from a BMI needs this wait first. A refused or timed-out connection is retried; a login rejected
    several times in a row fails at once, since waiting does not fix a wrong key or user.
    """
    last_output: dict[str, str] = {}
    auth_failures: dict[str, int] = {}

    def not_ready() -> list[str]:
        waiting = []
        for bmi in bmis:
            host = bmi["ssh_host"]
            if last_output.get(host) == "ready":
                continue
            output, rc = ssh_bmi_unchecked(host, "true", timeout=15)
            if rc == 0:
                last_output[host] = "ready"
                continue
            lowered = output.lower()
            last_output[host] = output.strip()[-300:] or f"ssh exited {rc} with no output"
            if any(marker in lowered for marker in _AUTH_MARKERS):
                auth_failures[host] = auth_failures.get(host, 0) + 1
                if auth_failures[host] >= _AUTH_FAILURES_ALLOWED:
                    raise E2EFailure(
                        Kind.TEST_SSH_FAILED,
                        f"SSH to BMI {bmi.get('name', host)} ({host}) was rejected {auth_failures[host]} times in a "
                        f"row: {last_output[host]}. Check the BMI SSH key Secret, user and identity file.",
                        ids={"bmi_id": bmi.get("id", ""), "ssh_host": host},
                    )
            else:
                auth_failures[host] = 0
                if not any(marker in lowered for marker in _NOT_READY_MARKERS):
                    log.warning("SSH to %s failed with an unrecognized error, retrying: %s", host, last_output[host])
            waiting.append(host)
        return waiting

    hosts = ", ".join(f"{bmi.get('name', bmi['ssh_host'])} ({bmi['ssh_host']})" for bmi in bmis)
    try:
        poll_until(
            fn=not_ready,
            until=lambda waiting: not waiting,
            retries=retries,
            delay=delay,
            description=f"SSH on BMIs {hosts}",
            resource_kind=WaitResourceKind.BARE_METAL_INSTANCE,
            resource_id=bmis[0].get("id") or bmis[0]["ssh_host"],
        )
    except TimeoutError as exc:
        pending = {host: out for host, out in last_output.items() if out != "ready"}
        detail = "; ".join(f"{host}: {out}" for host, out in pending.items())
        raise E2EFailure(
            Kind.STUCK_STATE,
            f"BMIs were RUNNING but SSH never came up on {', '.join(pending)} (last errors: {detail}).\n{exc}",
            ids={"ssh_hosts": ",".join(pending)},
        ) from exc


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
