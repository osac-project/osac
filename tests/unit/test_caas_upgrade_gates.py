"""Regression checks for the CaaS upgrade readiness gates."""

from __future__ import annotations

from collections.abc import Callable
from copy import deepcopy
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
