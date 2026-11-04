"""Allowlisted CaaS readiness facts for timeout diagnostics."""

from __future__ import annotations

import base64
import json
import re
import subprocess
import tempfile
from collections import Counter
from typing import Any

from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import run_unchecked

_SAFE_TOKEN = re.compile(r"[A-Za-z][A-Za-z0-9_-]{0,63}\Z")
_SAFE_LABEL = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}\Z")
_SAFE_AGENT_STATES = frozenset(
    {
        "discovering",
        "discovering-unbound",
        "known",
        "known-unbound",
        "binding",
        "insufficient",
        "insufficient-unbound",
        "installing",
        "installing-in-progress",
        "installed",
        "error",
    }
)


def _safe_token(value: object) -> str:
    return value if isinstance(value, str) and _SAFE_TOKEN.fullmatch(value) else "omitted"


def _safe_label(value: object) -> str:
    return value if isinstance(value, str) and _SAFE_LABEL.fullmatch(value) else "omitted"


def _safe_count(value: object) -> str:
    return str(value) if type(value) is int and value >= 0 else "unknown"


def _read(k8s: K8sClient, resource: str, namespace: str | None, name: str | None = None) -> dict[str, Any] | None:
    args = [*k8s._base(), "get", resource]
    if name is not None:
        args.append(name)
    if namespace is not None:
        args.extend(["-n", namespace])
    args.extend(["-o", "json"])
    try:
        output, rc = run_unchecked(*args, timeout=3)
        if rc != 0:
            return None
        result = json.loads(output)
        return result if isinstance(result, dict) else None
    except (subprocess.TimeoutExpired, ValueError):
        return None


def _items(k8s: K8sClient, resource: str, namespace: str | None) -> list[dict[str, Any]] | None:
    response = _read(k8s, resource, namespace)
    if response is None or not isinstance(response.get("items"), list):
        return None
    return [item for item in response["items"] if isinstance(item, dict)]


def _condition_lines(prefix: str, resource: dict[str, Any] | None) -> list[str]:
    if resource is None:
        return [f"{prefix} present=False"]

    lines = [f"{prefix} present=True"]
    conditions = resource.get("status", {}).get("conditions", [])
    if not isinstance(conditions, list):
        return lines
    for condition in conditions[:32]:
        if not isinstance(condition, dict):
            continue
        status = condition.get("status")
        status = status if status in {"True", "False", "Unknown"} else "omitted"
        lines.append(
            f"{prefix} condition={_safe_token(condition.get('type'))} "
            f"status={status} reason={_safe_token(condition.get('reason'))}"
        )
    return lines


def _node_pool_ready_count(node_pool: dict[str, Any]) -> int:
    versions = node_pool.get("status", {}).get("nodesInfo", {}).get("nodeVersions", [])
    if not isinstance(versions, list):
        return 0
    return sum(
        value.get("readyNodeCount", 0)
        for value in versions
        if isinstance(value, dict) and type(value.get("readyNodeCount", 0)) is int
    )


def _condition_status(resource: dict[str, Any], condition_type: str) -> str:
    conditions = resource.get("status", {}).get("conditions", [])
    if not isinstance(conditions, list):
        return ""
    for condition in conditions:
        if isinstance(condition, dict) and condition.get("type") == condition_type:
            status = condition.get("status")
            return status if isinstance(status, str) else ""
    return ""


def _operator_lines(prefix: str, operators: list[dict[str, Any]]) -> list[str]:
    lines = [f"{prefix} count={len(operators)}"]
    for operator in operators:
        metadata = operator.get("metadata", {})
        name = _safe_label(metadata.get("name") if isinstance(metadata, dict) else None)
        available = _condition_status(operator, "Available") == "True"
        progressing = _condition_status(operator, "Progressing") == "True"
        degraded = _condition_status(operator, "Degraded") == "True"
        lines.append(f"{prefix} name={name} available={available} progressing={progressing} degraded={degraded}")
        if not available or progressing or degraded:
            for condition_type in ("Available", "Progressing", "Degraded"):
                condition = next(
                    (
                        item
                        for item in operator.get("status", {}).get("conditions", [])
                        if isinstance(item, dict) and item.get("type") == condition_type
                    ),
                    {},
                )
                status = condition.get("status")
                status = status if status in {"True", "False", "Unknown"} else "omitted"
                lines.append(
                    f"{prefix} name={name} condition={_safe_token(condition.get('type'))} "
                    f"status={status} reason={_safe_token(condition.get('reason'))}"
                )
    return lines


