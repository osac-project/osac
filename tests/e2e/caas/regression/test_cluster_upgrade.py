"""Deployed, sequential control-plane and named-node-pool API upgrade."""

from __future__ import annotations

import json
import logging
import os
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, TypeVar

import pytest

from tests.e2e.caas.sanity.test_cluster_create import RHCOS_IMAGE
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deletion_without_cleanup,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression

SOURCE_VERSION = "4.22.0-rhcos"
SOURCE_IMAGE = "quay.io/openshift-release-dev/ocp-release:4.22.0-multi"
TARGET_VERSION = "4.22.1"
TARGET_IMAGE = "quay.io/openshift-release-dev/ocp-release:4.22.1-multi"
CP_LIMIT_SECONDS = 60 * 60
NP_LIMIT_SECONDS_PER_NODE = 30 * 60
START_LIMIT_SECONDS = 10 * 60
POLL_SECONDS = 20
SELECTED_NODE_SET = "compute"
SIBLING_NODE_SET = "gpu"
INSTANCE_TYPES = {SELECTED_NODE_SET: "ci-worker-bm", SIBLING_NODE_SET: "ci-worker-bm-gpu"}
_TERMINAL_JOB_FAILURES = {"Failed", "Canceled"}
_T = TypeVar("_T")
logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class _ClusterRefs:
    uuid: str
    co_name: str
    namespace: str
    hc_name: str
    pool_names: dict[str, str]


def _utc_now() -> str:
    return datetime.now(UTC).isoformat()


def _wait_elapsed(
    *, component: str, accepted_at: float, limit_seconds: int, observe: Callable[[], _T], ready: Callable[[_T], bool]
) -> _T:
    """Count observation time and sleeps against the original request deadline."""
    deadline = accepted_at + limit_seconds
    while time.monotonic() < deadline:
        value = observe()
        now = time.monotonic()
        if now > deadline:
            break
        if ready(value):
            return value
        time.sleep(min(POLL_SECONDS, deadline - now))
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
    return next((item for item in resource.get("status", {}).get("conditions", []) if item.get("type") == kind), {})


def _pool_identity(k8s: K8sClient, namespace: str, co_name: str) -> dict[str, str]:
    """Find the test-owned NodePools by their public NodeSet labels."""
    pools = k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=namespace).get("items", [])
    owned = [
        item
        for item in pools
        if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == co_name
    ]
    if len(owned) != len(INSTANCE_TYPES):
        return {}
    names = {item["metadata"]["labels"]["osac.openshift.io/node-set"]: item["metadata"]["name"] for item in owned}
    return names


