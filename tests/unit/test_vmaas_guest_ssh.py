from __future__ import annotations

import os
import subprocess
from collections.abc import Callable
from pathlib import Path
from types import SimpleNamespace

import pytest

from tests.e2e.core.ssh_probe import PROBE_EXIT_SENTINEL
from tests.e2e.networking.vmaas import guest_ssh


def test_ssh_guest_unchecked_uses_vm_virtctl_target_and_vm_kubeconfig(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_VM_KUBECONFIG", "/tmp/vm.kubeconfig")
    calls: list[dict[str, object]] = []

    def run(args: list[str], **kwargs: object) -> SimpleNamespace:
        calls.append({"args": args, **kwargs})
        return SimpleNamespace(stdout="guest output", stderr="", returncode=0)

    monkeypatch.setattr(guest_ssh.subprocess, "run", run)

    output, status = guest_ssh.ssh_guest_unchecked(
        vm_namespace="tenant-a", vm_name="vm-one", identity_file="/tmp/id_ed25519", command="hostname"
    )

    assert (output, status) == ("guest output", 0)
    assert calls[0]["args"] == [
        "virtctl",
        "ssh",
        "--identity-file",
        "/tmp/id_ed25519",
        "--command",
        "hostname",
        "fedora@vm/vm-one/tenant-a",
    ]
    assert calls[0]["timeout"] == 30
    assert calls[0]["env"]["KUBECONFIG"] == "/tmp/vm.kubeconfig"  # type: ignore[index]


def test_ssh_guest_unchecked_requires_vm_kubeconfig(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_VM_KUBECONFIG", raising=False)

    with pytest.raises(RuntimeError, match="OSAC_VM_KUBECONFIG"):
        guest_ssh.ssh_guest_unchecked(
            vm_namespace="tenant-a", vm_name="vm-one", identity_file="/tmp/id_ed25519", command="hostname"
        )


@pytest.mark.parametrize(("probe", "target_ip"), [(guest_ssh.arping, "192.0.2.2"), (guest_ssh.ping, "192.0.2.2")])
def test_completed_negative_probe_returns_false(
    monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool], target_ip: str
) -> None:
    commands: list[str] = []

    def ssh_guest_unchecked(**kwargs: str) -> tuple[str, int]:
        commands.append(kwargs["command"])
        return f"{PROBE_EXIT_SENTINEL}=1", 1

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", ssh_guest_unchecked)

    assert probe("tenant-a", "vm-one", "/tmp/id_ed25519", target_ip) is False
    assert len(commands) == 1
    assert "probe_rc=$?" in commands[0]
    assert PROBE_EXIT_SENTINEL in commands[0]
    assert commands[0].endswith('exit "$probe_rc"')


@pytest.mark.parametrize("probe", [guest_ssh.arping, guest_ssh.ping])
def test_successful_probe_returns_true(monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool]) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (f"{PROBE_EXIT_SENTINEL}=0", 0))

    assert probe("tenant-a", "vm-one", "/tmp/id_ed25519", "192.0.2.2") is True


@pytest.mark.parametrize(("probe", "target_ip"), [(guest_ssh.arping, "192.0.2.2"), (guest_ssh.ping, "192.0.2.2")])
def test_transport_failure_does_not_look_like_negative_probe(
    monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool], target_ip: str
) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: ("", 255))

    with pytest.raises(RuntimeError, match="did not complete"):
        probe("tenant-a", "vm-one", "/tmp/id_ed25519", target_ip)


def test_probe_fails_when_virtctl_status_does_not_match_remote_probe(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (f"{PROBE_EXIT_SENTINEL}=1", 255))

    with pytest.raises(RuntimeError, match="but virtctl exited 255"):
        guest_ssh.ping("tenant-a", "vm-one", "/tmp/id_ed25519", "192.0.2.2")


def test_probe_fails_for_unexpected_remote_command_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: (f"{PROBE_EXIT_SENTINEL}=127", 127))

    with pytest.raises(RuntimeError, match="unexpected command exit 127"):
        guest_ssh.ping("tenant-a", "vm-one", "/tmp/id_ed25519", "192.0.2.2")


def test_probe_rejects_invalid_ip_before_starting_virtctl(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_kwargs: pytest.fail("virtctl must not run"))

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
        "curl -sS -o /dev/null -w '\\n__OSAC_HTTP_STATUS__%{http_code}\\n' --connect-timeout 15 https://quay.io"
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


def test_guest_host_key_reads_key_through_virtctl(monkeypatch: pytest.MonkeyPatch) -> None:
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


def test_ssh_guest_preserves_existing_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_VM_KUBECONFIG", "/tmp/vm.kubeconfig")
    monkeypatch.setenv("OSAC_TEST_VALUE", "present")
    captured: dict[str, object] = {}

    def run(_args: list[str], **kwargs: object) -> SimpleNamespace:
        captured.update(kwargs)
        return SimpleNamespace(stdout="", stderr="", returncode=0)

    monkeypatch.setattr(guest_ssh.subprocess, "run", run)
    guest_ssh.ssh_guest_unchecked(vm_namespace="ns", vm_name="vm", identity_file="key", command="true")

    env = captured["env"]
    assert isinstance(env, dict)
    assert env["OSAC_TEST_VALUE"] == "present"
    assert os.environ["OSAC_VM_KUBECONFIG"] == "/tmp/vm.kubeconfig"


def test_virtctl_timeout_is_reported_as_transport_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_VM_KUBECONFIG", "/tmp/vm.kubeconfig")

    def run(*_args: object, **_kwargs: object) -> None:
        raise subprocess.TimeoutExpired(cmd="virtctl ssh", timeout=30, output="partial")

    monkeypatch.setattr(guest_ssh.subprocess, "run", run)

    with pytest.raises(RuntimeError, match="timed out"):
        guest_ssh.ssh_guest_unchecked(
            vm_namespace="tenant-a", vm_name="vm-one", identity_file="/tmp/id_ed25519", command="hostname"
        )


def test_missing_virtctl_is_reported_as_transport_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_VM_KUBECONFIG", "/tmp/vm.kubeconfig")

    def run(*_args: object, **_kwargs: object) -> None:
        raise FileNotFoundError("virtctl")

    monkeypatch.setattr(guest_ssh.subprocess, "run", run)

    with pytest.raises(RuntimeError, match="failed to start virtctl SSH"):
        guest_ssh.ssh_guest_unchecked(
            vm_namespace="tenant-a", vm_name="vm-one", identity_file="/tmp/id_ed25519", command="hostname"
        )
