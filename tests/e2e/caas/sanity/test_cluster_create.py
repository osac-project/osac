from __future__ import annotations

import contextlib
import logging
import subprocess
import tempfile
import time
from typing import Any

import pytest

from tests.e2e.caas.deletion_diagnostics import snapshot_cluster_deletion
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
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


@pytest.mark.metering
@pytest.mark.requires_caas_fabric
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
    run_owned_bmi_ids: set[str] = set()
    infra_env_name: str | None = None
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
        logger.info("CaaS: waiting for ClusterOrder")
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        assert uuid in grpc.list_cluster_ids()

        logger.info("CaaS: waiting for ClusterOrder to progress")
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)
        cluster = grpc.get_cluster(cluster_id=uuid)
        cluster_tenant = cluster["object"]["metadata"]["tenant"]
        bmi_filter = f'this.metadata.labels["osac.openshift.io/cluster-order"] == "{co_name}"'
        logger.info("CaaS: waiting for tenant-visible child BareMetalInstance")
        candidate_bmis = poll_until(
            fn=lambda: _list_owned_worker_bmis(grpc=grpc, co_name=co_name, tenant=cluster_tenant),
            until=bool,
            retries=60,
            delay=5,
            description=f"{co_name} tenant-visible child BareMetalInstances",
        )
        logger.info("CaaS: child BareMetalInstance found")

        co = k8s_hub_client.get_json(resource="clusterorder", name=co_name)
        assert co["metadata"]["annotations"]["osac.openshift.io/tenant"] == cluster_tenant
        namespace = co["metadata"]["namespace"]
        assert namespace == k8s_hub_client.namespace
        logger.info("CaaS: waiting for Agent in ClusterOrder namespace")
        agents = poll_until(
            fn=lambda: _cluster_order_agents(k8s=k8s_hub_client, co_name=co_name, namespace=namespace),
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
                    for item in _cluster_order_agents(k8s=k8s_hub_client, co_name=co_name, namespace=namespace)
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

        try:
            worker_metrics = poll_until(
                fn=k8s_hub_client.get_operator_metrics,
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

        metering.expect("osac.resource.started.v1", resource_id=uuid)
        metering.verify()

        wait_for_cluster_order_condition(k8s=k8s_hub_client, name=co_name, condition_type="ClusterAvailable")

        # Verify version resolved and propagated end-to-end:
        # fulfillment-service version resolution -> ClusterOrder releaseImage -> HostedCluster image
        cluster = grpc.get_cluster(cluster_id=uuid)
        version_name = cluster.get("object", {}).get("spec", {}).get("version", {}).get("name", "")
        assert version_name, "Cluster should have a resolved version name"
        assert version_name == version["name"]

        cluster_order_spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
        co_release_image = cluster_order_spec.get("releaseImage", "")
        assert co_release_image, "ClusterOrder should have a resolved releaseImage"

        hosted_cluster_name, hosted_cluster_ns = poll_until(
            fn=lambda: (
                k8s_hub_client.get_cluster_order_hosted_cluster_name(name=co_name),
                k8s_hub_client.get_cluster_order_namespace(name=co_name),
            ),
            until=lambda reference: all(reference),
            retries=30,
            delay=5,
            description=f"{co_name} HostedCluster reference",
        )
        hosted_cluster_image = run(
            *k8s_hub_client._base(),
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

        node_sets = cluster.get("object", {}).get("spec", {}).get("nodeSets", {})
        assert node_sets, "Cluster spec should have at least one node set for the scaling test"
        worker_node_set = next(iter(node_sets))
        original_size = int(node_sets[worker_node_set].get("size", 1))
        node_requests = cluster_order_spec.get("nodeRequests", [])
        assert len(node_requests) == 1, "Expected one node request for the CaaS sanity scenario"
        assert all("resourceClass" not in request for request in node_requests)
        worker_instance_type = node_requests[0]["bareMetal"]["instanceType"]
        assert worker_instance_type == node_sets[worker_node_set]["baremetalInstanceType"]["name"] == "ci-worker-bm"
        assert int(node_requests[0]["numberOfNodes"]) == original_size

        def _get_node_pool() -> dict[str, Any] | None:
            node_pools = k8s_hub_client.list_json(
                resource="nodepools.hypershift.openshift.io", namespace=hosted_cluster_ns
            ).get("items", [])
            return next(
                (
                    item
                    for item in node_pools
                    if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/instance_type")
                    == worker_instance_type
                ),
                None,
            )

        workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
            k8s=k8s_hub_client, hosted_cluster_namespace=hosted_cluster_ns, hosted_cluster_name=hosted_cluster_name
        )
        with tempfile.NamedTemporaryFile(prefix="osac-workload-", suffix=".kubeconfig") as kubeconfig_file:
            kubeconfig_file.write(workload_kubeconfig)
            kubeconfig_file.flush()
            workload_k8s_client = K8sClient(
                namespace=hosted_cluster_ns, kubeconfig=kubeconfig_file.name, as_system_admin=False
            )
            node_pool = wait_for_cluster_guest_readiness(
                k8s=k8s_hub_client,
                name=co_name,
                workload_k8s=workload_k8s_client,
                expected_workers=original_size,
                get_node_pool=_get_node_pool,
                expected_ready_nodes=original_size,
                node_pool_description=f"{hosted_cluster_name} NodePool {worker_instance_type} ready nodes",
            )
        assert node_pool is not None, f"No NodePool found for instance type {worker_instance_type!r}"
        assert "osac.openshift.io/resource_class" not in node_pool.get("metadata", {}).get("labels", {})
        selector = node_pool.get("spec", {}).get("platform", {}).get("agent", {}).get("agentLabelSelector", {})
        assert selector.get("matchLabels", {}).get("osac.openshift.io/instance_type") == worker_instance_type
        assert "osac.openshift.io/resource_class" not in selector.get("matchLabels", {})

        expected_owner = f"ClusterOrder/{co_name}"
        for bmi_id, bmi in candidate_bmis.items():
            spec = bmi["spec"]
            assert spec.get("catalogItem", spec.get("catalog_item")) is None
            assert spec["template"]["id"] == "osac.templates.bm_host_provisioning"
            assert spec["template"]["shared"] is True
            instance_type = spec.get("instanceType", spec.get("instance_type", {}))
            assert instance_type["name"] == "ci-worker-bm"
            assert instance_type["shared"] is True
            cr_name = poll_until(
                fn=lambda bmi_id=bmi_id: k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False),
                until=bool,
                retries=30,
                delay=2,
                description=f"{bmi_id} Kubernetes BMI CR",
            )
            cr = k8s_hub_client.get_json(resource="baremetalinstance", name=cr_name)
            assert cr["metadata"]["labels"]["osac.openshift.io/baremetalinstance-uuid"] == bmi_id
            assert cr["metadata"]["annotations"]["osac.openshift.io/tenant"] == cluster_tenant
            assert cr["metadata"]["annotations"]["osac.openshift.io/owner-reference"] == expected_owner
            run_owned_bmi_ids.add(bmi_id)  # Only verified test-owned IDs may be used in deletion assertions.
        infra_env_name = poll_until(
            fn=lambda: k8s_hub_client.get_cluster_order_infra_env_name(name=co_name, checked=False),
            until=lambda value: value != "",
            retries=30,
            delay=2,
            description=f"{co_name} InfraEnv name",
        )

        # Derive expected N+1 count from cluster spec
        expected_components = 1 + len(node_sets)

        # Verify N+1 heartbeat decomposition
        metering.expect("osac.resource.heartbeat.v1", resource_id=uuid, timeout=180)
        metering.verify()

        heartbeats = metering.get_all_events("osac.resource.heartbeat.v1", resource_id=uuid)
        hb_components = [ev.get("data", {}).get("billing_dimensions", {}).get("component") for ev in heartbeats]
        cp_count = sum(1 for c in hb_components if c == "control_plane")
        worker_count = sum(1 for c in hb_components if c == "worker")
        assert cp_count >= 1, f"Expected at least 1 control_plane heartbeat, got {cp_count}"
        assert worker_count >= len(node_sets), (
            f"Expected at least {len(node_sets)} worker heartbeat(s), got {worker_count}"
        )
        assert len(heartbeats) >= expected_components, (
            f"Expected at least {expected_components} heartbeat events (1 cp + {len(node_sets)} workers), "
            f"got {len(heartbeats)}"
        )

        # Verify started.v1 carries correct resource type and cluster template
        started = metering.get_event("osac.resource.started.v1", resource_id=uuid)
        assert started.get("osacresourcetype") == "cluster_order"
        started_bd = started.get("data", {}).get("billing_dimensions", {})
        assert started_bd.get("cluster_template") == cluster_template, (
            f"cluster_template mismatch: {started_bd.get('cluster_template')!r} != {cluster_template!r}"
        )

        # Scale a worker node set and verify updated.v1
        scaled_size = original_size + 1
        cli.scale_cluster(uuid=uuid, node_set=worker_node_set, size=scaled_size)
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

        # Scale back before deletion
        cli.scale_cluster(uuid=uuid, node_set=worker_node_set, size=original_size)

        cli.delete_cluster(uuid=uuid)
        metering.expect("osac.resource.deleted.v1", resource_id=uuid)

        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)

        # Temporary diagnostic only: sample the unmodified deletion path before
        # wait_for_cluster_deletion applies its existing HyperShift workarounds.
        last_snapshot = time.monotonic() - 60

        def _log_deletion_snapshot() -> None:
            nonlocal last_snapshot
            last_snapshot = time.monotonic()
            try:
                for line in snapshot_cluster_deletion(k8s_hub_client, co_name):
                    logger.info("CaaS deletion snapshot: %s", line)
            except Exception:  # Diagnostics must not change test outcomes.
                logger.warning("CaaS deletion snapshot unavailable")

        def _snapshot_if_due() -> None:
            if time.monotonic() - last_snapshot >= 60:
                _log_deletion_snapshot()

        def _bmis_removed_with_snapshot() -> bool:
            _snapshot_if_due()
            return run_owned_bmi_ids.isdisjoint(set(grpc.list_baremetal_instance_ids(filter_expr=bmi_filter)))

        poll_until(
            fn=_bmis_removed_with_snapshot,
            until=lambda value: value is True,
            # CAP-Agent unbinding has a 30-minute operator timeout; allow the
            # CAPI hooks and BMaaS deprovision to finish after it starts.
            retries=480,
            delay=5,
            description=f"{co_name} CaaS worker BMI removal",
        )
        assert infra_env_name is not None

        def _infraenv_absent_with_snapshot() -> bool:
            _snapshot_if_due()
            return k8s_hub_client.is_absent(resource="infraenv.agent-install.openshift.io", name=infra_env_name)

        # Capture the unmodified deletion state before the helper applies its
        # existing HyperShift cleanup workarounds.
        _log_deletion_snapshot()
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        assert not k8s_hub_client.is_present(resource="clusterorder", name=co_name)

        # InfraEnv is owned by the ClusterOrder, so Kubernetes garbage
        # collection can remove it only after the ClusterOrder is gone.
        try:
            poll_until(
                fn=_infraenv_absent_with_snapshot,
                until=lambda value: value is True,
                retries=60,
                delay=5,
                description=f"{infra_env_name} InfraEnv removal",
                retry_on_error=True,
            )
        except TimeoutError:
            _log_deletion_snapshot()
            raise

        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
        metering.verify()
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
