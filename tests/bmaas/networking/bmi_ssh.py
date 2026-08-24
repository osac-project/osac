from __future__ import annotations

import logging
import subprocess

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


def get_bmc_ip(bmh_name: str) -> str:
    domiflist = subprocess.run(["virsh", "domiflist", bmh_name], capture_output=True, text=True, timeout=10, check=True)
    bmc_mac = ""
    for line in domiflist.stdout.splitlines():
        if "bmc-net" in line:
            bmc_mac = line.split()[4]
            break
    if not bmc_mac:
        raise RuntimeError(f"No bmc-net interface found for VM {bmh_name}")

    leases = subprocess.run(
        ["virsh", "net-dhcp-leases", "bmc-net"], capture_output=True, text=True, timeout=10, check=True
    )
    for line in leases.stdout.splitlines():
        if bmc_mac in line:
            ip_with_prefix = line.split()[4]
            return ip_with_prefix.split("/")[0]
    raise RuntimeError(f"No DHCP lease found for MAC {bmc_mac} on bmc-net")


def ssh_bmi(bmc_ip: str, command: str, timeout: int = 30) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["ssh", *_SSH_OPTS, f"fedora@{bmc_ip}", command], capture_output=True, text=True, timeout=timeout, check=True
    )


def ssh_bmi_unchecked(bmc_ip: str, command: str, timeout: int = 30) -> tuple[str, int]:
    try:
        result = subprocess.run(
            ["ssh", *_SSH_OPTS, f"fedora@{bmc_ip}", command],
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        log.warning("ssh_bmi_unchecked(%s): subprocess timed out after %ds", bmc_ip, timeout)
        return f"ssh timed out after {timeout}s", 255
    return (result.stdout.strip() + "\n" + result.stderr.strip()).strip(), result.returncode


def arping(bmc_ip: str, target_ip: str, interface: str = "ens5", count: int = 3) -> bool:
    _, rc = ssh_bmi_unchecked(bmc_ip, f"arping -c {count} -I {interface} {target_ip}", timeout=30)
    return rc == 0


def ping(bmc_ip: str, target_ip: str, count: int = 3, wait: int = 3) -> bool:
    _, rc = ssh_bmi_unchecked(bmc_ip, f"ping -c {count} -W {wait} {target_ip}", timeout=30)
    return rc == 0


def curl_status(bmc_ip: str, url: str, timeout: int = 15) -> int:
    output, rc = ssh_bmi_unchecked(
        bmc_ip,
        f"curl -s -o /dev/null -w '%{{http_code}}' --connect-timeout {timeout} {url}",
        timeout=timeout + 30,
    )
    log.info("curl_status(%s, %s): ssh_rc=%d, output=%r", bmc_ip, url, rc, output[-200:])
    for line in output.strip().splitlines():
        line = line.strip()
        if line.isdigit() and len(line) == 3:
            return int(line)
    log.warning("curl_status: no HTTP status code found in output, returning 0")
    return 0


def ssh_via_external_ip(external_ip: str, command: str = "hostname", timeout: int = 15) -> str:
    result = subprocess.run(
        [
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
        ],
        capture_output=True,
        text=True,
        timeout=timeout + 10,
        check=True,
    )
    return result.stdout.strip()
