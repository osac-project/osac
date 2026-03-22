from __future__ import annotations

import time
from datetime import UTC, datetime

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cr, wait_for_deletion, wait_for_restart, wait_for_running
from tests.k8s_client import K8sClient


def test_compute_instance_restart_past_timestamp_ignored(
    cli: FulfillmentCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, vm_template: str
) -> None:
    uuid: str = cli.create_compute_instance(template=vm_template)
    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    # Do a real restart first
    initial_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
    restart_ts: str = datetime.now(tz=UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    grpc.update_restart(uuid=uuid, template=vm_template, timestamp=restart_ts)
    wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=initial_last_restarted, restart_ts=restart_ts)

    # Past timestamp should be ignored
    saved_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
    grpc.update_restart(uuid=uuid, template=vm_template, timestamp="2020-01-01T00:00:00Z")
    time.sleep(15)

    current_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
    assert current_last_restarted == saved_last_restarted, (
        f"Past timestamp should be ignored: was {saved_last_restarted!r}, now {current_last_restarted!r}"
    )

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
