"""Read the target facts the conformance report needs."""

from __future__ import annotations

import json
import ssl
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol
from urllib.request import Request, urlopen

from tests.conformance.config import Config
from tests.conformance.domain import DomainDeclaration
from tests.conformance.errors import ConfigError
from tests.e2e.core.runner import run


@dataclass(frozen=True)
class ComponentIdentity:
    domain: str
    fields: Mapping[str, Any]


@dataclass(frozen=True)
class TargetFacts:
    enabled_services: tuple[str, ...]
    components: tuple[ComponentIdentity, ...]
    installed_release: str


class TargetClient(Protocol):
    def capabilities(self, *, url: str, token: str, ca_file: Path | None) -> Mapping[str, Any]: ...
    def registrations(self, *, kubeconfig: Path, namespace: str) -> Mapping[str, Mapping[str, Any]]: ...
    def installed_release(self, *, kubeconfig: Path, namespace: str) -> str: ...


class HttpTargetClient:
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

    def registrations(self, *, kubeconfig: Path, namespace: str) -> Mapping[str, Mapping[str, Any]]:
        raise NotImplementedError

    def installed_release(self, *, kubeconfig: Path, namespace: str) -> str:
        raise NotImplementedError


class KubectlTargetClient(HttpTargetClient):
    """Read Capabilities over HTTP and registrations and release from the target hub."""

    def registrations(self, *, kubeconfig: Path, namespace: str) -> Mapping[str, Mapping[str, Any]]:
        output = run("kubectl", "--kubeconfig", str(kubeconfig), "get", "configmaps", "-n", namespace, "-o", "json")
        payload = json.loads(output)
        result: dict[str, Mapping[str, Any]] = {}
        for item in payload.get("items", []):
            name = item.get("metadata", {}).get("name")
            data = item.get("data")
            if isinstance(name, str) and isinstance(data, Mapping):
                result[name] = data
        return result

    def installed_release(self, *, kubeconfig: Path, namespace: str) -> str:
        output = run(
            "kubectl",
            "--kubeconfig",
            str(kubeconfig),
            "get",
            "secrets",
            "-n",
            namespace,
            "-l",
            "owner=helm",
            "-o",
            "json",
        )
        payload = json.loads(output)
        items = payload.get("items", [])
        if not items:
            raise ValueError("no Helm release Secret found on the target hub")
        labels = items[0].get("metadata", {}).get("labels", {})
        name = labels.get("name")
        revision = labels.get("version")
        if not isinstance(name, str) or not isinstance(revision, str):
            raise ValueError("Helm release Secret has no name or revision label")
        return f"{name}-{revision}"


def read_target(
    config: Config, client: TargetClient, domains: Mapping[str, DomainDeclaration] | None = None
) -> TargetFacts:
    try:
        token = config.target_token_file.read_text().strip()
    except OSError as exc:
        raise ConfigError([f"{config.target_token_file} cannot be read: {exc}"]) from None
    try:
        capabilities = client.capabilities(url=config.target_api_url, token=token, ca_file=config.target_ca_file)
        registrations = client.registrations(kubeconfig=config.target_kubeconfig, namespace=config.target_namespace)
        release = client.installed_release(kubeconfig=config.target_kubeconfig, namespace=config.target_namespace)
    except ConfigError:
        raise
    except Exception as exc:
        raise ConfigError([f"target read failed: {exc}"]) from None

    services = capabilities.get("enabled_services", [])
    if not isinstance(services, list) or any(not isinstance(item, str) for item in services):
        raise ConfigError(["Capabilities enabled_services must be a list of strings"])
    if domains:
        components = tuple(
            ComponentIdentity(domain=domain.name, fields=dict(registrations[domain.registration_source]))
            for domain in sorted(domains.values(), key=lambda item: item.name)
            if domain.registration_source in registrations
        )
    else:
        components = tuple(
            ComponentIdentity(domain=name, fields=dict(fields)) for name, fields in sorted(registrations.items())
        )
    return TargetFacts(enabled_services=tuple(services), components=components, installed_release=release)
