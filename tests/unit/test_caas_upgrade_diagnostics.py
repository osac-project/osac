"""Regression checks for safe CaaS upgrade diagnostics."""

from __future__ import annotations

import json
import logging
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.caas.regression import cluster_upgrade as scenario


def test_upgrade_diagnostics_reports_each_start_gate() -> None:
    """Diagnostics expose the exact readiness booleans without raw payloads."""
    snapshot: dict[str, Any] = {
        "cluster": {"status": {"state": "CLUSTER_STATE_PROGRESSING"}},
        "order": {"status": {"phase": "Progressing"}, "spec": {"releaseImage": "target-image"}},
        "hosted": {
            "metadata": {"generation": 2},
            "spec": {"release": {"image": "target-image"}},
            "status": {
                "version": {
                    "desired": {"image": "source-image"},
                    "observedGeneration": 1,
                    "history": [{"image": "source-image", "state": "Partial"}],
                },
                "conditions": [
                    {"type": "Available", "status": "True", "reason": "AsExpected"},
                    {"type": "Degraded", "status": "False", "reason": "AsExpected"},
                ],
            },
        },
        "pools": {
            "compute": {
                "spec": {"release": {"image": "source-image"}},
                "status": {
                    "version": "4.22.0",
                    "conditions": [
                        {"type": "Ready", "status": "True", "reason": "AsExpected"},
                        {"type": "AllMachinesReady", "status": "False", "reason": "WorkersJoining"},
                    ],
                },
            }
        },
    }
    job = {"jobID": "job-id", "type": "provision", "state": "Running", "message": "secret-message"}

    diagnostics = scenario._upgrade_diagnostics(snapshot=snapshot, target_image="target-image", job=job)

    assert diagnostics["cluster_state"] == "CLUSTER_STATE_PROGRESSING"
    assert diagnostics["order_release_image_matches"] is True
    assert diagnostics["new_provision_job"] is True
    assert diagnostics["job"] == {"type": "provision", "state": "Running"}
    assert diagnostics["hosted_spec_image_matches"] is True
    assert diagnostics["hosted_desired_image_matches"] is False
    assert diagnostics["hosted_generation"] == 2
    assert diagnostics["hosted_observed_generation"] == 1
    assert diagnostics["hosted_observed_generation_matches"] is False
    assert diagnostics["hosted_history_image_matches"] is False
    assert diagnostics["hosted_history_state"] == "Partial"
    assert diagnostics["hosted_available"] is True
    assert diagnostics["hosted_degraded"] is False
    assert diagnostics["node_pools"]["compute"]["image_matches"] is False
    assert diagnostics["node_pools"]["compute"]["ready"] is True
    assert diagnostics["node_pools"]["compute"]["all_machines_ready"] is False
    assert diagnostics["node_pools"]["compute"]["conditions"] == [
        {"type": "Ready", "status": "True", "reason": "AsExpected"},
        {"type": "AllMachinesReady", "status": "False", "reason": "WorkersJoining"},
    ]

    assert "secret-message" not in json.dumps(diagnostics)


def test_wait_elapsed_logs_initial_and_final_diagnostics(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    """A timeout leaves a final redacted snapshot, not only a generic message."""
    times = iter([0.0, 0.0, 1.0, 1.0])
    monkeypatch.setattr(scenario.time, "monotonic", lambda: next(times))
    monkeypatch.setattr(scenario.time, "sleep", lambda _seconds: None)

    with (
        caplog.at_level(logging.INFO, logger=scenario.diagnostics_logger.name),
        pytest.raises(TimeoutError, match="timed out after 1 elapsed second"),
    ):
        scenario._wait_elapsed(
            component="test component",
            accepted_at=0.0,
            limit_seconds=1,
            observe=lambda: "observed-value",
            ready=lambda _value: False,
            diagnose=lambda value: {"observed": value},
        )

    messages = [record.getMessage() for record in caplog.records if record.name == scenario.diagnostics_logger.name]
    assert len(messages) == 2
    initial = json.loads(messages[0].removeprefix("CaaS upgrade diagnostics: "))
    final = json.loads(messages[1].removeprefix("CaaS upgrade diagnostics: "))
    assert initial == {"component": "test component", "elapsed_seconds": 0.0, "observed": "observed-value"}
    assert final == {"component": "test component", "elapsed_seconds": 1.0, "final": True, "observed": "observed-value"}


def test_deletion_diagnostics_reports_order_hosted_cluster_and_pools() -> None:
    """Deletion diagnostics identify the resource that blocks natural teardown."""
    k8s = Mock()
    grpc = Mock()
    order: dict[str, Any] = {
        "metadata": {"deletionTimestamp": "2026-10-30T11:15:52Z", "finalizers": ["osac.openshift.io/clusterorder"]},
        "status": {
            "phase": "Deleting",
            "clusterReference": {"namespace": "cluster-ns", "hostedClusterName": "hosted"},
            "provisioningJobs": [{"jobID": "job-id", "type": "deprovision", "state": "Running", "message": "secret"}],
            "conditions": [{"type": "Deleting", "status": "True", "reason": "Deleting"}],
        },
    }
    hosted: dict[str, Any] = {
        "metadata": {"deletionTimestamp": "2026-10-30T11:16:00Z"},
        "status": {"conditions": [{"type": "Available", "status": "False", "reason": "Deleting"}]},
    }
    pools: dict[str, Any] = {
        "items": [
            {
                "metadata": {
                    "labels": {"osac.openshift.io/clusterorder": "order", "osac.openshift.io/node-set": "compute"}
                },
                "status": {"conditions": [{"type": "Ready", "status": "False", "reason": "Deleting"}]},
            }
        ]
    }
    k8s.get_cluster_order_phase.return_value = "Deleting"
    k8s.get_json.side_effect = [order, hosted]
    k8s.list_json.return_value = pools
    grpc.get_cluster.return_value = {"object": {"status": {"state": "CLUSTER_STATE_DELETING"}}}

    diagnostics = scenario._deletion_diagnostics(k8s=k8s, grpc=grpc, uuid="cluster-id", co_name="order")

    assert diagnostics["cluster_state"] == "CLUSTER_STATE_DELETING"
    assert diagnostics["cluster_order_exists"] is True
    assert diagnostics["cluster_order_phase"] == "Deleting"
    assert diagnostics["cluster_order_deletion_requested"] is True
    assert diagnostics["cluster_order_finalizers"] == ["osac.openshift.io/clusterorder"]
    assert diagnostics["latest_job"] == {"type": "deprovision", "state": "Running"}
    assert diagnostics["cluster_reference_present"] is True
    assert diagnostics["hosted_cluster_exists"] is True
    assert diagnostics["hosted_cluster_deletion_requested"] is True
    assert diagnostics["node_pool_count"] == 1
    assert diagnostics["node_pools"]["compute"]["ready"] is False
    assert "secret" not in json.dumps(diagnostics)
