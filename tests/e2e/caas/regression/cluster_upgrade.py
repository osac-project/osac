"""Observation and cleanup helpers for the CaaS upgrade E2E journey."""

from __future__ import annotations

import json
import logging
import math
import queue
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, TypeVar

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_cluster_deletion_without_cleanup, wait_for_cluster_grpc_removal
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until

TARGET_VERSION = "4.22.1"
TARGET_IMAGE = env("OSAC_TEST_UPGRADE_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.22.1-multi")
CP_LIMIT_SECONDS = 60 * 60
NP_LIMIT_SECONDS_PER_NODE = 30 * 60
START_LIMIT_SECONDS = 10 * 60
POLL_SECONDS = 20
_DIAGNOSTIC_INTERVAL_SECONDS = 60
_TERMINAL_JOB_FAILURES = {"Failed", "Canceled"}
_PHASES = (
    "preparing",
    "install-completed",
    "control-plane-requested",
    "control-plane-started",
    "control-plane-completed",
    "node-pool-requested",
    "node-pool-started",
    "node-pool-completed",
)
_T = TypeVar("_T")
logger = logging.getLogger(__name__)
diagnostics_logger = logging.getLogger(f"{__name__}.diagnostics")


@dataclass(frozen=True)
class _ClusterRefs:
    uuid: str
    co_name: str
    namespace: str
    hc_name: str
    pool_names: dict[str, str]


def _wait_elapsed(
    *,
    component: str,
    accepted_at: float,
    limit_seconds: int,
    observe: Callable[[], _T],
    ready: Callable[[_T], bool],
    diagnose: Callable[[_T], dict[str, Any]] | None = None,
) -> _T:
    """Count observation time and sleeps against the original request deadline."""
    deadline = accepted_at + limit_seconds
    last_diagnostic: float | None = None
    last_value: _T | None = None
    while time.monotonic() < deadline:
        value = observe()
        last_value = value
        now = time.monotonic()
        if now > deadline:
            break
        if ready(value):
            if diagnose is not None:
                _log_diagnostics(
                    component=component, data=diagnose(value), elapsed_seconds=now - accepted_at, final=True
                )
            return value
        if diagnose is not None and (last_diagnostic is None or now - last_diagnostic >= _DIAGNOSTIC_INTERVAL_SECONDS):
            last_diagnostic = now
            _log_diagnostics(component=component, data=diagnose(value), elapsed_seconds=now - accepted_at)
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(POLL_SECONDS, remaining))
    if diagnose is not None and last_value is not None:
        _log_diagnostics(
            component=component, data=diagnose(last_value), elapsed_seconds=time.monotonic() - accepted_at, final=True
        )
    raise TimeoutError(f"{component} timed out after {limit_seconds} elapsed seconds")


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


def _mapping(value: object) -> dict[str, Any]:
    """Return a mapping, treating an unexpected provider shape as empty."""
    return value if isinstance(value, dict) else {}


def _sequence(value: object) -> list[Any]:
    """Return a sequence, treating an unexpected provider shape as empty."""
    return value if isinstance(value, list) else []


def _condition_summary(resource: dict[str, Any]) -> list[dict[str, str]]:
    """Return condition identity and health without messages or secrets."""
    return [
        {
            "type": condition.get("type", ""),
            "status": condition.get("status", ""),
            "reason": condition.get("reason", ""),
        }
        for condition in _sequence(_mapping(resource.get("status", {})).get("conditions", []))
    ]


def _safe_job(job: dict[str, Any]) -> dict[str, str]:
    """Expose only the job identity fields needed to diagnose progress."""
    return {"type": job.get("type", ""), "state": job.get("state", "")}


