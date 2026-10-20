from __future__ import annotations

import time
from datetime import UTC, datetime

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression


@pytest.mark.metering
def test_compute_instance_stop_metering(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    metering: MeteringCollector,
) -> None:
    """Verify heartbeats arrive while a VM is billable and stop when it is stopped.

    A RUNNING VM bills per heartbeat round; a stopped VM bills nothing, so no
    further heartbeats may arrive for it.
    """
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        name=name, template=vm_template, network_attachments=[default_network_attachment]
    )

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    metering.expect(HEARTBEAT_TYPE, resource_id=uuid, timeout=180)
    metering.verify()

    grpc.update_compute_instance_run_strategy(ci_id=uuid, run_strategy="Halted")

    vm_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    poll_until(
        fn=lambda: k8s_virt_client.get_vm_printable_status(name=ci_name, vm_namespace=vm_ns, checked=False),
        until=lambda v: v == "Stopped",
        retries=30,
        delay=10,
        description=f"{ci_name} VM stopped",
    )

    # Verify heartbeats stop after the VM becomes non-billable. A round that ran
    # before the stop became visible may still send one more heartbeat; wait
    # past a full round interval so in-flight heartbeats drain, then assert
    # silence.
    time.sleep(90)
    no_more_after = datetime.now(UTC).isoformat()
    metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since=no_more_after, within=90)

    cli.delete_compute_instance(uuid=uuid)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
