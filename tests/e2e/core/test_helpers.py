from __future__ import annotations

import base64
import logging
import subprocess
from collections.abc import Callable
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.wait_kinds import WaitResourceKind

_AGENT_PROVIDER_ID = "agent://osac-order-agent/6b0b0c1e-0000-4000-8000-000000000000"


def _node(*, name: str, provider_id: str = _AGENT_PROVIDER_ID) -> dict[str, object]:
    spec: dict[str, str] = {"providerID": provider_id} if provider_id else {}
    return {
        "metadata": {"name": name, "labels": {"node-role.kubernetes.io/worker": ""}},
        "spec": spec,
        "status": {"conditions": [{"type": "Ready", "status": "True"}]},
    }


def _hosted_cluster(
    *, succeeding: str = "True", available: str = "True", progressing: str = "False"
) -> dict[str, object]:
    # HyperShift does not copy ClusterVersionFailing onto the HostedCluster.
    return {
        "status": {
            "conditions": [
                {"type": "ClusterVersionSucceeding", "status": succeeding},
                {"type": "ClusterVersionAvailable", "status": available},
                {"type": "ClusterVersionProgressing", "status": progressing},
            ]
        }
    }


def _hosted_control_plane(*, failing: str = "False") -> dict[str, object]:
    return {"status": {"conditions": [{"type": "ClusterVersionFailing", "status": failing}]}}


