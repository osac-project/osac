from __future__ import annotations

import ipaddress
import logging
import os
import re
import shlex
import subprocess
import tempfile

import pytest

from tests.e2e.core.failures import Kind, tagged
from tests.e2e.core.runner import poll_until
from tests.e2e.core.ssh_probe import probe_connectivity
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.vmaas import ssh_helper
from tests.e2e.networking.vmaas.ssh_helper import HelperExecError, HelperPodError

log = logging.getLogger(__name__)

_HTTP_STATUS_SENTINEL = "__OSAC_HTTP_STATUS__"
_HTTP_STATUS_RE = re.compile(rf"(?m)^{_HTTP_STATUS_SENTINEL}(\d{{3}})$")
_TCP_RE = re.compile(r"(?m)^__OSAC_TCP__=(open|refused|blocked)$")
_DNS_RE = re.compile(r"(?m)^__OSAC_DNS__=(\d+) (\d+) (\S*)$")
_ED25519_HOST_KEY_RE = re.compile(r"^ssh-ed25519 ([A-Za-z0-9+/]+={0,2})(?:\s+.*)?$")

# Run on the guest with python3: connect_ex tells open, refused (RST) and blocked (timeout) apart.
_TCP_PROBE = (
    "import errno,socket,sys;s=socket.socket();s.settimeout(5);r=s.connect_ex((sys.argv[1],int(sys.argv[2])));"
    "print('__OSAC_TCP__='+('open' if r==0 else 'refused' if r==errno.ECONNREFUSED else 'blocked'))"
)
# One A query without EDNS to a named server; prints "<rcode> <answer count> <last four bytes as dotted quad>".
_DNS_PROBE = (
    "import socket,struct,sys\n"
    "q=struct.pack('>HHHHHH',0x4f53,0x0100,1,0,0,0)"
    "+b''.join(bytes([len(p)])+p.encode() for p in sys.argv[1].split('.'))+b'\\0'+struct.pack('>HH',1,1)\n"
    "s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(5);s.sendto(q,(sys.argv[2],53));r=s.recv(512)\n"
    "n=struct.unpack('>H',r[6:8])[0]\n"
    "print('__OSAC_DNS__=%d %d %s'%(r[3]&15,n,'.'.join(map(str,r[-4:])) if n==1 else ''))"
)
_REQUIRED_GUEST_TOOLS = ("arping", "ping", "curl", "python3", "getent")
TRANSPORT_ERROR = "transport-error"
_SSH_TRANSPORT_STATUS = 255
_SSH_READY_RETRIES = 36
_SSH_READY_DELAY = 10


def _combined_output(stdout: str | bytes | None, stderr: str | bytes | None) -> str:
    def as_text(value: str | bytes | None) -> str:
        if isinstance(value, bytes):
            return value.decode(errors="replace")
        return value or ""

    return (as_text(stdout).strip() + "\n" + as_text(stderr).strip()).strip()


def _ssh_user() -> str:
    return ssh_helper.ssh_user()


def ssh_guest_unchecked(
    *, vm_namespace: str, vm_name: str, identity_file: str, command: str, timeout: int = 30
) -> tuple[str, int]:
    """Run a command on the guest through the helper Pod in its namespace; return (output, ssh status).

    Status 255 is an SSH transport failure (connect timeout or refusal, authentication). A Pod that is
    not Ready, a failed `kubectl exec` or a missing VM address raises RuntimeError, so none of them can
    be mistaken for a guest exit status. This function is the only place the transport is chosen.
    """
    try:
        return ssh_helper.exec_ssh(
            namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command, timeout=timeout
        )
    except RuntimeError as exc:
        # The helper Pod or kubectl failed before the guest answered: our own access broke, not the network.
        tagged(exc, Kind.TEST_SSH_FAILED, {"vm": vm_name, "vm_namespace": vm_namespace})
        raise


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
        transport="SSH",
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


