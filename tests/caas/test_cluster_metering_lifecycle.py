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
from tests.core.metering import MeteringCollector, validate_caas_billing
from tests.core.osac_cli import OsacCLI


@pytest.mark.metering
def test_cluster_metering_lifecycle(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify metering events for a full cluster lifecycle.

    Provisions a cluster, waits for READY, then deletes it. Validates:
    - created.v1 event appears
    - started.v1 event appears when cluster becomes billable (PROGRESSING)
    - N+1 heartbeat events (1 control_plane + N worker node sets)
    - deleted.v1 event appears after cluster deletion
    - All events carry correct CaaS billing dimensions
    """
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
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

        metering.expect("osac.resource.started.v1", resource_id=uuid)
        metering.verify()

        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

        # Verify N+1 heartbeat decomposition: at least one control_plane
        # and one worker heartbeat event should appear.
        metering.expect("osac.resource.heartbeat.v1", resource_id=uuid, timeout=180)
        metering.verify()

        heartbeats = metering.get_all_events("osac.resource.heartbeat.v1", resource_id=uuid)
        components = {ev.get("data", {}).get("billing_dimensions", {}).get("component") for ev in heartbeats}
        assert "control_plane" in components, (
            f"Expected control_plane heartbeat, got components: {components}"
        )
        assert "worker" in components, (
            f"Expected worker heartbeat, got components: {components}"
        )

        for hb in heartbeats:
            validate_caas_billing(hb)

        cli.delete_cluster(uuid=uuid)
        metering.expect("osac.resource.deleted.v1", resource_id=uuid)

        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)


@pytest.mark.metering
def test_cluster_metering_event_structure(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
    metering: MeteringCollector,
) -> None:
    """Verify CloudEvent structure and billing dimensions for CaaS events.

    Focuses on validating the created.v1 event carries correct CaaS-specific
    billing dimensions (cluster_template, component, node_set, host_type,
    node_count) and standard CloudEvent fields.
    """
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
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

        metering.expect("osac.resource.started.v1", resource_id=uuid)
        metering.verify()

        created = metering.get_event("osac.resource.created.v1", resource_id=uuid)
        assert created.get("osacresourcetype") == "cluster_order", (
            f"Expected cluster_order, got {created.get('osacresourcetype')}"
        )

        started = metering.get_event("osac.resource.started.v1", resource_id=uuid)
        started_data = started.get("data", {})
        assert started_data.get("resource_type") == "ClusterOrder", (
            f"Expected ClusterOrder resource_type, got {started_data.get('resource_type')}"
        )
        validate_caas_billing(started)

        bd = started_data.get("billing_dimensions", {})
        assert bd.get("cluster_template") == cluster_template, (
            f"cluster_template mismatch: {bd.get('cluster_template')!r} != {cluster_template!r}"
        )

        cli.delete_cluster(uuid=uuid)
        metering.expect("osac.resource.deleted.v1", resource_id=uuid)

        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)
