"""Shared fixtures and configuration for storage E2E tests."""

from __future__ import annotations

import os
import textwrap

import pytest

from tests.e2e.core.runner import env, run_unchecked

# ---------------------------------------------------------------------------
# Shared K8s manifest templates used across storage test files.
# ---------------------------------------------------------------------------

NAMESPACE_MANIFEST = textwrap.dedent("""\
    apiVersion: v1
    kind: Namespace
    metadata:
      name: {name}
""")

TENANT_MANIFEST = textwrap.dedent("""\
    apiVersion: osac.openshift.io/v1alpha1
    kind: Tenant
    metadata:
      name: {name}
      namespace: {namespace}
    spec: {{}}
""")


def _storage_controller_configured(namespace: str) -> bool:
    """Auto-detect whether the storage controller is enabled.

    Checks two sources: direct env vars on the operator deployment
    and envFrom secrets (e.g. osac-config) that may inject the var at runtime.
    """
    output, rc = run_unchecked(
        "kubectl",
        "--as",
        "system:admin",
        "get",
        "deployment",
        "-n",
        namespace,
        "-l",
        "control-plane=controller-manager",
        "-o",
        "jsonpath={.items[*].spec.template.spec.containers[*].env[*].name}",
    )
    if rc == 0 and (
        "OSAC_STORAGE_BACKEND_AAP_PROVISION_TEMPLATE" in output or "OSAC_ENABLE_STORAGE_CONTROLLER" in output
    ):
        return True

    # Check envFrom secrets for the storage env var
    secret_names, rc = run_unchecked(
        "kubectl",
        "--as",
        "system:admin",
        "get",
        "deployment",
        "-n",
        namespace,
        "-l",
        "control-plane=controller-manager",
        "-o",
        "jsonpath={.items[*].spec.template.spec.containers[*].envFrom[*].secretRef.name}",
    )
    if rc != 0:
        return False
    for secret_name in secret_names.strip().split():
        data_keys, rc = run_unchecked(
            "kubectl", "--as", "system:admin", "get", "secret", secret_name, "-n", namespace, "-o", "jsonpath={.data}"
        )
        if rc == 0 and "OSAC_STORAGE_BACKEND_AAP_PROVISION_TEMPLATE" in data_keys:
            return True
        if rc == 0 and "OSAC_ENABLE_STORAGE_CONTROLLER" in data_keys:
            return True
    return False


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Skip storage tests when the storage controller is not configured."""
    namespace: str = env("OSAC_NAMESPACE", "osac-devel")
    if not _storage_controller_configured(namespace):
        skip_storage = pytest.mark.skip(
            reason="Storage controller not configured"
            " (OSAC_ENABLE_STORAGE_CONTROLLER not found in operator deployment or envFrom secrets)"
        )
        for item in items:
            if "storage" in str(item.fspath):
                item.add_marker(skip_storage)
        return

    if not os.environ.get("OSAC_PULL_SECRET_PATH"):
        skip_caas = pytest.mark.skip(reason="CaaS infrastructure not configured (OSAC_PULL_SECRET_PATH not set)")
        for item in items:
            if "test_caas_cluster_storage" in str(item.fspath):
                item.add_marker(skip_caas)


@pytest.fixture(scope="session")
def storage_config_namespace() -> str:
    """Namespace where storage configuration secrets are stored (OSAC_STORAGE_CONFIG_NAMESPACE)."""
    return env("OSAC_STORAGE_CONFIG_NAMESPACE", "osac-system")


@pytest.fixture(scope="session")
def cluster_template() -> str:
    """Cluster template name for CaaS cluster provisioning (OSAC_CLUSTER_TEMPLATE)."""
    return env("OSAC_CLUSTER_TEMPLATE", "ocp-ci-small")


@pytest.fixture(scope="session")
def pull_secret_path() -> str:
    """Filesystem path to the pull secret for CaaS cluster provisioning (OSAC_PULL_SECRET_PATH)."""
    return env("OSAC_PULL_SECRET_PATH")


@pytest.fixture(scope="session")
def ssh_public_key_path() -> str:
    """Filesystem path to the SSH public key, default ~/.ssh/id_rsa.pub (OSAC_SSH_PUBLIC_KEY_PATH)."""
    return env("OSAC_SSH_PUBLIC_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa.pub"))
