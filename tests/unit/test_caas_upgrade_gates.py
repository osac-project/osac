"""Regression checks for the CaaS upgrade readiness gates."""

from __future__ import annotations

from collections.abc import Callable
from copy import deepcopy
from types import SimpleNamespace
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.caas.regression import cluster_upgrade as scenario
from tests.e2e.caas.regression import test_cluster_node_sets as journey


def test_baseline_waits_for_both_node_pool_readiness_conditions(monkeypatch: pytest.MonkeyPatch) -> None:
    """A ready Cluster does not make an unready NodePool a steady baseline."""
    first, second = "alpha", "beta"
    ready_pool = {
        "status": {"conditions": [{"type": "Ready", "status": "True"}, {"type": "AllMachinesReady", "status": "True"}]}
    }
    ready = {
        "hosted": {"metadata": {"name": "hosted"}},
        "cluster": {"status": {"state": "CLUSTER_STATE_READY"}},
        "pools": {first: deepcopy(ready_pool), second: deepcopy(ready_pool)},
    }
    absent = deepcopy(ready)
    absent["pools"][first] = {}
    not_ready = deepcopy(ready)
    not_ready["pools"][first]["status"]["conditions"][0]["status"] = "False"
    machines_not_ready = deepcopy(ready)
    machines_not_ready["pools"][second]["status"]["conditions"][1]["status"] = "False"
    no_hosted = deepcopy(ready)
    no_hosted["hosted"] = {}
    cluster_not_ready = deepcopy(ready)
    cluster_not_ready["cluster"]["status"]["state"] = "CLUSTER_STATE_PROVISIONING"
    snapshots = [absent, not_ready, machines_not_ready, no_hosted, cluster_not_ready, ready]
    monkeypatch.setattr(scenario, "_snapshot", Mock(side_effect=snapshots))
    accepted: list[bool] = []

    def poll_without_sleep(
        *, fn: Callable[[], dict[str, Any]], until: Callable[[dict[str, Any]], bool], **_kwargs: object
    ) -> dict[str, Any]:
        """Evaluate each prepared snapshot without delaying the unit test."""
        for _ in snapshots:
            value = fn()
            accepted.append(until(value))
            if accepted[-1]:
                return value
        raise AssertionError("baseline never became ready")

    monkeypatch.setattr(scenario, "poll_until", poll_without_sleep)
    refs = scenario._ClusterRefs("cluster-id", "order", "namespace", "hosted", {first: "a", second: "b"})

    assert scenario._ready_baseline(k8s=Mock(), grpc=Mock(), refs=refs) == ready
    assert accepted == [False, False, False, False, False, True]


