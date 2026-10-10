from __future__ import annotations

from collections.abc import Callable

import pytest

from tests.e2e.bmaas.regression.networking import bmi_ssh


@pytest.mark.parametrize(("probe", "target_ip"), [(bmi_ssh.arping, "192.0.2.2"), (bmi_ssh.ping, "192.0.2.2")])
def test_completed_negative_probe_returns_false(
    monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool], target_ip: str
) -> None:
    commands: list[str] = []

    def run_unchecked(_ssh_host: str, command: str, **_kwargs: object) -> tuple[str, int]:
        commands.append(command)
        return f"{bmi_ssh._PROBE_EXIT_SENTINEL}=1", 1

    monkeypatch.setattr(bmi_ssh, "ssh_bmi_unchecked", run_unchecked)

    assert probe("bmi.example.test", target_ip) is False
    assert len(commands) == 1
    assert "probe_rc=$?" in commands[0]
    assert bmi_ssh._PROBE_EXIT_SENTINEL in commands[0]
    assert commands[0].endswith('exit "$probe_rc"')


@pytest.mark.parametrize("probe", [bmi_ssh.arping, bmi_ssh.ping])
def test_successful_probe_returns_true(monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool]) -> None:
    monkeypatch.setattr(
        bmi_ssh, "ssh_bmi_unchecked", lambda *_args, **_kwargs: (f"{bmi_ssh._PROBE_EXIT_SENTINEL}=0", 0)
    )

    assert probe("bmi.example.test", "192.0.2.2") is True


@pytest.mark.parametrize(("probe", "target_ip"), [(bmi_ssh.arping, "192.0.2.2"), (bmi_ssh.ping, "192.0.2.2")])
def test_transport_failure_does_not_look_like_negative_probe(
    monkeypatch: pytest.MonkeyPatch, probe: Callable[..., bool], target_ip: str
) -> None:
    monkeypatch.setattr(bmi_ssh, "ssh_bmi_unchecked", lambda *_args, **_kwargs: ("", 255))

    with pytest.raises(RuntimeError, match="did not complete"):
        probe("bmi.example.test", target_ip)


def test_probe_fails_when_ssh_status_does_not_match_remote_probe(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        bmi_ssh, "ssh_bmi_unchecked", lambda *_args, **_kwargs: (f"{bmi_ssh._PROBE_EXIT_SENTINEL}=1", 255)
    )

    with pytest.raises(RuntimeError, match="but SSH exited 255"):
        bmi_ssh.ping("bmi.example.test", "192.0.2.2")


def test_probe_fails_for_unexpected_remote_command_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        bmi_ssh, "ssh_bmi_unchecked", lambda *_args, **_kwargs: (f"{bmi_ssh._PROBE_EXIT_SENTINEL}=127", 127)
    )

    with pytest.raises(RuntimeError, match="unexpected command exit 127"):
        bmi_ssh.ping("bmi.example.test", "192.0.2.2")