def _upgrade_diagnostics(*, snapshot: dict[str, Any], target_image: str, job: dict[str, Any]) -> dict[str, Any]:
    """Summarize every upgrade start gate without logging resource payloads."""
    hosted = snapshot.get("hosted", {})
    version = hosted.get("status", {}).get("version", {})
    history = version.get("history", [])
    newest = history[0] if history else {}
    generation = int(hosted.get("metadata", {}).get("generation", 0))
    observed_generation = int(version.get("observedGeneration", -1))
    pools: dict[str, dict[str, Any]] = {}
    for node_set, pool in snapshot.get("pools", {}).items():
        pools[node_set] = {
            "image_matches": pool.get("spec", {}).get("release", {}).get("image") == target_image,
            "version": pool.get("status", {}).get("version", ""),
            "ready": _condition(pool, "Ready").get("status") == "True",
            "all_machines_ready": _condition(pool, "AllMachinesReady").get("status") == "True",
            "conditions": _condition_summary(pool),
        }
    return {
        "cluster_state": snapshot.get("cluster", {}).get("status", {}).get("state", ""),
        "order_phase": snapshot.get("order", {}).get("status", {}).get("phase", ""),
        "order_release_image_matches": snapshot.get("order", {}).get("spec", {}).get("releaseImage") == target_image,
        "new_provision_job": bool(job.get("jobID")),
        "job": _safe_job(job),
        "hosted_spec_image_matches": hosted.get("spec", {}).get("release", {}).get("image") == target_image,
        "hosted_desired_image_matches": version.get("desired", {}).get("image") == target_image,
        "hosted_generation": generation,
        "hosted_observed_generation": observed_generation,
        "hosted_observed_generation_matches": observed_generation >= generation,
        "hosted_history_image_matches": newest.get("image") == target_image,
        "hosted_history_state": newest.get("state", ""),
        "hosted_progressing": _condition(hosted, "ClusterVersionProgressing").get("status") == "True",
        "hosted_available": _condition(hosted, "Available").get("status") == "True",
        "hosted_degraded": _condition(hosted, "Degraded").get("status") == "True",
        "hosted_conditions": _condition_summary(hosted),
        "node_pools": pools,
    }


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
    diagnostics_logger.info("CaaS upgrade diagnostics: %s", json.dumps(payload, sort_keys=True))


def _deletion_diagnostics(*, k8s: K8sClient, grpc: GRPCClient, uuid: str | None, co_name: str | None) -> dict[str, Any]:
    """Collect the lifecycle facts needed to diagnose a stuck teardown."""
    diagnostics: dict[str, Any] = {}
    if uuid is not None:
        try:
            cluster = _mapping(_mapping(grpc.get_cluster(cluster_id=uuid)).get("object", {}))
            diagnostics["cluster_state"] = _mapping(cluster.get("status", {})).get("state", "")
        except Exception:
            diagnostics["cluster_state"] = "unavailable"

    if co_name is None:
        diagnostics.update({"cluster_order_exists": False, "cluster_reference_present": False})
        return diagnostics

    try:
        phase = k8s.get_cluster_order_phase(name=co_name, checked=False)
    except Exception:
        diagnostics.update({"cluster_order_exists": False, "cluster_order_lookup_failed": True})
        return diagnostics
    if phase is None:
        diagnostics.update({"cluster_order_exists": False, "cluster_reference_present": False})
        return diagnostics

    try:
        order = _mapping(k8s.get_json(resource="clusterorder", name=co_name))
    except Exception:
        diagnostics.update({"cluster_order_exists": True, "cluster_order_lookup_failed": True})
        return diagnostics

    metadata = _mapping(order.get("metadata", {}))
    status = _mapping(order.get("status", {}))
    jobs = _sequence(status.get("provisioningJobs", []))
    latest_job = _mapping(jobs[-1]) if jobs else {}
    reference = _mapping(status.get("clusterReference") or {})
    diagnostics.update(
        {
            "cluster_order_exists": True,
            "cluster_order_phase": status.get("phase", ""),
            "cluster_order_deletion_requested": bool(metadata.get("deletionTimestamp")),
            "cluster_order_finalizers": _sequence(metadata.get("finalizers", [])),
            "cluster_order_conditions": _condition_summary(order),
            "latest_job": _safe_job(latest_job),
            "cluster_reference_present": bool(reference.get("namespace") or reference.get("hostedClusterName")),
        }
    )

    namespace = reference.get("namespace")
    hosted_name = reference.get("hostedClusterName")
    if not namespace or not hosted_name:
        diagnostics.update({"hosted_cluster_exists": False, "node_pool_count": 0})
        return diagnostics

    try:
        hosted = _mapping(
            k8s.get_json(resource="hostedclusters.hypershift.openshift.io", name=hosted_name, namespace=namespace)
        )
    except Exception:
        diagnostics.update({"hosted_cluster_exists": False, "node_pool_count": 0})
        return diagnostics

    hosted_metadata = _mapping(hosted.get("metadata", {}))
    diagnostics.update(
        {
            "hosted_cluster_exists": True,
            "hosted_cluster_deletion_requested": bool(hosted_metadata.get("deletionTimestamp")),
            "hosted_cluster_conditions": _condition_summary(hosted),
        }
    )

    try:
        pools = _sequence(
            _mapping(k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=namespace)).get("items", [])
        )
    except Exception:
        diagnostics["node_pool_count"] = 0
        return diagnostics

    owned = [
        pool
        for pool in pools
        if _mapping(_mapping(pool).get("metadata", {})).get("labels", {}).get("osac.openshift.io/clusterorder")
        == co_name
    ]
    diagnostics["node_pool_count"] = len(owned)
    diagnostics["node_pools"] = {
        _mapping(_mapping(pool).get("metadata", {})).get("labels", {}).get("osac.openshift.io/node-set", ""): {
            "ready": _condition(pool, "Ready").get("status") == "True",
            "all_machines_ready": _condition(pool, "AllMachinesReady").get("status") == "True",
            "conditions": _condition_summary(pool),
        }
        for pool in owned
    }
    return diagnostics


