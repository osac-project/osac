"""Regression checks for the CaaS upgrade evidence log."""

from __future__ import annotations

import json
import logging
from contextlib import nullcontext
from unittest.mock import Mock

import pytest

from tests.e2e.caas.regression import cluster_upgrade as scenario

INSTALL_VERSION = "4.22.0-rhcos"


@pytest.mark.parametrize("cleanup_fails", [False, True])
def test_cleanup_logs_only_allowlisted_upgrade_evidence(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture, cleanup_fails: bool
) -> None:
    """Sensitive deployment data cannot reach the success or failure log."""
    cli, grpc, private_grpc, k8s = (Mock() for _ in range(4))
    if cleanup_fails:
        cli.delete_cluster.side_effect = RuntimeError("secret-cleanup-error")
    monkeypatch.setattr(scenario, "wait_for_cluster_deletion_without_cleanup", lambda **_kwargs: None)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_removal", lambda **_kwargs: None)
    monkeypatch.setenv("OSAC_TEST_AAP_PROJECT_REVISION", "secret-env-revision")
    evidence = {
        "phase": "node-pool-completed",
        "outcome": "succeeded",
        "cp_elapsed_seconds": 12.5,
        "np_elapsed_seconds": "secret-elapsed-value",
        "tenant": "secret-tenant",
        "cluster_id": "secret-cluster-uuid",
        "cluster_order": "secret-cluster-order",
        "hosted_cluster": "secret-namespace/secret-hosted-cluster",
        "node_pools": {"compute": "secret-node-pool"},
        "cp_aap_job": {"id": "secret-job-id"},
        "catalog": {"source_id": "secret-catalog-id", "source_image": "registry.internal.example.test/secret-image"},
        "aap_project_revision": "secret-env-revision",
        "token": "secret-token",
    }
    expected_error = pytest.raises(RuntimeError, match="secret-cleanup-error") if cleanup_fails else nullcontext()

    with caplog.at_level(logging.INFO, logger=scenario.logger.name), expected_error:
        scenario._cleanup(
            cli=cli,
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s,
            uuid="secret-cluster-uuid",
            co_name="secret-cluster-order",
            created_version_ids=["secret-catalog-id"],
            evidence=evidence,
            source_version=INSTALL_VERSION,
        )

    messages = [record.getMessage() for record in caplog.records if record.name == scenario.logger.name]
    assert len(messages) == 1
    assert messages[0].startswith("CaaS upgrade evidence: ")
    assert json.loads(messages[0].removeprefix("CaaS upgrade evidence: ")) == {
        "phase": "node-pool-completed",
        "outcome": "failed" if cleanup_fails else "succeeded",
        "cleanup": "failed" if cleanup_fails else "completed",
        "source_release_version": INSTALL_VERSION,
        "target_release_version": scenario.TARGET_VERSION,
        "cp_elapsed_seconds": 12.5,
    }
    for secret in (
        "secret-cleanup-error",
        "secret-elapsed-value",
        "secret-tenant",
        "secret-cluster-uuid",
        "secret-cluster-order",
        "secret-namespace",
        "secret-hosted-cluster",
        "secret-node-pool",
        "secret-job-id",
        "secret-catalog-id",
        "registry.internal.example.test",
        "secret-env-revision",
        "secret-token",
    ):
        assert secret not in caplog.text
    private_grpc.delete_cluster_version.assert_called_once_with(version_id="secret-catalog-id")


def test_cleanup_continues_grpc_removal_after_cluster_order_wait_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    """A ClusterOrder wait failure must not skip the Fulfillment removal wait."""
    cli, grpc, private_grpc, k8s = (Mock() for _ in range(4))
    wait_order = Mock(side_effect=RuntimeError("order-failed"))
    wait_grpc = Mock(side_effect=RuntimeError("grpc-failed"))
    monkeypatch.setattr(scenario, "_deletion_diagnostics", Mock(return_value={}))
    monkeypatch.setattr(scenario, "wait_for_cluster_deletion_without_cleanup", wait_order)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_removal", wait_grpc)

    with pytest.raises(RuntimeError, match="order-failed"):
        scenario._cleanup(
            cli=cli,
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s,
            uuid="cluster-id",
            co_name="order-name",
            created_version_ids=["version-id"],
            evidence={"phase": "node-pool-completed", "outcome": "failed"},
            source_version=INSTALL_VERSION,
            delete_cluster_requested=True,
        )

    wait_order.assert_called_once_with(k8s=k8s, name="order-name")
    wait_grpc.assert_called_once_with(grpc=grpc, uuid="cluster-id")
    private_grpc.delete_cluster_version.assert_called_once_with(version_id="version-id")


def test_cleanup_waits_without_repeating_a_requested_cluster_delete(monkeypatch: pytest.MonkeyPatch) -> None:
    """A failed natural teardown may need another wait, not another Delete request."""
    cli, grpc, private_grpc, k8s = (Mock() for _ in range(4))
    wait_order = Mock()
    wait_grpc = Mock()
    monkeypatch.setattr(scenario, "wait_for_cluster_deletion_without_cleanup", wait_order)
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_removal", wait_grpc)

    scenario._cleanup(
        cli=cli,
        grpc=grpc,
        private_grpc=private_grpc,
        k8s=k8s,
        uuid="cluster-id",
        co_name="order-name",
        created_version_ids=[],
        evidence={"phase": "node-pool-completed", "outcome": "failed"},
        source_version=INSTALL_VERSION,
        delete_cluster_requested=True,
    )

    cli.delete_cluster.assert_not_called()
    wait_order.assert_called_once_with(k8s=k8s, name="order-name")
    wait_grpc.assert_called_once_with(grpc=grpc, uuid="cluster-id")
