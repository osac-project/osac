from __future__ import annotations

import contextlib
import logging
import os
import subprocess
import tempfile
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest

from tests.e2e.caas.deletion_diagnostics import snapshot_cluster_deletion
from tests.e2e.core.fulfillment_trust import assert_cluster_trust, assert_management_tls
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_deletion_without_cleanup,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_guest_readiness,
    wait_for_cluster_order_condition,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_hosted_cluster_kubeconfig,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until, run

pytestmark = pytest.mark.sanity

# Real OCP release image used to provision the ClusterVersion(s) created by these
# tests. Fixed (not randomly generated) so repeated runs reuse the same
# ClusterVersion via GRPCClient.ensure_cluster_version instead of accumulating
# duplicates on the shared cluster.
TEST_RELEASE_IMAGE = env("OSAC_TEST_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.22.0-multi")
RHCOS_IMAGE = env("OSAC_RHCOS_BMI_IMAGE", "oci://quay.io/rh_ee_rpiccoli/rhcos-bmi:4.22.0")
_WORKER_METRICS = ("osac_caas_worker_desired", "osac_caas_worker_ready", "osac_caas_worker_provisioning_failures_total")
_WORKER_LEVEL_METRICS = _WORKER_METRICS[:2]
_WORKER_FAILURE_METRIC = _WORKER_METRICS[2]
_WORKER_METRIC_LABELS = {"tenant", "worker_type", "instance_type"}
_INSTALLING_AGENT_STATES = {"installing", "installing-in-progress"}
# Only log known phase names, never Agent statusInfo or the full resource (which may contain sensitive data).
_SAFE_AGENT_STATES = {
    "discovering",
    "discovering-unbound",
    "known",
    "known-unbound",
    "insufficient",
    "insufficient-unbound",
    "disconnected",
    "disconnected-unbound",
    "disabled",
    "disabled-unbound",
    "pending-for-input",
    "binding",
    "preparing-for-installation",
    "preparing-failed",
    "preparing-successful",
    "installing",
    "installing-in-progress",
    "installing-pending-user-action",
    "installed",
    "error",
}
logger = logging.getLogger(__name__)


def _metric_samples(metrics: str, metric_name: str) -> list[str]:
    return [line for line in metrics.splitlines() if line.startswith(f"{metric_name}{{")]


def _assert_worker_metrics(metrics: str) -> None:
    for metric_name in _WORKER_LEVEL_METRICS:
        assert f"# TYPE {metric_name} " in metrics, f"Missing {metric_name} metric family"
        samples = _metric_samples(metrics, metric_name)
        assert samples, f"Missing {metric_name} samples"
        _assert_worker_metric_labels(metric_name, samples)

    failure_samples = _metric_samples(metrics, _WORKER_FAILURE_METRIC)
    if failure_samples:
        assert f"# TYPE {_WORKER_FAILURE_METRIC} counter" in metrics
        _assert_worker_metric_labels(_WORKER_FAILURE_METRIC, failure_samples)


def _assert_worker_metric_labels(metric_name: str, samples: list[str]) -> None:
    for sample in samples:
        labels = sample.split("{", 1)[1].split("}", 1)[0]
        label_names = {label.split("=", 1)[0] for label in labels.split(",")}
        assert label_names == _WORKER_METRIC_LABELS, (
            f"{metric_name} labels {sorted(label_names)} do not match {sorted(_WORKER_METRIC_LABELS)}"
        )


def _list_owned_worker_bmis(*, grpc: GRPCClient, co_name: str, tenant: str) -> dict[str, dict[str, Any]]:
    """Find tenant-visible BMIs and reject any that falsely claim this ClusterOrder."""
    bmi_filter = f'this.metadata.labels["osac.openshift.io/cluster-order"] == "{co_name}"'
    workers: dict[str, dict[str, Any]] = {}
    for bmi_id in grpc.list_baremetal_instance_ids(filter_expr=bmi_filter):
        bmi = grpc.get_baremetal_instance(bmi_id=bmi_id)["object"]
        metadata = bmi["metadata"]
        assert metadata["tenant"] == tenant
        assert metadata["labels"]["osac.openshift.io/cluster-order"] == co_name
        assert metadata["annotations"]["osac.openshift.io/owner-reference"] == f"ClusterOrder/{co_name}"
        workers[bmi_id] = bmi
    return workers


def _cluster_order_agents(*, k8s: K8sClient, co_name: str, namespace: str) -> list[dict[str, Any]]:
    """Find even unbound Agents using the InfraEnv label assigned by Assisted Service."""
    items = k8s.list_json(resource="agents.agent-install.openshift.io", namespace=namespace).get("items", [])
    return [
        agent
        for agent in items
        if agent.get("metadata", {}).get("namespace") == namespace
        and agent.get("metadata", {}).get("labels", {}).get("infraenvs.agent-install.openshift.io")
        == f"{co_name}-infraenv"
    ]


