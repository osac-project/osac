"""Contract tests for the conformance configuration loader."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.config import load_config
from tests.conformance.errors import ConfigError


def _write(path: Path, data: dict[str, object]) -> Path:
    path.write_text(yaml.safe_dump(data, sort_keys=False))
    return path


def _valid_data() -> dict[str, object]:
    return {
        "osacRelease": "1.4.0",
        "lab": {
            "selectors": ["vmaas", "netris"],
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
    assert cfg.selectors == ("generic", "vmaas", "netris")
    assert cfg.lab_api_url == "https://fulfillment.example.test"
    assert cfg.lab_kubeconfig == Path("/config/lab-hub.kubeconfig")
    assert cfg.timeouts == {"delete": "30m"}
    assert cfg.areas["networking"] == {"externalIPPoolCIDR": "198.51.100.0/28"}


def test_generic_is_first_and_not_duplicated(tmp_path: Path) -> None:
    data = _valid_data()
    data["lab"]["selectors"] = ["generic", "vmaas", "generic"]  # type: ignore[index]

    cfg = load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert cfg.selectors == ("generic", "vmaas")


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


def test_selectors_must_be_a_list_of_strings(tmp_path: Path) -> None:
    data = _valid_data()
    data["lab"]["selectors"] = "vmaas"  # type: ignore[index]

    with pytest.raises(ConfigError) as exc:
        load_config(_write(tmp_path / "conformance.yaml", data), area_names={"networking"})

    assert "lab.selectors" in "\n".join(exc.value.checks)


def test_missing_config_file_is_a_config_error(tmp_path: Path) -> None:
    with pytest.raises(ConfigError) as exc:
        load_config(tmp_path / "missing.yaml", area_names=set())

    assert "missing.yaml" in "\n".join(exc.value.checks)
