#!/usr/bin/env python3
"""Check rootless execution settings for osac-deps CLI hook Jobs."""

from __future__ import annotations

import os
import pathlib
import subprocess
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
DEPS_CHART = INSTALLER / "charts/osac-deps"
KIND_VALUES = INSTALLER / "values/dev/kind-infra.yaml"
HOOK_JOBS = ("osac-deps-approve-aap-installplan", "osac-deps-wait-aap", "osac-deps-wait-cert-manager")


def render(*values: pathlib.Path, set_overrides: tuple[str, ...] = ()) -> list[dict[str, Any]]:
    command = [os.environ.get("HELM_BIN", "helm"), "template", "osac-deps", str(DEPS_CHART), "--namespace", "osac-deps"]
    for values_file in values:
        command.extend(["--values", str(values_file)])
    for override in set_overrides:
        command.extend(["--set", override])
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {result.stderr}")
    return [document for document in yaml.safe_load_all(result.stdout) if isinstance(document, dict)]


def assert_container_is_non_root(container: dict[str, Any], pod_security: dict[str, Any], name: str) -> None:
    security = container.get("securityContext", {})
    run_as_non_root = security.get("runAsNonRoot", pod_security.get("runAsNonRoot"))
    assert run_as_non_root is True, f"{name} must require a non-root UID"
    run_as_user = security.get("runAsUser", pod_security.get("runAsUser"))
    assert run_as_user is None or run_as_user > 0, f"{name} must not run as UID 0"


def assert_hook_security(documents: list[dict[str, Any]], *, kind: bool) -> None:
    for name in HOOK_JOBS:
        matches = [
            document
            for document in documents
            if document.get("kind") == "Job" and document.get("metadata", {}).get("name") == name
        ]
        assert len(matches) == 1, f"expected one Job/{name}, found {len(matches)}"
        pod = matches[0]["spec"]["template"]["spec"]
        pod_security = pod.get("securityContext", {})
        assert pod_security.get("runAsNonRoot") is True, f"Job/{name} must require a non-root UID"
        if kind:
            assert pod_security.get("runAsUser") == 1000, f"Kind Job/{name} must use the configured non-root UID"
            assert pod_security.get("fsGroup") == 1000, f"Kind Job/{name} must use the configured writable volume group"
        else:
            assert "runAsUser" not in pod_security, f"OpenShift Job/{name} must leave UID allocation to SCC"

        containers = pod.get("containers", [])
        assert len(containers) == 1, f"Job/{name} must have one CLI container"
        container = containers[0]
        security = container.get("securityContext", {})
        assert_container_is_non_root(container, pod_security, f"Job/{name}")
        assert security.get("allowPrivilegeEscalation") is False, f"Job/{name} allows privilege escalation"
        assert security.get("readOnlyRootFilesystem") is True, f"Job/{name} root filesystem must be read-only"
        assert "ALL" in security.get("capabilities", {}).get("drop", []), f"Job/{name} must drop capabilities"
        assert not security.get("capabilities", {}).get("add", []), f"Job/{name} must not add capabilities"


def assert_standalone_approval_job() -> None:
    manifest = INSTALLER / "prerequisites/aap-installplan-approve.yaml"
    loaded = yaml.safe_load_all(manifest.read_text(encoding="utf-8"))
    documents = [document for document in loaded if isinstance(document, dict)]
    jobs = [
        document
        for document in documents
        if document.get("kind") == "Job" and document.get("metadata", {}).get("name") == "approve-aap-installplan"
    ]
    assert len(jobs) == 1, "expected one standalone InstallPlan approval Job"
    pod = jobs[0]["spec"]["template"]["spec"]
    pod_security = pod.get("securityContext", {})
    assert pod_security.get("runAsNonRoot") is True
    container = pod["containers"][0]
    security = container.get("securityContext", {})
    assert_container_is_non_root(container, pod_security, "standalone InstallPlan approval Job")
    assert security.get("allowPrivilegeEscalation") is False
    assert security.get("readOnlyRootFilesystem") is True
    assert "ALL" in security.get("capabilities", {}).get("drop", [])
    assert not security.get("capabilities", {}).get("add", [])
    assert security.get("seccompProfile", {}).get("type") == "RuntimeDefault"


def main() -> None:
    assert_hook_security(render(), kind=False)
    kind_render = render(KIND_VALUES, set_overrides=("aapOperator.enabled=true", "certManager.enabled=true"))
    assert_hook_security(kind_render, kind=True)
    assert_standalone_approval_job()
    print("osac-deps hook security render contract passed.")


if __name__ == "__main__":
    main()
