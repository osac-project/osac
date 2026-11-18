"""Helm-render contract for the AAP bootstrap job's security boundary."""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[3]
AAP_CHART = REPO_ROOT / "osac-aap/charts/aap"


def render_aap(*extra_args: str) -> list[dict[str, Any]]:
    """Render the AAP chart with optional Kubernetes capability overrides."""
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
    """Render the bootstrap Job with ordinary Kubernetes capabilities."""
    return render_aap()


@pytest.fixture(scope="module")
def rendered_openshift_aap() -> list[dict[str, Any]]:
    """Render the bootstrap Job with the OpenShift security API available."""
    return render_aap("--api-versions", "security.openshift.io/v1")


def bootstrap_job(documents: list[dict[str, Any]]) -> dict[str, Any]:
    """Return the unique bootstrap Job from a rendered AAP chart."""
    jobs = [
        document
        for document in documents
        if document.get("kind") == "Job" and document.get("metadata", {}).get("name", "").endswith("-bootstrap")
    ]
    assert len(jobs) == 1, "expected one AAP bootstrap Job"
    return jobs[0]


def test_bootstrap_container_has_restricted_security_context(rendered_aap: list[dict[str, Any]]) -> None:
    """Require the bootstrap container to use the restricted pod security profile."""
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
    """Keep Ansible's writable home, temporary files, and passwd map on volumes."""
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


@pytest.mark.parametrize(("runtime_uid", "runtime_gid"), [(1000, 2000), (1000710000, 1000710000)])
def test_prepare_nss_maps_an_absent_runtime_uid_without_editing_passwd(
    rendered_aap: list[dict[str, Any]], tmp_path: Path, runtime_uid: int, runtime_gid: int
) -> None:
    """Generate an NSS passwd entry in /tmp for a UID missing from the image."""
    pod = bootstrap_job(rendered_aap)["spec"]["template"]["spec"]
    prepare_nss = next(container for container in pod["initContainers"] if container.get("name") == "prepare-nss")
    source_passwd = tmp_path / "image-passwd"
    original_passwd = "root:x:0:0:root:/root:/bin/bash\n"
    source_passwd.write_text(original_passwd, encoding="utf-8")
    mapped_passwd = tmp_path / "osac-passwd"
    script = prepare_nss["command"][2]
    script = script.replace("/tmp/osac-passwd", str(mapped_passwd)).replace("/etc/passwd", str(source_passwd))

    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    fake_id = bin_dir / "id"
    fake_id.write_text(
        "#!/bin/sh\n"
        "case \"$1\" in\n"
        f"  -u) printf '{runtime_uid}\\n' ;;\n"
        f"  -g) printf '{runtime_gid}\\n' ;;\n"
        "  *) exit 1 ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    fake_id.chmod(0o755)
    env = os.environ.copy()
    env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"

    result = subprocess.run(
        ["bash", "-c", script],
        capture_output=True,
        text=True,
        check=False,
        env=env,
        timeout=10,
    )

    assert result.returncode == 0, result.stderr
    assert source_passwd.read_text(encoding="utf-8") == original_passwd
    assert (
        f"osac-user-{runtime_uid}:x:{runtime_uid}:{runtime_gid}:OSAC execution user:/runner:/bin/bash"
        in mapped_passwd.read_text(encoding="utf-8").splitlines()
    )
    assert mapped_passwd.stat().st_mode & 0o777 == 0o644


def test_execution_environment_includes_nss_wrapper() -> None:
    """Ensure the bootstrap image installs the NSS module used by its passwd map."""
    execution_environment = yaml.safe_load(
        (REPO_ROOT / "osac-aap/execution-environment/execution-environment.yaml").read_text(encoding="utf-8")
    )
    assert "nss_wrapper" in execution_environment["dependencies"]["system"]


def test_bootstrap_containers_have_cpu_and_memory_resources(rendered_aap: list[dict[str, Any]]) -> None:
    """Require resource requests and limits on the NSS and Ansible containers."""
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
    """Leave the OpenShift-assigned filesystem group to its SCC."""
    pod = bootstrap_job(rendered_openshift_aap)["spec"]["template"]["spec"]
    assert "fsGroup" not in pod.get("securityContext", {})


def test_bootstrap_admin_secret_role_is_name_scoped_read_only(rendered_aap: list[dict[str, Any]]) -> None:
    """Limit bootstrap's Secret access to reading the single AAP admin password."""
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
