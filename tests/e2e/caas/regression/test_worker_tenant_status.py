from __future__ import annotations

import contextlib
import subprocess
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_cluster_deletion, wait_for_cluster_order_cr
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until

pytestmark = pytest.mark.regression

TEST_RELEASE_IMAGE = env("OSAC_TEST_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.22.0-multi")
WORKER_PROVISIONING_BLOCKED = "CLUSTER_CONDITION_TYPE_WORKER_PROVISIONING_BLOCKED"
CONDITION_STATUS_TRUE = "CONDITION_STATUS_TRUE"
_CLUSTER_INFRASTRUCTURE_FIELDS = (
    "apiUrl",
    "consoleUrl",
    "apiEndpoint",
    "ingressEndpoint",
    "kubeconfigSecret",
    "passwordSecret",
)


def _assert_no_infrastructure_identifiers(cluster: dict[str, Any], order_status: dict[str, Any]) -> None:
    status = cluster.get("object", {}).get("status", {})
    assert not any(status.get(field) for field in _CLUSTER_INFRASTRUCTURE_FIELDS), (
        "Blocked cluster unexpectedly exposed infrastructure references: "
        f"{[(field, status.get(field)) for field in _CLUSTER_INFRASTRUCTURE_FIELDS if status.get(field)]}"
    )

    worker_resource_ids = [worker.get("resourceID", "") for worker in order_status.get("workers", [])]
    assert not any(worker_resource_ids), f"Blocked ClusterOrder exposed worker resource IDs: {worker_resource_ids}"


def test_cluster_worker_provisioning_blocked_without_disk_image(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
) -> None:
    """A ClusterVersion without a DiskImage blocks workers without creating BMIs."""
    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    version = private_grpc.ensure_cluster_version(
        version=unique_name("4.22.0-e2e-no-disk-image"), image=TEST_RELEASE_IMAGE
    )
    cluster_uuid: str | None = None
    co_name: str | None = None

    try:
        cluster_uuid = cli.create_cluster(
            name=unique_name("e2e-cluster-no-disk-image"),
            template=cluster_template,
            version=version["name"],
            node_sets={"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}},
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
        )
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_uuid)

        blocked_status = poll_until(
            fn=lambda: grpc.get_cluster_condition_status(
                cluster_id=cluster_uuid, condition_type=WORKER_PROVISIONING_BLOCKED
            ),
            until=lambda value: value == CONDITION_STATUS_TRUE,
            retries=60,
            delay=5,
            description=f"{cluster_uuid} WORKER_PROVISIONING_BLOCKED",
        )
        assert blocked_status == CONDITION_STATUS_TRUE

        cluster = grpc.get_cluster(cluster_id=cluster_uuid)
        order_status = k8s_hub_client.get_cluster_order_status(name=co_name)
        _assert_no_infrastructure_identifiers(cluster, order_status)

        bmi_filter = f'this.metadata.labels["osac.openshift.io/cluster-order"] == "{co_name}"'
        worker_bmi_ids = set(private_grpc.list_baremetal_instance_ids(filter_expr=bmi_filter))
        assert not worker_bmi_ids, f"Blocked ClusterOrder unexpectedly created worker BMIs: {sorted(worker_bmi_ids)}"
    finally:
        if cluster_uuid is not None:
            with contextlib.suppress(subprocess.SubprocessError):
                cli.delete_cluster(uuid=cluster_uuid)
        if co_name is not None:
            with contextlib.suppress(TimeoutError, subprocess.SubprocessError):
                wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
