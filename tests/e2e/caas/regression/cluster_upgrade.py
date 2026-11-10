"""Upgrade helpers for the CaaS two-node-set E2E."""

from __future__ import annotations

import logging
import tempfile
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, TypeVar

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_cluster_deletion_without_cleanup,
    wait_for_cluster_grpc_removal,
    wait_for_hosted_cluster_kubeconfig,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import StuckState, env, poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind

TARGET_VERSION = env("OSAC_TEST_UPGRADE_RELEASE_VERSION", "4.22.1")
TARGET_IMAGE = env(
    "OSAC_TEST_UPGRADE_RELEASE_IMAGE", f"quay.io/openshift-release-dev/ocp-release:{TARGET_VERSION}-multi"
)
READY_BASELINE_LIMIT_SECONDS = 10 * 60
READY_BASELINE_DELAY_SECONDS = 10
READY_BASELINE_RETRIES = READY_BASELINE_LIMIT_SECONDS // READY_BASELINE_DELAY_SECONDS
CP_LIMIT_SECONDS = 60 * 60
NP_LIMIT_SECONDS_PER_NODE = 30 * 60
START_LIMIT_SECONDS = 10 * 60
POLL_SECONDS = 20
_T = TypeVar("_T")
logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class _ClusterRefs:
    uuid: str
    co_name: str
    namespace: str
    hc_name: str
    pool_names: dict[str, str]


def _poll_upgrade(
    *, observe: Callable[[], _T], failure: Callable[[_T], str], description: str, limit_seconds: int, co_name: str
) -> _T:
    """Poll an upgrade gate and name the first condition that still fails."""

    def observe_with_failure() -> _T:
        nonlocal last_failure
        last_failure = "<observation failed>"
        value = observe()
        last_failure = failure(value)
        return value

    last_failure = "<no successful observation>"
    try:
        return poll_until(
            fn=observe_with_failure,
            until=lambda value: failure(value) == "",
            retries=limit_seconds // POLL_SECONDS,
            delay=POLL_SECONDS,
            description=description,
            retry_on_error=True,
            resource_kind=WaitResourceKind.CLUSTER_ORDER,
            resource_id=co_name,
        )
    except StuckState as exc:
        raise StuckState(
            f"{description}; failing gate: {last_failure}",
            exc.last_value,
            exc.elapsed_s,
            getattr(exc, "last_call_failure", ""),
        ) from None


def _catalog_version(
    *, private_grpc: GRPCClient, version: str, image: str, disk_image: str
) -> tuple[dict[str, str], bool]:
    """Use the first eligible List entry or create the fixed fixture."""
    listed = private_grpc.call(service="osac.private.v1.ClusterVersions/List")
    for candidate in listed.get("items", []):
        spec = candidate["spec"]
        disk_ref = spec.get("diskImage") or {}
        if (
            spec.get("version") == version
            and spec.get("image") == image
            and not candidate.get("metadata", {}).get("deletionTimestamp")
            and spec.get("enabled") is not False
            and spec.get("state") == "CLUSTER_VERSION_STATE_ACTIVE"
            and (disk_ref.get("id") or disk_ref.get("name"))
        ):
            return {"id": candidate["id"], "name": candidate["metadata"]["name"], "image": spec["image"]}, False
    created = private_grpc.create_cluster_version(version=version, image=image, disk_image=disk_image)
    return {**created, "image": image}, True


def _condition(resource: dict[str, Any], kind: str) -> dict[str, Any]:
    """Return the named status condition, or an empty mapping if absent."""
    return next((item for item in resource.get("status", {}).get("conditions", []) if item.get("type") == kind), {})


def _hosted_ready(hosted: dict[str, Any]) -> bool:
    """Use HyperShift health conditions for control-plane readiness."""
    return (
        _condition(hosted, "Available").get("status") == "True"
        and _condition(hosted, "Degraded").get("status") != "True"
    )


