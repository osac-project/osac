"""Helm-render contract for the AAP bootstrap job's security boundary."""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[3]
AAP_CHART = REPO_ROOT / "osac-aap/charts/aap"


def render_aap(*extra_args: str) -> list[dict[str, Any]]:
    if shutil.which("helm") is None:
        pytest.fail("helm executable is required for the AAP bootstrap render contract")
    result = subprocess.run(
        ["helm", "template", "security-test", str(AAP_CHART), "--namespace", "ansible-aap", *extra_args],
        capture_output=True,
        text=True,
        check=True,
        timeout=300,
    )
    return [document for document in yaml.safe_load_all(result.stdout) if isinstance(document, dict)]


@pytest.fixture(scope="module")
def rendered_aap() -> list[dict[str, Any]]:
    return render_aap()


@pytest.fixture(scope="module")
def rendered_openshift_aap() -> list[dict[str, Any]]:
    return render_aap("--api-versions", "security.openshift.io/v1")


def bootstrap_job(documents: list[dict[str, Any]]) -> dict[str, Any]:
    jobs = [
        document
        for document in documents
        if document.get("kind") == "Job" and document.get("metadata", {}).get("name", "").endswith("-bootstrap")
    ]
    assert len(jobs) == 1, "expected one AAP bootstrap Job"
    return jobs[0]


def test_bootstrap_container_has_restricted_security_context(rendered_aap: list[dict[str, Any]]) -> None:
    containers = bootstrap_job(rendered_aap)["spec"]["template"]["spec"]["containers"]
    bootstrap = next((container for container in containers if container.get("name") == "bootstrap"), None)
    assert bootstrap is not None, "bootstrap container is missing"
    security = bootstrap.get("securityContext", {})
    assert security.get("runAsNonRoot") is True, "bootstrap must run as a non-root image user"
    assert security.get("allowPrivilegeEscalation") is False, "bootstrap must not escalate privileges"
    assert security.get("readOnlyRootFilesystem") is True, "bootstrap root filesystem must be read-only"
    assert "ALL" in security.get("capabilities", {}).get("drop", []), "bootstrap must drop all capabilities"
    assert security.get("seccompProfile", {}).get("type") == "RuntimeDefault"


def test_bootstrap_writable_paths_are_volume_backed(rendered_aap: list[dict[str, Any]]) -> None:
    pod = bootstrap_job(rendered_aap)["spec"]["template"]["spec"]
    volumes = {volume["name"]: volume for volume in pod["volumes"]}
    assert volumes["runner"].get("emptyDir") is not None
    assert volumes["tmp"].get("emptyDir") is not None
    bootstrap = next(c for c in pod["containers"] if c.get("name") == "bootstrap")
    mounts = {mount["name"]: mount["mountPath"] for mount in bootstrap["volumeMounts"]}
    assert mounts["runner"] == "/runner"
    assert mounts["tmp"] == "/tmp"
    assert pod.get("securityContext", {}).get("fsGroup") == 0

    prepare_nss = next(c for c in pod["initContainers"] if c.get("name") == "prepare-nss")
    nss_security = prepare_nss.get("securityContext", {})
    assert nss_security.get("readOnlyRootFilesystem") is True
    assert nss_security.get("runAsNonRoot") is True
    assert any(
        mount.get("name") == "tmp" and mount.get("mountPath") == "/tmp" for mount in prepare_nss.get("volumeMounts", [])
    )
    assert "/tmp/osac-passwd" in prepare_nss["command"][2]

    bootstrap_env = {entry["name"]: entry.get("value") for entry in bootstrap.get("env", [])}
    assert bootstrap_env["NSS_WRAPPER_PASSWD"] == "/tmp/osac-passwd"
    assert bootstrap_env["NSS_WRAPPER_GROUP"] == "/etc/group"
    assert bootstrap_env["LD_PRELOAD"] == "/usr/lib64/libnss_wrapper.so"
    assert bootstrap_env["EP_ON_ERROR"] == "fail"


def test_bootstrap_containers_have_cpu_and_memory_resources(rendered_aap: list[dict[str, Any]]) -> None:
    pod = bootstrap_job(rendered_aap)["spec"]["template"]["spec"]
    containers = [next(c for c in pod["initContainers"] if c.get("name") == "prepare-nss")]
    containers.append(next(c for c in pod["containers"] if c.get("name") == "bootstrap"))

    for container in containers:
        resources = container.get("resources", {})
        for resource_class in ("requests", "limits"):
            assert resources.get(resource_class, {}).get("cpu"), (
                f"{container['name']} must declare {resource_class}.cpu"
            )
            assert resources.get(resource_class, {}).get("memory"), (
                f"{container['name']} must declare {resource_class}.memory"
            )


def test_bootstrap_leaves_openshift_fs_group_to_scc(rendered_openshift_aap: list[dict[str, Any]]) -> None:
    pod = bootstrap_job(rendered_openshift_aap)["spec"]["template"]["spec"]
    assert "fsGroup" not in pod.get("securityContext", {})


def test_bootstrap_admin_secret_role_is_name_scoped_read_only(rendered_aap: list[dict[str, Any]]) -> None:
    roles = [
        document
        for document in rendered_aap
        if document.get("kind") == "Role"
        and document.get("metadata", {}).get("name", "").endswith("-read-admin-secret")
    ]
    assert len(roles) == 1, "expected one name-scoped admin Secret Role"
    assert roles[0].get("rules") == [
        {"apiGroups": [""], "resources": ["secrets"], "resourceNames": ["osac-aap-admin-password"], "verbs": ["get"]}
    ], "bootstrap must only read the single generated admin password Secret"
