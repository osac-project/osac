from __future__ import annotations

from datetime import UTC, datetime

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_new_vmi,
    wait_for_restart,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, parse_time
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression


@pytest.mark.metering
def test_compute_instance_restart(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    metering: MeteringCollector,
) -> None:
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        name=name, template=vm_template, network_attachments=[default_network_attachment]
    )
    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    try:
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        # The VM was created without a project, so its heartbeats carry no project
        # dimension: the engine emits only the facts a resource carries.
        metering.without_project()
        metering.expect(HEARTBEAT_TYPE, resource_id=uuid, timeout=180)
        metering.verify()

        vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        original_vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(
            vmi_namespace=vmi_ns, compute_instance_name=ci_name
        )
        initial_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)

        restart_ts: str = datetime.now(tz=UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
        grpc.update_restart(uuid=uuid, template=vm_template, timestamp=restart_ts)
        wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=initial_last_restarted, restart_ts=restart_ts)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        new_vmi_ts: str = wait_for_new_vmi(
            k8s=k8s_virt_client, vmi_namespace=vmi_ns, compute_instance_name=ci_name, initial_timestamp=original_vmi_ts
        )
        running_again_at = datetime.now(tz=UTC)

        # The restart puts the VM back in a billable state, so billing resumes after it:
        # a heartbeat must arrive whose usage interval starts at or after the VM was
        # RUNNING again, which heartbeats from before the restart cannot satisfy.
        poll_until(
            fn=lambda: [
                event
                for event in metering.get_all_events(HEARTBEAT_TYPE, uuid)
                if parse_time(event["data"]["usage"]["from"]) >= running_again_at
            ],
            until=lambda events: len(events) > 0,
            retries=30,
            delay=10,
            description=f"heartbeat of {uuid} billed after the restart",
        )

        final_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
        assert final_last_restarted != ""
        assert final_last_restarted != initial_last_restarted
        assert final_last_restarted >= restart_ts

        assert new_vmi_ts > original_vmi_ts

        restart_failed: str = k8s_hub_client.get_jsonpath(
            resource="computeinstance", name=ci_name, jsonpath='{.status.conditions[?(@.type=="RestartFailed")].status}'
        )
        assert restart_failed in ("", "False")
    finally:
        cli.delete_compute_instance(uuid=uuid)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=uuid)
