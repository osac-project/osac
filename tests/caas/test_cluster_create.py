from __future__ import annotations

import contextlib
import subprocess
from pathlib import Path

import pytest

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_cluster_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.metering import MeteringCollector
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until, run

# Real OCP release image used to provision the ClusterVersion(s) created by these
# tests. Fixed (not randomly generated) so repeated runs reuse the same
# ClusterVersion via GRPCClient.ensure_cluster_version instead of accumulating
# duplicates on the shared cluster.
TEST_RELEASE_IMAGE = "quay.io/openshift-release-dev/ocp-release:4.20.0-multi"


@pytest.mark.metering
def test_cluster_create(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    name = unique_name("e2e-cluster")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        assert uuid in grpc.list_cluster_ids()

        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)
        metering.expect("osac.resource.started.v1", resource_id=uuid)
        metering.verify()

        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

        # Verify version resolved and propagated end-to-end:
        # fulfillment-service default resolution -> ClusterOrder releaseImage -> HostedCluster image
        cluster = grpc.get_cluster(cluster_id=uuid)
        version_name = cluster.get("object", {}).get("spec", {}).get("version", {}).get("name", "")
        assert version_name, "Cluster should have a resolved version name"

        co_release_image = k8s_hub_client.get_cluster_order_spec(name=co_name).get("releaseImage", "")
        assert co_release_image, "ClusterOrder should have a resolved releaseImage"

        hosted_cluster_name = k8s_hub_client.get_cluster_order_hosted_cluster_name(name=co_name)
        hosted_cluster_ns = k8s_hub_client.get_cluster_order_namespace(name=co_name)
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

        # Derive expected N+1 count from cluster spec
        node_sets = cluster.get("object", {}).get("spec", {}).get("nodeSets", {})
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
        worker_node_set = next(iter(node_sets))
        original_size = node_sets[worker_node_set].get("size", 1)
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

        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
        metering.verify()
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)


def test_cluster_create_with_version(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
) -> None:
    """Verify explicit --version resolution: the Cluster API resource stores the
    version reference, the ClusterVersion cannot be deleted while referenced,
    and the ClusterOrder CR's releaseImage is resolved from the matching
    ClusterVersion. Does not wait for full provisioning — the HostedCluster
    image propagation is covered by test_cluster_create."""
    version = private_grpc.ensure_cluster_version(version="4.20.0-e2e", image=TEST_RELEASE_IMAGE)

    name = unique_name("e2e-cluster-version")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        version=version["name"],
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
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
    disabled = private_grpc.ensure_cluster_version(version="4.20.0-e2e-disabled", image=TEST_RELEASE_IMAGE)
    private_grpc.update_cluster_version(version_id=disabled["id"], enabled=False)

    obsolete = private_grpc.ensure_cluster_version(version="4.20.0-e2e-obsolete", image=TEST_RELEASE_IMAGE)
    private_grpc.update_cluster_version(version_id=obsolete["id"], state="CLUSTER_VERSION_STATE_OBSOLETE")

    def _create_with_version(version_name: str) -> tuple[str, int]:
        return grpc.call_unchecked(
            service="osac.public.v1.Clusters/Create",
            data={"object": {"spec": {"template": {"name": cluster_template}, "version": {"name": version_name}}}},
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
