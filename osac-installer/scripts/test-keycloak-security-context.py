#!/usr/bin/env python3
"""Render contract for Keycloak's read-only container filesystems."""

from __future__ import annotations

import os
import pathlib
import shutil
import subprocess
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
INFRA_CHART = INSTALLER / "charts/osac-infra"
KIND_VALUES = INSTALLER / "values/dev/kind-infra.yaml"
PREREQUISITES = INSTALLER / "prerequisites/keycloak"


def render_infra() -> list[dict[str, Any]]:
    helm = os.environ.get("HELM_BIN", "helm")
    if shutil.which(helm) is None:
        raise RuntimeError("helm executable is required for the Keycloak security render contract")
    result = subprocess.run(
        [
            helm,
            "template",
            "security-test",
            str(INFRA_CHART),
            "--namespace",
            "osac-infra",
            "--values",
            str(KIND_VALUES),
            "--set",
            "keycloak.devFixtures.enabled=true",
        ],
        capture_output=True,
        text=True,
        check=False,
        timeout=300,
    )
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]


def load_yaml(path: pathlib.Path) -> list[dict[str, Any]]:
    return [doc for doc in yaml.safe_load_all(path.read_text(encoding="utf-8")) if isinstance(doc, dict)]


def pod_for(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [doc for doc in documents if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]["spec"]["template"]["spec"]


def container_named(pod: dict[str, Any], name: str, *, init: bool = False) -> dict[str, Any]:
    containers = pod.get("initContainers" if init else "containers", [])
    matches = [container for container in containers if container.get("name") == name]
    assert len(matches) == 1, f"expected one container {name}, found {len(matches)}"
    return matches[0]


def assert_secure(container: dict[str, Any], pod: dict[str, Any], name: str) -> None:
    security = container.get("securityContext", {})
    assert security.get("runAsNonRoot", pod.get("securityContext", {}).get("runAsNonRoot")) is True, (
        f"{name} must run as non-root"
    )
    assert security.get("allowPrivilegeEscalation") is False, f"{name} allows privilege escalation"
    assert security.get("readOnlyRootFilesystem") is True, f"{name} root filesystem must be read-only"
    assert "ALL" in security.get("capabilities", {}).get("drop", []), f"{name} must drop all capabilities"


def assert_empty_dir(pod: dict[str, Any], name: str) -> None:
    volumes = {volume["name"]: volume for volume in pod.get("volumes", [])}
    assert volumes.get(name, {}).get("emptyDir") is not None, f"{name} must be writable emptyDir storage"


def assert_mount(container: dict[str, Any], name: str, path: str, *, read_only: bool = False) -> None:
    matches = [
        mount
        for mount in container.get("volumeMounts", [])
        if mount.get("name") == name and mount.get("mountPath") == path
    ]
    assert len(matches) == 1, f"{container.get('name')} must mount {name} at {path}"
    assert matches[0].get("readOnly", False) is read_only, f"{path} readOnly={read_only} expected"


def check_database(pod: dict[str, Any], label: str, *, kind_profile: bool) -> None:
    pod_security = pod.get("securityContext", {})
    assert pod_security.get("runAsNonRoot") is True, f"{label} database pod must run as non-root"
    assert "runAsUser" not in pod_security, f"{label} database pod must preserve the image's PostgreSQL UID"
    assert_empty_dir(pod, "tmp")
    assert_empty_dir(pod, "postgres-run")

    password_generator = container_named(pod, "password-generator", init=True)
    assert_secure(password_generator, pod, f"{label} password-generator")
    assert_mount(password_generator, "tmp", "/tmp")
    if kind_profile:
        assert password_generator.get("securityContext", {}).get("runAsUser") == 1000

    server = container_named(pod, "server")
    assert_secure(server, pod, f"{label} database server")
    assert "runAsUser" not in server.get("securityContext", {}), (
        f"{label} database server must keep its image UID for existing PVC data"
    )
    assert_mount(server, "data", "/var/lib/pgsql/data")
    assert_mount(server, "postgres-run", "/var/run/postgresql")
    assert_mount(server, "tmp", "/tmp")


def check_service(pod: dict[str, Any], label: str, extra_env: list[dict[str, Any]] | None = None) -> None:
    assert_empty_dir(pod, "tmp")
    assert_empty_dir(pod, "keycloak-quarkus")

    quarkus_seed = container_named(pod, "seed-keycloak-quarkus", init=True)
    assert_secure(quarkus_seed, pod, f"{label} Keycloak Quarkus seed")
    assert_mount(quarkus_seed, "keycloak-quarkus", "/keycloak-quarkus")
    assert "cp -R /opt/keycloak/lib/quarkus/. /keycloak-quarkus/" in " ".join(quarkus_seed.get("command", []))

    quarkus_build = container_named(pod, "build-keycloak-quarkus", init=True)
    assert_secure(quarkus_build, pod, f"{label} Keycloak Quarkus build")
    assert_mount(quarkus_build, "keycloak-quarkus", "/opt/keycloak/lib/quarkus")
    assert_mount(quarkus_build, "keycloak-data", "/opt/keycloak/data")
    assert_mount(quarkus_build, "tmp", "/tmp")
    build_env = {env["name"]: env.get("value") for env in quarkus_build.get("env", [])}
    assert build_env.get("KC_DB") == "postgres", f"{label} Keycloak build must target PostgreSQL"
    assert quarkus_build.get("command", [])[-1:] == ["build"]

    wait_for_database = container_named(pod, "wait-for-database", init=True)
    assert_secure(wait_for_database, pod, f"{label} wait-for-database")
    assert_mount(wait_for_database, "tmp", "/tmp")

    resolver = container_named(pod, "resolve-realm-secrets", init=True)
    assert_secure(resolver, pod, f"{label} resolve-realm-secrets")
    assert_mount(resolver, "realm-raw", "/realm-raw", read_only=True)
    assert_mount(resolver, "realm", "/realm")
    assert_mount(resolver, "tmp", "/tmp")
    assert any(env.get("name") == "HOME" and env.get("value") == "/tmp" for env in resolver.get("env", []))

    assert_empty_dir(pod, "keycloak-data")
    keycloak = container_named(pod, "keycloak")
    assert_secure(keycloak, pod, f"{label} keycloak")
    assert_mount(keycloak, "keycloak-data", "/opt/keycloak/data")
    assert_mount(keycloak, "keycloak-quarkus", "/opt/keycloak/lib/quarkus", read_only=True)
    assert_mount(keycloak, "tmp", "/tmp")
    assert_mount(keycloak, "realm", "/opt/keycloak/data/import", read_only=True)
    assert "--optimized" in keycloak.get("args", []), f"{label} Keycloak must not build on startup"
    runtime_env = {env["name"]: env.get("value") for env in keycloak.get("env", [])}
    assert runtime_env.get("KC_DB") == build_env["KC_DB"]
    assert runtime_env.get("KC_FEATURES") == build_env.get("KC_FEATURES")
    for env in extra_env or []:
        assert build_env.get(env["name"]) == env.get("value"), (
            f"{label} Keycloak build must match extraEnv {env['name']}"
        )
        assert runtime_env.get(env["name"]) == env.get("value"), (
            f"{label} Keycloak runtime must match extraEnv {env['name']}"
        )


def main() -> None:
    rendered = render_infra()
    check_database(pod_for(rendered, "StatefulSet", "keycloak-database"), "chart", kind_profile=True)
    kind_values = yaml.safe_load(KIND_VALUES.read_text(encoding="utf-8"))
    check_service(
        pod_for(rendered, "Deployment", "keycloak-service"), "chart", kind_values["keycloak"].get("extraEnv", [])
    )

    standalone_database = load_yaml(PREREQUISITES / "database/statefulset.yaml")
    check_database(pod_for(standalone_database, "StatefulSet", "keycloak-database"), "standalone", kind_profile=False)
    standalone_service = load_yaml(PREREQUISITES / "service/deployment.yaml")
    check_service(pod_for(standalone_service, "Deployment", "keycloak-service"), "standalone")
    print("Keycloak security-context render contract passed.")


if __name__ == "__main__":
    main()
