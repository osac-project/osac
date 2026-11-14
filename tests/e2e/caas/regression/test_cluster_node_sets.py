from __future__ import annotations

import contextlib
import os
import subprocess

import pytest

from tests.e2e.caas.add_on_operators import (
    _assert_cluster_add_on_operators_installed,
    _ensure_published_operators,
    _restore_operator_publication,
)
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
    private_cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_key_secret: str,
    metering: MeteringCollector,
) -> None:
    """Verify NodePool replicas stay isolated when a cluster has two BMaaS node sets."""

    operator_names = ("node-feature-discovery", "nvidia-gpu")
    requested_operator_names = ("nvidia-gpu",)
    operator_specs, publication_states, _ = _ensure_published_operators(private_cli, private_grpc, operator_names)
    instance_types = {"compute": "ci-worker-bm", "gpu": "ci-worker-bm-gpu"}
    uuid = ""
    try:
        for instance_type in instance_types.values():
            private_grpc.ensure_bare_metal_instance_type(name=instance_type)

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
            ssh_key=ssh_key_secret,
            add_on_operators=requested_operator_names,
        )
        cluster = assert_cluster_with_two_node_sets_available(
            grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, instance_types=instance_types
        )
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client, require_metering=True)
            assert_cluster_trust(k8s_hub_client, cluster.co_name)

        resolved_operator_names = set(
            k8s_hub_client.get_json(resource="clusterorder", name=cluster.co_name)
            .get("spec", {})
            .get("addOnOperators", [])
        )
        assert resolved_operator_names == set(operator_names), (
            f"ClusterOrder did not expand add-on operator dependencies: {resolved_operator_names!r}"
        )
        _assert_cluster_add_on_operators_installed(
            client=grpc,
            hub_k8s=k8s_hub_client,
            cluster_id=uuid,
            cluster_order_name=cluster.co_name,
            expected_operator_specs=operator_specs,
            identity="CaaS two-node-set cluster",
            hosted_cluster_reference=(cluster.hosted_cluster_name, cluster.hosted_cluster_namespace),
        )
        cli.delete_cluster(uuid=uuid)
        _assert_cluster_deleted(grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, cluster=cluster)
    finally:
        with contextlib.suppress(subprocess.SubprocessError):
            if uuid:
                cli.delete_cluster(uuid=uuid)
        _restore_operator_publication(private_grpc, publication_states)