def _baseline_failure(snapshot: dict[str, Any]) -> str:
    """Return the first readiness condition that still blocks the baseline."""
    order = snapshot["order"]
    phase = order.get("status", {}).get("phase")
    if phase != "Ready":
        return f"ClusterOrder phase is {phase!r}, expected 'Ready'"
    control_plane = _condition(order, "ControlPlaneAvailable").get("status")
    if control_plane != "True":
        return f"ClusterOrder ControlPlaneAvailable is {control_plane!r}, expected 'True'"
    cluster_available = _condition(order, "ClusterAvailable").get("status")
    if cluster_available != "True":
        return f"ClusterOrder ClusterAvailable is {cluster_available!r}, expected 'True'"

    hosted = snapshot["hosted"]
    available = _condition(hosted, "Available").get("status")
    if available != "True":
        return f"HostedCluster Available is {available!r}, expected 'True'"
    degraded = _condition(hosted, "Degraded").get("status")
    if degraded == "True":
        return f"HostedCluster Degraded is {degraded!r}, expected not 'True'"

    for node_set, pool in snapshot["pools"].items():
        if not pool:
            return f"{node_set} NodePool is missing"
        ready = _condition(pool, "Ready").get("status")
        if ready != "True":
            return f"{node_set} NodePool Ready is {ready!r}, expected 'True'"
        all_machines_ready = _condition(pool, "AllMachinesReady").get("status")
        if all_machines_ready != "True":
            return f"{node_set} NodePool AllMachinesReady is {all_machines_ready!r}, expected 'True'"
    return ""


def _hosted_start_failure(hosted: dict[str, Any], target_image: str) -> str:
    """Return the first HostedCluster condition that blocks upgrade start."""
    spec_image = hosted.get("spec", {}).get("release", {}).get("image")
    if spec_image != target_image:
        return f"HostedCluster spec release image is {spec_image!r}, expected {target_image!r}"
    desired_version = hosted.get("status", {}).get("version", {}).get("desired", {}).get("version")
    if desired_version != TARGET_VERSION:
        return f"HostedCluster desired version is {desired_version!r}, expected {TARGET_VERSION!r}"
    history = hosted.get("status", {}).get("version", {}).get("history", [])
    newest = history[0] if history else {}
    progressing = _condition(hosted, "ClusterVersionProgressing").get("status")
    target_history = newest.get("version") == TARGET_VERSION and newest.get("state") in {"Partial", "Completed"}
    if not target_history and progressing != "True":
        return (
            f"HostedCluster target history is {newest.get('version')!r}/{newest.get('state')!r} "
            f"and ClusterVersionProgressing is {progressing!r}; expected target history or progressing"
        )
    return ""


def _hosted_completion_failure(hosted: dict[str, Any], target_image: str) -> str:
    """Return the first HostedCluster condition that blocks upgrade completion."""
    start_failure = _hosted_start_failure(hosted, target_image)
    if start_failure:
        return start_failure
    history = hosted.get("status", {}).get("version", {}).get("history", [])
    newest = history[0] if history else {}
    if newest.get("version") != TARGET_VERSION:
        return f"HostedCluster newest history version is {newest.get('version')!r}, expected {TARGET_VERSION!r}"
    if newest.get("state") != "Completed":
        return f"HostedCluster newest history state is {newest.get('state')!r}, expected 'Completed'"
    available = _condition(hosted, "Available").get("status")
    if available != "True":
        return f"HostedCluster Available is {available!r}, expected 'True'"
    degraded = _condition(hosted, "Degraded").get("status")
    if degraded == "True":
        return f"HostedCluster Degraded is {degraded!r}, expected not 'True'"
    return ""


def _pool_start_failure(pool: dict[str, Any], old_transition: str, target_image: str) -> str:
    """Return the first NodePool condition that blocks upgrade start."""
    spec_image = pool.get("spec", {}).get("release", {}).get("image")
    if spec_image != target_image:
        return f"NodePool spec release image is {spec_image!r}, expected {target_image!r}"
    if pool.get("status", {}).get("version") == TARGET_VERSION:
        return ""
    updating = _condition(pool, "UpdatingVersion")
    if updating.get("status") != "True":
        return f"NodePool UpdatingVersion is {updating.get('status')!r}, expected 'True'"
    if updating.get("lastTransitionTime") == old_transition:
        return (
            f"NodePool UpdatingVersion transition is {updating.get('lastTransitionTime')!r}, "
            f"which is not newer than {old_transition!r}"
        )
    return ""


