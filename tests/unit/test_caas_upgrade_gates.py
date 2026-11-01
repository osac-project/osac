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
        "order": {
            "status": {
                "phase": "Ready",
                "conditions": [
                    {"type": "ControlPlaneAvailable", "status": "True"},
                    {"type": "ClusterAvailable", "status": "True"},
                ],
            }
        },
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
    order_not_ready = deepcopy(ready)
    order_not_ready["order"]["status"]["phase"] = "Progressing"
    order_not_ready["order"]["status"]["conditions"][0]["status"] = "False"
    snapshots = [
        absent,
        not_ready,
        machines_not_ready,
        no_hosted,
        hosted_unavailable,
        hosted_degraded,
        order_not_ready,
        ready,
    ]
    monkeypatch.setattr(scenario, "_snapshot", Mock(side_effect=snapshots))
    accepted: list[bool] = []
    captured_kwargs: dict[str, object] = {}

    def poll_without_sleep(
        *, fn: Callable[[], dict[str, Any]], until: Callable[[dict[str, Any]], bool], **kwargs: object
    ) -> dict[str, Any]:
        """Evaluate each prepared snapshot without delaying the unit test."""
        captured_kwargs.update(kwargs)
        for _ in snapshots:
            value = fn()
            accepted.append(until(value))
            if accepted[-1]:
                return value
        raise AssertionError("baseline never became ready")

    monkeypatch.setattr(scenario, "poll_until", poll_without_sleep)
    refs = scenario._ClusterRefs("cluster-id", "order", "namespace", "hosted", {first: "a", second: "b"})

    assert scenario._ready_baseline(k8s=Mock(), grpc=Mock(), refs=refs) == ready
    assert accepted == [False] * 7 + [True]
    assert captured_kwargs["retry_on_error"] is True


def test_upgrade_target_preflight_rejects_noop_and_already_applied_targets() -> None:
    """A control-plane edit must not repeat the source or an applied release."""
    baseline = {
        "order": {"spec": {"releaseImage": "source-image"}},
        "hosted": {
            "spec": {"release": {"image": "source-image"}},
            "status": {
                "version": {
                    "desired": {"image": "source-image"},
                    "history": [{"image": "source-image", "version": "4.22.0", "state": "Completed"}],
                }
            },
        },
    }

    with pytest.raises(AssertionError, match="must differ from the source image"):
        scenario._assert_upgrade_target(baseline=baseline, target={"name": "target", "image": "source-image"})

    already_desired = deepcopy(baseline)
    already_desired["hosted"]["status"]["version"]["desired"]["image"] = "target-image"
    with pytest.raises(AssertionError, match="already HyperShift's desired image"):
        scenario._assert_upgrade_target(baseline=already_desired, target={"name": "target", "image": "target-image"})

    already_history = deepcopy(baseline)
    already_history["hosted"]["status"]["version"]["history"][0]["image"] = "target-image"
    with pytest.raises(AssertionError, match="already in HyperShift history"):
        scenario._assert_upgrade_target(baseline=already_history, target={"name": "target", "image": "target-image"})

    mismatched_order = deepcopy(baseline)
    mismatched_order["order"]["spec"]["releaseImage"] = "different-image"
    with pytest.raises(AssertionError, match="release images disagree"):
        scenario._assert_upgrade_target(baseline=mismatched_order, target={"name": "target", "image": "target-image"})


def test_catalog_version_reuses_matching_active_entry() -> None:
    """A selected existing target must use the configured image."""
    candidate = {
        "id": "candidate-id",
        "metadata": {"name": "candidate-name"},
        "spec": {
            "version": "4.22.1",
            "image": "configured-image",
            "enabled": True,
            "state": "CLUSTER_VERSION_STATE_ACTIVE",
            "diskImage": {"name": "disk-image"},
        },
    }
    private_grpc = Mock()
    private_grpc.call.return_value = {"items": [candidate]}

    target, created = scenario._catalog_version(
        private_grpc=private_grpc, version="4.22.1", image="configured-image", disk_image="disk-image"
    )

    assert created is False
    assert target == {"id": "candidate-id", "name": "candidate-name", "image": "configured-image"}
    private_grpc.create_cluster_version.assert_not_called()


def test_catalog_version_creates_new_entry_when_image_differs() -> None:
    """A matching version is not enough when its image differs."""
    candidate = {
        "id": "candidate-id",
        "metadata": {"name": "candidate-name"},
        "spec": {
            "version": "4.22.1",
            "image": "stored-image",
            "enabled": True,
            "state": "CLUSTER_VERSION_STATE_ACTIVE",
            "diskImage": {"name": "disk-image"},
        },
    }
    private_grpc = Mock()
    private_grpc.call.return_value = {"items": [candidate]}
    private_grpc.create_cluster_version.return_value = {"id": "created-id", "name": "created-name"}

    target, created = scenario._catalog_version(
        private_grpc=private_grpc, version="4.22.1", image="configured-image", disk_image="disk-image"
    )

    assert created is True
    assert target == {"id": "created-id", "name": "created-name", "image": "configured-image"}
    private_grpc.create_cluster_version.assert_called_once_with(
        version="4.22.1", image="configured-image", disk_image="disk-image"
    )


