"""Contract tests for the Netris settings conformance check."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tests.conformance.domains.networking import test_netris_settings as netris


def _config(tmp_path: Path) -> Path:
    kubeconfig = tmp_path / "hub.kubeconfig"
    kubeconfig.write_text("hub")
    config = tmp_path / "conformance.yaml"
    config.write_text(f"target:\n  kubeconfig: {kubeconfig}\n  namespace: osac-devel\n")
    return config


class _Run:
    def __init__(self, *, returncode: int = 0, data: dict[str, str] | None = None, stderr: str = "") -> None:
        self.returncode = returncode
        self.stdout = json.dumps({"data": data or {}})
        self.stderr = stderr


def test_netris_setting_present_passes(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[list[str]] = []
    monkeypatch.setattr(netris, "_CONFIG_PATH", _config(tmp_path))

    def fake_run(*args: str, **_kwargs: object) -> _Run:
        calls.append(args[0])
        return _Run(data={netris._SETTING: "https://netris.example.test"})

    monkeypatch.setattr(netris.subprocess, "run", fake_run)

    netris.test_netris_settings_are_present()

    assert calls
    assert calls[0][calls[0].index("-n") + 1] == "osac-devel"


def test_netris_setting_missing_fails(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(netris, "_CONFIG_PATH", _config(tmp_path))
    monkeypatch.setattr(netris.subprocess, "run", lambda *_args, **_kwargs: _Run(data={}))

    with pytest.raises(AssertionError, match=netris._SETTING):
        netris.test_netris_settings_are_present()


def test_unreadable_configmap_fails(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(netris, "_CONFIG_PATH", _config(tmp_path))
    monkeypatch.setattr(netris.subprocess, "run", lambda *_args, **_kwargs: _Run(returncode=1, stderr="not found"))

    with pytest.raises(AssertionError, match="cannot read"):
        netris.test_netris_settings_are_present()
