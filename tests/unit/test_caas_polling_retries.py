from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e import conftest as e2e_fixtures
from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector


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


def test_cluster_deletion_bmi_poll_retries_transient_grpc_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    """A DNS/transport blip during BMI-removal polling must not abort the teardown check.

    The merge-queue failure this covers: the runner's local resolver refused
    connections while ``BareMetalInstances/List`` was polled, and the poll
    was not configured to retry the resulting grpcurl ``CommandFailed``.
    """
    poll = Mock(side_effect=RuntimeError("poll captured"))
    monkeypatch.setattr(scenario, "wait_for_cluster_deleting", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_deleting_or_archived", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "poll_until", poll)

    cluster = scenario._ClusterResources(
        co_name="order-a",
        owned_bmi_ids={"bmi-a"},
        infra_env_name="order-a-infraenv",
        hosted_cluster_name="hosted-a",
        hosted_cluster_namespace="hosted-a-ns",
    )
    with pytest.raises(RuntimeError, match="poll captured"):
        scenario._assert_cluster_deleted(grpc=Mock(), k8s=Mock(), metering=Mock(), uuid="uuid-a", cluster=cluster)

    assert poll.call_args.kwargs["retry_on_error"] is True


@pytest.fixture
def cluster_deletion_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    """Isolate ``_assert_cluster_deleted`` to the BMI-removal poll."""
    monkeypatch.setattr(scenario, "wait_for_cluster_deleting", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_deleting_or_archived", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "snapshot_cluster_deletion", lambda *_args, **_kwargs: ())
    monkeypatch.setattr(scenario, "_wait_for_cluster_and_infraenv_deletion", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_removal", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "_assert_worker_billing_stops", lambda **_kwargs: None)
    monkeypatch.setattr(
        scenario, "poll_until", lambda **kwargs: runner.poll_until(**(kwargs | {"retries": 2, "delay": 0}))
    )


def test_cluster_deletion_bmi_poll_recovers_after_transient_grpc_failure(cluster_deletion_waits: None) -> None:
    """A retried transport failure must still reach the successful-removal outcome."""
    grpc = Mock()
    grpc.list_baremetal_instance_ids.side_effect = [subprocess.CalledProcessError(1, ["grpcurl"]), []]
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = False
    cluster = scenario._ClusterResources(
        co_name="order-a",
        owned_bmi_ids={"bmi-a"},
        infra_env_name="order-a-infraenv",
        hosted_cluster_name="hosted-a",
        hosted_cluster_namespace="hosted-a-ns",
    )

    scenario._assert_cluster_deleted(
        grpc=grpc, k8s=k8s, metering=Mock(spec=MeteringCollector), uuid="uuid-a", cluster=cluster
    )

    assert grpc.list_baremetal_instance_ids.call_count == 2


def test_cluster_deletion_bmi_poll_exhausts_on_persistent_grpc_failure(cluster_deletion_waits: None) -> None:
    """A persistent transport failure must exhaust the retry budget and fail."""
    grpc = Mock()
    grpc.list_baremetal_instance_ids.side_effect = subprocess.CalledProcessError(1, ["grpcurl"])
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = False
    cluster = scenario._ClusterResources(
        co_name="order-a",
        owned_bmi_ids={"bmi-a"},
        infra_env_name="order-a-infraenv",
        hosted_cluster_name="hosted-a",
        hosted_cluster_namespace="hosted-a-ns",
    )

    with pytest.raises(runner.StuckState, match="CaaS worker BMI removal"):
        scenario._assert_cluster_deleted(
            grpc=grpc, k8s=k8s, metering=Mock(spec=MeteringCollector), uuid="uuid-a", cluster=cluster
        )

    assert grpc.list_baremetal_instance_ids.call_count == 2
