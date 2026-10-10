from __future__ import annotations

import logging
import os
import subprocess

from tests.e2e.core.runner import env

log = logging.getLogger(__name__)


def ssh_user() -> str:
    # fedora-cloud-bmi disables root SSH ("Please login as fedora"); match bmi_ssh.py.
    return env("OSAC_BMI_SSH_USER", "fedora")


def _ssh_argv(host: str, command: str, timeout: int) -> list[str]:
    opts = [
        "-o",
        f"ConnectTimeout={timeout}",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
    ]
    target = f"{ssh_user()}@{host}"
    password = os.environ.get("OSAC_BMI_SSH_PASSWORD", "")
    if password:
        return ["sshpass", "-e", "ssh", *opts, "-o", "PreferredAuthentications=password", target, command]
    identity = os.environ.get("OSAC_BMI_SSH_IDENTITY", "/root/.ssh/id_rsa")
    return ["ssh", *opts, "-i", identity, target, command]


def _env() -> dict[str, str]:
    merged = os.environ.copy()
    password = os.environ.get("OSAC_BMI_SSH_PASSWORD")
    if password:
        merged["SSHPASS"] = password
    return merged


def ssh_unchecked(host: str, command: str, timeout: int = 30) -> tuple[str, int]:
    try:
        result = subprocess.run(
            _ssh_argv(host, command, timeout),
            capture_output=True,
            text=True,
            timeout=timeout + 10,
            check=False,
            env=_env(),
        )
    except subprocess.TimeoutExpired:
        return f"ssh timed out after {timeout}s", 255
    out = ((result.stdout or "") + "\n" + (result.stderr or "")).strip()
    log.info("guest ssh rc=%s cmd=%s", result.returncode, command)
    return out, result.returncode


def ping(host: str, target_ip: str, count: int = 3, wait: int = 3) -> bool:
    _, rc = ssh_unchecked(host, f"ping -c {count} -W {wait} {target_ip}")
    return rc == 0


def arping(host: str, target_ip: str, count: int = 3) -> bool:
    _, rc = ssh_unchecked(host, f"arping -c {count} {target_ip}")
    return rc == 0


def curl_status(host: str, url: str, timeout: int = 15) -> int:
    output, _ = ssh_unchecked(
        host,
        f"curl -s -o /dev/null -w '%{{http_code}}' --connect-timeout {timeout} {url}",
        timeout=timeout + 30,
    )
    for line in output.strip().splitlines():
        line = line.strip()
        if line.isdigit() and len(line) == 3:
            return int(line)
    return 0


def ssh_via_external_ip(external_ip: str, command: str = "hostname", timeout: int = 15) -> str:
    out, rc = ssh_unchecked(external_ip, command, timeout=timeout)
    if rc != 0:
        raise subprocess.CalledProcessError(rc, command, out)
    return out.splitlines()[0] if out else ""
