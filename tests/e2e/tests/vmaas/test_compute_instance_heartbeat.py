from __future__ import annotations

import pytest

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.metering import MeteringCollector
from tests.core.osac_cli import OsacCLI


@pytest.mark.metering
def test_compute_instance_heartbeat(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    metering: MeteringCollector,
) -> None:
    """Verify heartbeat events appear for a billable RUNNING VM.

    The metering system emits periodic heartbeat events (default interval 60s)
    for every VM in the RUNNING state. This test creates a VM, waits for it to
    reach RUNNING, then verifies that at least one heartbeat event is emitted
    within 180s.
    """
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        name=name, template=vm_template, network_attachments=[{"subnet": default_subnet}]
    )

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    metering.expect("osac.resource.heartbeat.v1", resource_id=uuid, timeout=180)
    metering.verify()  # verify heartbeat arrived BEFORE deleting

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
