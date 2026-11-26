"""Contract tests for the conformance configuration loader."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import replace
from pathlib import Path

import pytest
import yaml

from tests.conformance.cli import redactor_for_config
from tests.conformance.config import Config, load_config
from tests.conformance.errors import ConfigError

_REDACTION_MARKER_D = "redaction-marker-d"
_REDACTION_MARKER_B = "redaction-marker-b"


def _write(path: Path, data: dict[str, object]) -> Path:
    path.write_text(yaml.safe_dump(data, sort_keys=False))
    return path


def _valid_data() -> dict[str, object]:
    return {
        "osacRelease": "1.4.0",
        "profiles": ["vm", "network"],
        "target": {
            "namespace": "osac-devel",
            "kubeconfig": "/config/target-hub.kubeconfig",
            "apiURL": "https://fulfillment.example.test",
            "tokenFile": "/config/target-token",
        },
    }


def test_loads_common_keys(tmp_path: Path) -> None:
    cfg = load_config(_write(tmp_path / "conformance.yaml", _valid_data()))

    assert cfg.osac_release == "1.4.0"
    assert cfg.profiles == ("vm", "network")
    assert cfg.target_namespace == "osac-devel"
    assert cfg.target_api_url == "https://fulfillment.example.test"
    assert cfg.target_kubeconfig == Path("/config/target-hub.kubeconfig")


def test_target_namespace_defaults_to_osac(tmp_path: Path) -> None:
    data = _valid_data()
    del data["target"]["namespace"]  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data))

    assert cfg.target_namespace == "osac"


def test_profiles_are_deduplicated(tmp_path: Path) -> None:
    data = _valid_data()
    data["profiles"] = ["generic", "vm", "generic"]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data))

    assert cfg.profiles == ("generic", "vm")


def _add_unknown_top_level_keys(data: dict[str, object]) -> None:
    data["frobnicate"] = {}
    data["timeouts"] = {"delete": "30m"}


def _add_unknown_target_keys(data: dict[str, object]) -> None:
    data["target"]["caFile"] = "/config/api-ca.pem"  # type: ignore[index]
    data["target"]["aap"] = {"url": "https://aap.example.test", "tokenFile": "/config/aap-token"}  # type: ignore[index]


@pytest.mark.parametrize("mutate", [_add_unknown_top_level_keys, _add_unknown_target_keys], ids=["top-level", "target"])
def test_unknown_keys_are_ignored(tmp_path: Path, mutate: Callable[[dict[str, object]], None]) -> None:
    baseline = load_config(_write(tmp_path / "baseline.yaml", _valid_data()))
    data = _valid_data()
    mutate(data)
    loaded = load_config(_write(tmp_path / "conformance.yaml", data))

    # Only the config file path differs, so every parsed field must match the
    # baseline: the unknown keys were dropped, not partially interpreted.
    assert replace(loaded, path=baseline.path) == baseline


def test_missing_required_target_key_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    del data["target"]["apiURL"]  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "target.apiURL" in "\n".join(exc.value.checks)


def test_profiles_must_be_a_list_of_strings(tmp_path: Path) -> None:
    data = _valid_data()
    data["profiles"] = "vm"

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "profiles" in "\n".join(exc.value.checks)


def test_missing_config_file_is_a_config_error(tmp_path: Path) -> None:
    with pytest.raises(ConfigError) as exc:
        load_config(tmp_path / "missing.yaml")

    assert "missing.yaml" in "\n".join(exc.value.checks)


def test_redactor_covers_api_and_kubeconfig_credentials(tmp_path: Path) -> None:
    token = tmp_path / "token"
    token.write_text(_REDACTION_MARKER_D)
    hub = tmp_path / "hub.kubeconfig"
    hub.write_text(
        yaml.safe_dump(
            {
                "users": [{"user": {"token": _REDACTION_MARKER_D, "password": _REDACTION_MARKER_B}}],
                "clusters": [{"cluster": {"server": "https://api.hub.example.test"}}],
            }
        )
    )
    config = Config(
        osac_release="1.4.0",
        path=tmp_path / "conformance.yaml",
        profiles=("generic",),
        target_namespace="osac",
        target_kubeconfig=hub,
        target_api_url="https://api.target.example.test",
        target_token_file=token,
    )

    text = redactor_for_config(config).text(
        f"{_REDACTION_MARKER_D} {_REDACTION_MARKER_B} api.hub.example.test api.target.example.test"
    )

    assert _REDACTION_MARKER_D not in text
    assert _REDACTION_MARKER_B not in text
    assert "api.hub.example.test" not in text
    assert "api.target.example.test" not in text


def test_configured_paths_are_canonicalized(tmp_path: Path) -> None:
    data = _valid_data()
    data["target"]["tokenFile"] = "/config/./target-token"  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data))

    assert cfg.target_token_file == Path("/config/target-token")


@pytest.mark.parametrize("key", ["kubeconfig", "tokenFile"])
def test_parent_directory_traversal_is_a_config_error(tmp_path: Path, key: str) -> None:
    data = _valid_data()
    data["target"][key] = "/config/../outside/credential"  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert f"target.{key}" in "\n".join(exc.value.checks)


@pytest.mark.parametrize("url", ["https://[", "not-a-url"])
def test_target_api_url_must_parse(tmp_path: Path, url: str) -> None:
    data = _valid_data()
    data["target"]["apiURL"] = url  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "target.apiURL must be an absolute URL with a host" in "\n".join(exc.value.checks)
