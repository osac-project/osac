from __future__ import annotations

import contextlib
import subprocess
from pathlib import Path

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.helpers import (
    assert_cluster_order_deleting_event,
    assert_cluster_order_lifecycle_events,
    wait_for_cluster_deletion,
    wait_for_cluster_deleting,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_cluster_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.sanity


def test_cluster_lifecycle_events(
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
) -> None:
    """Verify ClusterOrder events across the complete create-to-delete lifecycle."""
    name = unique_name("e2e-cluster-events")
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )

    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)
        assert_cluster_order_lifecycle_events(k8s=k8s_hub_client, name=co_name)

        cli.delete_cluster(uuid=uuid)
        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        assert_cluster_order_deleting_event(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
    finally:
        with contextlib.suppress(subprocess.SubprocessError):
            cli.delete_cluster(uuid=uuid)
