"""Load and validate the conformance configuration."""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from tests.conformance import yamlio
from tests.conformance.errors import ConfigError


@dataclass(frozen=True)
class Config:
    osac_release: str
    path: Path
    profiles: tuple[str, ...]
    target_namespace: str
    target_kubeconfig: Path
    target_api_url: str
    target_token_file: Path


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
    if text is None:
        return None
    if ".." in Path(text).parts:
        checks.append(f"{name} must not contain parent-directory traversal")
        return None
    return Path(os.path.normpath(text))


def _url(value: object, name: str, checks: list[str]) -> str | None:
    text = _string(value, name, checks)
    if text is None:
        return None
    try:
        hostname = urlparse(text).hostname
    except ValueError:
        hostname = None
    if not hostname:
        # A malformed URL would otherwise escape as a ValueError from every
        # later reader, including the redactor on the error path.
        checks.append(f"{name} must be an absolute URL with a host")
        return None
    return text


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


def load_config(path: Path) -> Config:
    checks: list[str] = []
    try:
        text = path.read_text()
    except FileNotFoundError:
        raise ConfigError([f"{path} does not exist"]) from None
    except OSError as exc:
        raise ConfigError([f"{path} cannot be read: {exc}"]) from None
    raw = yamlio.loads(text, str(path))

    document = _mapping(raw, str(path), checks)
    if document is None:
        raise ConfigError(checks)

    release = _string(document.get("osacRelease"), "osacRelease", checks)
    profiles = _profiles(document.get("profiles"), checks)
    target = _mapping(document.get("target"), "target", checks)
    namespace = "osac"
    if target is not None:
        if target.get("namespace") is not None:
            parsed_namespace = _string(target.get("namespace"), "target.namespace", checks)
            if parsed_namespace is not None:
                namespace = parsed_namespace
        kubeconfig = _path(target.get("kubeconfig"), "target.kubeconfig", checks)
        api_url = _url(target.get("apiURL"), "target.apiURL", checks)
        token_file = _path(target.get("tokenFile"), "target.tokenFile", checks)
    else:
        kubeconfig = api_url = token_file = None  # type: ignore[assignment]

    if checks:
        raise ConfigError(checks)

    assert release is not None
    assert namespace
    assert kubeconfig is not None and api_url is not None and token_file is not None
    return Config(
        osac_release=release,
        path=path,
        profiles=profiles,
        target_namespace=namespace,
        target_kubeconfig=kubeconfig,
        target_api_url=api_url,
        target_token_file=token_file,
    )