def _pool_completion_failure(pool: dict[str, Any], old_transition: str, target_image: str) -> str:
    """Return the first NodePool condition that blocks upgrade completion."""
    start_failure = _pool_start_failure(pool, old_transition, target_image)
    if start_failure:
        return start_failure
    version = pool.get("status", {}).get("version")
    if version != TARGET_VERSION:
        return f"NodePool status version is {version!r}, expected {TARGET_VERSION!r}"
    updating = _condition(pool, "UpdatingVersion").get("status")
    if updating == "True":
        return f"NodePool UpdatingVersion is {updating!r}, expected not 'True'"
    ready = _condition(pool, "Ready").get("status")
    if ready != "True":
        return f"NodePool Ready is {ready!r}, expected 'True'"
    all_machines_ready = _condition(pool, "AllMachinesReady").get("status")
    if all_machines_ready != "True":
        return f"NodePool AllMachinesReady is {all_machines_ready!r}, expected 'True'"
    status_replicas = pool.get("status", {}).get("replicas")
    spec_replicas = pool.get("spec", {}).get("replicas")
    if status_replicas != spec_replicas:
        return f"NodePool ready replicas are {status_replicas!r}, expected {spec_replicas!r}"
    return ""


def _control_plane_start_failure(pair: tuple[dict[str, Any], dict[str, Any]], target_image: str) -> str:
    """Return the first control-plane start gate that still fails."""
    current, job = pair
    order_image = current["order"].get("spec", {}).get("releaseImage")
    if order_image != target_image:
        return f"ClusterOrder release image is {order_image!r}, expected {target_image!r}"
    if not job.get("jobID"):
        return "No new provision job was created"
    return _hosted_start_failure(current["hosted"], target_image)


def _control_plane_completion_failure(pair: tuple[dict[str, Any], dict[str, Any]], target_image: str) -> str:
    """Return the first control-plane completion gate that still fails."""
    current, _job = pair
    return _hosted_completion_failure(current["hosted"], target_image)


def _node_pool_start_failure(
    pair: tuple[dict[str, Any], dict[str, Any]], node_set: str, old_transition: str, target_image: str
) -> str:
    """Return the first NodePool start gate that still fails."""
    current, job = pair
    requests = current["order"].get("spec", {}).get("nodeRequests", [])
    if not any(
        request.get("nodeSet") == node_set and request.get("releaseImage") == target_image for request in requests
    ):
        return f"ClusterOrder has no {node_set!r} node request with release image {target_image!r}"
    if not job.get("jobID"):
        return "No new provision job was created"
    return _pool_start_failure(current["pools"][node_set], old_transition, target_image)


def _node_pool_completion_failure(
    pair: tuple[dict[str, Any], dict[str, Any]], node_set: str, old_transition: str, target_image: str
) -> str:
    """Return the first NodePool completion gate that still fails."""
    current, _job = pair
    return _pool_completion_failure(current["pools"][node_set], old_transition, target_image)


def _check_cluster_failure(snapshot: dict[str, Any]) -> None:
    """Do not report upgrade progress for an unusable tenant Cluster."""
    state = snapshot["cluster"].get("status", {}).get("state")
    assert state != "CLUSTER_STATE_FAILED", (
        f"Tenant Cluster entered FAILED state during upgrade (state={state!r}); upgrade cannot proceed"
    )


def _pool_identity(k8s: K8sClient, namespace: str, co_name: str, expected_node_sets: set[str]) -> dict[str, str]:
    """Find the test-owned NodePools by their public NodeSet labels."""
    pools = k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=namespace).get("items", [])
    owned = [
        item
        for item in pools
        if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == co_name
    ]
    if len(owned) != len(expected_node_sets):
        return {}
    return {item["metadata"]["labels"]["osac.openshift.io/node-set"]: item["metadata"]["name"] for item in owned}


def _snapshot(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
    """Read the test-owned API, ClusterOrder, HostedCluster, and NodePools."""
    hosted = k8s.list_json(resource="hostedclusters.hypershift.openshift.io", namespace=refs.namespace).get("items", [])
    pools = k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=refs.namespace).get("items", [])
    return {
        "order": k8s.get_json(resource="clusterorder", name=refs.co_name),
        "cluster": grpc.get_cluster(cluster_id=refs.uuid)["object"],
        "hosted": next((item for item in hosted if item.get("metadata", {}).get("name") == refs.hc_name), {}),
        "pools": {
            node_set: next((item for item in pools if item.get("metadata", {}).get("name") == name), {})
            for node_set, name in refs.pool_names.items()
        },
    }


