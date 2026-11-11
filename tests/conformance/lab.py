"""Read the lab facts the conformance report needs."""

from __future__ import annotations

import json
import ssl
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol
from urllib.request import Request, urlopen

from tests.conformance.area import AreaDeclaration
from tests.conformance.config import Config
from tests.conformance.errors import ConfigError
from tests.e2e.core.runner import run


@dataclass(frozen=True)
class ComponentIdentity:
    area: str
    fields: Mapping[str, Any]


@dataclass(frozen=True)
class LabFacts:
    enabled_services: tuple[str, ...]
    components: tuple[ComponentIdentity, ...]
    installed_release: str


class LabClient(Protocol):
    def capabilities(self, *, url: str, token: str, ca_file: Path | None) -> Mapping[str, Any]: ...
    def registrations(self, *, kubeconfig: Path) -> Mapping[str, Mapping[str, Any]]: ...
    def installed_release(self, *, kubeconfig: Path) -> str: ...


class HttpLabClient:
    """Read Capabilities over HTTP."""

    def capabilities(self, *, url: str, token: str, ca_file: Path | None) -> Mapping[str, Any]:
        endpoint = url.rstrip("/") + "/api/fulfillment/v1/capabilities"
        request = Request(endpoint, headers={"Authorization": f"Bearer {token}"})
        context = (
            ssl.create_default_context(cafile=str(ca_file)) if ca_file and endpoint.startswith("https://") else None
        )
        with urlopen(request, timeout=30, context=context) as response:
            payload = json.loads(response.read())
        if not isinstance(payload, Mapping):
            raise ValueError("Capabilities response is not an object")
        return payload

    def registrations(self, *, kubeconfig: Path) -> Mapping[str, Mapping[str, Any]]:
        raise NotImplementedError

    def installed_release(self, *, kubeconfig: Path) -> str:
        raise NotImplementedError


class KubectlLabClient(HttpLabClient):
    """Read Capabilities over HTTP and registrations and release from the lab hub."""

    def registrations(self, *, kubeconfig: Path) -> Mapping[str, Mapping[str, Any]]:
        output = run("kubectl", "--kubeconfig", str(kubeconfig), "get", "configmaps", "-n", "osac", "-o", "json")
        payload = json.loads(output)
        result: dict[str, Mapping[str, Any]] = {}
        for item in payload.get("items", []):
            name = item.get("metadata", {}).get("name")
            data = item.get("data")
            if isinstance(name, str) and isinstance(data, Mapping):
                result[name] = data
        return result

    def installed_release(self, *, kubeconfig: Path) -> str:
        output = run(
            "kubectl", "--kubeconfig", str(kubeconfig), "get", "secrets", "-n", "osac", "-l", "owner=helm", "-o", "json"
        )
        payload = json.loads(output)
        items = payload.get("items", [])
        if not items:
            raise ValueError("no Helm release Secret found on the lab hub")
        labels = items[0].get("metadata", {}).get("labels", {})
        name = labels.get("name")
        revision = labels.get("version")
        if not isinstance(name, str) or not isinstance(revision, str):
            raise ValueError("Helm release Secret has no name or revision label")
        return f"{name}-{revision}"


def read_lab(config: Config, client: LabClient, areas: Mapping[str, AreaDeclaration] | None = None) -> LabFacts:
    try:
        token = config.lab_token_file.read_text().strip()
    except OSError as exc:
        raise ConfigError([f"{config.lab_token_file} cannot be read: {exc}"]) from None
    try:
        capabilities = client.capabilities(url=config.lab_api_url, token=token, ca_file=config.lab_ca_file)
        registrations = client.registrations(kubeconfig=config.lab_kubeconfig)
        release = client.installed_release(kubeconfig=config.lab_kubeconfig)
    except ConfigError:
        raise
    except Exception as exc:
        raise ConfigError([f"lab read failed: {exc}"]) from None

    services = capabilities.get("enabled_services", [])
    if not isinstance(services, list) or any(not isinstance(item, str) for item in services):
        raise ConfigError(["Capabilities enabled_services must be a list of strings"])
    if areas:
        components = tuple(
            ComponentIdentity(area=area.name, fields=dict(registrations[area.registration_source]))
            for area in sorted(areas.values(), key=lambda item: item.name)
            if area.registration_source in registrations
        )
    else:
        components = tuple(
            ComponentIdentity(area=name, fields=dict(fields)) for name, fields in sorted(registrations.items())
        )
    return LabFacts(enabled_services=tuple(services), components=components, installed_release=release)
