"""Regression checks for the CaaS upgrade readiness gates."""

from __future__ import annotations

from collections.abc import Callable
from copy import deepcopy
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.caas.regression import cluster_upgrade as scenario
from tests.e2e.caas.regression import test_cluster_node_sets as journey


def test_baseline_waits_for_hypershift_readiness(monkeypatch: pytest.MonkeyPatch) -> None:
    """HyperShift health, not the API provisioning state, gates the upgrade."""
    first, second = "alpha", "beta"
    ready_pool = {
        "status": {"conditions": [{"type": "Ready", "status": "True"}, {"type": "AllMachinesReady", "status": "True"}]}
    }
    ready = {
        "hosted": {
            "metadata": {"name": "hosted"},
            "status": {
                "conditions": [{"type": "Available", "status": "True"}, {"type": "Degraded", "status": "False"}]
            },
        },
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
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
    hosted_unavailable = deepcopy(ready)
    hosted_unavailable["hosted"]["status"]["conditions"][0]["status"] = "False"
    hosted_degraded = deepcopy(ready)
    hosted_degraded["hosted"]["status"]["conditions"][1]["status"] = "True"
    snapshots = [absent, not_ready, machines_not_ready, no_hosted, hosted_unavailable, hosted_degraded, ready]
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
    assert accepted == [False] * 6 + [True]


def test_readiness_mismatch_warns_once_per_observed_state(caplog: pytest.LogCaptureFixture) -> None:
    """Readiness disagreement is visible but does not reject the observation."""
    snapshot = {
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
        "hosted": {"status": {"conditions": [{"type": "Available", "status": "True"}]}},
    }
    reported: set[tuple[str | None, bool]] = set()

    scenario._warn_readiness_mismatch(snapshot, reported)
    scenario._warn_readiness_mismatch(snapshot, reported)
    assert len(caplog.messages) == 1
    assert "CLUSTER_STATE_PROGRESSING" in caplog.messages[0]

    snapshot["cluster"]["status"]["state"] = "CLUSTER_STATE_READY"
    scenario._warn_readiness_mismatch(snapshot, reported)
    assert len(caplog.messages) == 1

    snapshot["hosted"]["status"]["conditions"][0]["status"] = "False"
    scenario._warn_readiness_mismatch(snapshot, reported)
    assert len(caplog.messages) == 2
    assert "ready=False" in caplog.messages[1]


def test_unselected_components_survive_independent_cluster_state_change() -> None:
    """Upgrade checks do not own the Cluster's provisioning state."""
    snapshot = {
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
        "hosted": {"spec": {"release": {"image": "target-image"}}},
        "pools": {"gpu": {"spec": {"release": {"image": "source-image"}}, "status": {"version": "4.22.0"}}},
    }
    expected = {"images": {"gpu": "source-image"}, "versions": {"gpu": "4.22.0"}, "hosted_image": "target-image"}

    scenario._assert_unchanged(snapshot, **expected)

    changed_image = deepcopy(snapshot)
    changed_image["pools"]["gpu"]["spec"]["release"]["image"] = "unexpected-image"
    with pytest.raises(AssertionError, match="Unselected gpu NodePool image changed"):
        scenario._assert_unchanged(changed_image, **expected)

    changed_version = deepcopy(snapshot)
    changed_version["pools"]["gpu"]["status"]["version"] = "4.22.1"
    with pytest.raises(AssertionError, match="Unselected gpu NodePool version changed"):
        scenario._assert_unchanged(changed_version, **expected)


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