def _new_provision_job(snapshot: dict[str, Any], previous_ids: set[str]) -> dict[str, Any]:
    """Select the latest provision job absent before the upgrade request."""
    jobs = snapshot["order"].get("status", {}).get("provisioningJobs", [])
    new = [job for job in jobs if job.get("type") == "provision" and job.get("jobID") not in previous_ids]
    return max(new, key=lambda job: job.get("timestamp", ""), default={})


def _check_hypershift_failure(resource: dict[str, Any], component: str) -> None:
    """Fail on HyperShift conditions that explicitly report upgrade failure."""
    for condition in resource.get("status", {}).get("conditions", []):
        kind = condition.get("type", "")
        reason = condition.get("reason", "")
        status = condition.get("status")
        failed = (
            (kind == "ClusterVersionFailing" and status == "True")
            or (kind == "Degraded" and status == "True" and "Fail" in reason)
            or (kind == "UpdatingVersion" and status == "False" and "Fail" in reason)
        )
        if failed:
            raise AssertionError(
                f"{component} HyperShift reported failure condition {kind!r}={status!r} "
                f"with reason {reason!r}; upgrade cannot proceed"
            )


def _assert_unchanged(
    snapshot: dict[str, Any], *, baseline_pools: dict[str, dict[str, Any]], hosted_image: str | None = None
) -> None:
    """Keep unselected HyperShift components at their baseline."""

    def unchanged(node_set: str, field: str, actual: object, expected: object) -> None:
        assert actual == expected, (
            f"Unselected {node_set} NodePool {field} changed during upgrade: expected {expected!r}, got {actual!r}"
        )

    if hosted_image is not None:
        actual_image = snapshot["hosted"].get("spec", {}).get("release", {}).get("image")
        assert actual_image == hosted_image, (
            f"Unselected HostedCluster image changed during NodePool upgrade: "
            f"expected {hosted_image!r}, got {actual_image!r}"
        )
    for node_set, baseline_pool in baseline_pools.items():
        pool = snapshot["pools"][node_set]

        unchanged(
            node_set,
            "image",
            pool.get("spec", {}).get("release", {}).get("image"),
            baseline_pool.get("spec", {}).get("release", {}).get("image"),
        )
        unchanged(
            node_set, "version", pool.get("status", {}).get("version"), baseline_pool.get("status", {}).get("version")
        )
        unchanged(
            node_set,
            "replica count",
            pool.get("spec", {}).get("replicas"),
            baseline_pool.get("spec", {}).get("replicas"),
        )
        unchanged(
            node_set,
            "instance type",
            pool.get("metadata", {}).get("labels", {}).get("osac.openshift.io/instance_type"),
            baseline_pool.get("metadata", {}).get("labels", {}).get("osac.openshift.io/instance_type"),
        )
        unchanged(
            node_set,
            "agent selector",
            pool.get("spec", {}).get("platform", {}).get("agent", {}).get("agentLabelSelector"),
            baseline_pool.get("spec", {}).get("platform", {}).get("agent", {}).get("agentLabelSelector"),
        )
        for kind in ("Ready", "AllMachinesReady"):
            unchanged(
                node_set,
                f"{kind} condition",
                _condition(pool, kind).get("status"),
                _condition(baseline_pool, kind).get("status"),
            )


def _cp_started(hosted: dict[str, Any], target_image: str) -> bool:
    """Require target intent and HyperShift progress by release version."""
    return _hosted_start_failure(hosted, target_image) == ""


def _cp_completed(hosted: dict[str, Any], target_image: str) -> bool:
    """Require completed target history and an available control plane."""
    return _hosted_completion_failure(hosted, target_image) == ""


def _np_started(pool: dict[str, Any], old_transition: str, target_image: str) -> bool:
    """Require the target image and a fresh update or completed version."""
    return _pool_start_failure(pool, old_transition, target_image) == ""


def _np_completed(pool: dict[str, Any], old_transition: str, target_image: str) -> bool:
    """Require the selected NodePool at the target version and ready."""
    return _pool_completion_failure(pool, old_transition, target_image) == ""