def _guest_cluster_operator_lines(
    k8s: K8sClient, control_plane_namespace: str, hosted_control_plane: dict[str, Any] | None
) -> list[str]:
    if hosted_control_plane is None:
        return ["guest clusteroperators unavailable"]

    kubeconfig_ref = hosted_control_plane.get("status", {}).get("kubeConfig", {})
    secret_name = kubeconfig_ref.get("name") if isinstance(kubeconfig_ref, dict) else ""
    secret_key = kubeconfig_ref.get("key") if isinstance(kubeconfig_ref, dict) else ""
    if not isinstance(secret_name, str) or not secret_name or not isinstance(secret_key, str) or not secret_key:
        return ["guest clusteroperators unavailable"]

    secret = _read(k8s, "secret", control_plane_namespace, secret_name)
    encoded = secret.get("data", {}).get(secret_key) if isinstance(secret, dict) else None
    if not isinstance(encoded, str) or not encoded:
        return ["guest clusteroperators unavailable"]

    try:
        kubeconfig = base64.b64decode(encoded, validate=True)
    except (ValueError, TypeError):
        return ["guest clusteroperators unavailable"]

    with tempfile.NamedTemporaryFile(prefix="osac-readiness-", suffix=".kubeconfig") as kubeconfig_file:
        kubeconfig_file.write(kubeconfig)
        kubeconfig_file.flush()
        workload_k8s = K8sClient(
            namespace=control_plane_namespace, kubeconfig=kubeconfig_file.name, as_system_admin=False
        )
        operators = _items(workload_k8s, "clusteroperators.config.openshift.io", None)
        if operators is None:
            return ["guest clusteroperators unavailable"]
        return _operator_lines("guest clusteroperator", operators)


_POD_PREFIXES = (
    "osac-aap-controller-task-",
    "osac-aap-controller-web-",
    "osac-operator-",
    "fulfillment-",
    "bmf-operator-",
    "metal3-",
    "cluster-baremetal-operator-",
    "machine-api-operator-",
)
_INTERESTING_WAITING_REASONS = {
    "ImagePullBackOff",
    "ErrImagePull",
    "CrashLoopBackOff",
    "CreateContainerConfigError",
    "RunContainerError",
}


def _pod_status_lines(k8s: K8sClient, namespace: str) -> list[str]:
    pods = _items(k8s, "pods", namespace)
    if pods is None:
        return [f"pods namespace={_safe_label(namespace)} unavailable"]

    lines: list[str] = []
    for pod in pods:
        metadata = pod.get("metadata", {})
        name = _safe_label(metadata.get("name") if isinstance(metadata, dict) else None)
        status = pod.get("status", {})
        phase = _safe_token(status.get("phase") if isinstance(status, dict) else None)
        restarts = 0
        waiting_reason = ""
        container_statuses = status.get("containerStatuses", []) if isinstance(status, dict) else []
        if isinstance(container_statuses, list):
            for container in container_statuses:
                if not isinstance(container, dict):
                    continue
                restart_count = container.get("restartCount")
                if type(restart_count) is int and restart_count >= 0:
                    restarts += restart_count
                waiting = container.get("state", {}).get("waiting", {})
                reason = waiting.get("reason") if isinstance(waiting, dict) else None
                if isinstance(reason, str) and reason in _INTERESTING_WAITING_REASONS:
                    waiting_reason = reason
        pod_reason = status.get("reason") if isinstance(status, dict) else None
        interesting = (
            name.startswith(_POD_PREFIXES)
            or waiting_reason != ""
            or (isinstance(pod_reason, str) and pod_reason == "Evicted")
        )
        if not interesting:
            continue
        lines.append(
            f"pod namespace={_safe_label(namespace)} name={name} phase={phase} "
            f"restarts={_safe_count(restarts)} waiting={_safe_token(waiting_reason)} "
            f"reason={_safe_token(pod_reason)}"
        )
    return lines


def _warning_event_lines(k8s: K8sClient, namespace: str) -> list[str]:
    events = _items(k8s, "events", namespace)
    if events is None:
        return [f"warning events namespace={_safe_label(namespace)} unavailable"]

    counts: Counter[str] = Counter()
    for event in events:
        if event.get("type") != "Warning":
            continue
        reason = event.get("reason")
        counts[_safe_token(reason)] += 1
    return [
        f"warning event namespace={_safe_label(namespace)} reason={reason} count={count}"
        for reason, count in counts.most_common(12)
    ]


def _node_pressure_lines(k8s: K8sClient) -> list[str]:
    nodes = _items(k8s, "nodes", None)
    if nodes is None:
        return ["nodes unavailable"]

    lines: list[str] = []
    for node in nodes:
        metadata = node.get("metadata", {})
        name = _safe_label(metadata.get("name") if isinstance(metadata, dict) else None)
        conditions = node.get("status", {}).get("conditions", [])
        pressures = {
            condition.get("type"): condition.get("status")
            for condition in conditions
            if isinstance(condition, dict)
            and condition.get("type") in {"DiskPressure", "MemoryPressure", "PIDPressure"}
        }
        if any(status != "False" for status in pressures.values()):
            details = " ".join(f"{kind}={status}" for kind, status in sorted(pressures.items()))
            lines.append(f"node name={name} {details}")
    return lines