def _hosted_ready(hosted: dict[str, Any]) -> bool:
    """Use HyperShift health conditions for control-plane readiness."""
    return (
        _condition(hosted, "Available").get("status") == "True"
        and _condition(hosted, "Degraded").get("status") != "True"
    )


def _warn_readiness_mismatch(snapshot: dict[str, Any], reported: set[tuple[str | None, bool]]) -> None:
    """Report each distinct disagreement without making upgrade progress depend on it."""
    state = snapshot["cluster"].get("status", {}).get("state")
    hosted_ready = _hosted_ready(snapshot["hosted"])
    mismatch = (state, hosted_ready)
    if (state == "CLUSTER_STATE_READY") != hosted_ready and mismatch not in reported:
        reported.add(mismatch)
        logger.warning("OSAC Cluster state %s disagrees with HyperShift HostedCluster ready=%s", state, hosted_ready)


def _check_cluster_failure(snapshot: dict[str, Any]) -> None:
    """Do not report upgrade progress for an unusable tenant Cluster."""
    if snapshot["cluster"].get("status", {}).get("state") == "CLUSTER_STATE_FAILED":
        raise AssertionError("OSAC Cluster entered FAILED state during upgrade")


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
    names = {item["metadata"]["labels"]["osac.openshift.io/node-set"]: item["metadata"]["name"] for item in owned}
    return names


