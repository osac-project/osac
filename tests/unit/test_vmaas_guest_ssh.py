from __future__ import annotations

import os
import socket
import struct
import subprocess
import sys
import threading
from pathlib import Path
from types import SimpleNamespace

import pytest

from tests.e2e.core.ssh_probe import PROBE_EXIT_SENTINEL
from tests.e2e.networking.vmaas import guest_ssh


def test_probe_fails_when_ssh_status_does_not_match_remote_probe(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (f"{PROBE_EXIT_SENTINEL}=1", 255))

    with pytest.raises(RuntimeError, match="but SSH exited 255"):
        guest_ssh.ping("tenant-a", "vm-one", "/tmp/id_ed25519", "192.0.2.2")


def test_probe_fails_for_unexpected_remote_command_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (f"{PROBE_EXIT_SENTINEL}=127", 127))

    with pytest.raises(RuntimeError, match="unexpected command exit 127"):
        guest_ssh.ping("tenant-a", "vm-one", "/tmp/id_ed25519", "192.0.2.2")


def test_probe_rejects_invalid_ip_before_starting_ssh(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: pytest.fail("SSH must not run"))

    with pytest.raises(ValueError, match="IPv4"):
        guest_ssh.ping("tenant-a", "vm-one", "/tmp/id_ed25519", "invalid-ip")


def test_curl_status_extracts_http_code(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[str] = []

    def ssh_guest_unchecked(**kwargs: object) -> tuple[str, int]:
        commands.append(str(kwargs["command"]))
        return f"{guest_ssh._HTTP_STATUS_SENTINEL}200", 0

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", ssh_guest_unchecked)

    assert guest_ssh.curl_status("tenant-a", "vm-one", "/tmp/id_ed25519") == 200
    assert commands == [
        "curl -sS -o /dev/null -w '\\n__OSAC_HTTP_STATUS__%{http_code}\\n' "
        "--connect-timeout 15 --max-time 30 https://quay.io; true"
    ]


def test_ssh_via_external_ip_verifies_expected_host_key_and_uses_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[dict[str, object]] = []

    def run(args: list[str], **kwargs: object) -> SimpleNamespace:
        known_hosts_option = next(option for option in args if option.startswith("UserKnownHostsFile="))
        known_hosts_path = known_hosts_option.removeprefix("UserKnownHostsFile=")
        known_hosts = Path(known_hosts_path).read_text(encoding="utf-8")
        mode = os.stat(known_hosts_path).st_mode & 0o777
        calls.append({"args": args, "known_hosts": known_hosts, "known_hosts_mode": mode, **kwargs})
        return SimpleNamespace(stdout="fedora-vm\n", stderr="", returncode=0)

    monkeypatch.setattr(guest_ssh.subprocess, "run", run)

    key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
    assert guest_ssh.ssh_via_external_ip("192.0.2.10", "/tmp/id_ed25519", expected_host_key=key) == "fedora-vm"
    args = calls[0]["args"]
    assert "-i" in args  # type: ignore[operator]
    assert "/tmp/id_ed25519" in args  # type: ignore[operator]
    assert "fedora@192.0.2.10" in args  # type: ignore[operator]
    assert "StrictHostKeyChecking=yes" in args  # type: ignore[operator]
    assert "GlobalKnownHostsFile=/dev/null" in args  # type: ignore[operator]
    assert any(str(option).startswith("UserKnownHostsFile=") for option in args)  # type: ignore[union-attr]
    assert calls[0]["known_hosts"] == f"192.0.2.10 {key}\n"
    assert calls[0]["known_hosts_mode"] == 0o600
    assert calls[0]["check"] is True


def test_guest_host_key_reads_key_through_the_helper(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[str] = []
    key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA vm"

    def run_unchecked(**kwargs: str) -> tuple[str, int]:
        commands.append(kwargs["command"])
        return key, 0

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", run_unchecked)

    assert guest_ssh.guest_host_key("tenant-a", "vm-one", "/tmp/id_ed25519") == key.rsplit(" ", 1)[0]
    assert commands == ["cat /etc/ssh/ssh_host_ed25519_key.pub"]


@pytest.mark.parametrize(
    "output",
    [
        "not a key",
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
    ],
)
def test_guest_host_key_rejects_missing_or_multiple_keys(monkeypatch: pytest.MonkeyPatch, output: str) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (output, 0))

    with pytest.raises(RuntimeError, match="exactly one ED25519"):
        guest_ssh.guest_host_key("tenant-a", "vm-one", "/tmp/id_ed25519")


def test_external_ip_ssh_rejects_invalid_expected_host_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh.subprocess, "run", lambda *_args, **_kwargs: pytest.fail("ssh must not run"))

    with pytest.raises(ValueError, match="valid ED25519"):
        guest_ssh.ssh_via_external_ip("192.0.2.10", "/tmp/id_ed25519", expected_host_key="ssh-rsa invalid")