def _snapshot(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
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
    jobs = snapshot["order"].get("status", {}).get("provisioningJobs", [])
    new = [job for job in jobs if job.get("type") == "provision" and job.get("jobID") not in previous_ids]
    return max(new, key=lambda job: job.get("timestamp", ""), default={})


def _check_job(job: dict[str, Any], component: str) -> None:
    if job.get("state") in _TERMINAL_JOB_FAILURES:
        raise AssertionError(f"{component} AAP provision job {job.get('jobID')} ended {job['state']}")


def _check_hypershift_failure(resource: dict[str, Any], component: str) -> None:
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
            safe_reason = reason if reason.isalnum() else "unknown"
            raise AssertionError(f"{component} HyperShift condition {kind} failed: {safe_reason}")


def _assert_unchanged(
    snapshot: dict[str, Any],
    *,
    state: str,
    images: dict[str, str],
    versions: dict[str, str],
    hosted_image: str | None = None,
) -> None:
    assert snapshot["cluster"].get("status", {}).get("state") == state, "Cluster provisioning state changed"
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
    if pool.get("spec", {}).get("release", {}).get("image") != target_image:
        return False
    if pool.get("status", {}).get("version") == TARGET_VERSION:
        return True
    updating = _condition(pool, "UpdatingVersion")
    return updating.get("status") == "True" and updating.get("lastTransitionTime", "") != old_transition


def _np_completed(pool: dict[str, Any], old_transition: str, target_image: str) -> bool:
    if not _np_started(pool, old_transition, target_image):
        return False
    return (
        pool.get("status", {}).get("version") == TARGET_VERSION
        and _condition(pool, "UpdatingVersion").get("status") != "True"
        and _condition(pool, "Ready").get("status") == "True"
        and _condition(pool, "AllMachinesReady").get("status") == "True"
        and int(pool.get("status", {}).get("replicas", -1)) == 1
    )


def _optional_runtime_revisions() -> dict[str, str]:
    keys = {
        "fulfillment_image_digest": "OSAC_TEST_FULFILLMENT_IMAGE_DIGEST",
        "operator_image_digest": "OSAC_TEST_OPERATOR_IMAGE_DIGEST",
        "aap_project_revision": "OSAC_TEST_AAP_PROJECT_REVISION",
        "aap_ee_image_digest": "OSAC_TEST_AAP_EE_IMAGE_DIGEST",
    }
    return {field: os.environ[name] for field, name in keys.items() if os.environ.get(name)}


def _create_cluster(
    *, cli: OsacCLI, source_name: str, cluster_template: str, pull_secret_name: str, ssh_public_key_path: str
) -> str:
    return cli.create_cluster(
        name=unique_name("e2e-cluster-upgrade"),
        template=cluster_template,
        version=source_name,
        node_sets={
            node_set: {"size": 1, "baremetal_instance_type": {"name": instance_type}}
            for node_set, instance_type in INSTANCE_TYPES.items()
        },
        pull_secret=pull_secret_name,
        ssh_public_key_file=ssh_public_key_path,
    )


def _cluster_references(*, k8s: K8sClient, uuid: str, co_name: str) -> _ClusterRefs:
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
        fn=lambda: _pool_identity(k8s, namespace, co_name),
        until=lambda names: set(names) == set(INSTANCE_TYPES),
        retries=60,
        delay=10,
        description="test-owned NodePool identities",
    )
    return _ClusterRefs(uuid=uuid, co_name=co_name, namespace=namespace, hc_name=hc_name, pool_names=pool_names)


def _ready_baseline(*, k8s: K8sClient, grpc: GRPCClient, refs: _ClusterRefs) -> dict[str, Any]:
    return poll_until(
        fn=lambda: _snapshot(k8s=k8s, grpc=grpc, refs=refs),
        until=lambda value: (
            bool(value["hosted"])
            and all(value["pools"].values())
            and value["cluster"].get("status", {}).get("state") == "CLUSTER_STATE_READY"
        ),
        retries=60,
        delay=10,
        description="test-owned Cluster ready with HostedCluster and NodePools",
    )


