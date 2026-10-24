from __future__ import annotations

import json
import logging
import subprocess
from typing import Any

from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)

CONSOLE_API_GROUP = "console.osac.openshift.io"
CONSOLE_RESOURCES = ("computeinstances/console", "computeinstances/vnc")
HUB_ACCESS_CONSOLE_RULE: dict[str, list[str]] = {
    "apiGroups": [CONSOLE_API_GROUP],
    "resources": list(CONSOLE_RESOURCES),
    "verbs": ["get"],
}


def hub_access_cluster_role_name(namespace: str) -> str:
    """Return the installer ClusterRole name for hub-access in namespace."""
    return f"{namespace}-hub-access"


def cluster_role_grants_console_access(rules: list[Any] | None) -> bool:
    """Return True when rules allow get on serial and VNC ComputeInstance console subresources."""
    missing = set(CONSOLE_RESOURCES)
    for rule in rules or []:
        if not isinstance(rule, dict):
            continue
        groups = set(rule.get("apiGroups") or [])
        verbs = set(rule.get("verbs") or [])
        resources = set(rule.get("resources") or [])
        if CONSOLE_API_GROUP not in groups and "*" not in groups:
            continue
        if "get" not in verbs and "*" not in verbs:
            continue
        if "*" in resources:
            return True
        missing -= resources
        if not missing:
            return True
    return False


def ensure_hub_access_console_rbac(*, k8s: K8sClient, namespace: str) -> bool:
    """Add chart console get rules to ``{namespace}-hub-access`` when the live ClusterRole omitted them.

    The fulfillment console proxy uses this ClusterRole (hub-access ServiceAccount)
    for the backend WebSocket to ``console.osac.openshift.io``. A 403 there is a
    transport failure, not overlay isolation. Matches ``osac-installer`` hub-access.yaml.

    Returns True when a patch was applied.
    """
    name = hub_access_cluster_role_name(namespace)
    try:
        role = k8s.get_cluster_json(resource="clusterrole", name=name)
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(
            f"ClusterRole {name} not found; cannot grant {CONSOLE_API_GROUP} get "
            f"for {', '.join(CONSOLE_RESOURCES)} (hubAccess must be enabled)"
        ) from exc
    rules = role.get("rules")
    if not isinstance(rules, list):
        rules = []
    if cluster_role_grants_console_access(rules):
        logger.info("ClusterRole %s already grants serial/VNC console get", name)
        return False
    if rules:
        ops: list[dict[str, Any]] = [{"op": "add", "path": "/rules/-", "value": HUB_ACCESS_CONSOLE_RULE}]
    else:
        ops = [{"op": "add", "path": "/rules", "value": [HUB_ACCESS_CONSOLE_RULE]}]
    logger.info("Patching ClusterRole %s to add %s get on %s", name, CONSOLE_API_GROUP, ",".join(CONSOLE_RESOURCES))
    k8s.patch_cluster(resource="clusterrole", name=name, patch=json.dumps(ops))
    updated = k8s.get_cluster_json(resource="clusterrole", name=name)
    updated_rules = updated.get("rules")
    if not cluster_role_grants_console_access(updated_rules if isinstance(updated_rules, list) else []):
        raise RuntimeError(f"ClusterRole {name} still missing {CONSOLE_API_GROUP} console get after patch")
    return True


def _is_chart_console_rule(rule: object) -> bool:
    """Return True when rule is exactly the installer hub-access console get rule."""
    if not isinstance(rule, dict):
        return False
    return (
        set(rule.get("apiGroups") or []) == {CONSOLE_API_GROUP}
        and set(rule.get("resources") or []) == set(CONSOLE_RESOURCES)
        and set(rule.get("verbs") or []) == {"get"}
    )


def remove_hub_access_console_rbac(*, k8s: K8sClient, namespace: str) -> bool:
    """Remove the chart console get rule from ``{namespace}-hub-access`` if present.

    Returns True when a patch was applied.
    """
    name = hub_access_cluster_role_name(namespace)
    try:
        role = k8s.get_cluster_json(resource="clusterrole", name=name)
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(f"ClusterRole {name} not found; cannot remove console get rules") from exc
    rules = role.get("rules")
    if not isinstance(rules, list):
        return False
    indexes = [i for i, rule in enumerate(rules) if _is_chart_console_rule(rule)]
    if not indexes:
        logger.info("ClusterRole %s has no chart console get rule to remove", name)
        return False
    ops = [{"op": "remove", "path": f"/rules/{i}"} for i in reversed(indexes)]
    logger.info("Removing console get rule from ClusterRole %s", name)
    k8s.patch_cluster(resource="clusterrole", name=name, patch=json.dumps(ops))
    return True
