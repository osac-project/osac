from __future__ import annotations

import contextlib
import subprocess
import time
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion_with_deadline,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.sanity


def test_cluster_delete_reports_deleting_state_without_provisioning(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
) -> None:
    """Verify that cluster deletion transitions through DELETING state
    without waiting for full provisioning. Runs on kind without HyperShift
    (OSAC-1586)."""
    name = unique_name("e2e-cluster")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )

    # Single deletion deadline shared between normal wait and cleanup.
    deletion_deadline = time.monotonic() + 1200
    co_name: str | None = None

    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        assert uuid in grpc.list_cluster_ids()

        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

        cli.delete_cluster(uuid=uuid)

        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
        wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)
        if co_name:
            try:
                wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
            except Exception as cleanup_err:
                print(f"WARNING: Cluster cleanup wait failed for {co_name}: {cleanup_err}")