def _agent_is_installing(agent: dict[str, Any]) -> bool:
    return agent.get("status", {}).get("debugInfo", {}).get("state") in _INSTALLING_AGENT_STATES


def _wait_for_cluster_and_infraenv_deletion(
    *, k8s: K8sClient, co_name: str, infra_env_name: str, on_poll: Callable[[], None], on_timeout: Callable[[], None]
) -> None:
    """Observe parent removal before starting the dependent garbage-collection budget."""
    try:
        wait_for_cluster_deletion_without_cleanup(k8s=k8s, name=co_name, on_poll=on_poll)
    except TimeoutError:
        on_timeout()
        raise TimeoutError("ClusterOrder natural teardown timed out") from None

    def _infraenv_absent_with_snapshot() -> bool:
        on_poll()
        return k8s.is_absent(resource="infraenv.agent-install.openshift.io", name=infra_env_name)

    try:
        poll_until(
            fn=_infraenv_absent_with_snapshot,
            until=lambda value: value is True,
            retries=60,
            delay=5,
            description="InfraEnv removal after ClusterOrder deletion",
            retry_on_error=True,
        )
    except TimeoutError:
        on_timeout()
        raise TimeoutError("InfraEnv garbage collection timed out after ClusterOrder removal") from None


@dataclass(frozen=True)
class _ClusterResources:
    """Verified test-owned resources used by the shared natural teardown assertions."""

    co_name: str
    owned_bmi_ids: set[str]
    infra_env_name: str


@dataclass(frozen=True)
class _AvailableCluster(_ClusterResources):
    """Additional cluster details needed by metering and scaling."""

    node_sets: dict[str, Any]
    worker_node_set: str
    original_size: int


def _assert_worker_agent_installing(*, k8s: K8sClient, co_name: str, namespace: str) -> None:
    logger.info("CaaS: waiting for Agent in ClusterOrder namespace")
    agents = poll_until(
        fn=lambda: _cluster_order_agents(k8s=k8s, co_name=co_name, namespace=namespace),
        until=bool,
        retries=180,
        delay=10,
        description="CaaS worker Agent registration",
    )
    agent_name = agents[0]["metadata"]["name"]
    logger.info("CaaS: Agent found; waiting for installing state")
    previous_state: str | None = None

    def _current_agent() -> dict[str, Any] | None:
        nonlocal previous_state
        agent = next(
            (
                item
                for item in _cluster_order_agents(k8s=k8s, co_name=co_name, namespace=namespace)
                if item.get("metadata", {}).get("name") == agent_name
            ),
            None,
        )
        state = agent.get("status", {}).get("debugInfo", {}).get("state", "") if agent else ""
        if state != previous_state:
            logger.info("CaaS Agent state: %s", state if state in _SAFE_AGENT_STATES else "unknown")
            previous_state = state
        return agent

    poll_until(
        fn=_current_agent,
        until=lambda agent: agent is not None and _agent_is_installing(agent),
        retries=180,
        delay=10,
        description="CaaS worker Agent installing state",
    )
    logger.info("CaaS: Agent entered installing state")


def _assert_operator_worker_metrics(*, k8s: K8sClient, co_name: str) -> None:
    try:
        worker_metrics = poll_until(
            fn=k8s.get_operator_metrics,
            until=lambda output: all(_metric_samples(output, metric_name) for metric_name in _WORKER_LEVEL_METRICS),
            retries=30,
            delay=5,
            description=f"{co_name} worker metrics",
        )
    except (subprocess.CalledProcessError, RuntimeError) as exc:
        detail = (
            (exc.stderr or exc.output or str(exc)).strip()
            if isinstance(exc, subprocess.CalledProcessError)
            else str(exc)
        )
        pytest.fail(
            f"Cannot verify CaaS worker metrics for ClusterOrder {co_name}: "
            f"the operator metrics endpoint is unavailable ({detail}); "
            "this is an infrastructure/profile failure.",
            pytrace=False,
        )
    _assert_worker_metrics(worker_metrics)