def test_node_pool_poll_rejects_later_control_plane_degradation(monkeypatch: pytest.MonkeyPatch) -> None:
    """Control-plane health must hold during the NodePool completion wait."""
    regular = journey.REGULAR_NODE_SET
    gpu = journey.GPU_NODE_SET
    target_image = "target-image"
    unchanged_pool = {"spec": {"release": {"image": "source-image"}}, "status": {"version": "4.22.0"}}
    baseline = {"cluster": {"status": {"state": "CLUSTER_STATE_READY"}}, "pools": {gpu: unchanged_pool}}
    cp_snapshot = {"pools": {regular: {"spec": {"replicas": 1}}}, "order": {"status": {}}}
    healthy = {
        "cluster": {
            "status": {"state": "CLUSTER_STATE_READY"},
            "spec": {"nodeSets": {regular: {"version": {"name": "target-name"}}}},
        },
        "order": {"spec": {"releaseImage": target_image}},
        "hosted": {
            "metadata": {"generation": 1},
            "spec": {"release": {"image": target_image}},
            "status": {
                "version": {
                    "desired": {"image": target_image},
                    "observedGeneration": 1,
                    "history": [{"image": target_image, "version": scenario.TARGET_VERSION, "state": "Completed"}],
                },
                "conditions": [{"type": "Available", "status": "True"}, {"type": "Degraded", "status": "False"}],
            },
        },
        "pools": {gpu: unchanged_pool, regular: {}},
    }
    degraded = deepcopy(healthy)
    degraded["hosted"]["status"]["conditions"][1]["status"] = "True"
    snapshot = Mock(side_effect=[healthy, degraded])
    monkeypatch.setattr(scenario, "_snapshot", snapshot)

    def observe_once(
        *, observe: Callable[[], tuple[dict[str, Any], dict[str, Any]]], **_kwargs: object
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        """Take one stage observation without waiting for wall-clock time."""
        return observe()

    monkeypatch.setattr(scenario, "_wait_elapsed", observe_once)
    refs = scenario._ClusterRefs("cluster-id", "order", "namespace", "hosted", {regular: "a", gpu: "b"})
    evidence: dict[str, Any] = {}

    with pytest.raises(AssertionError, match="Control plane is not completed and healthy"):
        scenario._upgrade_node_pool(
            refs=refs,
            baseline=baseline,
            cp_snapshot=cp_snapshot,
            target={"name": "target-name", "image": target_image},
            jwt_grpc_tenant1=Mock(),
            grpc=Mock(),
            k8s=Mock(),
            evidence=evidence,
            upgraded_node_set=regular,
            unchanged_node_set=gpu,
        )

    assert snapshot.call_count == 2
    assert evidence["phase"] == "node-pool-started"


def test_install_only_skips_upgrade_and_tenant_user_fixture(monkeypatch: pytest.MonkeyPatch) -> None:
    """The on-demand install path retains teardown without requesting an upgrade."""
    monkeypatch.setenv("OSAC_CAAS_RUN_UPGRADE", "false")
    monkeypatch.setenv("OSAC_FULFILLMENT_TRUST_E2E", "false")
    request, cli, grpc, private_grpc, k8s, metering = (Mock() for _ in range(6))
    cli.create_cluster.return_value = "cluster-id"
    private_grpc.ensure_disk_image.return_value = "disk-image"
    private_grpc.ensure_cluster_version.return_value = {"name": "source-version"}
    cluster = SimpleNamespace(co_name="order-name", owned_bmi_ids={"cpu-id", "gpu-id"})
    installed = Mock(return_value=cluster)
    deleted = Mock()
    references = Mock()
    baseline = Mock()
    catalog = Mock()
    available = Mock()
    version_propagated = Mock()
    cluster_metering = Mock()
    worker_metering = Mock()
    monkeypatch.setattr(journey, "assert_cluster_with_two_node_sets_available", installed)
    monkeypatch.setattr(journey, "_assert_cluster_deleted", deleted)
    monkeypatch.setattr(journey, "wait_for_cluster_order_condition", available)
    monkeypatch.setattr(journey, "_assert_cluster_version_propagated", version_propagated)
    monkeypatch.setattr(journey, "_assert_cluster_metering", cluster_metering)
    monkeypatch.setattr(journey, "_assert_two_node_set_worker_metering", worker_metering)
    monkeypatch.setattr(scenario, "_cluster_references", references)
    monkeypatch.setattr(scenario, "_ready_baseline", baseline)
    monkeypatch.setattr(scenario, "_catalog_version", catalog)

    journey.test_cluster_create_with_two_node_sets(
        request=request,
        cli=cli,
        grpc=grpc,
        private_grpc=private_grpc,
        k8s_hub_client=k8s,
        cluster_template="template",
        pull_secret_name="pull-secret",
        ssh_public_key_path="key-path",
        metering=metering,
    )

    installed.assert_called_once()
    available.assert_called_once_with(k8s=k8s, name="order-name", condition_type="ClusterAvailable")
    version_propagated.assert_called_once()
    cluster_metering.assert_called_once_with(metering=metering, uuid="cluster-id", cluster_template="template")
    worker_metering.assert_called_once_with(
        grpc=grpc, metering=metering, uuid="cluster-id", owned_bmi_ids=cluster.owned_bmi_ids
    )
    metering.expect.assert_not_called()
    cli.delete_cluster.assert_called_once_with(uuid="cluster-id")
    deleted.assert_called_once_with(grpc=grpc, k8s=k8s, metering=metering, uuid="cluster-id", cluster=cluster)
    request.getfixturevalue.assert_not_called()
    references.assert_not_called()
    baseline.assert_not_called()
    catalog.assert_not_called()
