from __future__ import annotations

from unittest.mock import Mock, call

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core import helpers, runner
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE


@pytest.fixture
def readiness_clients(monkeypatch: pytest.MonkeyPatch) -> dict[str, Mock]:
    grpc, metering = Mock(), Mock()
    k8s = Mock(spec=K8sClient)
    k8s.namespace = "osac"
    k8s.get_json.return_value = {
        "metadata": {"namespace": "osac", "annotations": {"osac.openshift.io/tenant": "tenant1"}},
        "status": {
            "phase": "Progressing",
            "conditions": [{"type": "ClusterAvailable", "status": "True"}],
            "desiredWorkers": 2,
            "currentWorkers": 2,
            "readyWorkers": 2,
        },
    }
    grpc.list_cluster_ids.return_value = ["cluster-id"]
    grpc.get_cluster.return_value = {"object": {"metadata": {"tenant": "tenant1"}, "spec": {"nodeSets": {}}}}
    for name, result in (
        ("wait_for_cluster_order_cr", "order-a"),
        ("wait_for_cluster_progressing", None),
        ("wait_for_cluster_order_condition", None),
        ("_assert_worker_provisioning", ("tenant1", {})),
        ("_assert_cluster_version_propagated", ("hosted-cluster", "hosted-namespace")),
        ("_assert_worker_node_request", ("compute", 1, "ci-worker-bm")),
        ("_assert_worker_bmi_resources", {"bmi-id"}),
        ("_assert_two_node_set_requests", {"compute": 1, "gpu": 1}),
        ("_assert_installed_agents_by_node_set", None),
        ("_assert_isolated_node_pools", None),
        ("_assert_owned_workers_for_node_sets", {"cpu-id", "gpu-id"}),
        ("_wait_for_cluster_infra_env_name", "infraenv"),
    ):
        monkeypatch.setattr(scenario, name, Mock(return_value=result))
    monkeypatch.setattr(runner.time, "sleep", Mock())
    return {"grpc": grpc, "k8s": k8s, "metering": metering}


@pytest.mark.parametrize("helper_name", ["sanity", "two_node_sets"])
@pytest.mark.parametrize("final_phase", ["Ready", "Failed"])
def test_cluster_heartbeat_waits_for_full_readiness(
    monkeypatch: pytest.MonkeyPatch, readiness_clients: dict[str, Mock], helper_name: str, final_phase: str
) -> None:
    k8s, metering = readiness_clients["k8s"], readiness_clients["metering"]
    if helper_name == "sanity":
        k8s.get_json.return_value["status"].update(desiredWorkers=1, currentWorkers=1, readyWorkers=0)
    phases = iter(["Progressing", final_phase])

    def observe_phase(**_kwargs: object) -> str:
        metering.expect.assert_not_called()
        metering.verify.assert_not_called()
        phase = next(phases)
        k8s.get_json.return_value["status"]["phase"] = phase
        return phase

    k8s.get_cluster_order_phase.side_effect = observe_phase
    ready = Mock(wraps=helpers.wait_for_cluster_ready)
    monkeypatch.setattr(scenario, "wait_for_cluster_ready", ready, raising=False)
    monkeypatch.setattr(
        scenario, "_assert_guest_workers_ready", lambda **kwargs: ready(k8s=kwargs["k8s"], name=kwargs["co_name"])
    )
    ordered = Mock()
    ordered.attach_mock(ready, "ready")
    ordered.attach_mock(metering.expect, "expect")
    ordered.attach_mock(metering.verify, "verify")

    def assert_available() -> None:
        if helper_name == "sanity":
            scenario.assert_cluster_available(**readiness_clients, uuid="cluster-id", version_name="version")
        else:
            scenario.assert_cluster_with_two_node_sets_available(
                **readiness_clients,
                uuid="cluster-id",
                instance_types={"compute": "ci-worker-bm", "gpu": "ci-worker-bm-gpu"},
            )

    if final_phase == "Failed":
        with pytest.raises(AssertionError, match="order-a entered Failed phase before becoming Ready"):
            assert_available()
        metering.expect.assert_not_called()
        metering.verify.assert_not_called()
        assert ordered.mock_calls == [call.ready(k8s=k8s, name="order-a")]
    else:
        assert_available()
        assert ordered.mock_calls == [
            call.ready(k8s=k8s, name="order-a"),
            call.expect(HEARTBEAT_TYPE, resource_id="cluster-id", timeout=180),
            call.verify(),
        ]
        metering.expect_no_project.assert_called_once_with()
    assert k8s.get_cluster_order_phase.call_count == 2