def _snapshot(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
    """Read test-owned API and HyperShift resources for one observation."""
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


def _check_job(job: dict[str, Any], component: str) -> None:
    """Fail when AAP reports a terminal provision-job failure."""
    if job.get("state") in _TERMINAL_JOB_FAILURES:
        raise AssertionError(f"{component} AAP provision job failed")


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
            raise AssertionError(f"{component} HyperShift reported a failure condition")


def _assert_unchanged(
    snapshot: dict[str, Any], *, images: dict[str, str], versions: dict[str, str], hosted_image: str | None = None
) -> None:
    """Keep unselected HyperShift components at their baseline."""
    if hosted_image is not None:
        assert snapshot["hosted"].get("spec", {}).get("release", {}).get("image") == hosted_image, (
            "Unselected HostedCluster image changed"
        )
    for node_set, image in images.items():
        pool = snapshot["pools"][node_set]
        assert pool.get("spec", {}).get("release", {}).get("image") == image, (
            f"Unselected {node_set} NodePool image changed"
        )
        assert pool.get("status", {}).get("version") == versions[node_set], (
            f"Unselected {node_set} NodePool version changed"
        )


def _cp_started(hosted: dict[str, Any], target_image: str) -> bool:
    """Require target-image intent and observed HyperShift progress."""
    if hosted.get("spec", {}).get("release", {}).get("image") != target_image:
        return False
    version = hosted.get("status", {}).get("version", {})
    if version.get("desired", {}).get("image") != target_image:
        return False
    if int(version.get("observedGeneration", -1)) < int(hosted.get("metadata", {}).get("generation", 0)):
        return False
    history = version.get("history", [])
    newest = history[0] if history else {}
    progressing = _condition(hosted, "ClusterVersionProgressing")
    current_generation = int(hosted.get("metadata", {}).get("generation", 0))
    return (newest.get("image") == target_image and newest.get("state") in {"Partial", "Completed"}) or (
        progressing.get("status") == "True" and int(progressing.get("observedGeneration", -1)) >= current_generation
    )


def _cp_completed(hosted: dict[str, Any], target_image: str) -> bool:
    """Require completed target history and an available control plane."""
    if not _cp_started(hosted, target_image):
        return False
    version = hosted["status"]["version"]
    history = version.get("history", [])
    newest = history[0] if history else {}
    return (
        newest.get("image") == target_image
        and newest.get("version") == TARGET_VERSION
        and newest.get("state") == "Completed"
        and _condition(hosted, "Available").get("status") == "True"
        and _condition(hosted, "Degraded").get("status") != "True"
    )


def _np_started(pool: dict[str, Any], old_transition: str, target_image: str) -> bool:
    """Require the target image and a fresh update or completed version."""
    if pool.get("spec", {}).get("release", {}).get("image") != target_image:
        return False
    if pool.get("status", {}).get("version") == TARGET_VERSION:
        return True
    updating = _condition(pool, "UpdatingVersion")
    return updating.get("status") == "True" and updating.get("lastTransitionTime", "") != old_transition


def _np_completed(pool: dict[str, Any], old_transition: str, target_image: str) -> bool:
    """Require the selected NodePool at the target version and ready."""
    if not _np_started(pool, old_transition, target_image):
        return False
    return (
        pool.get("status", {}).get("version") == TARGET_VERSION
        and _condition(pool, "UpdatingVersion").get("status") != "True"
        and _condition(pool, "Ready").get("status") == "True"
        and _condition(pool, "AllMachinesReady").get("status") == "True"
        and int(pool.get("status", {}).get("replicas", -1)) == 1
    )


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
    )
    pool_names = poll_until(
        fn=lambda: _pool_identity(k8s, namespace, co_name, expected_node_sets),
        until=lambda names: set(names) == expected_node_sets,
        retries=60,
        delay=10,
        description="test-owned NodePool identities",
    )
    return _ClusterRefs(uuid=uuid, co_name=co_name, namespace=namespace, hc_name=hc_name, pool_names=pool_names)


