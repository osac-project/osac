"""Contract tests for the conformance configuration loader."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.cli import redactor_for_config
from tests.conformance.config import Config, load_config
from tests.conformance.errors import ConfigError


def _write(path: Path, data: dict[str, object]) -> Path:
    path.write_text(yaml.safe_dump(data, sort_keys=False))
    return path


def _valid_data() -> dict[str, object]:
    return {
        "osacRelease": "1.4.0",
        "profiles": ["vm", "netris"],
        "lab": {
            "kubeconfig": "/config/lab-hub.kubeconfig",
            "apiURL": "https://fulfillment.example.test",
            "tokenFile": "/config/admin-token",
            "caFile": "/config/api-ca.pem",
        },
        "timeouts": {"delete": "30m"},
        "networking": {"externalIPPoolCIDR": "198.51.100.0/28"},
    }


def test_loads_common_keys_and_area_sections(tmp_path: Path) -> None:
    cfg = load_config(_write(tmp_path / "conformance.yaml", _valid_data()), area_names={"networking"})

    assert cfg.osac_release == "1.4.0"
    assert cfg.profiles == ("vm", "netris")
    assert cfg.lab_api_url == "https://fulfillment.example.test"
    assert cfg.lab_kubeconfig == Path("/config/lab-hub.kubeconfig")
    assert cfg.timeouts == {"delete": "30m"}
    assert cfg.areas["networking"] == {"externalIPPoolCIDR": "198.51.100.0/28"}


def test_profiles_are_deduplicated(tmp_path: Path) -> None:
    data = _valid_data()
    data["profiles"] = ["generic", "vm", "generic"]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert cfg.profiles == ("generic", "vm")


def test_optional_aap_is_parsed(tmp_path: Path) -> None:
    data = _valid_data()
    data["lab"]["aap"] = {"url": "https://aap.example.test", "tokenFile": "/config/aap-token"}  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert cfg.lab_aap is not None
    assert cfg.lab_aap.url == "https://aap.example.test"
    assert cfg.lab_aap.token_file == Path("/config/aap-token")


def test_unknown_top_level_key_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    data["frobnicate"] = {}

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert "frobnicate" in "\n".join(exc.value.checks)


def test_unknown_area_section_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    data["storage"] = {}

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert "storage" in "\n".join(exc.value.checks)


def test_missing_required_lab_key_is_a_config_error(tmp_path: Path) -> None:
    data = _valid_data()
    del data["lab"]["apiURL"]  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert "lab.apiURL" in "\n".join(exc.value.checks)


def test_profiles_must_be_a_list_of_strings(tmp_path: Path) -> None:
    data = _valid_data()
    data["profiles"] = "vm"

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert "profiles" in "\n".join(exc.value.checks)


def test_missing_config_file_is_a_config_error(tmp_path: Path) -> None:
    with pytest.raises(ConfigError) as exc:
        load_config(tmp_path / "missing.yaml", area_names=set())

    assert "missing.yaml" in "\n".join(exc.value.checks)


def test_redactor_covers_api_and_kubeconfig_credentials(tmp_path: Path) -> None:
    token = tmp_path / "admin-token"
    token.write_text("admin-token-value")
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
    hub = tmp_path / "hub.kubeconfig"
    hub.write_text(
        yaml.safe_dump(
            {
                "users": [{"user": {"token": "hub-token-value", "password": "hub-password-value"}}],
                "clusters": [{"cluster": {"server": "https://api.hub.example.test"}}],
            }
        )
    )
    config = Config(
        osac_release="1.4.0",
        profiles=("generic",),
        lab_kubeconfig=hub,
        lab_api_url="https://api.lab.example.test",
        lab_token_file=token,
        lab_ca_file=ca,
        lab_aap=None,
        timeouts={},
        areas={},
        raw={},
    )

    text = redactor_for_config(config).text(
        "admin-token-value hub-token-value hub-password-value api.hub.example.test api.lab.example.test"
    )

    assert "admin-token-value" not in text
    assert "hub-token-value" not in text
    assert "hub-password-value" not in text
    assert "api.hub.example.test" not in text
    assert "api.lab.example.test" not in text