def snapshot_cluster_readiness(k8s: K8sClient, name: str) -> list[str]:
    """Return bounded readiness facts without exposing resource payloads."""
    namespace = k8s.namespace
    lines: list[str] = []

    cluster_order = _read(k8s, "clusterorders.osac.openshift.io", namespace, name)
    if cluster_order is None:
        return ["clusterorder unavailable"]

    status = cluster_order.get("status", {})
    lines.append(
        "clusterorder "
        f"phase={_safe_token(status.get('phase'))} "
        f"desired_workers={_safe_count(status.get('desiredWorkers'))} "
        f"current_workers={_safe_count(status.get('currentWorkers'))} "
        f"ready_workers={_safe_count(status.get('readyWorkers'))}"
    )
    lines.extend(_condition_lines("clusterorder", cluster_order))

    reference = status.get("clusterReference", {})
    hosted_cluster_name = reference.get("hostedClusterName")
    hosted_cluster_namespace = reference.get("namespace")
    if not isinstance(hosted_cluster_name, str) or not hosted_cluster_name:
        hosted_cluster_name = name
    if not isinstance(hosted_cluster_namespace, str) or not hosted_cluster_namespace:
        hosted_cluster_namespace = f"{namespace}-{name}"
    control_plane_namespace = f"{hosted_cluster_namespace}-{hosted_cluster_name}"

    hosted_clusters = _items(k8s, "hostedclusters.hypershift.openshift.io", hosted_cluster_namespace)
    hosted_cluster = next(
        (item for item in hosted_clusters or [] if item.get("metadata", {}).get("name") == hosted_cluster_name), None
    )
    lines.extend(_condition_lines("hostedcluster", hosted_cluster))

    hosted_control_planes = _items(k8s, "hostedcontrolplanes.hypershift.openshift.io", control_plane_namespace)
    hosted_control_plane = next(
        (item for item in hosted_control_planes or [] if item.get("metadata", {}).get("name") == hosted_cluster_name),
        None,
    )
    lines.extend(_condition_lines("hostedcontrolplane", hosted_control_plane))

    node_pools = _items(k8s, "nodepools.hypershift.openshift.io", hosted_cluster_namespace)
    if node_pools is None:
        lines.append("nodepools unavailable")
    else:
        owned_pools = [
            item
            for item in node_pools
            if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == name
        ]
        desired = sum(
            item.get("spec", {}).get("replicas", 0)
            for item in owned_pools
            if type(item.get("spec", {}).get("replicas", 0)) is int
        )
        ready = sum(_node_pool_ready_count(item) for item in owned_pools)
        lines.append(f"nodepools count={len(owned_pools)} desired={desired} ready={ready}")
        for pool in owned_pools:
            pool_name = _safe_label(pool.get("metadata", {}).get("name"))
            pool_desired = pool.get("spec", {}).get("replicas", -1)
            pool_ready = _node_pool_ready_count(pool)
            lines.append(
                f"nodepool name={pool_name} desired={_safe_count(pool_desired)} ready={_safe_count(pool_ready)}"
            )
            lines.extend(_condition_lines(f"nodepool name={pool_name}", pool))

    agents = _items(k8s, "agents.agent-install.openshift.io", namespace)
    if agents is None:
        lines.append("agents unavailable")
    else:
        states: Counter[str] = Counter()
        installed = 0
        selected = 0
        for agent in agents:
            labels = agent.get("metadata", {}).get("labels", {})
            if (
                labels.get("osac.openshift.io/clusterorder") != name
                and labels.get("infraenvs.agent-install.openshift.io") != f"{name}-infraenv"
            ):
                continue
            selected += 1
            state = agent.get("status", {}).get("debugInfo", {}).get("state")
            states[state if isinstance(state, str) and state in _SAFE_AGENT_STATES else "omitted"] += 1
            if any(
                condition.get("type") == "Installed" and condition.get("status") == "True"
                for condition in agent.get("status", {}).get("conditions", [])
                if isinstance(condition, dict)
            ):
                installed += 1
        lines.append(f"agents count={selected} installed={installed}")
        lines.extend(f"agent state={state} count={count}" for state, count in sorted(states.items()))

    lines.extend(_guest_cluster_operator_lines(k8s, control_plane_namespace, hosted_control_plane))
    lines.extend(_node_pressure_lines(k8s))
    for diagnostic_namespace in ("osac-e2e-ci", "openshift-machine-api"):
        lines.extend(_pod_status_lines(k8s, diagnostic_namespace))
        lines.extend(_warning_event_lines(k8s, diagnostic_namespace))

    return lines
