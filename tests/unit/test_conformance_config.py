"""Contract tests for the conformance configuration loader."""

from __future__ import annotations

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
        "profiles": ["vm", "netris"],
        "target": {
            "namespace": "osac-devel",
            "kubeconfig": "/config/target-hub.kubeconfig",
            "apiURL": "https://fulfillment.example.test",
            "tokenFile": "/config/target-token",
            "caFile": "/config/api-ca.pem",
        },
        "timeouts": {"delete": "30m"},
    }


def test_loads_common_keys(tmp_path: Path) -> None:
    cfg = load_config(_write(tmp_path / "conformance.yaml", _valid_data()))

    assert cfg.osac_release == "1.4.0"
    assert cfg.profiles == ("vm", "netris")
    assert cfg.target_namespace == "osac-devel"
    assert cfg.target_api_url == "https://fulfillment.example.test"
    assert cfg.target_kubeconfig == Path("/config/target-hub.kubeconfig")
    assert cfg.timeouts == {"delete": "30m"}


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


def test_optional_aap_is_parsed(tmp_path: Path) -> None:
    data = _valid_data()
    data["target"]["aap"] = {"url": "https://aap.example.test", "tokenFile": "/config/aap-token"}  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data))

    assert cfg.target_aap is not None
    assert cfg.target_aap.url == "https://aap.example.test"
    assert cfg.target_aap.token_file == Path("/config/aap-token")


def test_unknown_top_level_key_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    data["frobnicate"] = {}

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "frobnicate" in "\n".join(exc.value.checks)


def test_domain_section_is_rejected(tmp_path: Path) -> None:
    data = _valid_data()
    data["storage"] = {}

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "storage" in "\n".join(exc.value.checks)


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
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
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
        target_ca_file=ca,
        target_aap=None,
        timeouts={},
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
    data["target"]["caFile"] = "/config/./api-ca.pem"  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data))

    assert cfg.target_ca_file == Path("/config/api-ca.pem")


@pytest.mark.parametrize("key", ["kubeconfig", "tokenFile", "caFile"])
def test_parent_directory_traversal_is_a_config_error(tmp_path: Path, key: str) -> None:
    data = _valid_data()
    data["target"][key] = "/config/../outside/credential"  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert f"target.{key}" in "\n".join(exc.value.checks)


def test_aap_token_file_traversal_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    data["target"]["aap"] = {"url": "https://aap.example.test", "tokenFile": "/config/../aap-token"}  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "target.aap.tokenFile" in "\n".join(exc.value.checks)


def test_duplicate_mapping_keys_are_a_config_error(tmp_path: Path) -> None:
    path = tmp_path / "conformance.yaml"
    path.write_text(
        'osacRelease: "1.4.0"\n'
        "profiles: [vm]\n"
        "profiles: [netris]\n"
        "target:\n"
        "  kubeconfig: /config/hub\n"
        "  apiURL: https://fulfillment.example.test\n"
        "  tokenFile: /config/token\n"
        "  caFile: /config/ca.pem\n"
    )

    with pytest.raises(ConfigError, match="duplicate key"):
        load_config(path)


def test_unhashable_mapping_keys_are_a_config_error(tmp_path: Path) -> None:
    path = tmp_path / "conformance.yaml"
    path.write_text("? {a: b}\n: c\n")

    with pytest.raises(ConfigError, match="unhashable key"):
        load_config(path)


@pytest.mark.parametrize("url", ["https://[", "not-a-url"])
def test_target_api_url_must_parse(tmp_path: Path, url: str) -> None:
    data = _valid_data()
    data["target"]["apiURL"] = url  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "target.apiURL must be an absolute URL with a host" in "\n".join(exc.value.checks)


def test_aap_url_must_parse(tmp_path: Path) -> None:
    data = _valid_data()
    data["target"]["aap"] = {"url": "https://[", "tokenFile": "/config/aap-token"}  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data))

    assert "target.aap.url must be an absolute URL with a host" in "\n".join(exc.value.checks)
