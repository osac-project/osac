from __future__ import annotations

import os
from typing import Any

import pytest

from tests.e2e.caas.regression import cluster_upgrade as upgrade
from tests.e2e.caas.sanity.test_cluster_create import (
    RHCOS_IMAGE,
    TEST_RELEASE_IMAGE,
    _assert_cluster_deleted,
    assert_cluster_with_two_node_sets_available,
)
from tests.e2e.core.fulfillment_trust import assert_cluster_trust, assert_management_tls
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cluster_order_cr
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.regression

REGULAR_NODE_SET = "compute"
GPU_NODE_SET = "gpu"
INSTANCE_TYPES = {REGULAR_NODE_SET: "ci-worker-bm", GPU_NODE_SET: "ci-worker-bm-gpu"}


@pytest.mark.metering
def test_cluster_create_with_two_node_sets(
    request: pytest.FixtureRequest,
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify two-node-set installation before upgrading the ready cluster."""

    instance_types = INSTANCE_TYPES
    for instance_type in instance_types.values():
        private_grpc.ensure_bare_metal_instance_type(
            name=instance_type, host_label_selector={"osac.openshift.io/host-type": "default"}
        )

    disk_image = private_grpc.ensure_disk_image(name="rhcos-4-22", source_ref=RHCOS_IMAGE)
    version = private_grpc.ensure_cluster_version(
        version="4.22.0-rhcos", image=TEST_RELEASE_IMAGE, disk_image=disk_image
    )
    name = unique_name("e2e-cluster-two-node-sets")
    evidence: dict[str, Any] = {"phase": "preparing", "outcome": "failed"}
    uuid: str | None = None
    co_name: str | None = None
    delete_cluster_requested = False
    created_version_ids: list[str] = []
    try:
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
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        cluster = assert_cluster_with_two_node_sets_available(
            grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, instance_types=instance_types
        )
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client, require_metering=True)
            assert_cluster_trust(k8s_hub_client, co_name)
        evidence["phase"] = "install-completed"

        jwt_grpc_tenant1: GRPCClient = request.getfixturevalue("jwt_grpc_tenant1")
        refs = upgrade._cluster_references(
            k8s=k8s_hub_client, uuid=uuid, co_name=co_name, expected_node_sets=set(instance_types)
        )
        baseline = upgrade._ready_baseline(k8s=k8s_hub_client, grpc=grpc, refs=refs)
        target_version, target_created = upgrade._catalog_version(
            private_grpc=private_grpc, version=upgrade.TARGET_VERSION, image=upgrade.TARGET_IMAGE, disk_image=disk_image
        )
        if target_created:
            created_version_ids.append(target_version["id"])
        assert target_version["image"] != baseline["hosted"]["spec"]["release"]["image"], (
            "Upgrade target release image must differ from the installed control-plane image"
        )
        cp_snapshot = upgrade._upgrade_control_plane(
            refs=refs,
            baseline=baseline,
            target=target_version,
            jwt_grpc_tenant1=jwt_grpc_tenant1,
            grpc=grpc,
            k8s=k8s_hub_client,
            evidence=evidence,
        )
        upgrade._upgrade_node_pool(
            refs=refs,
            baseline=baseline,
            cp_snapshot=cp_snapshot,
            target=target_version,
            jwt_grpc_tenant1=jwt_grpc_tenant1,
            grpc=grpc,
            k8s=k8s_hub_client,
            evidence=evidence,
            upgraded_node_set=REGULAR_NODE_SET,
            unchanged_node_set=GPU_NODE_SET,
        )
        evidence["outcome"] = "succeeded"
        cli.delete_cluster(uuid=uuid)
        delete_cluster_requested = True
        _assert_cluster_deleted(grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, cluster=cluster)
        uuid = None
    except BaseException:
        evidence["outcome"] = "failed"
        raise
    finally:
        upgrade._cleanup(
            cli=cli,
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s_hub_client,
            uuid=uuid,
            co_name=co_name,
            created_version_ids=created_version_ids,
            evidence=evidence,
            source_version="4.22.0-rhcos",
            delete_cluster_requested=delete_cluster_requested,
        )
