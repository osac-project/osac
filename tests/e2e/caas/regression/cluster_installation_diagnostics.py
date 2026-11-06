"""Read-only diagnostics for the two-node-set CaaS installation E2E."""

from __future__ import annotations

import json
import logging
import tempfile
from dataclasses import dataclass
from typing import Any

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    node_pools_by_node_set,
    wait_for_cluster_order_hosted_cluster_reference,
    wait_for_hosted_cluster_kubeconfig,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import time

POST_INSTALL_MONITOR_SECONDS = 10 * 60
_DIAGNOSTIC_INTERVAL_SECONDS = 60

diagnostics_logger = logging.getLogger(f"{__name__}.diagnostics")


@dataclass(frozen=True)
class ClusterReferences:
    """Identity of the resources owned by one CaaS installation."""

    uuid: str
    co_name: str
    namespace: str
    hc_name: str
    expected_node_sets: set[str]


def _mapping(value: object) -> dict[str, Any]:
    """Return a mapping, treating an unexpected provider shape as empty."""
    return value if isinstance(value, dict) else {}


def _sequence(value: object) -> list[Any]:
    """Return a sequence, treating an unexpected provider shape as empty."""
    return value if isinstance(value, list) else []


def _condition(resource: dict[str, Any], kind: str) -> dict[str, Any]:
    """Return the named status condition, or an empty mapping if absent."""
    return next(
        (
            item
            for item in _sequence(_mapping(resource.get("status", {})).get("conditions", []))
            if _mapping(item).get("type") == kind
        ),
        {},
    )


def _condition_summary(resource: dict[str, Any]) -> list[dict[str, str]]:
    """Return condition identity and health without messages or secrets."""
    return [
        {
            "type": _mapping(condition).get("type", ""),
            "status": _mapping(condition).get("status", ""),
            "reason": _mapping(condition).get("reason", ""),
        }
        for condition in _sequence(_mapping(resource.get("status", {})).get("conditions", []))
    ]


def _safe_operator_versions(operator: dict[str, Any]) -> list[dict[str, str]]:
    """Return only operator version names and numbers, never related objects."""
    return [
        {"name": _mapping(version).get("name", ""), "version": _mapping(version).get("version", "")}
        for version in _sequence(_mapping(operator.get("status", {})).get("versions", []))
    ]


def cluster_references(*, k8s: K8sClient, uuid: str, co_name: str, expected_node_sets: set[str]) -> ClusterReferences:
    """Resolve the test-owned HostedCluster and NodePool names."""
    hc_name, namespace = wait_for_cluster_order_hosted_cluster_reference(k8s=k8s, name=co_name, retries=60, delay=10)
    return ClusterReferences(
        uuid=uuid, co_name=co_name, namespace=namespace, hc_name=hc_name, expected_node_sets=expected_node_sets
    )


def _snapshot(*, k8s: K8sClient, grpc: GRPCClient, refs: ClusterReferences, workload_k8s: K8sClient) -> dict[str, Any]:
    """Read test-owned API, HyperShift, and guest health resources."""
    hosted = k8s.get_json(
        resource="hostedclusters.hypershift.openshift.io", name=refs.hc_name, namespace=refs.namespace
    )
    pools = node_pools_by_node_set(
        k8s=k8s, namespace=refs.namespace, co_name=refs.co_name, expected_node_sets=refs.expected_node_sets
    )
    cluster_operators = _sequence(
        _mapping(workload_k8s.list_json(resource="clusteroperators.config.openshift.io")).get("items", [])
    )
    return {
        "order": k8s.get_json(resource="clusterorder", name=refs.co_name),
        "cluster": grpc.get_cluster(cluster_id=refs.uuid)["object"],
        "hosted": hosted,
        "pools": pools,
        "cluster_operators": cluster_operators,
    }


def _post_install_diagnostics(snapshot: dict[str, Any]) -> dict[str, Any]:
    """Summarize HyperShift and guest operator health without raw payloads."""
    hosted = _mapping(snapshot.get("hosted", {}))
    version = _mapping(_mapping(hosted.get("status", {})).get("version", {}))
    history = _sequence(version.get("history", []))
    newest = _mapping(history[0]) if history else {}
    pools: dict[str, dict[str, Any]] = {}
    for node_set, pool in _mapping(snapshot.get("pools", {})).items():
        pools[node_set] = {
            "version": _mapping(pool).get("status", {}).get("version", ""),
            "ready": _condition(pool, "Ready").get("status") == "True",
            "all_machines_ready": _condition(pool, "AllMachinesReady").get("status") == "True",
            "updating_version": _condition(pool, "UpdatingVersion").get("status") == "True",
            "conditions": _condition_summary(pool),
        }
    operators: dict[str, dict[str, Any]] = {}
    for operator in _sequence(snapshot.get("cluster_operators", [])):
        name = _mapping(_mapping(operator).get("metadata", {})).get("name", "")
        operators[name] = {
            "available": _condition(operator, "Available").get("status") == "True",
            "progressing": _condition(operator, "Progressing").get("status") == "True",
            "degraded": _condition(operator, "Degraded").get("status") == "True",
            "conditions": _condition_summary(operator),
            "versions": _safe_operator_versions(_mapping(operator)),
        }
    return {
        "cluster_state": _mapping(snapshot.get("cluster", {})).get("status", {}).get("state", ""),
        "order_phase": _mapping(snapshot.get("order", {})).get("status", {}).get("phase", ""),
        "hosted_available": _condition(hosted, "Available").get("status") == "True",
        "hosted_degraded": _condition(hosted, "Degraded").get("status") == "True",
        "hosted_progressing": _condition(hosted, "ClusterVersionProgressing").get("status") == "True",
        "hosted_conditions": _condition_summary(hosted),
        "hosted_desired_version": _mapping(version.get("desired", {})).get("version", ""),
        "hosted_history_version": newest.get("version", ""),
        "hosted_history_state": newest.get("state", ""),
        "node_pools": pools,
        "cluster_operators": operators,
    }