def _record_baseline(evidence: dict[str, Any], refs: _ClusterRefs, baseline: dict[str, Any]) -> None:
    evidence.update(
        {
            "cluster_id": refs.uuid,
            "tenant": baseline["cluster"]["metadata"]["tenant"],
            "cluster_order": refs.co_name,
            "hosted_cluster": f"{refs.namespace}/{refs.hc_name}",
            "node_pools": {name: f"{refs.namespace}/{pool}" for name, pool in refs.pool_names.items()},
            "baseline_state": baseline["cluster"]["status"]["state"],
            "baseline_utc": _utc_now(),
            "baseline_image": baseline["hosted"]["spec"]["release"]["image"],
            "baseline_node_pool_versions": {
                name: pool["status"]["version"] for name, pool in baseline["pools"].items()
            },
        }
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
    old_images = {name: pool["spec"]["release"]["image"] for name, pool in baseline["pools"].items()}
    old_versions = {name: pool["status"]["version"] for name, pool in baseline["pools"].items()}
    initial_state = baseline["cluster"]["status"]["state"]
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
    evidence["cp_request_accepted_utc"] = _utc_now()

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _assert_unchanged(current, state=initial_state, images=old_images, versions=old_versions)
        assert current["cluster"]["spec"]["version"]["name"] == target["name"]
        _check_hypershift_failure(current["hosted"], "control plane")
        job = _new_provision_job(current, prior_job_ids)
        _check_job(job, "control plane")
        return current, job

    started = _wait_elapsed(
        component="control plane start",
        accepted_at=accepted,
        limit_seconds=START_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: (
            pair[0]["order"]["spec"].get("releaseImage") == target_image
            and bool(pair[1].get("jobID"))
            and _cp_started(pair[0]["hosted"], target_image)
        ),
    )
    evidence["cp_started_utc"] = _utc_now()
    evidence["cp_aap_job"] = {"id": started[1]["jobID"], "state": started[1].get("state")}
    completed = _wait_elapsed(
        component="control plane completion",
        accepted_at=accepted,
        limit_seconds=CP_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: _cp_completed(pair[0]["hosted"], target_image),
    )
    evidence["cp_completed_utc"] = _utc_now()
    evidence["cp_elapsed_seconds"] = round(time.monotonic() - accepted, 1)
    evidence["cp_observed_image"] = completed[0]["hosted"]["spec"]["release"]["image"]
    evidence["cp_aap_job"]["state"] = completed[1].get("state")
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
) -> None:
    initial_state = baseline["cluster"]["status"]["state"]
    sibling = baseline["pools"][SIBLING_NODE_SET]
    selected_before = cp_snapshot["pools"][SELECTED_NODE_SET]
    old_transition = _condition(selected_before, "UpdatingVersion").get("lastTransitionTime", "")
    prior_job_ids = {job["jobID"] for job in cp_snapshot["order"].get("status", {}).get("provisioningJobs", [])}
    target_image = target["image"]

    jwt_grpc_tenant1.call(
        service="osac.public.v1.Clusters/Update",
        data={
            "object": {
                "id": refs.uuid,
                "spec": {"nodeSets": {SELECTED_NODE_SET: {"version": {"name": target["name"]}}}},
            },
            "updateMask": {"paths": [f"spec.node_sets.{SELECTED_NODE_SET}.version"]},
        },
    )
    accepted = time.monotonic()
    evidence["np_request_accepted_utc"] = _utc_now()

    def observe() -> tuple[dict[str, Any], dict[str, Any]]:
        current = _snapshot(k8s=k8s, grpc=grpc, refs=refs)
        _assert_unchanged(
            current,
            state=initial_state,
            images={SIBLING_NODE_SET: sibling["spec"]["release"]["image"]},
            versions={SIBLING_NODE_SET: sibling["status"]["version"]},
            hosted_image=target_image,
        )
        assert current["cluster"]["spec"]["nodeSets"][SELECTED_NODE_SET]["version"]["name"] == target["name"]
        assert current["order"]["spec"].get("releaseImage") == target_image
        _check_hypershift_failure(current["pools"][SELECTED_NODE_SET], "selected node pool")
        job = _new_provision_job(current, prior_job_ids)
        _check_job(job, "selected node pool")
        return current, job

    started = _wait_elapsed(
        component="selected node pool start",
        accepted_at=accepted,
        limit_seconds=START_LIMIT_SECONDS,
        observe=observe,
        ready=lambda pair: (
            any(
                request.get("nodeSet") == SELECTED_NODE_SET and request.get("releaseImage") == target_image
                for request in pair[0]["order"]["spec"].get("nodeRequests", [])
            )
            and bool(pair[1].get("jobID"))
            and _np_started(pair[0]["pools"][SELECTED_NODE_SET], old_transition, target_image)
        ),
    )
    evidence["np_started_utc"] = _utc_now()
    evidence["np_aap_job"] = {"id": started[1]["jobID"], "state": started[1].get("state")}
    completed = _wait_elapsed(
        component="selected node pool completion",
        accepted_at=accepted,
        limit_seconds=NP_LIMIT_SECONDS_PER_NODE * int(selected_before["spec"]["replicas"]),
        observe=observe,
        ready=lambda pair: _np_completed(pair[0]["pools"][SELECTED_NODE_SET], old_transition, target_image),
    )
    evidence["np_completed_utc"] = _utc_now()
    evidence["np_elapsed_seconds"] = round(time.monotonic() - accepted, 1)
    evidence["np_observed_image"] = completed[0]["pools"][SELECTED_NODE_SET]["spec"]["release"]["image"]
    evidence["np_aap_job"]["state"] = completed[1].get("state")


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
) -> None:
    try:
        if uuid is not None:
            cli.delete_cluster(uuid=uuid)
            if co_name is not None:
                wait_for_cluster_deletion_without_cleanup(k8s=k8s, name=co_name)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
        for version_id in reversed(created_version_ids):
            private_grpc.delete_cluster_version(version_id=version_id)
    except BaseException:
        evidence["cleanup"] = "failed"
        evidence["outcome"] = "failed"
        raise
    else:
        evidence["cleanup"] = "completed"
    finally:
        logger.info("CaaS upgrade evidence: %s", json.dumps(evidence, sort_keys=True))