def test_control_plane_start_matches_release_version_not_image_form() -> None:
    """Start is version-based and does not depend on stale status generations."""
    hosted = {
        "metadata": {"generation": 3},
        "spec": {"release": {"image": "target-tag"}},
        "status": {
            "version": {
                "desired": {"version": scenario.TARGET_VERSION, "image": "target-digest"},
                "observedGeneration": 1,
                "history": [{"version": scenario.TARGET_VERSION, "image": "target-digest", "state": "Partial"}],
            },
            "conditions": [{"type": "ClusterVersionProgressing", "status": "True", "observedGeneration": 1}],
        },
    }

    assert scenario._cp_started(hosted, "target-tag") is True

    wrong_desired_version = deepcopy(hosted)
    wrong_desired_version["status"]["version"]["desired"]["version"] = "4.22.0"
    assert scenario._cp_started(wrong_desired_version, "target-tag") is False

    wrong_spec_image = deepcopy(hosted)
    wrong_spec_image["spec"]["release"]["image"] = "different-tag"
    assert scenario._cp_started(wrong_spec_image, "target-tag") is False


def test_cluster_references_retry_transient_lookup_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    """HostedCluster and NodePool identity lookups retry transient errors."""
    poll_calls: list[dict[str, object]] = []

    def poll_without_sleep(**kwargs: object) -> object:
        poll_calls.append(kwargs)
        if len(poll_calls) == 1:
            return ("cluster-namespace", "hosted-cluster")
        return {"compute": "compute-pool"}

    monkeypatch.setattr(scenario, "poll_until", poll_without_sleep)
    refs = scenario._cluster_references(
        k8s=Mock(), uuid="cluster-id", co_name="order-name", expected_node_sets={"compute"}
    )

    assert refs == scenario._ClusterRefs(
        uuid="cluster-id",
        co_name="order-name",
        namespace="cluster-namespace",
        hc_name="hosted-cluster",
        pool_names={"compute": "compute-pool"},
    )
    assert len(poll_calls) == 2
    assert all(call["retry_on_error"] is True for call in poll_calls)


def test_node_pool_start_accepts_fresh_update_before_version_completes() -> None:
    """NodePool start does not wait for status.version to reach the target."""
    pool = {
        "spec": {"release": {"image": "target-tag"}},
        "status": {
            "version": "4.22.0",
            "conditions": [{"type": "UpdatingVersion", "status": "True", "lastTransitionTime": "new-transition"}],
        },
    }

    assert scenario._np_started(pool, "old-transition", "target-tag") is True

    wrong_spec_image = deepcopy(pool)
    wrong_spec_image["spec"]["release"]["image"] = "different-tag"
    assert scenario._np_started(wrong_spec_image, "old-transition", "target-tag") is False

    stale_transition = deepcopy(pool)
    stale_transition["status"]["conditions"][0]["lastTransitionTime"] = "old-transition"
    assert scenario._np_started(stale_transition, "old-transition", "target-tag") is False

    not_updating = deepcopy(pool)
    not_updating["status"]["conditions"][0]["status"] = "False"
    assert scenario._np_started(not_updating, "old-transition", "target-tag") is False


def test_node_pool_completion_matches_desired_replicas() -> None:
    """A multi-replica NodePool completes when all desired replicas are ready."""
    pool = {
        "spec": {"release": {"image": "target-tag"}, "replicas": 3},
        "status": {
            "version": scenario.TARGET_VERSION,
            "replicas": 3,
            "conditions": [
                {"type": "UpdatingVersion", "status": "False", "lastTransitionTime": "new-transition"},
                {"type": "Ready", "status": "True"},
                {"type": "AllMachinesReady", "status": "True"},
            ],
        },
    }

    assert scenario._np_completed(pool, "old-transition", "target-tag") is True

    incomplete_replicas = deepcopy(pool)
    incomplete_replicas["status"]["replicas"] = 2
    assert scenario._np_completed(incomplete_replicas, "old-transition", "target-tag") is False

    missing_replicas = deepcopy(pool)
    del missing_replicas["status"]["replicas"]
    assert scenario._np_completed(missing_replicas, "old-transition", "target-tag") is False


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
    baseline_pool = {
        "metadata": {"labels": {"osac.openshift.io/instance_type": "gpu-instance-type"}},
        "spec": {
            "replicas": 1,
            "release": {"image": "source-image"},
            "platform": {
                "agent": {
                    "agentLabelSelector": {
                        "matchLabels": {
                            "osac.openshift.io/clusterorder": "order-a",
                            "osac.openshift.io/node-set": "gpu",
                        }
                    }
                }
            },
        },
        "status": {
            "version": "4.22.0",
            "conditions": [
                {"type": "Ready", "status": "True", "reason": "AsExpected"},
                {"type": "AllMachinesReady", "status": "True", "reason": "AsExpected"},
                {"type": "ValidReleaseImage", "status": "True", "reason": "AsExpected"},
            ],
        },
    }
    snapshot = {
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
        "hosted": {"spec": {"release": {"image": "target-image"}}},
        "pools": {"gpu": deepcopy(baseline_pool)},
    }
    expected = {"baseline_pools": {"gpu": baseline_pool}, "hosted_image": "target-image"}

    scenario._assert_unchanged(snapshot, **expected)

    mutations = (
        ("image changed", lambda pool: pool["spec"]["release"].update(image="unexpected-image")),
        ("version changed", lambda pool: pool["status"].update(version="4.22.1")),
        ("replica count changed", lambda pool: pool["spec"].update(replicas=2)),
        (
            "instance type changed",
            lambda pool: pool["metadata"]["labels"].update(
                **{"osac.openshift.io/instance_type": "unexpected-instance-type"}
            ),
        ),
        (
            "agent selector changed",
            lambda pool: pool["spec"]["platform"]["agent"]["agentLabelSelector"]["matchLabels"].update(
                **{"osac.openshift.io/node-set": "unexpected-node-set"}
            ),
        ),
        ("Ready changed", lambda pool: pool["status"]["conditions"][0].update(status="False")),
        ("AllMachinesReady changed", lambda pool: pool["status"]["conditions"][1].update(status="False")),
    )
    for expected_message, mutate in mutations:
        changed = deepcopy(snapshot)
        mutate(changed["pools"]["gpu"])
        with pytest.raises(AssertionError, match=f"Unselected gpu NodePool {expected_message}"):
            scenario._assert_unchanged(changed, **expected)