def _assert_cluster_version_propagated(
    *, k8s: K8sClient, co_name: str, cluster: dict[str, Any], cluster_order_spec: dict[str, Any], version_name: str
) -> tuple[str, str]:
    """Check fulfillment version resolution through to the HostedCluster release image."""
    resolved_version = cluster.get("object", {}).get("spec", {}).get("version", {}).get("name", "")
    assert resolved_version, "Cluster should have a resolved version name"
    assert resolved_version == version_name

    co_release_image = cluster_order_spec.get("releaseImage", "")
    assert co_release_image, "ClusterOrder should have a resolved releaseImage"

    hosted_cluster_name, hosted_cluster_ns = poll_until(
        fn=lambda: (
            k8s.get_cluster_order_hosted_cluster_name(name=co_name),
            k8s.get_cluster_order_namespace(name=co_name),
        ),
        until=lambda reference: all(reference),
        retries=30,
        delay=5,
        description=f"{co_name} HostedCluster reference",
    )
    hosted_cluster_image = run(
        *k8s._base(),
        "get",
        "hostedcluster",
        hosted_cluster_name,
        "-n",
        hosted_cluster_ns,
        "-o",
        "jsonpath={.spec.release.image}",
    )
    assert hosted_cluster_image == co_release_image, (
        f"HostedCluster image {hosted_cluster_image!r} != ClusterOrder releaseImage {co_release_image!r}"
    )
    return hosted_cluster_name, hosted_cluster_ns


def _assert_worker_node_request(
    *, node_sets: dict[str, Any], cluster_order_spec: dict[str, Any]
) -> tuple[str, int, str]:
    assert node_sets, "Cluster spec should have at least one node set for the scaling test"
    worker_node_set = next(iter(node_sets))
    original_size = int(node_sets[worker_node_set].get("size", 1))
    node_requests = cluster_order_spec.get("nodeRequests", [])
    assert len(node_requests) == 1, "Expected one node request for the CaaS sanity scenario"
    assert all("resourceClass" not in request for request in node_requests)
    worker_instance_type = node_requests[0]["bareMetal"]["instanceType"]
    assert worker_instance_type == node_sets[worker_node_set]["baremetalInstanceType"]["name"] == "ci-worker-bm"
    assert int(node_requests[0]["numberOfNodes"]) == original_size
    return worker_node_set, original_size, worker_instance_type


def _assert_guest_workers_ready(
    *,
    k8s: K8sClient,
    co_name: str,
    hosted_cluster_name: str,
    hosted_cluster_ns: str,
    worker_node_set: str,
    worker_instance_type: str,
    expected_workers: int,
) -> None:
    def _get_node_pool() -> dict[str, Any] | None:
        node_pools = k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=hosted_cluster_ns).get(
            "items", []
        )
        return next(
            (
                item
                for item in node_pools
                if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == co_name
                and item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/node-set") == worker_node_set
            ),
            None,
        )

    workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
        k8s=k8s, hosted_cluster_namespace=hosted_cluster_ns, hosted_cluster_name=hosted_cluster_name
    )
    with tempfile.NamedTemporaryFile(prefix="osac-workload-", suffix=".kubeconfig") as kubeconfig_file:
        kubeconfig_file.write(workload_kubeconfig)
        kubeconfig_file.flush()
        workload_k8s_client = K8sClient(
            namespace=hosted_cluster_ns, kubeconfig=kubeconfig_file.name, as_system_admin=False
        )
        node_pool = wait_for_cluster_guest_readiness(
            k8s=k8s,
            name=co_name,
            workload_k8s=workload_k8s_client,
            expected_workers=expected_workers,
            get_node_pool=_get_node_pool,
            expected_ready_nodes=expected_workers,
            node_pool_description=f"{hosted_cluster_name} NodePool {worker_node_set} ready nodes",
        )
    assert node_pool is not None, f"No NodePool found for NodeSet {worker_node_set!r}"
    labels = node_pool.get("metadata", {}).get("labels", {})
    assert labels.get("osac.openshift.io/instance_type") == worker_instance_type
    assert "osac.openshift.io/resource_class" not in labels
    selector = node_pool.get("spec", {}).get("platform", {}).get("agent", {}).get("agentLabelSelector", {})
    assert selector.get("matchLabels", {}) == {
        "osac.openshift.io/clusterorder": co_name,
        "osac.openshift.io/node-set": worker_node_set,
    }


def _assert_worker_bmi_resources(
    *, k8s: K8sClient, co_name: str, tenant: str, candidate_bmis: dict[str, dict[str, Any]], instance_types: set[str]
) -> set[str]:
    """Return only verified test-owned IDs for use in deletion assertions."""
    owned_bmi_ids: set[str] = set()
    expected_owner = f"ClusterOrder/{co_name}"
    for bmi_id, bmi in candidate_bmis.items():
        spec = bmi["spec"]
        assert spec.get("catalogItem", spec.get("catalog_item")) is None
        assert spec["template"]["id"] == "osac.templates.bm_host_provisioning"
        assert spec["template"]["shared"] is True
        instance_type = spec.get("instanceType", spec.get("instance_type", {}))
        assert instance_type["name"] in instance_types
        assert instance_type["shared"] is True
        cr_name = poll_until(
            fn=lambda bmi_id=bmi_id: k8s.get_baremetal_instance_name(uuid=bmi_id, checked=False),
            until=bool,
            retries=30,
            delay=2,
            description=f"{bmi_id} Kubernetes BMI CR",
        )
        cr = k8s.get_json(resource="baremetalinstance", name=cr_name)
        assert cr["metadata"]["labels"]["osac.openshift.io/baremetalinstance-uuid"] == bmi_id
        assert cr["metadata"]["annotations"]["osac.openshift.io/tenant"] == tenant
        assert cr["metadata"]["annotations"]["osac.openshift.io/owner-reference"] == expected_owner
        owned_bmi_ids.add(bmi_id)
    return owned_bmi_ids