def _assert_post_install_healthy(diagnostics: dict[str, Any], expected_node_sets: set[str]) -> None:
    """Require the installed HyperShift cluster and guest operators to stay healthy."""
    assert diagnostics["cluster_state"] == "CLUSTER_STATE_READY", "OSAC Cluster became unhealthy after installation"
    assert diagnostics["order_phase"] == "Ready", "ClusterOrder became unhealthy after installation"
    assert diagnostics["hosted_available"], "HostedCluster became unhealthy after installation"
    assert not diagnostics["hosted_degraded"], "HostedCluster became degraded after installation"
    assert not diagnostics["hosted_progressing"], "HostedCluster kept progressing after installation"

    node_pools = _mapping(diagnostics.get("node_pools", {}))
    assert set(node_pools) == expected_node_sets, "Expected NodePool NodeSets are missing"
    for node_set, pool in node_pools.items():
        assert pool["version"] != "", f"{node_set} NodePool lost its version"
        assert pool["ready"], f"{node_set} NodePool became unhealthy"
        assert pool["all_machines_ready"], f"{node_set} NodePool machines became unhealthy"
        assert not pool["updating_version"], f"{node_set} NodePool kept updating"

    operators = _mapping(diagnostics.get("cluster_operators", {}))
    assert operators, "Guest ClusterOperator diagnostics became unavailable"
    for name, operator in operators.items():
        assert operator["available"], f"{name} ClusterOperator became unavailable"
        assert not operator["progressing"], f"{name} ClusterOperator kept progressing"
        assert not operator["degraded"], f"{name} ClusterOperator became degraded"


def _log_diagnostics(
    *, component: str, data: dict[str, Any], elapsed_seconds: float | None = None, final: bool = False
) -> None:
    """Log one stable, redacted diagnostic envelope."""
    payload: dict[str, Any] = {"component": component}
    if elapsed_seconds is not None:
        payload["elapsed_seconds"] = round(elapsed_seconds, 1)
    if final:
        payload["final"] = True
    payload.update(data)
    if diagnostics_logger.isEnabledFor(logging.INFO):
        diagnostics_logger.info("CaaS installation diagnostics: %s", json.dumps(payload, sort_keys=True))


def monitor_post_install_health(*, k8s: K8sClient, grpc: GRPCClient, refs: ClusterReferences) -> None:
    """Monitor the installed cluster for the configured stability window."""
    workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
        k8s=k8s, hosted_cluster_namespace=refs.namespace, hosted_cluster_name=refs.hc_name
    )
    started = time.monotonic()

    with tempfile.NamedTemporaryFile(prefix="osac-post-install-", suffix=".kubeconfig") as kubeconfig_file:
        kubeconfig_file.write(workload_kubeconfig)
        kubeconfig_file.flush()
        workload_k8s = K8sClient(namespace=refs.namespace, kubeconfig=kubeconfig_file.name, as_system_admin=False)
        last_attempt_successful = False
        final_data: dict[str, Any] | None = None
        while True:
            try:
                snapshot = _snapshot(k8s=k8s, grpc=grpc, refs=refs, workload_k8s=workload_k8s)
                final_data = _post_install_diagnostics(snapshot)
                last_attempt_successful = True
                _log_diagnostics(
                    component="post-install health", data=final_data, elapsed_seconds=time.monotonic() - started
                )
                _assert_post_install_healthy(final_data, refs.expected_node_sets)
            except AssertionError:
                if final_data is not None:
                    _log_diagnostics(
                        component="post-install health",
                        data=final_data,
                        elapsed_seconds=time.monotonic() - started,
                        final=True,
                    )
                raise
            except Exception:
                last_attempt_successful = False
                _log_diagnostics(
                    component="post-install health",
                    data={"diagnostics_error": "lookup_failed"},
                    elapsed_seconds=time.monotonic() - started,
                )
            remaining = started + POST_INSTALL_MONITOR_SECONDS - time.monotonic()
            if remaining <= 0:
                if final_data is None:
                    raise AssertionError("Post-install health diagnostics could not be collected")
                if not last_attempt_successful:
                    raise AssertionError("Post-install health diagnostics were unavailable at the window end")
                if final_data is not None:
                    _log_diagnostics(
                        component="post-install health",
                        data=final_data,
                        elapsed_seconds=time.monotonic() - started,
                        final=True,
                    )
                return
            time.sleep(min(_DIAGNOSTIC_INTERVAL_SECONDS, remaining))
