from __future__ import annotations

import contextlib
import subprocess
from pathlib import Path

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.helpers import (
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_order_cr,
    wait_for_cluster_order_event_reasons,
    wait_for_cluster_progressing,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.sanity


def test_cluster_provisioning_events(
    cli: OsacCLI, k8s_hub_client: K8sClient, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
) -> None:
    name = unique_name("e2e-cluster-events")
    cluster_order_name = ""
    uuid = cli.create_cluster(
        name=name,
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )

    try:
        cluster_order_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=cluster_order_name)
        expected_messages = {
            "PreparingInfrastructure": "Preparing infrastructure",
            "ControlPlaneStarting": "Control plane starting",
            "WorkersJoining": "Workers joining",
        }
        expected_reasons = {"PreparingInfrastructure", "ControlPlaneStarting"}
        if k8s_hub_client.get_cluster_order_spec(name=cluster_order_name).get("nodeSets"):
            expected_reasons.add("WorkersJoining")

        events = wait_for_cluster_order_event_reasons(
            k8s=k8s_hub_client, name=cluster_order_name, reasons=expected_reasons
        )
        for reason in expected_reasons:
            event = events[reason]
            assert event.get("type") == "Normal", f"Expected Normal event for {reason}: {event}"
            assert event.get("action") == "Provisioning", f"Expected Provisioning action for {reason}: {event}"
            assert expected_messages[reason] in event.get("message", ""), (
                f"Expected stage message for {reason}: {event}"
            )
    finally:
        with contextlib.suppress(subprocess.CalledProcessError):
            cli.delete_cluster(uuid=uuid)
        if cluster_order_name:
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=cluster_order_name)
            wait_for_cluster_deletion(k8s=k8s_hub_client, name=cluster_order_name)