def _run_curl(vm_namespace: str, vm_name: str, identity_file: str, timeout: int) -> tuple[str, int]:
    # "; true" keeps curl's own failure out of the exit status, so a non-zero status from the transport means
    # the transport failed, while curl still prints 000 through -w when it could not connect.
    command = (
        f"curl -sS -o /dev/null -w '\\n{_HTTP_STATUS_SENTINEL}%{{http_code}}\\n' "
        f"--connect-timeout {timeout} --max-time {timeout * 2} https://quay.io; true"
    )
    return ssh_guest_unchecked(
        vm_namespace=vm_namespace,
        vm_name=vm_name,
        identity_file=identity_file,
        command=command,
        timeout=timeout * 2 + 30,
    )


def curl_status(vm_namespace: str, vm_name: str, identity_file: str, timeout: int = 15) -> int:
    """HTTP status of https://quay.io from the guest; 0 when curl could not connect or SSH returned a failure.

    A helper Pod that is unusable or an exec timeout raises instead (HelperPodError, RuntimeError).
    """
    output, ssh_rc = _run_curl(vm_namespace, vm_name, identity_file, timeout)
    if ssh_rc != 0:
        log.warning("VM guest curl via %s/%s exited %d", vm_namespace, vm_name, ssh_rc)
        return 0
    matches = _HTTP_STATUS_RE.findall(output)
    if len(matches) != 1:
        log.warning("VM guest curl via %s/%s returned no unique HTTP status", vm_namespace, vm_name)
        return 0
    return int(matches[0])


def curl_status_checked(vm_namespace: str, vm_name: str, identity_file: str, timeout: int = 15) -> int:
    """Like curl_status, but a transport failure raises, so 0 (curl printed 000) means the guest tried and failed."""
    output, ssh_rc = _run_curl(vm_namespace, vm_name, identity_file, timeout)
    ids = {"vm": vm_name, "vm_namespace": vm_namespace}
    if ssh_rc != 0:
        raise tagged(
            RuntimeError(f"SSH to {vm_namespace}/{vm_name} failed (rc={ssh_rc}): {output!r}"), Kind.TEST_SSH_FAILED, ids
        )
    matches = _HTTP_STATUS_RE.findall(output)
    if len(matches) != 1:
        raise tagged(
            RuntimeError(f"guest curl on {vm_namespace}/{vm_name} returned no unique HTTP status: {output!r}"),
            Kind.TEST_SSH_FAILED,
            ids,
        )
    return int(matches[0])


def guest_host_key(vm_namespace: str, vm_name: str, identity_file: str) -> str:
    """Read and validate the VM's ED25519 host key through the helper-Pod SSH path.

    The channel is SSH over the test-owned network with host key checking off, not the authenticated
    API channel; that is acceptable because the Pod reaches the VMI's own address.
    """
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


def classify_login_failure(output: str, status: int) -> str:
    """Name why an `ssh ... true` from the helper Pod failed, from ssh's own message."""
    text = output.lower()
    if "permission denied" in text or "authentication" in text:
        return (
            "authentication refused: the VM does not accept the key held by the helper Pod. The "
            f"ComputeInstance must be created with the public half of that key, and OSAC_VM_SSH_USER "
            f"({ssh_helper.ssh_user()}) must be the guest user."
        )
    if "timed out" in text or "no route to host" in text or "network is unreachable" in text:
        return (
            "TCP 22 is unreachable (connection timed out): the VM SecurityGroup must allow ingress tcp/22, "
            "and osac-baseline-deny must not block the helper Pod or the VM"
        )
    if "connection refused" in text:
        return "TCP 22 is refused: the VM answers but sshd is not listening yet or has stopped"
    return f"SSH failed with status {status}: {output.strip()[:300]!r}"


def login_failure_kind(output: str) -> Kind:
    """Traffic-blocked when a healthy guest cannot be reached, test-ssh-failed when our credentials are at fault."""
    text = output.lower()
    if any(word in text for word in ("permission denied", "authentication", "host key", "identification")):
        return Kind.TEST_SSH_FAILED
    return Kind.TRAFFIC_BLOCKED