def _stub_guest(monkeypatch: pytest.MonkeyPatch, output: str, status: int = 0) -> list[str]:
    commands: list[str] = []

    def ssh_guest_unchecked(**kwargs: object) -> tuple[str, int]:
        commands.append(str(kwargs["command"]))
        return output, status

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", ssh_guest_unchecked)
    return commands


@pytest.mark.parametrize("state", ["open", "refused", "blocked"])
def test_tcp_state_returns_the_reported_state(monkeypatch: pytest.MonkeyPatch, state: str) -> None:
    commands = _stub_guest(monkeypatch, f"noise\n__OSAC_TCP__={state}")

    assert guest_ssh.tcp_state("ns", "vm", "key", "192.0.2.5", 8080) == state
    assert commands[0].startswith("python3 -c ")
    assert commands[0].endswith(" 192.0.2.5 8080")


@pytest.mark.parametrize("output", ["", "__OSAC_TCP__=open\n__OSAC_TCP__=blocked", "__OSAC_TCP__=weird"])
def test_tcp_state_requires_exactly_one_marker(monkeypatch: pytest.MonkeyPatch, output: str) -> None:
    _stub_guest(monkeypatch, output)

    with pytest.raises(RuntimeError, match="no unique result"):
        guest_ssh.tcp_state("ns", "vm", "key", "192.0.2.5", 8080)


def test_tcp_probe_runs_on_a_real_socket() -> None:
    import socket
    import sys

    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        listener.listen(1)
        port = listener.getsockname()[1]
        result = subprocess.run(
            [sys.executable, "-c", guest_ssh._TCP_PROBE, "127.0.0.1", str(port)], capture_output=True, text=True
        )
        assert guest_ssh._TCP_RE.findall(result.stdout) == ["open"]
    closed = subprocess.run(
        [sys.executable, "-c", guest_ssh._TCP_PROBE, "127.0.0.1", str(port)], capture_output=True, text=True
    )
    assert guest_ssh._TCP_RE.findall(closed.stdout) == ["refused"]


def test_dns_a_record_accepts_one_answer(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "__OSAC_DNS__=0 1 172.30.0.1")

    assert guest_ssh.dns_a_record("ns", "vm", "key", "kubernetes.default.svc.cluster.local", "172.30.0.10") == (
        "172.30.0.1"
    )


@pytest.mark.parametrize(
    ("output", "status"),
    [("__OSAC_DNS__=3 0 ", 0), ("__OSAC_DNS__=0 2 ", 0), ("", 0), ("__OSAC_DNS__=0 1 172.30.0.1", 1)],
)
def test_dns_a_record_is_none_without_exactly_one_answer(
    monkeypatch: pytest.MonkeyPatch, output: str, status: int
) -> None:
    _stub_guest(monkeypatch, output, status)

    assert guest_ssh.dns_a_record("ns", "vm", "key", "example.test", "172.30.0.10") is None


def _serve_dns(rcode: int, answers: list[str]) -> tuple[int, list[bytes], threading.Thread]:
    server = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    server.bind(("127.0.0.1", 0))
    server.settimeout(10)
    queries: list[bytes] = []

    def serve() -> None:
        query, peer = server.recvfrom(512)
        queries.append(query)
        question = query[12:]
        reply = struct.pack(">HHHHHH", struct.unpack(">H", query[:2])[0], 0x8180 | rcode, 1, len(answers), 0, 0)
        reply += question
        for address in answers:
            reply += b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 30, 4) + socket.inet_aton(address)
        server.sendto(reply, peer)
        server.close()

    thread = threading.Thread(target=serve, daemon=True)
    thread.start()
    return server.getsockname()[1], queries, thread


def _run_dns_probe(name: str, port: int) -> str:
    # The probe targets port 53; the stub cannot bind it unprivileged, so only the port is swapped.
    probe = guest_ssh._DNS_PROBE.replace("(sys.argv[2],53)", f"(sys.argv[2],{port})")
    assert probe != guest_ssh._DNS_PROBE
    return subprocess.run(
        [sys.executable, "-c", probe, name, "127.0.0.1"], capture_output=True, text=True, timeout=30
    ).stdout