def _wait_for_cluster_infra_env_name(*, k8s: K8sClient, co_name: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_cluster_order_infra_env_name(name=co_name, checked=False),
        until=lambda value: value != "",
        retries=30,
        delay=2,
        description=f"{co_name} InfraEnv name",
    )


def _assert_worker_provisioning(
    *, grpc: GRPCClient, k8s: K8sClient, uuid: str, co_name: str
) -> tuple[str, dict[str, dict[str, Any]]]:
    """Check tenant-visible workers, Agent installation, and operator metrics."""
    logger.info("CaaS: waiting for ClusterOrder to progress")
    wait_for_cluster_progressing(k8s=k8s, name=co_name)
    cluster_tenant = grpc.get_cluster(cluster_id=uuid)["object"]["metadata"]["tenant"]
    logger.info("CaaS: waiting for tenant-visible child BareMetalInstance")
    candidate_bmis = poll_until(
        fn=lambda: _list_owned_worker_bmis(grpc=grpc, co_name=co_name, tenant=cluster_tenant),
        until=bool,
        retries=60,
        delay=5,
        description=f"{co_name} tenant-visible child BareMetalInstances",
    )
    logger.info("CaaS: child BareMetalInstance found")

    co = k8s.get_json(resource="clusterorder", name=co_name)
    assert co["metadata"]["annotations"]["osac.openshift.io/tenant"] == cluster_tenant
    namespace = co["metadata"]["namespace"]
    assert namespace == k8s.namespace
    _assert_worker_agent_installing(k8s=k8s, co_name=co_name, namespace=namespace)
    _assert_operator_worker_metrics(k8s=k8s, co_name=co_name)
    return cluster_tenant, candidate_bmis


def assert_cluster_available(
    *, grpc: GRPCClient, k8s: K8sClient, metering: MeteringCollector, uuid: str, version_name: str
) -> _AvailableCluster:
    """Verify provisioning, version propagation, guest readiness, and worker ownership."""
    logger.info("CaaS: waiting for ClusterOrder")
    co_name = wait_for_cluster_order_cr(k8s=k8s, uuid=uuid)
    assert uuid in grpc.list_cluster_ids()
    cluster_tenant, candidate_bmis = _assert_worker_provisioning(grpc=grpc, k8s=k8s, uuid=uuid, co_name=co_name)

    metering.expect("osac.resource.started.v1", resource_id=uuid)
    metering.verify()
    wait_for_cluster_order_condition(k8s=k8s, name=co_name, condition_type="ClusterAvailable")

    cluster = grpc.get_cluster(cluster_id=uuid)
    cluster_order_spec = k8s.get_cluster_order_spec(name=co_name)
    hosted_cluster_name, hosted_cluster_ns = _assert_cluster_version_propagated(
        k8s=k8s, co_name=co_name, cluster=cluster, cluster_order_spec=cluster_order_spec, version_name=version_name
    )
    node_sets = cluster.get("object", {}).get("spec", {}).get("nodeSets", {})
    worker_node_set, original_size, worker_instance_type = _assert_worker_node_request(
        node_sets=node_sets, cluster_order_spec=cluster_order_spec
    )
    _assert_guest_workers_ready(
        k8s=k8s,
        co_name=co_name,
        hosted_cluster_name=hosted_cluster_name,
        hosted_cluster_ns=hosted_cluster_ns,
        worker_node_set=worker_node_set,
        worker_instance_type=worker_instance_type,
        expected_workers=original_size,
    )
    owned_bmi_ids = _assert_worker_bmi_resources(
        k8s=k8s,
        co_name=co_name,
        tenant=cluster_tenant,
        candidate_bmis=candidate_bmis,
        instance_types={worker_instance_type},
    )
    infra_env_name = _wait_for_cluster_infra_env_name(k8s=k8s, co_name=co_name)
    return _AvailableCluster(
        co_name=co_name,
        node_sets=node_sets,
        worker_node_set=worker_node_set,
        original_size=original_size,
        owned_bmi_ids=owned_bmi_ids,
        infra_env_name=infra_env_name,
    )