def _cluster_references(*, k8s: K8sClient, uuid: str, co_name: str, expected_node_sets: set[str]) -> _ClusterRefs:
    """Resolve the test-owned HostedCluster and NodePool names."""
    namespace, hc_name = poll_until(
        fn=lambda: (
            k8s.get_cluster_order_namespace(name=co_name),
            k8s.get_cluster_order_hosted_cluster_name(name=co_name),
        ),
        until=lambda names: all(names),
        retries=60,
        delay=10,
        description="test-owned HostedCluster reference",
        retry_on_error=True,
        resource_kind=WaitResourceKind.CLUSTER_ORDER,
        resource_id=co_name,
    )
    pool_names = poll_until(
        fn=lambda: _pool_identity(k8s, namespace, co_name, expected_node_sets),
        until=lambda names: set(names) == expected_node_sets,
        retries=60,
        delay=10,
        description="test-owned NodePool identities",
        retry_on_error=True,
        resource_kind=WaitResourceKind.CLUSTER_ORDER,
        resource_id=co_name,
    )
    return _ClusterRefs(uuid=uuid, co_name=co_name, namespace=namespace, hc_name=hc_name, pool_names=pool_names)


def _order_ready(order: dict[str, Any]) -> bool:
    """Require the ClusterOrder to report a complete, available cluster."""
    return (
        order.get("status", {}).get("phase") == "Ready"
        and _condition(order, "ControlPlaneAvailable").get("status") == "True"
        and _condition(order, "ClusterAvailable").get("status") == "True"
    )


def _assert_upgrade_target(*, baseline: dict[str, Any], target: dict[str, str]) -> None:
    """Reject a no-op or already-applied control-plane release before editing."""
    hosted = baseline["hosted"]
    version = hosted.get("status", {}).get("version", {})
    history = version.get("history", [])
    newest = history[0] if history else {}
    current_image = hosted.get("spec", {}).get("release", {}).get("image", "")
    target_image = target["image"]
    order_image = baseline["order"].get("spec", {}).get("releaseImage", "")

    assert order_image == current_image, (
        "Cannot start control-plane upgrade because ClusterOrder and HostedCluster release images disagree: "
        f"ClusterOrder={order_image!r}, HostedCluster={current_image!r}"
    )
    assert target_image != current_image, (
        "Cannot start control-plane upgrade because the target image equals the current image: "
        f"target={target_image!r}, current={current_image!r}"
    )
    desired_image = version.get("desired", {}).get("image")
    assert target_image != desired_image, (
        "Cannot start control-plane upgrade because the target image is already HyperShift's desired image: "
        f"target={target_image!r}, desired={desired_image!r}"
    )
    history_image = newest.get("image")
    assert target_image != history_image, (
        "Cannot start control-plane upgrade because the target image is already in HyperShift history: "
        f"target={target_image!r}, newest history={history_image!r}"
    )


