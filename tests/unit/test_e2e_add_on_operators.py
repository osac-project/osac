from __future__ import annotations

import subprocess
from collections.abc import Callable
from typing import Any
from unittest.mock import Mock

import pytest

import tests.e2e.caas.add_on_operators as add_on_operators
from tests.e2e.caas.add_on_operators import _ensure_published_operators, _operator_state_map


def test_operator_publication_preserves_explicit_order() -> None:
    private_cli = Mock()
    private_cli.get_add_on_operator.side_effect = [
        '{"id":"nfd-id","metadata":{"name":"node-feature-discovery"},"published":false}',
        '{"id":"gpu-id","metadata":{"name":"nvidia-gpu"},"published":false}',
    ]
    grpc = Mock()
    grpc.call.side_effect = [{}, {}]

    specs, _, operator_ids = _ensure_published_operators(private_cli, grpc, ("node-feature-discovery", "nvidia-gpu"))

    assert [spec.name for spec in specs] == ["node-feature-discovery", "nvidia-gpu"]
    assert operator_ids == ("nfd-id", "gpu-id")


def test_operator_state_map_reads_public_cluster_status() -> None:
    response = {
        "object": {
            "status": {
                "addOnOperators": [
                    {"name": "cert-manager", "state": "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED", "message": ""},
                    {"name": "serverless", "state": "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLING", "message": "waiting"},
                ]
            }
        }
    }

    assert _operator_state_map(response) == {
        "cert-manager": {"name": "cert-manager", "state": "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED", "message": ""},
        "serverless": {"name": "serverless", "state": "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLING", "message": "waiting"},
    }


def test_operator_publication_restores_original_states() -> None:
    private_cli = Mock()
    private_cli.get_add_on_operator.side_effect = [
        '{"id":"nfd-id","metadata":{"name":"node-feature-discovery"},"published":false}',
        '{"id":"gpu-id","metadata":{"name":"nvidia-gpu"},"published":true}',
    ]
    grpc = Mock()
    grpc.call.side_effect = [{}, {}]

    _, original_states, _ = add_on_operators._ensure_published_operators(
        private_cli, grpc, ("node-feature-discovery", "nvidia-gpu")
    )
    add_on_operators._restore_operator_publication(grpc, original_states)

    update_calls = [
        call for call in grpc.call.call_args_list if call.kwargs["service"].endswith("AddOnOperators/Update")
    ]
    assert [call.kwargs["data"]["object"]["id"] for call in update_calls] == ["nfd-id", "nfd-id"]


def test_subscription_and_csv_assertion_reports_missing_names() -> None:
    class FakeK8s:
        namespace = "workload-namespace"

        def get_json(self, *, resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
            assert resource == "subscriptions.operators.coreos.com"
            assert name == "serverless-operator"
            assert namespace == "openshift-serverless"
            raise subprocess.CalledProcessError(1, ["kubectl"])

    with pytest.raises(AssertionError, match="Missing Subscription openshift-serverless/serverless-operator"):
        add_on_operators._assert_subscriptions_and_csvs(
            workload_k8s=FakeK8s(),
            expected=(
                add_on_operators.AddOnOperatorSpec(
                    name="serverless",
                    subscription_name="serverless-operator",
                    subscription_namespace="openshift-serverless",
                ),
            ),
        )


def test_subscription_and_csv_assertion_requires_a_succeeded_installed_csv() -> None:
    class FakeK8s:
        namespace = "workload-namespace"

        def get_json(self, *, resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
            if resource == "subscriptions.operators.coreos.com":
                assert name == "serverless-operator"
                assert namespace == "openshift-serverless"
                return {"metadata": {"name": name}, "status": {"installedCSV": "serverlessoperator.v1.2.3"}}
            assert resource == "clusterserviceversions.operators.coreos.com"
            assert name == "serverlessoperator.v1.2.3"
            assert namespace == "openshift-serverless"
            return {"status": {"phase": "Succeeded"}}

    add_on_operators._assert_subscriptions_and_csvs(
        workload_k8s=FakeK8s(),
        expected=(
            add_on_operators.AddOnOperatorSpec(
                name="serverless",
                subscription_name="serverless-operator",
                subscription_namespace="openshift-serverless",
            ),
        ),
    )


def test_wait_for_installed_operator_rejects_non_empty_success_message(monkeypatch: pytest.MonkeyPatch) -> None:
    client = Mock()
    client.get_cluster.return_value = {
        "object": {
            "status": {
                "addOnOperators": [
                    {
                        "name": "serverless",
                        "state": "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED",
                        "message": "unexpected detail",
                    }
                ]
            }
        }
    }

    def fake_poll_until(
        *, fn: Callable[[], dict[str, dict[str, Any]]], until: Callable[[dict[str, dict[str, Any]]], bool], **_: object
    ) -> dict[str, dict[str, Any]]:
        observed = fn()
        assert until(observed)
        return observed

    monkeypatch.setattr(add_on_operators, "poll_until", fake_poll_until)

    with pytest.raises(AssertionError, match="serverless") as exc_info:
        add_on_operators._wait_for_api_operators_installed(
            client=client, cluster_id="cluster-id", identity="test cluster", operator_names=("serverless",)
        )
    assert "unexpected detail" not in str(exc_info.value)
    client.get_cluster.assert_called_once_with(cluster_id="cluster-id")
