"""Contract tests for the conformance CLI parser and early exits."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance import cli
from tests.conformance.cli import _parser, main
from tests.conformance.errors import ConfigError


def test_unknown_command_exits_2(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["frobnicate"]) == 2
    assert "frobnicate" in capsys.readouterr().err


def test_unknown_flag_exits_2(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["run", "--frobnicate"]) == 2
    assert "--frobnicate" in capsys.readouterr().err


def test_missing_config_exits_2_without_a_report(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    results = tmp_path / "results"

    assert main(["list", "--config", str(tmp_path / "missing.yaml"), "--results", str(results)]) == 2

    assert "missing.yaml" in capsys.readouterr().err
    assert not results.exists()


def test_config_errors_are_redacted(
    tmp_path: Path, capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    fake_value = "redaction-marker-d"
    fake_host = "api.internal.test"
    token = tmp_path / "token"
    token.write_text(fake_value)
    config = tmp_path / "conformance.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "target": {
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
        raise ConfigError([f"target read failed: {fake_host} rejected {fake_value}"])

    monkeypatch.setattr(cli, "_build_runtime", fail)

    assert main(["list", "--config", str(config)]) == 2

    err = capsys.readouterr().err
    assert fake_value not in err
    assert fake_host not in err
    assert "<redacted>" in err


def test_default_collection_root_covers_the_shipped_tests() -> None:
    args = _parser().parse_args(["list"])

    assert args.tree == "tests"


def test_relative_config_is_resolved_before_the_child_runs(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    for name in ("token", "ca.pem", "hub"):
        (tmp_path / name).write_text(name)
    config = tmp_path / "conformance.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "target": {
                    "kubeconfig": str(tmp_path / "hub"),
                    "apiURL": "http://target.example.test",
                    "tokenFile": str(tmp_path / "token"),
                    "caFile": str(tmp_path / "ca.pem"),
                },
            },
            sort_keys=False,
        )
    )
    catalog = tmp_path / "catalog.yaml"
    catalog.write_text(
        yaml.safe_dump(
            {
                "selectors": [{"id": "generic", "type": "marker", "markers": ["conformance_generic"]}],
                "profiles": [{"id": "generic", "selectors": ["generic"]}],
            },
            sort_keys=False,
        )
    )
    monkeypatch.chdir(tmp_path)
    args = _parser().parse_args(
        ["list", "--config", "conformance.yaml", "--results", str(tmp_path / "results"), "--catalog", str(catalog)]
    )

    runtime = cli._build_runtime(args)

    assert runtime.config.path == config.resolve()


def test_duplicate_keys_in_a_plan_are_a_config_error(tmp_path: Path) -> None:
    plan = tmp_path / "conformance-plan.yaml"
    plan.write_text("tests:\n  - a\ntests:\n  - b\n")

    with pytest.raises(ConfigError, match="duplicate key"):
        cli._plan_names(plan)


def test_a_malformed_url_does_not_replace_the_config_error(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    config = tmp_path / "conformance.yaml"
    config.write_text(
        "osacRelease: 1.4.0\n"
        "unknownKey: 1\n"
        "target:\n"
        "  kubeconfig: /config/hub\n"
        '  apiURL: "https://["\n'
        "  tokenFile: /config/token\n"
        "  caFile: /config/ca.pem\n"
    )

    assert main(["list", "--config", str(config), "--results", str(tmp_path / "results")]) == 2

    err = capsys.readouterr().err
    assert "unknownKey is not defined by this release" in err
    assert "absolute URL with a host" in err