def test_dns_probe_sends_a_valid_query_and_parses_one_answer() -> None:
    port, queries, thread = _serve_dns(0, ["172.30.0.1"])

    output = _run_dns_probe("kubernetes.default.svc.cluster.local", port)
    thread.join(timeout=10)

    assert guest_ssh._DNS_RE.findall(output) == [("0", "1", "172.30.0.1")]
    query = queries[0]
    _, flags, qdcount, ancount, _, arcount = struct.unpack(">HHHHHH", query[:12])
    assert (flags & 0x0100, qdcount, ancount, arcount) == (0x0100, 1, 0, 0)
    labels, offset = [], 12
    while query[offset]:
        labels.append(query[offset + 1 : offset + 1 + query[offset]].decode())
        offset += query[offset] + 1
    assert ".".join(labels) == "kubernetes.default.svc.cluster.local"
    assert struct.unpack(">HH", query[offset + 1 :]) == (1, 1)


def test_dns_probe_reports_nxdomain_without_an_address() -> None:
    port, _, thread = _serve_dns(3, [])

    output = _run_dns_probe("missing.example.test", port)
    thread.join(timeout=10)

    assert guest_ssh._DNS_RE.findall(output) == [("3", "0", "")]


def test_dhcp_nameservers_drop_the_resolved_stub(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "# comment\nnameserver 127.0.0.53\nnameserver 172.30.0.10\nsearch example.test\n")

    assert guest_ssh.dhcp_nameservers("ns", "vm", "key") == ["172.30.0.10"]


def test_require_guest_tools_lists_every_missing_tool(monkeypatch: pytest.MonkeyPatch) -> None:
    commands = _stub_guest(monkeypatch, "MISSING:arping\nMISSING:getent")

    with pytest.raises(pytest.fail.Exception, match="arping, getent"):
        guest_ssh.require_guest_tools("ns", "vm", "key")
    assert "for t in arping ping curl python3 getent" in commands[0]


def test_require_guest_tools_passes_when_nothing_is_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "")

    guest_ssh.require_guest_tools("ns", "vm", "key")


def test_require_guest_tools_raises_on_transport_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "connection refused", 255)

    with pytest.raises(RuntimeError, match="could not check guest tools"):
        guest_ssh.require_guest_tools("ns", "vm", "key")


def test_wait_for_guest_ssh_retries_transport_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    results: list[object] = [RuntimeError("timed out"), ("", 255), ("", 0)]

    def ssh_guest_unchecked(**_kwargs: object) -> tuple[str, int]:
        result = results.pop(0)
        if isinstance(result, Exception):
            raise result
        return result  # type: ignore[return-value]

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", ssh_guest_unchecked)

    guest_ssh.wait_for_guest_ssh("ns", "vm", "key")

    assert results == []


def test_wait_for_guest_ssh_names_the_port_forward_on_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: ("", 255))

    with pytest.raises(TimeoutError, match="helper Pod"):
        guest_ssh.wait_for_guest_ssh("ns", "vm", "key")


def test_start_tcp_listener_detaches_and_waits_for_the_port(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    commands = _stub_guest(monkeypatch, "")
    states = iter(["refused", "open"])
    monkeypatch.setattr(guest_ssh, "tcp_state", lambda *_args: next(states))

    guest_ssh.start_tcp_listener("ns", "vm", "key", 8080)

    assert commands == ["setsid nohup python3 -m http.server 8080 --bind 0.0.0.0 >/dev/null 2>&1 </dev/null &"]


@pytest.mark.parametrize(("status", "expected"), [(0, True), (2, False)])
def test_resolves_uses_the_getent_exit_status(monkeypatch: pytest.MonkeyPatch, status: int, expected: bool) -> None:
    commands = _stub_guest(monkeypatch, "", status)

    assert guest_ssh.resolves("ns", "vm", "key", "quay.io") is expected
    assert commands == ["getent ahostsv4 quay.io"]


@pytest.mark.parametrize(
    ("output", "status", "expected"), [("__OSAC_HTTP_STATUS__200", 0, 200), ("__OSAC_HTTP_STATUS__000", 0, 0)]
)
def test_curl_status_checked_reports_what_the_guest_curl_saw(
    monkeypatch: pytest.MonkeyPatch, output: str, status: int, expected: int
) -> None:
    _stub_guest(monkeypatch, output, status)

    assert guest_ssh.curl_status_checked("ns", "vm", "key") == expected
    assert guest_ssh.curl_status("ns", "vm", "key") == expected


def test_curl_status_checked_raises_on_a_transport_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "", 255)

    with pytest.raises(RuntimeError, match="rc=255"):
        guest_ssh.curl_status_checked("ns", "vm", "key")
    assert guest_ssh.curl_status("ns", "vm", "key") == 0


def test_curl_status_checked_raises_without_a_status_marker(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_guest(monkeypatch, "garbage", 0)

    with pytest.raises(RuntimeError, match="no unique HTTP status"):
        guest_ssh.curl_status_checked("ns", "vm", "key")
