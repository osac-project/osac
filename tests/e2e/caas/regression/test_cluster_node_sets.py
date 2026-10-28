from __future__ import annotations

import os
from typing import Any

import pytest

from tests.e2e.caas.regression import cluster_upgrade as upgrade
from tests.e2e.caas.sanity.test_cluster_create import (
    INSTALL_DISK_IMAGE_NAME,
    INSTALL_VERSION,
    RHCOS_IMAGE,
    TEST_RELEASE_IMAGE,
    WORKER_INSTANCE_TYPE,
    _assert_cluster_deleted,
    _assert_cluster_metering,
    _assert_cluster_version_propagated,
    _assert_worker_bmi_metering,
    assert_cluster_with_two_node_sets_available,
)
from tests.e2e.core.fulfillment_trust import assert_cluster_trust, assert_management_tls
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cluster_order_condition
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.regression

REGULAR_NODE_SET = "compute"
GPU_NODE_SET = "gpu"
INSTANCE_TYPES = {REGULAR_NODE_SET: WORKER_INSTANCE_TYPE, GPU_NODE_SET: "ci-worker-bm-gpu"}


def _assert_two_node_set_worker_metering(
    *, grpc: GRPCClient, metering: MeteringCollector, uuid: str, owned_bmi_ids: set[str]
) -> None:
    """Apply the install heartbeat assertions to each distinct worker set."""
    assert len(owned_bmi_ids) == len(INSTANCE_TYPES), "Expected one owned worker per NodeSet"
    node_set_by_instance_type = {instance_type: node_set for node_set, instance_type in INSTANCE_TYPES.items()}
    assert len(node_set_by_instance_type) == len(INSTANCE_TYPES), (
        "Worker instance types must identify distinct NodeSets"
    )
    checked_node_sets: set[str] = set()
    for bmi_id in sorted(owned_bmi_ids):
        bmi = grpc.get_baremetal_instance(bmi_id=bmi_id)["object"]
        instance_type = bmi["spec"].get("instanceType", bmi["spec"].get("instance_type", {}))["name"]
        assert instance_type in node_set_by_instance_type, f"Unexpected worker instance type {instance_type!r}"
        node_set = node_set_by_instance_type[instance_type]
        assert node_set not in checked_node_sets, f"More than one worker belongs to {node_set!r}"
        _assert_worker_bmi_metering(
            metering=metering, uuid=uuid, bmi_id=bmi_id, node_set=node_set, instance_type=instance_type
        )
        checked_node_sets.add(node_set)
    assert checked_node_sets == set(INSTANCE_TYPES), "No heartbeat assertion for every NodeSet"


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

    disk_image = private_grpc.ensure_disk_image(name=INSTALL_DISK_IMAGE_NAME, source_ref=RHCOS_IMAGE)
    version = private_grpc.ensure_cluster_version(
        version=INSTALL_VERSION, image=TEST_RELEASE_IMAGE, disk_image=disk_image
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
        cluster = assert_cluster_with_two_node_sets_available(
            grpc=grpc, k8s=k8s_hub_client, metering=metering, uuid=uuid, instance_types=instance_types
        )
        co_name = cluster.co_name
        wait_for_cluster_order_condition(k8s=k8s_hub_client, name=co_name, condition_type="ClusterAvailable")
        _assert_cluster_version_propagated(
            k8s=k8s_hub_client,
            co_name=co_name,
            cluster=grpc.get_cluster(cluster_id=uuid),
            cluster_order_spec=k8s_hub_client.get_cluster_order_spec(name=co_name),
            version_name=version["name"],
        )
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client, require_metering=True)
            assert_cluster_trust(k8s_hub_client, cluster.co_name)
        _assert_cluster_metering(metering=metering, uuid=uuid, cluster_template=cluster_template)
        _assert_two_node_set_worker_metering(
            grpc=grpc, metering=metering, uuid=uuid, owned_bmi_ids=cluster.owned_bmi_ids
        )
        evidence["phase"] = "install-completed"

        jwt_grpc_tenant1: GRPCClient = request.getfixturevalue("jwt_grpc_tenant1")
        refs = upgrade._cluster_references(
            k8s=k8s_hub_client, uuid=uuid, co_name=cluster.co_name, expected_node_sets=set(instance_types)
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
            source_version=INSTALL_VERSION,
            delete_cluster_requested=delete_cluster_requested,
        )
