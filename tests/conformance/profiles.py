"""Load the suite-level profile catalog and expand selected profiles."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path

import yaml

from tests.conformance.errors import ConfigError


@dataclass(frozen=True)
class ProfileDefinition:
    name: str
    selectors: tuple[str, ...]


def load_profiles(path: Path) -> dict[str, ProfileDefinition]:
    try:
        data = yaml.safe_load(path.read_text())
    except OSError as exc:
        raise ConfigError([f"{path} cannot be read: {exc}"]) from None
    except yaml.YAMLError as exc:
        raise ConfigError([f"{path} is not valid YAML: {exc}"]) from None
    if not isinstance(data, Mapping):
        raise ConfigError([f"{path} must map profile names to selector lists"])
    profiles: dict[str, ProfileDefinition] = {}
    for name, selectors in data.items():
        if not isinstance(name, str) or not name:
            raise ConfigError([f"{path} has a profile without a name"])
        if not isinstance(selectors, list) or any(not isinstance(item, str) or not item for item in selectors):
            raise ConfigError([f"{path}: profile {name} must map to a list of selectors"])
        profiles[name] = ProfileDefinition(name=name, selectors=tuple(selectors))
    return profiles


def expand_profiles(
    selected: Sequence[str], catalog: Mapping[str, ProfileDefinition]
) -> tuple[tuple[str, ...], tuple[str, ...]]:
    names: list[str] = ["generic"]
    for name in selected:
        if name not in names:
            names.append(name)
    missing = [name for name in names if name not in catalog]
    if missing:
        raise ConfigError([f"profile {name} is not shipped by this image" for name in missing])
    selectors: list[str] = []
    for name in names:
        for selector in catalog[name].selectors:
            if selector not in selectors:
                selectors.append(selector)
    return tuple(names), tuple(selectors)
