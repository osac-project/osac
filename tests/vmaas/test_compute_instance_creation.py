from __future__ import annotations

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cr, wait_for_deletion, wait_for_provision, wait_for_running
from tests.k8s_client import K8sClient


def test_compute_instance_lifecycle(
    cli: FulfillmentCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, vm_template: str
) -> None:
    uuid: str = cli.create_compute_instance(template=vm_template)
    assert uuid in grpc.list_compute_instance_ids()

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    # Verify VM exists on virt cluster
    vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vmi_ns, compute_instance_name=ci_name)
    assert vmi_ts != "", f"No VMI found on virt cluster for {ci_name}"

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    assert uuid not in grpc.list_compute_instance_ids()
