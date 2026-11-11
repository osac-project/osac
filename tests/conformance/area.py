"""Load area declarations for the conformance suite."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

from tests.conformance.errors import ConfigError

_LIST_FIELDS = ("componentTests", "identityFields", "configurationErrors", "checks", "evidence", "referenceComponents")
_STRING_FIELDS = ("supportCondition", "registrationSource", "componentJobTimeout", "settingsSource")
_MAPPING_LIST_FIELDS = ("selectors",)
_MAPPING_STRING_FIELDS = ("profiles", "titles", "timeoutDefaults")


@dataclass(frozen=True)
class AreaDeclaration:
    name: str
    component_tests: tuple[str, ...]
    profiles: Mapping[str, str]
    identity_fields: tuple[str, ...]
    configuration_errors: tuple[str, ...]
    support_condition: str
    registration_source: str
    titles: Mapping[str, str]
    selectors: Mapping[str, tuple[str, ...]]
    checks: tuple[str, ...]
    timeout_defaults: Mapping[str, str]
    component_job_timeout: str
    settings_source: str
    evidence: tuple[str, ...]
    reference_components: tuple[str, ...]


def _list_of_strings(value: object, field: str, filename: str) -> tuple[str, ...]:
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise ConfigError([f"{filename}: {field} must be a list of non-empty strings"])
    return tuple(value)


def _mapping_of_strings(value: object, field: str, filename: str) -> Mapping[str, str]:
    if not isinstance(value, Mapping) or any(
        not isinstance(k, str) or not isinstance(v, str) for k, v in value.items()
    ):
        raise ConfigError([f"{filename}: {field} must map strings to strings"])
    return dict(value)


def _mapping_of_lists(value: object, field: str, filename: str) -> Mapping[str, tuple[str, ...]]:
    if not isinstance(value, Mapping):
        raise ConfigError([f"{filename}: {field} must be a mapping"])
    result: dict[str, tuple[str, ...]] = {}
    for key, item in value.items():
        if not isinstance(key, str) or not isinstance(item, list) or any(not isinstance(v, str) or not v for v in item):
            raise ConfigError([f"{filename}: {field} must map strings to lists of strings"])
        result[key] = tuple(item)
    return result


def _parse(path: Path) -> AreaDeclaration:
    try:
        data: Any = yaml.safe_load(path.read_text())
    except OSError as exc:
        raise ConfigError([f"{path.name}: cannot read: {exc}"]) from None
    except yaml.YAMLError as exc:
        raise ConfigError([f"{path.name}: invalid YAML: {exc}"]) from None
    if not isinstance(data, Mapping):
        raise ConfigError([f"{path.name}: area declaration must be a mapping"])

    checks: list[str] = []
    name = data.get("name")
    if not isinstance(name, str) or not name:
        checks.append("name must be a non-empty string")
    for field in _LIST_FIELDS:
        if field not in data:
            checks.append(f"{field} is required")
    for field in _STRING_FIELDS:
        if field not in data:
            checks.append(f"{field} is required")
    for field in (*_MAPPING_LIST_FIELDS, *_MAPPING_STRING_FIELDS):
        if field not in data:
            checks.append(f"{field} is required")
    if checks:
        raise ConfigError([f"{path.name}: {check}" for check in checks])

    return AreaDeclaration(
        name=name,
        component_tests=_list_of_strings(data["componentTests"], "componentTests", path.name),
        profiles=_mapping_of_strings(data["profiles"], "profiles", path.name),
        identity_fields=_list_of_strings(data["identityFields"], "identityFields", path.name),
        configuration_errors=_list_of_strings(data["configurationErrors"], "configurationErrors", path.name),
        support_condition=str(data["supportCondition"]),
        registration_source=str(data["registrationSource"]),
        titles=_mapping_of_strings(data["titles"], "titles", path.name),
        selectors=_mapping_of_lists(data["selectors"], "selectors", path.name),
        checks=_list_of_strings(data["checks"], "checks", path.name),
        timeout_defaults=_mapping_of_strings(data["timeoutDefaults"], "timeoutDefaults", path.name),
        component_job_timeout=str(data["componentJobTimeout"]),
        settings_source=str(data["settingsSource"]),
        evidence=_list_of_strings(data["evidence"], "evidence", path.name),
        reference_components=_list_of_strings(data["referenceComponents"], "referenceComponents", path.name),
    )


def load_areas(path: Path) -> dict[str, AreaDeclaration]:
    """Return area declarations found under ``path`` keyed by area name."""
    if not path.exists():
        return {}
    areas: dict[str, AreaDeclaration] = {}
    for source in sorted((*path.glob("*.yaml"), *path.glob("*.yml"))):
        area = _parse(source)
        if area.name in areas:
            raise ConfigError([f"{source.name}: duplicate area name {area.name}"])
        areas[area.name] = area
    return areas
