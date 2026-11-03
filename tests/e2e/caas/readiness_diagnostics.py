"""Allowlisted CaaS readiness facts for timeout diagnostics."""

from __future__ import annotations

import json
import re
import subprocess
from collections import Counter
from typing import Any

from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import run_unchecked

_SAFE_TOKEN = re.compile(r"[A-Za-z][A-Za-z0-9_-]{0,63}\Z")
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


def _safe_count(value: object) -> str:
    return str(value) if type(value) is int and value >= 0 else "unknown"


def _read(k8s: K8sClient, resource: str, namespace: str, name: str | None = None) -> dict[str, Any] | None:
    args = [*k8s._base(), "get", resource]
    if name is not None:
        args.append(name)
    args.extend(["-n", namespace, "-o", "json"])
    try:
        output, rc = run_unchecked(*args, timeout=3)
        if rc != 0:
            return None
        result = json.loads(output)
        return result if isinstance(result, dict) else None
    except (subprocess.TimeoutExpired, ValueError):
        return None


def _items(k8s: K8sClient, resource: str, namespace: str) -> list[dict[str, Any]] | None:
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

    return lines
