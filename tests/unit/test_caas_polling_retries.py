from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e import conftest as e2e_fixtures
from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient


@pytest.fixture(autouse=True)
def fast_polling(monkeypatch: pytest.MonkeyPatch) -> None:
    """Exercise retry outcomes without waiting or depending on production budgets."""
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)
    monkeypatch.setattr(helpers, "poll_until", lambda **kwargs: runner.poll_until(**(kwargs | {"retries": 2})))


def test_hub_startup_does_not_retry_broken_kubectl_access(monkeypatch: pytest.MonkeyPatch) -> None:
    read = Mock(side_effect=subprocess.CalledProcessError(1, ["kubectl"]))
    monkeypatch.setattr(k8s_client_module, "run", read)

    with pytest.raises(subprocess.CalledProcessError):
        e2e_fixtures.k8s_hub_client.__wrapped__(namespace="osac")

    read.assert_called_once()


@pytest.mark.parametrize("resource", ["clusterorder", "tenant"])
def test_condition_wait_recovers_from_a_lookup_error(resource: str) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = False  # An unchecked lookup conflates error with absence.
    k8s.is_absent.side_effect = [subprocess.CalledProcessError(1, ["kubectl"]), False]
    query_prefix = "cluster_order" if resource == "clusterorder" else "tenant"
    getattr(k8s, f"get_{query_prefix}_phase").return_value = "Ready"
    getattr(k8s, f"get_{query_prefix}_condition_status").return_value = "True"

    getattr(helpers, f"wait_for_{query_prefix}_condition")(k8s=k8s, name="resource-a", condition_type="Available")


@pytest.mark.parametrize("resource", ["clusterorder", "tenant"])
def test_condition_wait_still_fails_for_a_genuinely_missing_resource(resource: str) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = False
    k8s.is_absent.return_value = True
    query_prefix = "cluster_order" if resource == "clusterorder" else "tenant"

    with pytest.raises(AssertionError, match="disappeared"):
        getattr(helpers, f"wait_for_{query_prefix}_condition")(k8s=k8s, name="resource-a", condition_type="Available")


@pytest.mark.parametrize("canonical_resource", ["tenants", "tenants.osac.openshift.io"])
def test_tenant_absence_requires_the_exact_resource_not_found(
    monkeypatch: pytest.MonkeyPatch, canonical_resource: str
) -> None:
    lookup = Mock(return_value=(f'Error from server (NotFound): {canonical_resource} "tenant-a" not found', 1))
    monkeypatch.setattr(k8s_client_module, "run_unchecked", lookup)

    assert K8sClient(namespace="osac").is_absent(resource="tenant", name="tenant-a") is True


def test_tenant_lookup_error_does_not_count_as_absence(monkeypatch: pytest.MonkeyPatch) -> None:
    output = 'Error from server (NotFound): namespaces "tenant-a" not found'
    monkeypatch.setattr(k8s_client_module, "run_unchecked", Mock(return_value=(output, 1)))

    with pytest.raises(subprocess.CalledProcessError):
        K8sClient(namespace="osac").is_absent(resource="tenant", name="tenant-a")


def test_storage_lookup_errors_do_not_count_as_successful_removal(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(k8s_client_module, "run_unchecked", Mock(return_value=("Error from server (Forbidden)", 1)))

    with pytest.raises(TimeoutError):
        helpers.wait_for_tenant_cluster_storage_entry_removed(
            k8s=K8sClient(namespace="osac"), tenant_name="tenant-a", cluster_name="order-a"
        )
