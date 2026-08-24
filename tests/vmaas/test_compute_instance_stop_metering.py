from __future__ import annotations

import time
from datetime import UTC, datetime

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
from tests.core.runner import poll_until


@pytest.mark.metering
def test_compute_instance_stop_metering(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    metering: MeteringCollector,
) -> None:
    """Verify metering captures suspended.v1 when a running VM is stopped.

    Also verifies that heartbeats stop after the VM becomes non-billable.
    """
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        name=name,
        template=vm_template,
        network_attachments=[{"subnet": default_subnet}],
    )

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    metering.expect("osac.resource.created.v1", resource_id=uuid)
    metering.expect("osac.resource.started.v1", resource_id=uuid)
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

    metering.expect("osac.resource.suspended.v1", resource_id=uuid, timeout=120)
    metering.verify()

    # The VM was explicitly stopped from RUNNING — verify the suspended event
    # closed the billing interval with a positive duration.
    suspended_event = metering.get_event("osac.resource.suspended.v1", resource_id=uuid)
    suspended_data = suspended_event.get("data", {})
    assert suspended_data.get("previous_state") == "RUNNING", (
        f"Stop from RUNNING should have previous_state=RUNNING, got {suspended_data.get('previous_state')!r}"
    )
    ds = suspended_data.get("duration_seconds")
    assert isinstance(ds, (int, float)) and ds > 0, (
        f"Stop from RUNNING should have positive duration_seconds, got {ds}"
    )

    # Verify heartbeats stop after VM becomes non-billable.
    # The heartbeat generator queries the projection DB, which is updated
    # after the Kafka publish (publishAndUpsert). One more heartbeat sweep
    # may fire against the stale projection. Wait past one full heartbeat
    # interval so in-flight heartbeats drain, then assert silence.
    time.sleep(90)
    no_more_after = datetime.now(UTC).isoformat()
    metering.assert_no_events(
        "osac.resource.heartbeat.v1",
        resource_id=uuid,
        since=no_more_after,
        within=90,
    )

    cli.delete_compute_instance(uuid=uuid)
    metering.expect("osac.resource.deleted.v1", resource_id=uuid)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