def test_wait_for_cluster_ready_fails_immediately_on_failed_order(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.get_cluster_order_phase.return_value = "Failed"

    def run_once(*, fn: Callable[[], str], until: Callable[[str], bool], **_kwargs: object) -> str:
        return fn()

    monkeypatch.setattr(helpers, "poll_until", run_once)

    with pytest.raises(AssertionError, match="order-test entered Failed phase"):
        helpers.wait_for_cluster_ready(k8s=k8s, name="order-test")


def test_wait_for_cluster_ready_logs_sanitized_timeout_diagnostics(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    k8s = Mock()
    k8s.get_cluster_order_phase.return_value = "Progressing"
    diagnostics = ["clusterorder phase=Progressing", "agent state=installing count=1"]
    monkeypatch.setattr(helpers, "snapshot_cluster_readiness", Mock(return_value=diagnostics))

    def run_once(**_kwargs: object) -> None:
        raise runner.StuckState("order-test ClusterOrder Ready", "'Progressing'", 7185)

    monkeypatch.setattr(helpers, "poll_until", run_once)
    caplog.set_level(logging.ERROR)

    with pytest.raises(runner.StuckState):
        helpers.wait_for_cluster_ready(k8s=k8s, name="order-test")

    assert "CaaS readiness timeout: clusterorder phase=Progressing" in caplog.text
    assert "CaaS readiness timeout: agent state=installing count=1" in caplog.text
    helpers.snapshot_cluster_readiness.assert_called_once_with(k8s, "order-test")


def test_node_pool_ready_node_count_uses_node_versions() -> None:
    node_pool = {"status": {"nodesInfo": {"nodeVersions": [{"readyNodeCount": 1}, {"readyNodeCount": 2}]}}}

    assert helpers.node_pool_ready_node_count(node_pool) == 3


def test_node_pool_readiness_requires_replicas_to_match_ready_nodes() -> None:
    node_pool = {"status": {"replicas": 0, "nodesInfo": {"nodeVersions": [{"readyNodeCount": 1}]}}}

    assert helpers.node_pool_ready(node_pool, expected_ready_nodes=1) is False


def test_wait_for_hosted_cluster_kubeconfig_reads_hcp_secret(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.get_json.side_effect = [
        {"status": {"kubeConfig": {"name": "admin-kubeconfig", "key": "kubeconfig"}}},
        {"data": {"kubeconfig": base64.b64encode(b"apiVersion: v1\n").decode()}},
    ]

    def run_once(*, fn: Callable[[], bytes], until: Callable[[bytes], bool], **_kwargs: object) -> bytes:
        value = fn()
        assert until(value)
        return value

    monkeypatch.setattr(helpers, "poll_until", run_once)

    result = helpers.wait_for_hosted_cluster_kubeconfig(
        k8s=k8s, hosted_cluster_namespace="osac-order", hosted_cluster_name="order"
    )

    assert result == b"apiVersion: v1\n"
    assert k8s.get_json.call_args_list[0].kwargs == {
        "resource": "hostedcontrolplane",
        "name": "order",
        "namespace": "osac-order-order",
    }
    assert k8s.get_json.call_args_list[1].kwargs == {
        "resource": "secret",
        "name": "admin-kubeconfig",
        "namespace": "osac-order-order",
    }


def test_wait_for_hosted_cluster_kubeconfig_recovers_after_a_command_error(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.get_json.side_effect = [
        subprocess.CalledProcessError(1, ["kubectl"], stderr="Forbidden"),
        {"status": {"kubeConfig": {"name": "admin-kubeconfig", "key": "kubeconfig"}}},
        {"data": {"kubeconfig": base64.b64encode(b"apiVersion: v1\n").decode()}},
    ]
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    assert (
        helpers.wait_for_hosted_cluster_kubeconfig(
            k8s=k8s, hosted_cluster_namespace="osac-order", hosted_cluster_name="order"
        )
        == b"apiVersion: v1\n"
    )


def test_workload_cluster_health_waits_for_worker_and_all_operators() -> None:
    nodes = [
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1)


def test_workload_cluster_health_ignores_control_plane_nodes() -> None:
    nodes = [
        {
            "metadata": {"name": "control-plane-0", "labels": {"node-role.kubernetes.io/control-plane": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "False"}]},
        },
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        },
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is True


def test_workload_cluster_health_waits_for_worker_before_checking_operators() -> None:
    operators = [{"metadata": {"name": "ingress"}, "status": {"conditions": [{"type": "Degraded", "status": "True"}]}}]

    assert helpers.workload_cluster_health_ready(nodes=[], operators=operators, expected_workers=1) is False


def test_workload_cluster_health_requires_worker_role_label() -> None:
    nodes = [
        {
            "metadata": {"name": "infra-0", "labels": {"node-role.kubernetes.io/infra": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is False


def test_workload_cluster_health_rejects_worker_without_ready_true() -> None:
    nodes = [
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        },
        {
            "metadata": {"name": "worker-1", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "Unknown"}]},
        },
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is False


def test_workload_cluster_health_rejects_worker_without_ready_condition() -> None:
    nodes = [
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "MemoryPressure", "status": "False"}]},
        }
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is False


def test_workload_cluster_health_rejects_not_ready_worker_with_control_plane_role() -> None:
    nodes = [
        {
            "metadata": {
                "name": "worker-control-plane-0",
                "labels": {"node-role.kubernetes.io/worker": "", "node-role.kubernetes.io/control-plane": ""},
            },
            "status": {"conditions": [{"type": "Ready", "status": "False"}]},
        },
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        },
    ]
    operators = [
        {
            "metadata": {"name": "network"},
            "status": {
                "conditions": [
                    {"type": "Available", "status": "True"},
                    {"type": "Progressing", "status": "False"},
                    {"type": "Degraded", "status": "False"},
                ]
            },
        }
    ]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is False


def test_workload_cluster_health_waits_for_degraded_operator_to_clear() -> None:
    nodes = [
        {
            "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
    ]
    operators = [{"metadata": {"name": "network"}, "status": {"conditions": [{"type": "Degraded", "status": "True"}]}}]

    assert helpers.workload_cluster_health_ready(nodes=nodes, operators=operators, expected_workers=1) is False


def test_workload_provider_ids_assigned_for_agent_bound_workers() -> None:
    nodes = [_node(name="worker-0")]

    assert helpers.workload_cluster_provider_ids_assigned(nodes=nodes, expected_workers=1) is True


def test_workload_provider_ids_reject_an_unbound_worker() -> None:
    nodes = [_node(name="worker-0"), _node(name="worker-1", provider_id="")]

    assert helpers.workload_cluster_provider_ids_assigned(nodes=nodes, expected_workers=2) is False


def test_workload_provider_ids_reject_a_foreign_scheme() -> None:
    nodes = [_node(name="worker-0", provider_id="aws:///eu-west-1a/i-0123456789")]

    assert helpers.workload_cluster_provider_ids_assigned(nodes=nodes, expected_workers=1) is False


def test_workload_provider_ids_reject_a_bare_scheme() -> None:
    nodes = [_node(name="worker-0", provider_id="agent://")]

    assert helpers.workload_cluster_provider_ids_assigned(nodes=nodes, expected_workers=1) is False


def test_workload_provider_ids_require_every_expected_worker() -> None:
    nodes = [_node(name="worker-0")]

    assert helpers.workload_cluster_provider_ids_assigned(nodes=nodes, expected_workers=2) is False


def test_wait_for_workload_node_provider_ids_reports_the_unbound_node(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.list_json.return_value = {"items": [_node(name="worker-0"), _node(name="worker-1", provider_id="")]}

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    with pytest.raises(AssertionError, match=r"missing on \['worker-1'\]"):
        helpers.wait_for_workload_node_provider_ids(k8s=k8s, expected_workers=2)


def test_workload_cluster_health_checks_provider_ids_before_operators(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[str] = []

    monkeypatch.setattr(helpers, "wait_for_workload_node_provider_ids", lambda **_kwargs: events.append("provider-id"))
    monkeypatch.setattr(helpers, "poll_until", lambda **_kwargs: events.append("operators"))

    helpers.wait_for_workload_cluster_health(k8s=Mock(), expected_workers=1)

    assert events == ["provider-id", "operators"]


def test_hosted_cluster_version_conditions_settled_when_cvo_is_healthy() -> None:
    assert helpers.hosted_cluster_version_conditions_settled(_hosted_cluster(), _hosted_control_plane()) is True


@pytest.mark.parametrize("unhealthy", [{"succeeding": "False"}, {"available": "False"}, {"progressing": "True"}])
def test_hosted_cluster_version_conditions_reject_an_installing_cvo(unhealthy: dict[str, str]) -> None:
    assert (
        helpers.hosted_cluster_version_conditions_settled(_hosted_cluster(**unhealthy), _hosted_control_plane())
        is False
    )


def test_hosted_cluster_version_conditions_reject_a_failing_control_plane() -> None:
    assert (
        helpers.hosted_cluster_version_conditions_settled(_hosted_cluster(), _hosted_control_plane(failing="True"))
        is False
    )


def test_hosted_cluster_version_conditions_reject_a_missing_condition() -> None:
    empty: dict[str, object] = {"status": {"conditions": []}}

    assert helpers.hosted_cluster_version_conditions_settled(empty, _hosted_control_plane()) is False
    assert helpers.hosted_cluster_version_conditions_settled(_hosted_cluster(), empty) is False


def test_wait_for_hosted_cluster_version_health_reports_unsettled_conditions(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.get_json.side_effect = lambda *, resource, **_kwargs: (
        _hosted_control_plane(failing="True") if resource == "hostedcontrolplane" else _hosted_cluster()
    )

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    with pytest.raises(AssertionError, match=r"'ClusterVersionFailing': 'True'"):
        helpers.wait_for_hosted_cluster_version_health(k8s=k8s, name="order-test", namespace="osac-order-test")

    # Retried until the budget runs out, so each iteration reads both resources.
    assert k8s.get_json.call_args_list[:2] == [
        call(resource="hostedcluster", name="order-test", namespace="osac-order-test"),
        call(resource="hostedcontrolplane", name="order-test", namespace="osac-order-test-order-test"),
    ]


def test_guest_health_recovers_from_access_and_readiness_delays(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.list_json.side_effect = [
        subprocess.CalledProcessError(1, ["kubectl"], stderr="Forbidden"),
        {"items": []},
        {"items": [_node(name="worker-0")]},
        {"items": [_node(name="worker-0")]},
        {
            "items": [
                {
                    "metadata": {"name": "network"},
                    "status": {
                        "conditions": [
                            {"type": "Available", "status": "True"},
                            {"type": "Progressing", "status": "False"},
                            {"type": "Degraded", "status": "False"},
                        ]
                    },
                }
            ]
        },
    ]

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    helpers.wait_for_workload_cluster_health(k8s=k8s, cluster_name="guest-cluster", expected_workers=1)


def test_guest_health_has_at_most_five_minutes_of_scheduled_waits() -> None:
    scheduled_wait = (helpers._WORKLOAD_HEALTH_RETRIES - 1) * helpers._WORKLOAD_HEALTH_DELAY

    assert 0 < scheduled_wait <= 5 * 60


def test_wait_for_cluster_guest_readiness_waits_for_hub_ready_before_guest_checks(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    events: list[str] = []
    poll_identity: dict[str, object] = {}
    node_pool = {"status": {"replicas": 1, "nodesInfo": {"nodeVersions": [{"readyNodeCount": 1}]}}}

    monkeypatch.setattr(helpers, "wait_for_workload_cluster_health", lambda **_kwargs: events.append("guest"))
    monkeypatch.setattr(helpers, "wait_for_cluster_ready", lambda **_kwargs: events.append("ready"))
    monkeypatch.setattr(helpers, "wait_for_hosted_cluster_version_health", lambda **_kwargs: events.append("version"))

    def run_once(
        *,
        fn: Callable[[], dict[str, object] | None],
        until: Callable[[dict[str, object] | None], bool],
        **kwargs: object,
    ) -> dict[str, object] | None:
        poll_identity["resource_kind"] = kwargs["resource_kind"]
        poll_identity["resource_id"] = kwargs["resource_id"]
        events.append("nodepool")
        value = fn()
        assert until(value)
        return value

    monkeypatch.setattr(helpers, "poll_until", run_once)

    helpers.wait_for_cluster_guest_readiness(
        k8s=Mock(),
        name="order-test",
        workload_k8s=Mock(),
        cluster_name="guest-cluster",
        expected_workers=1,
        get_node_pool=lambda: node_pool,
        expected_ready_nodes=1,
        node_pool_description="order-test NodePool",
        node_pool_id="nodepool-guest-cluster-worker-node-set",
        hosted_cluster_namespace="osac-order-test",
    )

    assert events == ["ready", "guest", "nodepool", "version"]
    assert poll_identity == {
        "resource_kind": WaitResourceKind.NODE_POOL,
        "resource_id": "nodepool-guest-cluster-worker-node-set",
    }


def test_guest_health_checks_the_guest_before_the_hosted_cluster_conditions(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[str] = []
    guest = Mock()

    monkeypatch.setattr(
        helpers, "wait_for_hosted_cluster_reference", lambda **_kwargs: ("order-test", "osac-order-test")
    )
    monkeypatch.setattr(helpers, "wait_for_hosted_cluster_kubeconfig", lambda **_kwargs: b"apiVersion: v1\n")
    monkeypatch.setattr(helpers, "K8sClient", lambda **_kwargs: guest)
    monkeypatch.setattr(
        helpers,
        "wait_for_workload_cluster_health",
        lambda **kwargs: events.append(f"guest:{kwargs['k8s'] is guest}:{kwargs['expected_workers']}"),
    )
    monkeypatch.setattr(
        helpers,
        "wait_for_hosted_cluster_version_health",
        lambda **kwargs: events.append(f"version:{kwargs['name']}/{kwargs['namespace']}"),
    )

    helpers.wait_for_guest_cluster_health(k8s=Mock(), co_name="order-test", expected_workers=2)

    assert events == ["guest:True:2", "version:order-test/osac-order-test"]



def test_guest_k8s_client_does_not_impersonate_system_admin() -> None:
    client = K8sClient(namespace="guest", kubeconfig="guest.kubeconfig", as_system_admin=False)

    assert client._base() == ["kubectl", "--kubeconfig", "guest.kubeconfig"]


def test_k8s_client_supports_explicit_namespaces_and_cluster_scoped_lists(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[tuple[str, ...]] = []

    def run(*args: str, **_kwargs: object) -> str:
        commands.append(args)
        return '{"items": []}'

    monkeypatch.setattr(k8s_client_module, "run", run)
    client = K8sClient(namespace="hub", as_system_admin=False)

    client.get_json(resource="hostedcontrolplane", name="cluster", namespace="hcp")
    client.list_json(resource="nodes")
    client.list_json(resource="nodepools", namespace="hcp")

    assert commands == [
        ("kubectl", "get", "hostedcontrolplane", "cluster", "-n", "hcp", "-o", "json"),
        ("kubectl", "get", "nodes", "-o", "json"),
        ("kubectl", "get", "nodepools", "-n", "hcp", "-o", "json"),
    ]