def _assert_cluster_metering(
    *, metering: MeteringCollector, uuid: str, node_sets: dict[str, Any], cluster_template: str
) -> None:
    """Verify N+1 heartbeat decomposition and the started event's billing dimensions."""
    expected_components = 1 + len(node_sets)
    metering.expect("osac.resource.heartbeat.v1", resource_id=uuid, timeout=180)
    metering.verify()

    heartbeats = metering.get_all_events("osac.resource.heartbeat.v1", resource_id=uuid)
    hb_components = [ev.get("data", {}).get("billing_dimensions", {}).get("component") for ev in heartbeats]
    cp_count = sum(1 for c in hb_components if c == "control_plane")
    worker_count = sum(1 for c in hb_components if c == "worker")
    assert cp_count >= 1, f"Expected at least 1 control_plane heartbeat, got {cp_count}"
    assert worker_count >= len(node_sets), f"Expected at least {len(node_sets)} worker heartbeat(s), got {worker_count}"
    assert len(heartbeats) >= expected_components, (
        f"Expected at least {expected_components} heartbeat events (1 cp + {len(node_sets)} workers), "
        f"got {len(heartbeats)}"
    )

    started = metering.get_event("osac.resource.started.v1", resource_id=uuid)
    assert started.get("osacresourcetype") == "cluster_order"
    started_bd = started.get("data", {}).get("billing_dimensions", {})
    assert started_bd.get("cluster_template") == cluster_template, (
        f"cluster_template mismatch: {started_bd.get('cluster_template')!r} != {cluster_template!r}"
    )


def _assert_worker_scale_metering(
    *, metering: MeteringCollector, uuid: str, worker_node_set: str, scaled_size: int
) -> None:
    metering.expect("osac.resource.updated.v1", resource_id=uuid, timeout=120)
    metering.verify()
    updated = metering.get_event("osac.resource.updated.v1", resource_id=uuid)
    updated_bd = updated.get("data", {}).get("billing_dimensions", {})
    assert updated_bd.get("node_set") == worker_node_set, (
        f"updated.v1 node_set mismatch: {updated_bd.get('node_set')!r} != {worker_node_set!r}"
    )
    assert updated_bd.get("node_count") == scaled_size, (
        f"updated.v1 node_count should be {scaled_size}, got {updated_bd.get('node_count')}"
    )


def _assert_cluster_deleted(
    *, grpc: GRPCClient, k8s: K8sClient, metering: MeteringCollector, uuid: str, cluster: _ClusterResources
) -> None:
    """Observe natural worker/parent teardown and dependent GC without forced cleanup."""
    co_name = cluster.co_name
    metering.expect("osac.resource.deleted.v1", resource_id=uuid)
    wait_for_cluster_deleting(k8s=k8s, name=co_name)
    wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
    bmi_filter = f'this.metadata.labels["osac.openshift.io/cluster-order"] == "{co_name}"'
    last_snapshot = time.monotonic() - 60

    def _log_deletion_snapshot() -> None:
        nonlocal last_snapshot
        last_snapshot = time.monotonic()
        try:
            for line in snapshot_cluster_deletion(k8s, co_name):
                logger.info("CaaS deletion snapshot: %s", line)
        except Exception:  # Diagnostics must not change test outcomes.
            logger.warning("CaaS deletion snapshot unavailable")

    def _snapshot_if_due() -> None:
        if time.monotonic() - last_snapshot >= 60:
            _log_deletion_snapshot()

    def _bmis_removed_with_snapshot() -> bool:
        _snapshot_if_due()
        return cluster.owned_bmi_ids.isdisjoint(set(grpc.list_baremetal_instance_ids(filter_expr=bmi_filter)))

    poll_until(
        fn=_bmis_removed_with_snapshot,
        until=lambda value: value is True,
        # CAP-Agent unbinding has a 30-minute operator timeout; allow the
        # CAPI hooks and BMaaS deprovision to finish after it starts.
        retries=480,
        delay=5,
        description=f"{co_name} CaaS worker BMI removal",
    )
    assert cluster.infra_env_name is not None
    _wait_for_cluster_and_infraenv_deletion(
        k8s=k8s,
        co_name=co_name,
        infra_env_name=cluster.infra_env_name,
        on_poll=_snapshot_if_due,
        on_timeout=_log_deletion_snapshot,
    )
    assert not k8s.is_present(resource="clusterorder", name=co_name)
    wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
    metering.verify()


