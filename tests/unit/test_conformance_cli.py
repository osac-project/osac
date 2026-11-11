"""Contract tests for the conformance CLI parser and early exits."""

from __future__ import annotations

from pathlib import Path

import pytest

from tests.conformance.cli import main


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
