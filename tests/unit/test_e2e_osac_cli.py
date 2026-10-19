from __future__ import annotations

import subprocess

import pytest

from tests.e2e.core import osac_cli


def _make_cli(monkeypatch: pytest.MonkeyPatch) -> tuple[osac_cli.OsacCLI, list[str]]:
    captured_args: list[str] = []

    def fake_run(args: list[str], **_: object) -> subprocess.CompletedProcess[str]:
        captured_args.extend(args)
        return subprocess.CompletedProcess(args, 0, stdout="Created BMI 'bmi-123'", stderr="")

    monkeypatch.setattr(osac_cli.subprocess, "run", fake_run)
    cli = object.__new__(osac_cli.OsacCLI)
    cli.binary = "osac"
    cli._config_dir = "/tmp/osac-cli-test"
    return cli, captured_args


def test_create_baremetal_instance_sets_explicit_instance_type(monkeypatch: pytest.MonkeyPatch) -> None:
    cli, args = _make_cli(monkeypatch)

    bmi_id, warnings = cli.create_baremetal_instance(
        name="test-bmi", catalog_item="test-catalog", instance_type="compute.large"
    )

    assert bmi_id == "bmi-123"
    assert warnings == []
    assert args.count("--set") == 2
    assert "instance_type.name=compute.large" in args
    assert "instance_type.shared=true" in args


def test_create_baremetal_instance_can_use_catalog_or_template_type_default(monkeypatch: pytest.MonkeyPatch) -> None:
    cli, args = _make_cli(monkeypatch)

    cli.create_baremetal_instance(name="test-bmi", catalog_item="test-catalog")

    assert "--set" not in args