def external_ssh_error(exc: BaseException) -> tuple[str, Kind]:
    """Output and kind of a failed `ssh_via_external_ip`; the guest was already read through the helper Pod."""
    if isinstance(exc, subprocess.TimeoutExpired):
        return f"ssh timed out after {exc.timeout}s", Kind.TRAFFIC_BLOCKED
    if isinstance(exc, subprocess.CalledProcessError):
        stderr = exc.stderr.decode(errors="replace") if isinstance(exc.stderr, bytes) else (exc.stderr or "")
        detail = (stderr or "").strip()[-300:] or f"exit status {exc.returncode}"
        return f"ssh exited {exc.returncode}: {detail}", login_failure_kind(detail)
    return f"{type(exc).__name__}: {exc}", Kind.TEST_SSH_FAILED


def login_problem(vm_namespace: str, vm_name: str, identity_file: str) -> str | None:
    """None when `ssh ... true` from the helper Pod succeeds, else a classified message."""
    try:
        output, status = ssh_guest_unchecked(
            vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command="true"
        )
    except HelperPodError as exc:
        return f"helper Pod not ready or exec failed: {exc}"
    except RuntimeError as exc:
        return f"transport failed before SSH could run: {exc}"
    return None if status == 0 else classify_login_failure(output, status)


def assert_guest_login(vm_namespace: str, vm_name: str, identity_file: str) -> None:
    """Fail with a message that tells a missing Pod, a blocked port 22 and a rejected key apart."""
    problem = login_problem(vm_namespace, vm_name, identity_file)
    if problem is not None:
        failure = pytest.fail.Exception(f"cannot log in to {vm_namespace}/{vm_name} from the helper Pod: {problem}")
        raise tagged(failure, Kind.TEST_SSH_FAILED, {"vm": vm_name, "vm_namespace": vm_namespace})


def wait_for_guest_ssh(vm_namespace: str, vm_name: str, identity_file: str) -> None:
    """Wait until sshd answers from the helper Pod; Running does not mean the guest finished booting.

    A helper Pod that is not Ready fails at once: waiting cannot fix it.
    """
    last: list[str] = []

    def ready() -> bool:
        try:
            output, status = ssh_guest_unchecked(
                vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command="true"
            )
        except HelperExecError as exc:
            last[:] = [f"kubectl exec into the helper Pod failed: {exc}"]
            return False
        except HelperPodError:
            raise
        except RuntimeError as exc:
            last[:] = [f"transport failed before SSH could run: {exc}"]
            return False
        if status == 0:
            return True
        last[:] = [classify_login_failure(output, status)]
        return False

    try:
        poll_until(
            fn=ready,
            until=bool,
            retries=_SSH_READY_RETRIES,
            delay=_SSH_READY_DELAY,
            description=f"guest SSH on {vm_namespace}/{vm_name}",
            resource_kind=WaitResourceKind.VIRTUAL_MACHINE_INSTANCE,
            resource_id=vm_name,
        )
    except TimeoutError as exc:
        raise tagged(
            TimeoutError(
                f"SSH from the helper Pod never reached {vm_namespace}/{vm_name}: {last[0] if last else 'no attempt'}"
            ),
            Kind.TEST_SSH_FAILED,
            {"vm": vm_name, "vm_namespace": vm_namespace},
        ) from exc


def require_guest_tools(vm_namespace: str, vm_name: str, identity_file: str) -> None:
    """Fail naming any tool the connectivity checks need that the guest image lacks."""
    tools = " ".join(_REQUIRED_GUEST_TOOLS)
    command = f'for t in {tools}; do command -v "$t" >/dev/null || echo "MISSING:$t"; done'
    output, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command
    )
    if status != 0:
        raise RuntimeError(f"could not check guest tools on {vm_namespace}/{vm_name}: {output!r}")
    missing = sorted(set(re.findall(r"(?m)^MISSING:(\S+)$", output)))
    if missing:
        pytest.fail(
            f"VM {vm_namespace}/{vm_name} lacks {', '.join(missing)}; the connectivity checks need "
            f"{', '.join(_REQUIRED_GUEST_TOOLS)} in the guest image."
        )