def test_control_plane_then_named_node_pool_upgrade(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
) -> None:
    """Upgrade the control plane, then one named pool, on a dedicated Cluster."""
    evidence: dict[str, Any] = {
        "procedure": "uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_upgrade.py::"
        "test_control_plane_then_named_node_pool_upgrade",
        "boundary": "real tenant API, PostgreSQL, operator, AAP, HyperShift, and provider; controlled catalog",
        "outcome": "failed",
        **_optional_runtime_revisions(),
    }
    uuid: str | None = None
    co_name: str | None = None
    created_version_ids: list[str] = []
    try:
        for instance_type in INSTANCE_TYPES.values():
            private_grpc.ensure_bare_metal_instance_type(
                name=instance_type, host_label_selector={"osac.openshift.io/host-type": "default"}
            )
        disk_image = private_grpc.ensure_disk_image(name="rhcos-4-22", source_ref=RHCOS_IMAGE)
        source_version, source_created = _catalog_version(
            private_grpc=private_grpc, version=SOURCE_VERSION, image=SOURCE_IMAGE, disk_image=disk_image
        )
        if source_created:
            created_version_ids.append(source_version["id"])
        target_version, target_created = _catalog_version(
            private_grpc=private_grpc, version=TARGET_VERSION, image=TARGET_IMAGE, disk_image=disk_image
        )
        if target_created:
            created_version_ids.append(target_version["id"])
        evidence["catalog"] = {
            "source_id": source_version["id"],
            "source_version": SOURCE_VERSION,
            "source_image": source_version["image"],
            "target_id": target_version["id"],
            "target_version": TARGET_VERSION,
            "target_image": target_version["image"],
            "target_created": target_created,
        }

        uuid = _create_cluster(
            cli=cli,
            source_name=source_version["name"],
            cluster_template=cluster_template,
            pull_secret_name=pull_secret_name,
            ssh_public_key_path=ssh_public_key_path,
        )
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        refs = _cluster_references(k8s=k8s_hub_client, uuid=uuid, co_name=co_name)
        baseline = _ready_baseline(k8s=k8s_hub_client, grpc=grpc, refs=refs)
        _record_baseline(evidence, refs, baseline)

        cp_snapshot = _upgrade_control_plane(
            refs=refs,
            baseline=baseline,
            target=target_version,
            jwt_grpc_tenant1=jwt_grpc_tenant1,
            grpc=grpc,
            k8s=k8s_hub_client,
            evidence=evidence,
        )
        _upgrade_node_pool(
            refs=refs,
            baseline=baseline,
            cp_snapshot=cp_snapshot,
            target=target_version,
            jwt_grpc_tenant1=jwt_grpc_tenant1,
            grpc=grpc,
            k8s=k8s_hub_client,
            evidence=evidence,
        )
        evidence["outcome"] = "succeeded"
    finally:
        _cleanup(
            cli=cli,
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s_hub_client,
            uuid=uuid,
            co_name=co_name,
            created_version_ids=created_version_ids,
            evidence=evidence,
        )