def test_unselected_node_pool_ignores_non_readiness_condition_changes() -> None:
    """Only readiness status gates an otherwise unchanged NodePool."""
    baseline_pool = {
        "status": {
            "conditions": [
                {"type": "Ready", "status": "True", "reason": "AsExpected"},
                {"type": "AllMachinesReady", "status": "True", "reason": "AsExpected"},
                {"type": "ValidReleaseImage", "status": "True", "reason": "AsExpected"},
            ]
        }
    }
    snapshot = {
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
        "hosted": {"spec": {"release": {"image": "target-image"}}},
        "pools": {"gpu": deepcopy(baseline_pool)},
    }
    snapshot["pools"]["gpu"]["status"]["conditions"][2]["reason"] = "ValidReleaseImageReasonChanged"
    snapshot["pools"]["gpu"]["status"]["conditions"].append(
        {"type": "SupportedVersionSkew", "status": "True", "reason": "AsExpected"}
    )

    scenario._assert_unchanged(snapshot, baseline_pools={"gpu": baseline_pool}, hosted_image="target-image")


def test_cluster_failure_guard_allows_progressing() -> None:
    """Independent provisioning progress does not fail the upgrade check."""
    scenario._check_cluster_failure({"cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}}})


@pytest.mark.parametrize("stage", ("control_plane", "node_pool"))
def test_upgrade_observers_reject_failed_cluster(stage: str, monkeypatch: pytest.MonkeyPatch) -> None:
    """Neither upgrade stage may complete after the tenant Cluster fails."""
    failed = {
        "cluster": {"status": {"state": "CLUSTER_STATE_FAILED"}},
        "hosted": {"status": {"conditions": [{"type": "Available", "status": "True"}]}},
    }
    snapshot = Mock(return_value=failed)
    monkeypatch.setattr(scenario, "_snapshot", snapshot)
    monkeypatch.setattr(scenario, "_wait_elapsed", lambda *, observe, **_kwargs: observe())
    refs = scenario._ClusterRefs("cluster-id", "order", "namespace", "hosted", {"compute": "a", "gpu": "b"})
    source_pool = {"spec": {"release": {"image": "source-image"}}, "status": {"version": "4.22.0"}}
    common = {
        "refs": refs,
        "target": {"name": "target-name", "image": "target-image"},
        "jwt_grpc_tenant1": Mock(),
        "grpc": Mock(),
        "k8s": Mock(),
        "evidence": {},
    }

    with pytest.raises(AssertionError, match="OSAC Cluster entered FAILED state during upgrade"):
        if stage == "control_plane":
            scenario._upgrade_control_plane(
                baseline={
                    "pools": {"gpu": source_pool},
                    "order": {"status": {}, "spec": {"releaseImage": "source-image"}},
                    "hosted": {
                        "spec": {"release": {"image": "source-image"}},
                        "status": {
                            "version": {
                                "desired": {"version": "4.22.0"},
                                "history": [{"version": "4.22.0", "state": "Completed"}],
                            }
                        },
                    },
                },
                **common,
            )
        else:
            scenario._upgrade_node_pool(
                baseline={"pools": {"gpu": source_pool}},
                cp_snapshot={"pools": {"compute": {"spec": {"replicas": 1}}}, "order": {"status": {}}},
                upgraded_node_set="compute",
                unchanged_node_set="gpu",
                **common,
            )

    assert snapshot.call_count == 1


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
                    "desired": {"version": scenario.TARGET_VERSION},
                    "history": [{"version": scenario.TARGET_VERSION, "state": "Completed"}],
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
