from __future__ import annotations

from datetime import UTC, datetime

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cr, wait_for_deletion, wait_for_restart, wait_for_running
from tests.k8s_client import K8sClient
from tests.runner import poll_until


def _wait_for_new_vmi(k8s_virt: K8sClient, *, vmi_namespace: str, ci_name: str, original_ts: str) -> str:
    poll_until(
        fn=lambda: k8s_virt.get_vmi_creation_timestamp(vmi_namespace=vmi_namespace, compute_instance_name=ci_name),
        until=lambda v: v != original_ts,
        retries=30,
        delay=10,
        description=f"new VMI for {ci_name}",
    )
    return k8s_virt.get_vmi_creation_timestamp(vmi_namespace=vmi_namespace, compute_instance_name=ci_name)


def test_compute_instance_restart(
    cli: FulfillmentCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, vm_template: str
) -> None:
    uuid: str = cli.create_compute_instance(template=vm_template)
    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    original_vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(
        vmi_namespace=vmi_ns, compute_instance_name=ci_name
    )
    initial_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)

    restart_ts: str = datetime.now(tz=UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    grpc.update_restart(uuid=uuid, template=vm_template, timestamp=restart_ts)

    wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=initial_last_restarted, restart_ts=restart_ts)

    final_last_restarted: str = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
    assert final_last_restarted != ""
    assert final_last_restarted != initial_last_restarted
    assert final_last_restarted >= restart_ts

    new_vmi_ts: str = _wait_for_new_vmi(
        k8s_virt_client, vmi_namespace=vmi_ns, ci_name=ci_name, original_ts=original_vmi_ts
    )
    assert new_vmi_ts > original_vmi_ts

    restart_failed: str = k8s_hub_client.get_jsonpath(
        resource="computeinstance", name=ci_name, jsonpath='{.status.conditions[?(@.type=="RestartFailed")].status}'
    )
    assert restart_failed in ("", "False")

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
