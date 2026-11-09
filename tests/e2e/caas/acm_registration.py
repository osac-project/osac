"""Check effective hub import policy and the selected template's registration.

A bounded absence observation is not proof of permanent non-import: the shared
administrator-owned policy is what disables the upstream controller.
"""

from __future__ import annotations

import json
import logging
from typing import Any

from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import StuckState, poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind

CLUSTERSET_LABEL = "cluster.open-cluster-management.io/clusterset"
_MANAGED_CLUSTER = "managedclusters.cluster.open-cluster-management.io"
_ABSENCE_OBSERVATIONS = 25  # 120 seconds of scheduled observation at five-second intervals.
_REGISTRATION_RETRIES = 121
logger = logging.getLogger(__name__)


def _assert_disabled_agent(agent: dict[str, Any]) -> None:
    spec, status = agent["spec"], agent.get("status", {})
    values = [
        env.get("value", "").lower()
        for container in spec["template"]["spec"]["containers"]
        if container["name"] == "hypershift-addon-agent"
        for env in container.get("env", [])
        if env["name"] == "DISABLE_AUTO_IMPORT"
    ]
    replicas = spec.get("replicas", 1)
    assert (
        values == ["true"]
        and status.get("observedGeneration", 0) >= agent["metadata"]["generation"]
        and all(status.get(field, 0) == replicas for field in ("replicas", "updatedReplicas", "availableReplicas"))
    ), "HyperShift agent must finish its rollout with DISABLE_AUTO_IMPORT=true and no old replicas"


def assert_auto_import_disabled(*, k8s: K8sClient) -> None:
    """Reject an enabled or unverified importer; missing APIs/RBAC errors propagate."""
    policy = k8s.get_json(
        resource="addondeploymentconfigs.addon.open-cluster-management.io",
        name="hypershift-addon-deploy-config",
        namespace="multicluster-engine",
    )
    disabled = [
        variable.get("value", "").lower()
        for variable in policy.get("spec", {}).get("customizedVariables", [])
        if variable.get("name") == "autoImportDisabled"
    ]
    assert disabled == ["true"], "Hub AddOnDeploymentConfig must explicitly set autoImportDisabled=true"
    agents = json.loads(
        run(
            *k8s.base_args(),
            "get",
            "deployments",
            "--all-namespaces",
            "--selector=app=hypershift-addon-agent",
            "-o",
            "json",
        )
    )["items"]
    for agent in agents:
        _assert_disabled_agent(agent)
    if not agents:
        logger.warning(
            "Auto-import default is disabled, but there is no active import controller: "
            "this run checks the policy, not an active ACM import path"
        )


def _registration_ready(k8s: K8sClient, name: str) -> bool:
    if k8s.is_absent(resource=_MANAGED_CLUSTER, name=name):
        return False
    cluster = k8s.get_json(resource=_MANAGED_CLUSTER, name=name)
    owner = cluster.get("metadata", {}).get("annotations", {}).get("osac.openshift.io/owner-reference")
    assert owner == f"ClusterOrder/{name}", "ManagedCluster must belong to this ClusterOrder"
    conditions = {
        condition["type"]: condition["status"] for condition in cluster.get("status", {}).get("conditions", [])
    }
    return all(conditions.get(kind) == "True" for kind in ("ManagedClusterJoined", "ManagedClusterConditionAvailable"))


def assert_acm_registration(
    *, k8s: K8sClient, hosted_cluster_name: str, hosted_cluster_ns: str, cluster_template: str
) -> None:
    """Check regular-cluster absence or opted-in AI MaaS Joined/Available state."""
    assert_auto_import_disabled(k8s=k8s)
    expect_registered = cluster_template == "ocp-4-20-ai-maas"
    hosted_cluster = k8s.get_json(
        resource="hostedclusters.hypershift.openshift.io", name=hosted_cluster_name, namespace=hosted_cluster_ns
    )
    label = hosted_cluster.get("metadata", {}).get("labels", {}).get(CLUSTERSET_LABEL)
    assert label == ("default" if expect_registered else None), (
        f"HostedCluster {hosted_cluster_name} clusterset label does not match template {cluster_template}"
    )
    ids = {"cluster_order": hosted_cluster_name}
    observations = 0

    def observe_absence() -> int:
        nonlocal observations
        if not k8s.is_absent(resource=_MANAGED_CLUSTER, name=hosted_cluster_name):
            raise E2EFailure(
                Kind.ASSERTION,
                f"ManagedCluster {hosted_cluster_name} exists for an ACM-disabled CaaS template",
                ids=ids,
            )
        observations += 1
        return observations

    try:
        if expect_registered:
            poll_until(
                fn=lambda: _registration_ready(k8s, hosted_cluster_name),
                until=bool,
                retries=_REGISTRATION_RETRIES,
                delay=5,
                description=f"{hosted_cluster_name} explicit ACM registration Joined and Available",
                resource_kind=WaitResourceKind.MANAGED_CLUSTER,
                resource_id=hosted_cluster_name,
            )
        else:
            poll_until(
                fn=observe_absence,
                until=lambda count: count >= _ABSENCE_OBSERVATIONS,
                retries=_ABSENCE_OBSERVATIONS,
                delay=5,
                description=f"{hosted_cluster_name} ManagedCluster absence observation window",
                resource_kind=WaitResourceKind.MANAGED_CLUSTER,
                resource_id=hosted_cluster_name,
            )
    except StuckState:
        raise E2EFailure(
            Kind.STUCK_STATE, f"ACM registration policy check timed out for {hosted_cluster_name}", ids=ids
        ) from None
    assert_auto_import_disabled(k8s=k8s)
