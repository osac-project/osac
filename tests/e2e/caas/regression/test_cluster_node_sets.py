from __future__ import annotations

import contextlib
import os
import subprocess

import pytest

from tests.e2e.caas.sanity.test_cluster_create import (
    RHCOS_IMAGE,
    TEST_RELEASE_IMAGE,
    _assert_cluster_deleted,
    assert_cluster_with_two_node_sets_available,
)
from tests.e2e.core.fulfillment_trust import assert_cluster_trust, assert_management_tls
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.regression


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