def _ready_baseline(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
    """Wait for a READY ClusterOrder, healthy HostedCluster, and both NodePools."""

    return poll_until(
        fn=lambda: _snapshot(k8s=k8s, grpc=grpc, refs=refs),
        until=lambda value: _baseline_failure(value) == "",
        retries=READY_BASELINE_RETRIES,
        delay=READY_BASELINE_DELAY_SECONDS,
        description="test-owned ClusterOrder, HostedCluster, and NodePools ready before upgrade",
        retry_on_error=True,
        resource_kind=WaitResourceKind.CLUSTER_ORDER,
        resource_id=refs.co_name,
    )


def _upgrade_control_plane(
    *,
    refs: _ClusterRefs,
    baseline: dict[str, Any],
    target: dict[str, str],
    jwt_grpc_tenant1: GRPCClient,
    grpc: GRPCClient,
    k8s: K8sClient,
) -> dict[str, Any]:
    """Submit the control-plane version edit and await HyperShift completion."""
    prior_job_ids = {job["jobID"] for job in baseline["order"].get("status", {}).get("provisioningJobs", [])}
    target_image = target["image"]
    _assert_upgrade_target(baseline=baseline, target=target)

    jwt_grpc_tenant1.call(
        service="osac.public.v1.Clusters/Update",
        data={
            "object": {"id": refs.uuid, "spec": {"version": {"name": target["name"]}}},
            "updateMask": {"paths": ["spec.version"]},
        },
    )

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        """Read control-plane progress while rejecting unrelated changes."""
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _check_cluster_failure(current)
        _assert_unchanged(current, baseline_pools=baseline["pools"])
        current_version = current["cluster"].get("spec", {}).get("version", {}).get("name")
        assert current_version == target["name"], (
            "Tenant Cluster control-plane version did not change to the target after the update: "
            f"expected {target['name']!r}, got {current_version!r}"
        )
        _check_hypershift_failure(current["hosted"], "control plane")
        return current, _new_provision_job(current, prior_job_ids)

    _poll_upgrade(
        observe=observe,
        failure=lambda pair: _control_plane_start_failure(pair, target_image),
        description=(
            "control-plane upgrade to start: ClusterOrder target release, a new provision job, "
            "and HyperShift target progress"
        ),
        limit_seconds=START_LIMIT_SECONDS,
        co_name=refs.co_name,
    )
    completed = _poll_upgrade(
        observe=observe,
        failure=lambda pair: _control_plane_completion_failure(pair, target_image),
        description=(
            "control-plane upgrade to complete: target history Completed, HostedCluster Available, "
            "and HostedCluster not Degraded"
        ),
        limit_seconds=CP_LIMIT_SECONDS,
        co_name=refs.co_name,
    )
    return completed[0]


def _apply_single_worker_upgrade_workaround(
    *, k8s: K8sClient, workload_k8s: K8sClient, refs: _ClusterRefs, node_set: str
) -> None:
    """Apply the temporary OSAC-7087 single-worker upgrade workaround.

    AI agents: this is a deliberate E2E-only workaround for OSAC-7087.  A
    single-worker tenant cannot drain its only node under the default migrator
    PodDisruptionBudget.  Until OSAC-7087 provides product support for
    in-place upgrades of single-worker tenant clusters, permit one unavailable
    in-place worker and allow every migrator pod to be unavailable.

    Keep this function separate and remove it with OSAC-7087; do not move it
    into production reconciliation code.
    """
    node_pool_name = refs.pool_names[node_set]
    node_pool_patch = '{"spec":{"management":{"inPlace":{"maxUnavailable":1}}}}'
    migrator_pdb_patch = '{"spec":{"maxUnavailable":null,"minAvailable":0}}'
    node_pool_k8s = (
        k8s
        if k8s.namespace == refs.namespace
        else K8sClient(namespace=refs.namespace, kubeconfig=k8s.kubeconfig, as_system_admin=k8s.as_system_admin)
    )

    logger.info("OSAC-7087 workaround: permitting one in-place worker and zero migrator availability")
    _, node_pool_rc = node_pool_k8s.patch(
        resource="nodepools.hypershift.openshift.io", name=node_pool_name, patch=node_pool_patch
    )
    if node_pool_rc != 0:
        logger.warning("OSAC-7087 workaround: NodePool patch failed with rc=%s", node_pool_rc)

    _, migrator_rc = workload_k8s.patch(
        resource="poddisruptionbudgets.policy", name="migrator", patch=migrator_pdb_patch
    )
    if migrator_rc != 0:
        logger.warning("OSAC-7087 workaround: migrator PDB patch failed with rc=%s", migrator_rc)


def _upgrade_node_pool(
    *,
    refs: _ClusterRefs,
    baseline: dict[str, Any],
    cp_snapshot: dict[str, Any],
    target: dict[str, str],
    jwt_grpc_tenant1: GRPCClient,
    grpc: GRPCClient,
    k8s: K8sClient,
    upgraded_node_set: str,
    unchanged_node_set: str,
) -> None:
    """Upgrade one named NodePool while preserving another pool and the control plane."""
    unchanged_pool = baseline["pools"][unchanged_node_set]
    upgraded_before = cp_snapshot["pools"][upgraded_node_set]
    old_transition = _condition(upgraded_before, "UpdatingVersion").get("lastTransitionTime", "")
    prior_job_ids = {job["jobID"] for job in cp_snapshot["order"].get("status", {}).get("provisioningJobs", [])}
    target_image = target["image"]

    jwt_grpc_tenant1.call(
        service="osac.public.v1.Clusters/Update",
        data={
            "object": {
                "id": refs.uuid,
                "spec": {"nodeSets": {upgraded_node_set: {"version": {"name": target["name"]}}}},
            },
            "updateMask": {"paths": [f"spec.node_sets.{upgraded_node_set}.version"]},
        },
    )

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        """Read NodePool progress and recheck the other pool and control-plane health."""
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _check_cluster_failure(current)
        _assert_unchanged(current, baseline_pools={unchanged_node_set: unchanged_pool}, hosted_image=target_image)
        control_plane_failure = _hosted_completion_failure(current["hosted"], target_image)
        assert not control_plane_failure, (
            f"Control plane did not remain completed and healthy during the NodePool upgrade: {control_plane_failure}"
        )
        node_set_spec = current["cluster"].get("spec", {}).get("nodeSets", {}).get(upgraded_node_set, {})
        current_version = node_set_spec.get("version", {}).get("name")
        assert current_version == target["name"], (
            f"Tenant Cluster {upgraded_node_set!r} NodeSet version did not change to the target "
            f"after the update: expected {target['name']!r}, got {current_version!r}"
        )
        current_order_image = current["order"].get("spec", {}).get("releaseImage")
        assert current_order_image == target_image, (
            "ClusterOrder release image changed unexpectedly during the NodePool upgrade: "
            f"expected {target_image!r}, got {current_order_image!r}"
        )
        _check_hypershift_failure(current["pools"][upgraded_node_set], "upgraded node pool")
        return current, _new_provision_job(current, prior_job_ids)

    _poll_upgrade(
        observe=observe,
        failure=lambda pair: _node_pool_start_failure(pair, upgraded_node_set, old_transition, target_image),
        description=(
            f"{upgraded_node_set} NodePool upgrade to start: matching ClusterOrder node request, "
            "a new provision job, target release image, and a fresh UpdatingVersion condition"
        ),
        limit_seconds=START_LIMIT_SECONDS,
        co_name=refs.co_name,
    )

    workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
        k8s=k8s, hosted_cluster_namespace=refs.namespace, hosted_cluster_name=refs.hc_name
    )
    with tempfile.NamedTemporaryFile(prefix="osac-node-pool-upgrade-", suffix=".kubeconfig") as kubeconfig_file:
        kubeconfig_file.write(workload_kubeconfig)
        kubeconfig_file.flush()
        workload_k8s = K8sClient(
            namespace="openshift-kube-storage-version-migrator", kubeconfig=kubeconfig_file.name, as_system_admin=False
        )

        # OSAC-7087: controllers may revert these temporary drain gates, so
        # reapply them on every completion poll until progress is confirmed.
        def observe_with_single_worker_workaround() -> tuple[dict[str, Any], dict[str, Any]]:
            """Reapply the temporary gates before every completion observation."""
            _apply_single_worker_upgrade_workaround(
                k8s=k8s, workload_k8s=workload_k8s, refs=refs, node_set=upgraded_node_set
            )
            return observe()

        _poll_upgrade(
            observe=observe_with_single_worker_workaround,
            failure=lambda pair: _node_pool_completion_failure(pair, upgraded_node_set, old_transition, target_image),
            description=(
                f"{upgraded_node_set} NodePool upgrade to complete: target version, UpdatingVersion cleared, "
                "Ready and AllMachinesReady True, and all desired replicas ready"
            ),
            limit_seconds=NP_LIMIT_SECONDS_PER_NODE * int(upgraded_before["spec"]["replicas"]),
            co_name=refs.co_name,
        )


def _cleanup(
    *,
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s: K8sClient,
    uuid: str | None,
    co_name: str | None,
    created_version_ids: list[str],
    delete_cluster_requested: bool = False,
) -> None:
    """Delete the cluster naturally, then release test-created catalog versions."""
    first_error: BaseException | None = None
    try:
        if uuid is not None:
            if not delete_cluster_requested:
                cli.delete_cluster(uuid=uuid)
            wait_error: BaseException | None = None
            if co_name is not None:
                try:
                    wait_for_cluster_deletion_without_cleanup(k8s=k8s, name=co_name)
                except BaseException as exc:
                    wait_error = exc
            try:
                wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
            except BaseException as exc:
                if wait_error is None:
                    wait_error = exc
            if wait_error is not None:
                raise wait_error
    except BaseException as exc:
        first_error = exc
    for version_id in reversed(created_version_ids):
        try:
            private_grpc.delete_cluster_version(version_id=version_id)
        except BaseException as exc:
            if first_error is None:
                first_error = exc
    if first_error is not None:
        raise first_error