def tcp_state(vm_namespace: str, vm_name: str, identity_file: str, host: str, port: int) -> str:
    """Return open, refused or blocked for a TCP connect made from inside the guest.

    Returns TRANSPORT_ERROR when SSH to the guest failed (status 255) or timed out, so a polling caller
    retries on a blip. Callers that expect a closed port must accept only refused or blocked.
    """
    command = f"python3 -c {shlex.quote(_TCP_PROBE)} {shlex.quote(_ipv4_target(host))} {int(port)}"
    try:
        output, status = ssh_guest_unchecked(
            vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command, timeout=30
        )
    except HelperExecError:
        return TRANSPORT_ERROR
    except HelperPodError:
        raise
    except RuntimeError:
        return TRANSPORT_ERROR
    if status == _SSH_TRANSPORT_STATUS:
        return TRANSPORT_ERROR
    matches = _TCP_RE.findall(output)
    if len(matches) != 1:
        raise RuntimeError(
            f"TCP probe on {vm_namespace}/{vm_name} to {host}:{port} returned no unique result (rc={status}): "
            f"{output!r}"
        )
    return matches[0]


def start_tcp_listener(vm_namespace: str, vm_name: str, identity_file: str, port: int) -> None:
    """Start a python HTTP listener that outlives the SSH session, then wait until it accepts locally."""
    # The redirections are required, or the SSH session never returns.
    command = f"setsid nohup python3 -m http.server {int(port)} --bind 0.0.0.0 >/dev/null 2>&1 </dev/null &"
    output, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command
    )
    if status != 0:
        raise RuntimeError(f"could not start a listener on {vm_namespace}/{vm_name}:{port}: {output!r}")
    poll_until(
        fn=lambda: tcp_state(vm_namespace, vm_name, identity_file, "127.0.0.1", port),
        until=lambda state: state == "open",
        retries=6,
        delay=5,
        description=f"listener on {vm_namespace}/{vm_name}:{port}",
        resource_kind=WaitResourceKind.VIRTUAL_MACHINE_INSTANCE,
        resource_id=vm_name,
    )


def dhcp_nameservers(vm_namespace: str, vm_name: str, identity_file: str) -> list[str]:
    """Return the DHCP-supplied resolvers, without the systemd-resolved stub address."""
    output, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace,
        vm_name=vm_name,
        identity_file=identity_file,
        command="cat /run/systemd/resolve/resolv.conf 2>/dev/null || cat /etc/resolv.conf",
    )
    if status != 0:
        # SSH hiccup or unreadable file: an empty list makes a polling caller retry.
        log.warning("could not read resolvers on %s/%s (status %d)", vm_namespace, vm_name, status)
        return []
    servers = re.findall(r"(?m)^\s*nameserver\s+(\S+)\s*$", output)
    return [server for server in servers if server != "127.0.0.53"]


def dns_a_record(vm_namespace: str, vm_name: str, identity_file: str, name: str, server: str) -> str | None:
    """Ask `server` directly for one A record of `name`; None when it does not answer with exactly one."""
    command = f"python3 -c {shlex.quote(_DNS_PROBE)} {shlex.quote(name)} {shlex.quote(_ipv4_target(server))}"
    output, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace, vm_name=vm_name, identity_file=identity_file, command=command, timeout=30
    )
    if status != 0:
        return None
    matches = _DNS_RE.findall(output)
    if len(matches) != 1:
        return None
    rcode, count, address = matches[0]
    if int(rcode) != 0 or int(count) != 1 or not address:
        return None
    return address


def resolves(vm_namespace: str, vm_name: str, identity_file: str, name: str) -> bool:
    """True when the guest's system resolver resolves `name`; getent exits 2 for an unknown name."""
    _, status = ssh_guest_unchecked(
        vm_namespace=vm_namespace,
        vm_name=vm_name,
        identity_file=identity_file,
        command=f"getent ahostsv4 {shlex.quote(name)}",
    )
    return status == 0


def remove_helper(vm_namespace: str) -> None:
    """Delete the helper Pod, Secret and NetworkPolicy of a VM namespace (idempotent)."""
    ssh_helper.remove_helper(vm_namespace)


def note_namespace(group: str, vm_namespace: str) -> None:
    ssh_helper.note_namespace(group, vm_namespace)


def remove_helpers_for(group: str) -> None:
    """Delete the helpers of every namespace recorded for a VirtualNetwork ID."""
    ssh_helper.remove_helpers_for(group)