@pytest.mark.metering
def test_cluster_create(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify the full CaaS cluster lifecycle: create, provision to Ready, version and
    releaseImage propagation to the HostedCluster, N+1 metering heartbeat decomposition,
    worker scale-up reflected in updated.v1 metering, and deletion."""

    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    logger.info("CaaS: ensuring DiskImage and ClusterVersion")
    disk_image_id = private_grpc.ensure_disk_image(name="rhcos-4-22", source_ref=RHCOS_IMAGE)
    version = private_grpc.ensure_cluster_version(
        version="4.22.0-rhcos", image=TEST_RELEASE_IMAGE, disk_image=disk_image_id
    )
    logger.info("CaaS: ClusterVersion available")
    name = unique_name("e2e-cluster")
    logger.info("CaaS: creating cluster")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        version=version["name"],
        node_sets={"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}},
        pull_secret=pull_secret_name,
        ssh_public_key_file=ssh_public_key_path,
    )
    logger.info("CaaS: cluster created")
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    try:
        cluster = assert_cluster_available(
            grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, version_name=version["name"]
        )
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client, require_metering=True)
            assert_cluster_trust(k8s_hub_client, cluster.co_name)
        _assert_cluster_metering(
            metering=metering, uuid=uuid, node_sets=cluster.node_sets, cluster_template=cluster_template
        )

        scaled_size = cluster.original_size + 1
        cli.scale_cluster(uuid=uuid, node_set=cluster.worker_node_set, size=scaled_size)
        _assert_worker_scale_metering(
            metering=metering, uuid=uuid, worker_node_set=cluster.worker_node_set, scaled_size=scaled_size
        )

        # Scale back before deletion.
        cli.scale_cluster(uuid=uuid, node_set=cluster.worker_node_set, size=cluster.original_size)
        cli.delete_cluster(uuid=uuid)
        _assert_cluster_deleted(grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, cluster=cluster)
    finally:
        with contextlib.suppress(subprocess.SubprocessError):
            cli.delete_cluster(uuid=uuid)


def _assert_two_node_set_requests(
    *, cluster_order: dict[str, Any], cluster_node_sets: dict[str, Any], instance_types: dict[str, str]
) -> dict[str, int]:
    node_requests = cluster_order.get("spec", {}).get("nodeRequests", [])
    assert len(node_requests) == len(instance_types)
    assert all("resourceClass" not in request for request in node_requests)
    assert {request["nodeSet"]: request["bareMetal"]["instanceType"] for request in node_requests} == instance_types
    assert {
        name: node_set["baremetalInstanceType"]["name"] for name, node_set in cluster_node_sets.items()
    } == instance_types
    expected_replicas = {request["nodeSet"]: int(request["numberOfNodes"]) for request in node_requests}
    assert expected_replicas == dict.fromkeys(instance_types, 1)
    assert {name: int(node_set["size"]) for name, node_set in cluster_node_sets.items()} == expected_replicas
    return expected_replicas


def _assert_worker_aggregates(*, k8s: K8sClient, co_name: str, expected_workers: int) -> None:
    def _get_worker_counts() -> tuple[int, int, int]:
        status = k8s.get_json(resource="clusterorder", name=co_name).get("status", {})
        return tuple(int(status.get(key, -1)) for key in ("desiredWorkers", "currentWorkers", "readyWorkers"))

    poll_until(
        fn=_get_worker_counts,
        until=lambda value: value == (expected_workers, expected_workers, expected_workers),
        retries=120,
        delay=10,
        description=f"{co_name} worker aggregates before NodePool isolation check",
    )


def _agent_is_installed(agent: dict[str, Any]) -> bool:
    return any(
        condition.get("type") == "Installed" and condition.get("status") == "True"
        for condition in agent.get("status", {}).get("conditions", [])
    )


def _assert_installed_agents_by_node_set(
    *, k8s: K8sClient, co_name: str, expected_replicas: dict[str, int], instance_types: dict[str, str]
) -> dict[str, list[dict[str, Any]]]:
    def _get_agents_by_node_set() -> dict[str, list[dict[str, Any]]]:
        agents: dict[str, list[dict[str, Any]]] = {}
        items = k8s.list_json(resource="agents.agent-install.openshift.io", namespace=k8s.namespace).get("items", [])
        for item in items:
            labels = item.get("metadata", {}).get("labels", {})
            if labels.get("osac.openshift.io/clusterorder") != co_name:
                continue
            node_set = labels.get("osac.openshift.io/node-set")
            if node_set in expected_replicas:
                assert labels.get("osac.openshift.io/instance_type") == instance_types[node_set]
                assert "osac.openshift.io/resource_class" not in labels
                agents.setdefault(node_set, []).append(item)
        return agents

    return poll_until(
        fn=_get_agents_by_node_set,
        until=lambda agents: (
            set(agents) == set(expected_replicas)
            and all(
                len(items) == expected_replicas[node_set] and all(_agent_is_installed(item) for item in items)
                for node_set, items in agents.items()
            )
        ),
        retries=120,
        delay=10,
        description=f"{co_name} installed Agents by NodeSet",
    )


def _assert_isolated_node_pools(
    *,
    k8s: K8sClient,
    co_name: str,
    hosted_cluster_ns: str,
    expected_replicas: dict[str, int],
    instance_types: dict[str, str],
) -> None:
    node_set_label = "osac.openshift.io/node-set"
    instance_type_label = "osac.openshift.io/instance_type"
    old_label = "osac.openshift.io/resource_class"

    def _get_node_pools() -> dict[str, dict[str, Any]]:
        items = k8s.list_json(resource="nodepools.hypershift.openshift.io", namespace=hosted_cluster_ns).get(
            "items", []
        )
        return {
            item.get("metadata", {}).get("labels", {}).get(node_set_label, ""): item
            for item in items
            if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == co_name
            and item.get("metadata", {}).get("labels", {}).get(node_set_label) in expected_replicas
        }

    node_pools = poll_until(
        fn=_get_node_pools,
        until=lambda pools: (
            set(pools) == set(expected_replicas)
            and all(
                int(pools[node_set].get("spec", {}).get("replicas", -1)) == replicas
                for node_set, replicas in expected_replicas.items()
            )
        ),
        retries=60,
        delay=10,
        description=f"{co_name} per-NodeSet NodePool replicas",
    )
    for node_set, node_pool in node_pools.items():
        labels = node_pool.get("metadata", {}).get("labels", {})
        assert labels.get(instance_type_label) == instance_types[node_set]
        assert old_label not in labels
        selector = node_pool.get("spec", {}).get("platform", {}).get("agent", {}).get("agentLabelSelector", {})
        assert selector.get("matchLabels", {}) == {"osac.openshift.io/clusterorder": co_name, node_set_label: node_set}


def _assert_owned_workers_for_node_sets(
    *,
    grpc: GRPCClient,
    k8s: K8sClient,
    co_name: str,
    tenant: str,
    expected_replicas: dict[str, int],
    instance_types: set[str],
) -> set[str]:
    candidate_bmis = poll_until(
        fn=lambda: _list_owned_worker_bmis(grpc=grpc, co_name=co_name, tenant=tenant),
        until=lambda workers: len(workers) == sum(expected_replicas.values()),
        retries=60,
        delay=5,
        description=f"{co_name} tenant-visible child BareMetalInstances",
    )
    return _assert_worker_bmi_resources(
        k8s=k8s, co_name=co_name, tenant=tenant, candidate_bmis=candidate_bmis, instance_types=instance_types
    )


def assert_cluster_with_two_node_sets_available(
    *, grpc: GRPCClient, k8s: K8sClient, metering: MeteringCollector, uuid: str, instance_types: dict[str, str]
) -> _ClusterResources:
    """Verify two ready worker sets remain isolated and record their owned resources."""
    co_name = wait_for_cluster_order_cr(k8s=k8s, uuid=uuid)
    assert uuid in grpc.list_cluster_ids()
    wait_for_cluster_progressing(k8s=k8s, name=co_name)
    metering.expect("osac.resource.started.v1", resource_id=uuid)
    metering.verify()

    cluster_order = k8s.get_json(resource="clusterorder", name=co_name)
    cluster = grpc.get_cluster(cluster_id=uuid)["object"]
    expected_replicas = _assert_two_node_set_requests(
        cluster_order=cluster_order, cluster_node_sets=cluster["spec"]["nodeSets"], instance_types=instance_types
    )
    hosted_cluster_ns = k8s.get_cluster_order_namespace(name=co_name)
    _assert_worker_aggregates(k8s=k8s, co_name=co_name, expected_workers=sum(expected_replicas.values()))
    _assert_installed_agents_by_node_set(
        k8s=k8s, co_name=co_name, expected_replicas=expected_replicas, instance_types=instance_types
    )
    _assert_isolated_node_pools(
        k8s=k8s,
        co_name=co_name,
        hosted_cluster_ns=hosted_cluster_ns,
        expected_replicas=expected_replicas,
        instance_types=instance_types,
    )

    cluster_tenant = cluster["metadata"]["tenant"]
    assert cluster_order["metadata"]["annotations"]["osac.openshift.io/tenant"] == cluster_tenant
    assert cluster_order["metadata"]["namespace"] == k8s.namespace
    owned_bmi_ids = _assert_owned_workers_for_node_sets(
        grpc=grpc,
        k8s=k8s,
        co_name=co_name,
        tenant=cluster_tenant,
        expected_replicas=expected_replicas,
        instance_types=set(instance_types.values()),
    )
    infra_env_name = _wait_for_cluster_infra_env_name(k8s=k8s, co_name=co_name)
    return _ClusterResources(co_name=co_name, owned_bmi_ids=owned_bmi_ids, infra_env_name=infra_env_name)


@pytest.mark.metering
def test_cluster_create_with_two_node_sets(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify NodePool replicas stay isolated when a cluster has two BMaaS node sets."""

    instance_types = {"compute": "ci-worker-bm", "gpu": "ci-worker-bm-gpu"}
    for instance_type in instance_types.values():
        private_grpc.ensure_bare_metal_instance_type(
            name=instance_type, host_label_selector={"osac.openshift.io/host-type": "default"}
        )

    version = private_grpc.ensure_cluster_version(
        version="4.22.0-rhcos",
        image=TEST_RELEASE_IMAGE,
        disk_image=private_grpc.ensure_disk_image(name="rhcos-4-22", source_ref=RHCOS_IMAGE),
    )
    name = unique_name("e2e-cluster-two-node-sets")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        version=version["name"],
        node_sets={
            node_set: {"size": 1, "baremetal_instance_type": {"name": instance_type}}
            for node_set, instance_type in instance_types.items()
        },
        pull_secret=pull_secret_name,
        ssh_public_key_file=ssh_public_key_path,
    )
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    try:
        cluster = assert_cluster_with_two_node_sets_available(
            grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, instance_types=instance_types
        )
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client, require_metering=True)
            assert_cluster_trust(k8s_hub_client, cluster.co_name)
        cli.delete_cluster(uuid=uuid)
        _assert_cluster_deleted(grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, cluster=cluster)
    finally:
        with contextlib.suppress(subprocess.SubprocessError):
            cli.delete_cluster(uuid=uuid)