def _ready_baseline(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
    """Wait for a healthy HostedCluster and both NodePools before upgrading."""
    reported: set[tuple[str | None, bool]] = set()

    def ready(value: dict[str, Any]) -> bool:
        _warn_readiness_mismatch(value, reported)
        return _hosted_ready(value["hosted"]) and all(
            pool
            and _condition(pool, "Ready").get("status") == "True"
            and _condition(pool, "AllMachinesReady").get("status") == "True"
            for pool in value["pools"].values()
        )

    return poll_until(
        fn=lambda: _snapshot(k8s=k8s, grpc=grpc, refs=refs),
        until=ready,
        retries=60,
        delay=10,
        description="test-owned HostedCluster and NodePools ready",
    )


def _upgrade_control_plane(
    *,
    refs: _ClusterRefs,
    baseline: dict[str, Any],
    target: dict[str, str],
    jwt_grpc_tenant1: GRPCClient,
    grpc: GRPCClient,
    k8s: K8sClient,
    evidence: dict[str, Any],
) -> dict[str, Any]:
    """Submit the control-plane version edit and await HyperShift completion."""
    old_images = {name: pool["spec"]["release"]["image"] for name, pool in baseline["pools"].items()}
    old_versions = {name: pool["status"]["version"] for name, pool in baseline["pools"].items()}
    prior_job_ids = {job["jobID"] for job in baseline["order"].get("status", {}).get("provisioningJobs", [])}
    target_image = target["image"]

    jwt_grpc_tenant1.call(
        service="osac.public.v1.Clusters/Update",
        data={
            "object": {"id": refs.uuid, "spec": {"version": {"name": target["name"]}}},
            "updateMask": {"paths": ["spec.version"]},
        },
    )
    accepted = time.monotonic()
    evidence["phase"] = "control-plane-requested"
    reported: set[tuple[str | None, bool]] = set()

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        """Read control-plane progress while rejecting unrelated changes."""
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _warn_readiness_mismatch(current, reported)
        _check_cluster_failure(current)
        _assert_unchanged(current, images=old_images, versions=old_versions)
        assert current["cluster"]["spec"]["version"]["name"] == target["name"]
        _check_hypershift_failure(current["hosted"], "control plane")
        job = _new_provision_job(current, prior_job_ids)
        _check_job(job, "control plane")
        return current, job

    def diagnose(pair: tuple[dict[str, Any], dict[str, Any]]) -> dict[str, Any]:
        current, job = pair
        return _upgrade_diagnostics(snapshot=current, target_image=target_image, job=job)

    _wait_elapsed(
        component="control plane start",
        accepted_at=accepted,
        limit_seconds=START_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: (
            pair[0]["order"]["spec"].get("releaseImage") == target_image
            and bool(pair[1].get("jobID"))
            and _cp_started(pair[0]["hosted"], target_image)
        ),
        diagnose=diagnose,
    )
    evidence["phase"] = "control-plane-started"
    completed = _wait_elapsed(
        component="control plane completion",
        accepted_at=accepted,
        limit_seconds=CP_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: _cp_completed(pair[0]["hosted"], target_image),
        diagnose=diagnose,
    )
    evidence["phase"] = "control-plane-completed"
    evidence["cp_elapsed_seconds"] = round(time.monotonic() - accepted, 1)
    return completed[0]


def _upgrade_node_pool(
    *,
    refs: _ClusterRefs,
    baseline: dict[str, Any],
    cp_snapshot: dict[str, Any],
    target: dict[str, str],
    jwt_grpc_tenant1: GRPCClient,
    grpc: GRPCClient,
    k8s: K8sClient,
    evidence: dict[str, Any],
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
    accepted = time.monotonic()
    evidence["phase"] = "node-pool-requested"
    reported: set[tuple[str | None, bool]] = set()

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        """Read NodePool progress and recheck the other pool and control-plane health."""
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _warn_readiness_mismatch(current, reported)
        _check_cluster_failure(current)
        _assert_unchanged(
            current,
            images={unchanged_node_set: unchanged_pool["spec"]["release"]["image"]},
            versions={unchanged_node_set: unchanged_pool["status"]["version"]},
            hosted_image=target_image,
        )
        assert _cp_completed(current["hosted"], target_image), "Control plane is not completed and healthy"
        assert current["cluster"]["spec"]["nodeSets"][upgraded_node_set]["version"]["name"] == target["name"]
        assert current["order"]["spec"].get("releaseImage") == target_image
        _check_hypershift_failure(current["pools"][upgraded_node_set], "upgraded node pool")
        job = _new_provision_job(current, prior_job_ids)
        _check_job(job, "upgraded node pool")
        return current, job

    def diagnose(pair: tuple[dict[str, Any], dict[str, Any]]) -> dict[str, Any]:
        current, job = pair
        return _upgrade_diagnostics(snapshot=current, target_image=target_image, job=job)

    _wait_elapsed(
        component="upgraded node pool start",
        accepted_at=accepted,
        limit_seconds=START_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: (
            any(
                request.get("nodeSet") == upgraded_node_set and request.get("releaseImage") == target_image
                for request in pair[0]["order"]["spec"].get("nodeRequests", [])
            )
            and bool(pair[1].get("jobID"))
            and _np_started(pair[0]["pools"][upgraded_node_set], old_transition, target_image)
        ),
        diagnose=diagnose,
    )
    evidence["phase"] = "node-pool-started"
    _wait_elapsed(
        component="upgraded node pool completion",
        accepted_at=accepted,
        limit_seconds=NP_LIMIT_SECONDS_PER_NODE * int(upgraded_before["spec"]["replicas"]),
        observe=observe,
        ready=lambda pair: _np_completed(pair[0]["pools"][upgraded_node_set], old_transition, target_image),
        diagnose=diagnose,
    )
    evidence["phase"] = "node-pool-completed"
    evidence["np_elapsed_seconds"] = round(time.monotonic() - accepted, 1)


def _safe_evidence(evidence: dict[str, Any], *, source_version: str) -> dict[str, str | float]:
    """Allow only fixed states, the install fixture version, and elapsed times into logs."""
    phase = evidence.get("phase")
    outcome = evidence.get("outcome")
    cleanup = evidence.get("cleanup")
    safe: dict[str, str | float] = {
        "phase": phase if phase in _PHASES else "unknown",
        "outcome": outcome if outcome in ("failed", "succeeded") else "unknown",
        "cleanup": cleanup if cleanup in ("failed", "completed") else "unknown",
        "source_release_version": source_version,
        "target_release_version": TARGET_VERSION,
    }
    for field in ("cp_elapsed_seconds", "np_elapsed_seconds"):
        elapsed = evidence.get(field)
        if isinstance(elapsed, float) and math.isfinite(elapsed) and 0 <= elapsed <= 24 * 60 * 60:
            safe[field] = elapsed
    return safe


def _cleanup(
    *,
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s: K8sClient,
    uuid: str | None,
    co_name: str | None,
    created_version_ids: list[str],
    evidence: dict[str, Any],
    source_version: str,
    delete_cluster_requested: bool = False,
) -> None:
    """Try cluster and catalog deletion, then log the sanitized outcome."""
    try:
        first_error: BaseException | None = None
        deletion_started = time.monotonic()
        diagnostics_stop = threading.Event()
        diagnostics_results: queue.Queue[dict[str, Any]] = queue.Queue()

        def log_deletion_diagnostics() -> None:
            while not diagnostics_stop.is_set():
                try:
                    data = _deletion_diagnostics(k8s=k8s, grpc=grpc, uuid=uuid, co_name=co_name)
                except Exception:
                    data = {"diagnostics_error": "lookup_failed"}
                diagnostics_results.put(data)
                if diagnostics_stop.is_set():
                    return
                _log_diagnostics(
                    component="cluster deletion", data=data, elapsed_seconds=time.monotonic() - deletion_started
                )
                if diagnostics_stop.wait(_DIAGNOSTIC_INTERVAL_SECONDS):
                    return

        diagnostics_thread = threading.Thread(
            target=log_deletion_diagnostics, daemon=True, name="caas-deletion-diagnostics"
        )

        try:
            if uuid is not None:
                if not delete_cluster_requested:
                    cli.delete_cluster(uuid=uuid)
                if co_name is not None:
                    diagnostics_thread.start()
                    try:
                        wait_for_cluster_deletion_without_cleanup(k8s=k8s, name=co_name)
                    finally:
                        diagnostics_stop.set()
                        latest_diagnostics: dict[str, Any] | None = None
                        while True:
                            try:
                                latest_diagnostics = diagnostics_results.get_nowait()
                            except queue.Empty:
                                break
                        if latest_diagnostics is None:
                            latest_diagnostics = {"diagnostics_pending": True}
                        _log_diagnostics(
                            component="cluster deletion",
                            data=latest_diagnostics,
                            elapsed_seconds=time.monotonic() - deletion_started,
                            final=True,
                        )
                wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
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
    except BaseException:
        evidence["cleanup"] = "failed"
        evidence["outcome"] = "failed"
        raise
    else:
        evidence["cleanup"] = "completed"
    finally:
        logger.info(
            "CaaS upgrade evidence: %s",
            json.dumps(_safe_evidence(evidence, source_version=source_version), sort_keys=True),
        )
