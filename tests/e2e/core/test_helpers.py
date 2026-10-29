from __future__ import annotations

import base64
import subprocess
from collections.abc import Callable
from unittest.mock import Mock

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient


def test_wait_for_cluster_ready_fails_immediately_on_failed_order(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.get_cluster_order_phase.return_value = "Failed"

    def run_once(*, fn: Callable[[], str], until: Callable[[str], bool], **_kwargs: object) -> str:
        return fn()

    monkeypatch.setattr(helpers, "poll_until", run_once)

    with pytest.raises(AssertionError, match="order-test entered Failed phase"):
        helpers.wait_for_cluster_ready(k8s=k8s, name="order-test")


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


def test_guest_health_recovers_from_access_and_readiness_delays(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock()
    k8s.list_json.side_effect = [
        subprocess.CalledProcessError(1, ["kubectl"], stderr="Forbidden"),
        {"items": []},
        {"items": []},
        {
            "items": [
                {
                    "metadata": {"name": "worker-0", "labels": {"node-role.kubernetes.io/worker": ""}},
                    "status": {"conditions": [{"type": "Ready", "status": "True"}]},
                }
            ]
        },
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

    helpers.wait_for_workload_cluster_health(k8s=k8s, expected_workers=1)


def test_guest_health_has_at_most_five_minutes_of_scheduled_waits() -> None:
    scheduled_wait = (helpers._WORKLOAD_HEALTH_RETRIES - 1) * helpers._WORKLOAD_HEALTH_DELAY

    assert 0 < scheduled_wait <= 5 * 60


def test_wait_for_cluster_guest_readiness_waits_for_hub_ready_before_guest_checks(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    events: list[str] = []
    node_pool = {"status": {"replicas": 1, "nodesInfo": {"nodeVersions": [{"readyNodeCount": 1}]}}}

    monkeypatch.setattr(helpers, "wait_for_workload_cluster_health", lambda **_kwargs: events.append("guest"))
    monkeypatch.setattr(helpers, "wait_for_cluster_ready", lambda **_kwargs: events.append("ready"))

    def run_once(
        *,
        fn: Callable[[], dict[str, object] | None],
        until: Callable[[dict[str, object] | None], bool],
        **_kwargs: object,
    ) -> dict[str, object] | None:
        events.append("nodepool")
        value = fn()
        assert until(value)
        return value

    monkeypatch.setattr(helpers, "poll_until", run_once)

    helpers.wait_for_cluster_guest_readiness(
        k8s=Mock(),
        name="order-test",
        workload_k8s=Mock(),
        expected_workers=1,
        get_node_pool=lambda: node_pool,
        expected_ready_nodes=1,
        node_pool_description="order-test NodePool",
    )

    assert events == ["ready", "guest", "nodepool"]


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
