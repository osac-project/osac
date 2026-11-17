from __future__ import annotations

import json
import logging
import os
import subprocess
from collections.abc import Callable
from pathlib import Path

from tests.e2e.core import failure_snapshot
from tests.e2e.core.runner import env, run

log = logging.getLogger(__name__)


def ssh_user() -> str:
    # fedora-cloud-bmi disables root SSH ("Please login as fedora"); match bmi_ssh.py.
    return env("OSAC_BMI_SSH_USER", "fedora")


def _ssh_argv(host: str, command: str, timeout: int) -> list[str]:
    opts = ["-o", f"ConnectTimeout={timeout}", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"]
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
        log.info("guest ssh host=%s timed out after %ss cmd=%s", host, timeout, command)
        return f"ssh timed out after {timeout}s", 255
    out = ((result.stdout or "") + "\n" + (result.stderr or "")).strip()
    log.info("guest ssh host=%s rc=%s cmd=%s", host, result.returncode, command)
    return out, result.returncode


def ping(host: str, target_ip: str, count: int = 3, wait: int = 3) -> bool:
    _, rc = ssh_unchecked(host, f"ping -c {count} -W {wait} {target_ip}")
    return rc == 0


def arping(host: str, target_ip: str, count: int = 3) -> bool:
    _, rc = ssh_unchecked(host, f"arping -c {count} {target_ip}")
    return rc == 0


def curl_status(host: str, url: str, timeout: int = 15) -> int:
    output, _ = ssh_unchecked(
        host, f"curl -s -o /dev/null -w '%{{http_code}}' --connect-timeout {timeout} {url}", timeout=timeout + 30
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


def register_guest_snapshot(name: str, bmis: Callable[[], list[dict[str, str]]]) -> None:
    """Add the BMIs returned by `bmis()` (dicts with `name` and `ssh_host`) to the failure snapshot."""

    def runner_for(host: str) -> Callable[[str, int], tuple[str, int]]:
        return lambda command, timeout: ssh_unchecked(host, command, timeout=timeout)

    def guests() -> list[tuple[str, Callable[[str, int], tuple[str, int]]]]:
        return [(b.get("name", b["ssh_host"]), runner_for(b["ssh_host"])) for b in bmis() if b.get("ssh_host")]

    failure_snapshot.SOURCES.register(name, failure_snapshot.guest_source(guests), kinds=failure_snapshot.GUEST_KINDS)


def unregister_guest_snapshot(name: str) -> None:
    failure_snapshot.SOURCES.unregister(name)
    failure_snapshot.SOURCES.unregister(f"{name}-hosts")


def register_host_snapshot(name: str, kubectl: list[str], bmh_namespace: str) -> None:
    """Add the BareMetalHosts' provisioning, power and operation history to the failure snapshot.

    The job collects them after the suite's teardown, when the hosts are deprovisioned; only a failure-time read
    shows what a host was doing when a test failed (for example, still rebooting onto the tenant network).
    """

    def source(directory: Path, window: failure_snapshot.Window) -> None:
        try:
            hosts = json.loads(run(*kubectl, "get", "bmh", "-n", bmh_namespace, "-o", "json", timeout=window.timeout()))
        except Exception as exc:
            failure_snapshot.record_error(directory, "bmh", f"{type(exc).__name__}: {exc}")
            return
        summary = [
            {
                "name": host.get("metadata", {}).get("name"),
                "consumer": (host.get("spec", {}).get("consumerRef") or {}).get("name"),
                "online": host.get("spec", {}).get("online"),
                "state": host.get("status", {}).get("provisioning", {}).get("state"),
                "poweredOn": host.get("status", {}).get("poweredOn"),
                "operationalStatus": host.get("status", {}).get("operationalStatus"),
                "errorMessage": host.get("status", {}).get("errorMessage"),
                "lastUpdated": host.get("status", {}).get("lastUpdated"),
                "operationHistory": host.get("status", {}).get("operationHistory"),
                "nics": [
                    {"name": nic.get("name"), "mac": nic.get("mac"), "ip": nic.get("ip")}
                    for nic in (host.get("status", {}).get("hardware") or {}).get("nics", [])
                ],
            }
            for host in hosts.get("items", [])
        ]
        failure_snapshot.write_json(directory, "bmh-status.json", summary)

    failure_snapshot.SOURCES.register(f"{name}-hosts", source)
