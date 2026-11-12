"""Contract tests for conformance domain declarations."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.domain import load_domains
from tests.conformance.errors import ConfigError


def _domain(name: str = "networking") -> dict[str, object]:
    return {
        "name": name,
        "componentTests": ["TC-COMP-01"],
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


def test_loads_a_valid_domain(tmp_path: Path) -> None:
    _write(tmp_path / "networking.yaml", _domain())

    domains = load_domains(tmp_path)

    assert set(domains) == {"networking"}
    domain = domains["networking"]
    assert domain.name == "networking"
    assert domain.component_tests == ("TC-COMP-01",)
    assert domain.selectors["vmaas"] == ("TC-PROFILE-01",)


def test_missing_directory_returns_no_domains(tmp_path: Path) -> None:
    assert load_domains(tmp_path / "missing") == {}


def test_missing_field_is_a_config_error(tmp_path: Path) -> None:
    data = _domain()
    del data["componentTests"]
    _write(tmp_path / "networking.yaml", data)

    with pytest.raises(ConfigError) as exc:
        load_domains(tmp_path)

    assert "componentTests" in "\n".join(exc.value.checks)


def test_duplicate_domain_name_is_a_config_error(tmp_path: Path) -> None:
    _write(tmp_path / "one.yaml", _domain("networking"))
    _write(tmp_path / "two.yaml", _domain("networking"))

    with pytest.raises(ConfigError) as exc:
        load_domains(tmp_path)

    assert "networking" in "\n".join(exc.value.checks)


def test_unknown_domain_field_is_a_config_error(tmp_path: Path) -> None:
    data = _domain()
    data["profiles"] = {"vm": "vmaas"}
    _write(tmp_path / "networking.yaml", data)

    with pytest.raises(ConfigError) as exc:
        load_domains(tmp_path)

    assert "profiles" in "\n".join(exc.value.checks)
