"""Load and validate the conformance configuration."""

from __future__ import annotations

from collections.abc import Collection, Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

from tests.conformance.errors import ConfigError

_COMMON_KEYS = frozenset({"osacRelease", "profiles", "target", "timeouts"})
_TARGET_KEYS = frozenset({"namespace", "kubeconfig", "apiURL", "tokenFile", "caFile", "aap"})
_AAP_KEYS = frozenset({"url", "tokenFile"})


@dataclass(frozen=True)
class AapConfig:
    url: str
    token_file: Path


@dataclass(frozen=True)
class Config:
    osac_release: str
    profiles: tuple[str, ...]
    target_namespace: str
    target_kubeconfig: Path
    target_api_url: str
    target_token_file: Path
    target_ca_file: Path
    target_aap: AapConfig | None
    timeouts: Mapping[str, str]
    areas: Mapping[str, Mapping[str, Any]]
    raw: Mapping[str, Any]


def _mapping(value: object, name: str, checks: list[str]) -> Mapping[str, Any] | None:
    if not isinstance(value, Mapping):
        checks.append(f"{name} must be a mapping")
        return None
    return value


def _string(value: object, name: str, checks: list[str]) -> str | None:
    if not isinstance(value, str) or not value:
        checks.append(f"{name} must be a non-empty string")
        return None
    return value


def _path(value: object, name: str, checks: list[str]) -> Path | None:
    text = _string(value, name, checks)
    return Path(text) if text is not None else None


def _profiles(value: object, checks: list[str]) -> tuple[str, ...]:
    if value is None:
        return ("generic",)
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        checks.append("profiles must be a list of non-empty strings")
        return ()
    ordered: list[str] = []
    for item in value:
        if item not in ordered:
            ordered.append(item)
    return tuple(ordered)


def load_config(path: Path, *, area_names: Collection[str]) -> Config:
    checks: list[str] = []
    try:
        raw = yaml.safe_load(path.read_text())
    except FileNotFoundError:
        raise ConfigError([f"{path} does not exist"]) from None
    except OSError as exc:
        raise ConfigError([f"{path} cannot be read: {exc}"]) from None
    except yaml.YAMLError as exc:
        raise ConfigError([f"{path} is not valid YAML: {exc}"]) from None

    document = _mapping(raw, str(path), checks)
    if document is None:
        raise ConfigError(checks)

    for key in document:
        if key not in _COMMON_KEYS and key not in area_names:
            checks.append(f"{key} is not defined by this release")

    release = _string(document.get("osacRelease"), "osacRelease", checks)
    profiles = _profiles(document.get("profiles"), checks)
    target = _mapping(document.get("target"), "target", checks)
    namespace = "osac"
    if target is not None:
        for key in target:
            if key not in _TARGET_KEYS:
                checks.append(f"target.{key} is not defined by this release")
        if target.get("namespace") is not None:
            parsed_namespace = _string(target.get("namespace"), "target.namespace", checks)
            if parsed_namespace is not None:
                namespace = parsed_namespace
        kubeconfig = _path(target.get("kubeconfig"), "target.kubeconfig", checks)
        api_url = _string(target.get("apiURL"), "target.apiURL", checks)
        token_file = _path(target.get("tokenFile"), "target.tokenFile", checks)
        ca_file = _path(target.get("caFile"), "target.caFile", checks)
        aap: AapConfig | None = None
        if target.get("aap") is not None:
            aap_doc = _mapping(target.get("aap"), "target.aap", checks)
            if aap_doc is not None:
                for key in aap_doc:
                    if key not in _AAP_KEYS:
                        checks.append(f"target.aap.{key} is not defined by this release")
                aap_url = _string(aap_doc.get("url"), "target.aap.url", checks)
                aap_token = _path(aap_doc.get("tokenFile"), "target.aap.tokenFile", checks)
                if aap_url is not None and aap_token is not None:
                    aap = AapConfig(url=aap_url, token_file=aap_token)
    else:
        kubeconfig = api_url = token_file = ca_file = None  # type: ignore[assignment]
        aap = None

    timeouts_doc = document.get("timeouts", {})
    timeouts = _mapping(timeouts_doc, "timeouts", checks)
    if timeouts is not None and any(not isinstance(k, str) or not isinstance(v, str) for k, v in timeouts.items()):
        checks.append("timeouts must map keys to duration strings")
        timeouts = {}

    areas: dict[str, Mapping[str, Any]] = {}
    for name in area_names:
        section = document.get(name)
        if section is not None:
            section_doc = _mapping(section, name, checks)
            if section_doc is not None:
                areas[name] = section_doc

    if checks:
        raise ConfigError(checks)

    assert release is not None
    assert namespace
    assert kubeconfig is not None and api_url is not None and token_file is not None and ca_file is not None
    assert timeouts is not None
    return Config(
        osac_release=release,
        profiles=profiles,
        target_namespace=namespace,
        target_kubeconfig=kubeconfig,
        target_api_url=api_url,
        target_token_file=token_file,
        target_ca_file=ca_file,
        target_aap=aap,
        timeouts=timeouts,
        areas=areas,
        raw=document,
    )
