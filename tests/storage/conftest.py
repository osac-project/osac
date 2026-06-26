from __future__ import annotations

import pytest

from tests.core.runner import env, run_unchecked


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
        "OSAC_STORAGE_BACKEND_AAP_PROVISION_TEMPLATE" in output
        or "OSAC_ENABLE_STORAGE_CONTROLLER" in output
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
            "kubectl",
            "--as",
            "system:admin",
            "get",
            "secret",
            secret_name,
            "-n",
            namespace,
            "-o",
            "jsonpath={.data}",
        )
        if rc == 0 and "OSAC_STORAGE_BACKEND_AAP_PROVISION_TEMPLATE" in data_keys:
            return True
        if rc == 0 and "OSAC_ENABLE_STORAGE_CONTROLLER" in data_keys:
            return True
    return False


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    namespace: str = env("OSAC_NAMESPACE", "osac-devel")
    if _storage_controller_configured(namespace):
        return
    skip_marker = pytest.mark.skip(
        reason="Storage controller not configured"
        " (OSAC_ENABLE_STORAGE_CONTROLLER not found in operator deployment or envFrom secrets)"
    )
    for item in items:
        if "storage" in str(item.fspath):
            item.add_marker(skip_marker)


@pytest.fixture(scope="session")
def storage_config_namespace() -> str:
    return env("OSAC_STORAGE_CONFIG_NAMESPACE", "osac-system")