def test_cluster_create_with_version(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
) -> None:
    """Verify explicit --version resolution: the Cluster API resource stores the
    version reference, the ClusterVersion cannot be deleted while referenced,
    and the ClusterOrder CR's releaseImage is resolved from the matching
    ClusterVersion. Does not wait for full provisioning — the HostedCluster
    image propagation is covered by test_cluster_create."""
    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    disk_image_id = private_grpc.ensure_disk_image(name="rhcos-4-22", source_ref=RHCOS_IMAGE)
    version = private_grpc.ensure_cluster_version(
        version="4.20.0-e2e", image=TEST_RELEASE_IMAGE, disk_image=disk_image_id
    )

    name = unique_name("e2e-cluster-version")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        version=version["name"],
        node_sets={"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}},
        pull_secret=pull_secret_name,
        ssh_public_key_file=ssh_public_key_path,
    )

    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)

        cluster = grpc.get_cluster(cluster_id=uuid)
        assert cluster["object"]["spec"]["version"]["name"] == version["name"]

        # While the cluster references this version, deletion must be rejected
        output, rc = private_grpc.call_unchecked(
            service="osac.private.v1.ClusterVersions/Delete", data={"id": version["id"]}
        )
        assert rc != 0, f"Expected delete to be rejected for referenced version, got: {output}"
        assert "FailedPrecondition" in output or "referenced" in output.lower(), (
            f"Expected FailedPrecondition or 'referenced' in rejection, got: {output}"
        )

        release_image = poll_until(
            fn=lambda: k8s_hub_client.get_cluster_order_spec(name=co_name).get("releaseImage", ""),
            until=lambda v: v != "",
            retries=30,
            delay=5,
            description=f"{co_name} ClusterOrder releaseImage resolution",
        )
        assert release_image == TEST_RELEASE_IMAGE

        cli.delete_cluster(uuid=uuid)

        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)


