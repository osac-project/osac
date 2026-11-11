"""Contract tests for conformance area declarations."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.area import load_areas
from tests.conformance.errors import ConfigError


def _area(name: str = "networking") -> dict[str, object]:
    return {
        "name": name,
        "componentTests": ["TC-COMP-01"],
        "profiles": {"vm": "vmaas"},
        "identityFields": ["name", "ansibleRole"],
        "configurationErrors": ["vm needs evpn"],
        "supportCondition": "a conformant report is required",
        "registrationSource": "fabric-registration",
        "titles": {"TC-COMP-01": "component works"},
        "selectors": {"generic": ["TC-COMP-01"], "vmaas": ["TC-PROFILE-01"]},
        "checks": ["every selected profile passes"],
        "timeoutDefaults": {"vmReady": "15m"},
        "componentJobTimeout": "60m",
        "settingsSource": "network-fulfillment-ig",
        "evidence": ["VirtualMachine"],
        "referenceComponents": ["noop", "mock"],
    }


def _write(path: Path, data: object) -> Path:
    path.write_text(yaml.safe_dump(data, sort_keys=False))
    return path


def test_loads_a_valid_area(tmp_path: Path) -> None:
    _write(tmp_path / "networking.yaml", _area())

    areas = load_areas(tmp_path)

    assert set(areas) == {"networking"}
    area = areas["networking"]
    assert area.name == "networking"
    assert area.profiles == {"vm": "vmaas"}
    assert area.component_tests == ("TC-COMP-01",)
    assert area.selectors["vmaas"] == ("TC-PROFILE-01",)


def test_missing_directory_returns_no_areas(tmp_path: Path) -> None:
    assert load_areas(tmp_path / "missing") == {}


def test_missing_field_is_a_config_error(tmp_path: Path) -> None:
    data = _area()
    del data["componentTests"]
    _write(tmp_path / "networking.yaml", data)

    with pytest.raises(ConfigError) as exc:
        load_areas(tmp_path)

    assert "componentTests" in "\n".join(exc.value.checks)


def test_duplicate_area_name_is_a_config_error(tmp_path: Path) -> None:
    _write(tmp_path / "one.yaml", _area("networking"))
    _write(tmp_path / "two.yaml", _area("networking"))

    with pytest.raises(ConfigError) as exc:
        load_areas(tmp_path)

    assert "networking" in "\n".join(exc.value.checks)


def test_profile_mapping_must_be_a_mapping(tmp_path: Path) -> None:
    data = _area()
    data["profiles"] = ["vm"]
    _write(tmp_path / "networking.yaml", data)

    with pytest.raises(ConfigError) as exc:
        load_areas(tmp_path)

    assert "profiles" in "\n".join(exc.value.checks)
