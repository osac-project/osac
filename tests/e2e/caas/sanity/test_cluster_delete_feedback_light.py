from __future__ import annotations

import contextlib
import logging
import subprocess
from datetime import UTC, datetime

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.sanity

logger = logging.getLogger(__name__)


@pytest.mark.metering
def test_cluster_delete_reports_deleting_state_without_provisioning(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    caas_disk_image_version: str,
    pull_secret_name: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify that cluster deletion transitions through DELETING state
    without waiting for full provisioning (OSAC-1586).

    The cluster never reaches READY, so the gate its workers bill under stays
    shut: no control plane time and no worker hardware bills, although the
    workers' own meters would bill a standalone host from STARTING. That is the
    no-double-billing guarantee for owned hardware, verified end to end.
    """
    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    node_sets = {"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}}
    name = unique_name("e2e-cluster")
    # Nothing may bill at all, so the silence window is anchored at the test
    # start: any heartbeat for the cluster or a worker, whenever it arrives,
    # violates the gate.
    started_at = datetime.now(UTC).isoformat()
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        version=caas_disk_image_version,
        node_sets=node_sets,
        pull_secret=pull_secret_name,
        ssh_public_key_file=ssh_public_key_path,
    )

    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        assert uuid in grpc.list_cluster_ids()

        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

        # Wait for a worker to be allocated while the cluster installs: the
        # no-billing assertion below is about hardware that exists and would
        # bill if the gate were broken.
        bmi_filter = f'this.metadata.labels["osac.openshift.io/cluster-order"] == "{co_name}"'
        worker_bmi_ids = poll_until(
            fn=lambda: grpc.list_baremetal_instance_ids(filter_expr=bmi_filter),
            until=lambda ids: bool(ids),
            retries=60,
            delay=5,
            description=f"{co_name} worker BareMetalInstances",
        )
        logger.info("CaaS: %d worker BMI(s) allocated before the delete: %s", len(worker_bmi_ids), worker_bmi_ids)

        cli.delete_cluster(uuid=uuid)

        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)

        # The gate stayed shut: no control plane time and no worker hardware
        # bills, although the workers' own meters would bill a standalone host
        # from STARTING. That is the no-double-billing guarantee for owned
        # hardware, verified end to end. The window is polled, not slept
        # through, so a heartbeat delivered after a single fetch cannot slip
        # through.
        metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since=started_at, within=120)
        for bmi_id in worker_bmi_ids:
            metering.assert_no_events(HEARTBEAT_TYPE, resource_id=bmi_id, since=started_at, within=120)
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)