def test_cluster_create_rejected_for_invalid_version(
    grpc: GRPCClient, private_grpc: GRPCClient, cluster_template: str
) -> None:
    """Verify cluster creation is rejected for disabled, obsolete, and
    non-existent versions."""
    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    disabled = private_grpc.ensure_cluster_version(version="4.20.0-e2e-disabled", image=TEST_RELEASE_IMAGE)
    private_grpc.update_cluster_version(version_id=disabled["id"], enabled=False)

    obsolete = private_grpc.ensure_cluster_version(version="4.20.0-e2e-obsolete", image=TEST_RELEASE_IMAGE)
    private_grpc.update_cluster_version(version_id=obsolete["id"], state="CLUSTER_VERSION_STATE_OBSOLETE")

    def _create_with_version(version_name: str) -> tuple[str, int]:
        return grpc.call_unchecked(
            service="osac.public.v1.Clusters/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-cluster-invalid-version")},
                    "spec": {
                        "template": {"name": cluster_template, "shared": True},
                        "version": {"name": version_name, "shared": True},
                        "nodeSets": {
                            "workers": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm", "shared": True}}
                        },
                    },
                }
            },
        )

    output, rc = _create_with_version(disabled["name"])
    assert rc != 0, f"Expected create to reject disabled version, got: {output}"
    assert "disabled" in output.lower(), f"Expected 'disabled' in rejection, got: {output}"

    output, rc = _create_with_version(obsolete["name"])
    assert rc != 0, f"Expected create to reject obsolete version, got: {output}"
    assert "obsolete" in output.lower(), f"Expected 'obsolete' in rejection, got: {output}"

    output, rc = _create_with_version("4-20-0-e2e-does-not-exist")
    assert rc != 0, f"Expected create to reject non-existent version, got: {output}"
    assert "not found" in output.lower(), f"Expected 'not found' in rejection, got: {output}"
