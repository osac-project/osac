"""Contract tests for the conformance CLI parser and early exits."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance import cli
from tests.conformance.cli import main
from tests.conformance.errors import ConfigError


def test_unknown_command_exits_2(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["frobnicate"]) == 2
    assert "frobnicate" in capsys.readouterr().err


def test_unknown_flag_exits_2(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["run", "--frobnicate"]) == 2
    assert "--frobnicate" in capsys.readouterr().err


def test_missing_config_exits_2_without_a_report(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    results = tmp_path / "results"
    areas = tmp_path / "areas"
    areas.mkdir()

    assert (
        main(["list", "--config", str(tmp_path / "missing.yaml"), "--results", str(results), "--areas", str(areas)])
        == 2
    )

    assert "missing.yaml" in capsys.readouterr().err
    assert not results.exists()


def test_config_errors_are_redacted(
    tmp_path: Path, capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    fake_token = "not-a-real-credential"
    fake_host = "api.internal.test"
    token = tmp_path / "token"
    token.write_text(fake_token)
    config = tmp_path / "conformance.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "lab": {
                    "kubeconfig": str(tmp_path / "hub"),
                    "apiURL": f"https://{fake_host}",
                    "tokenFile": str(token),
                    "caFile": str(tmp_path / "ca.pem"),
                },
            },
            sort_keys=False,
        )
    )

    def fail(_args: object) -> None:
        raise ConfigError([f"lab read failed: {fake_host} rejected {fake_token}"])

    monkeypatch.setattr(cli, "_build_runtime", fail)

    assert main(["list", "--config", str(config)]) == 2

    err = capsys.readouterr().err
    assert fake_token not in err
    assert fake_host not in err
    assert "<redacted>" in err
