"""Load the suite catalog of selectors and profiles."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path

from tests.conformance import yamlio
from tests.conformance.errors import ConfigError
from tests.conformance.model import Selector

_SUPPORTED_SELECTOR_TYPES = frozenset({"marker"})


@dataclass(frozen=True)
class ProfileDefinition:
    id: str
    selectors: tuple[str, ...]


@dataclass(frozen=True)
class Catalog:
    selectors: Mapping[str, Selector]
    profiles: Mapping[str, ProfileDefinition]


def _mapping(value: object, name: str) -> Mapping[str, object]:
    if not isinstance(value, Mapping):
        raise ConfigError([f"{name} must be a mapping"])
    return value


def _non_empty_string(value: object, name: str) -> str:
    if not isinstance(value, str) or not value:
        raise ConfigError([f"{name} must be a non-empty string"])
    return value


def _string_list(value: object, name: str) -> tuple[str, ...]:
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise ConfigError([f"{name} must be a list of non-empty strings"])
    return tuple(value)


def load_catalog(path: Path) -> Catalog:
    try:
        text = path.read_text()
    except OSError as exc:
        raise ConfigError([f"{path} cannot be read: {exc}"]) from None
    data = yamlio.loads(text, str(path))

    document = _mapping(data, str(path))
    raw_selectors = document.get("selectors")
    raw_profiles = document.get("profiles")
    if not isinstance(raw_selectors, list):
        raise ConfigError([f"{path}: selectors must be a list"])
    if not isinstance(raw_profiles, list):
        raise ConfigError([f"{path}: profiles must be a list"])

    selectors: dict[str, Selector] = {}
    for item in raw_selectors:
        entry = _mapping(item, f"{path}: selector")
        selector_id = _non_empty_string(entry.get("id"), f"{path}: selector id")
        selector_type = _non_empty_string(entry.get("type"), f"{path}: selector {selector_id} type")
        if selector_type not in _SUPPORTED_SELECTOR_TYPES:
            raise ConfigError([f"{path}: selector {selector_id} has unsupported type {selector_type}"])
        markers = _string_list(entry.get("markers"), f"{path}: selector {selector_id} markers")
        if selector_id in selectors:
            raise ConfigError([f"{path}: duplicate selector id {selector_id}"])
        selectors[selector_id] = Selector(id=selector_id, type=selector_type, markers=markers)

    profiles: dict[str, ProfileDefinition] = {}
    for item in raw_profiles:
        entry = _mapping(item, f"{path}: profile")
        profile_id = _non_empty_string(entry.get("id"), f"{path}: profile id")
        selector_ids = _string_list(entry.get("selectors"), f"{path}: profile {profile_id} selectors")
        if profile_id in profiles:
            raise ConfigError([f"{path}: duplicate profile id {profile_id}"])
        missing = [selector_id for selector_id in selector_ids if selector_id not in selectors]
        if missing:
            raise ConfigError(
                [f"{path}: profile {profile_id} references unknown selector {selector_id}" for selector_id in missing]
            )
        profiles[profile_id] = ProfileDefinition(id=profile_id, selectors=selector_ids)

    return Catalog(selectors=selectors, profiles=profiles)


def expand_profiles(selected: Sequence[str], catalog: Catalog) -> tuple[tuple[str, ...], tuple[Selector, ...]]:
    ids: list[str] = ["generic"]
    for profile_id in selected:
        if profile_id not in ids:
            ids.append(profile_id)
    missing = [profile_id for profile_id in ids if profile_id not in catalog.profiles]
    if missing:
        raise ConfigError([f"profile {profile_id} is not shipped by this image" for profile_id in missing])

    selectors: list[Selector] = []
    seen: set[str] = set()
    for profile_id in ids:
        for selector_id in catalog.profiles[profile_id].selectors:
            if selector_id not in seen:
                seen.add(selector_id)
                selectors.append(catalog.selectors[selector_id])
    return tuple(ids), tuple(selectors)
