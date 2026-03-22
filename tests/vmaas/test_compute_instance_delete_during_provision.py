from __future__ import annotations

import time

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cr, wait_for_deletion
from tests.k8s_client import K8sClient
from tests.runner import poll_until

TERMINAL_JOB_STATES: tuple[str, ...] = ("Canceled", "Failed", "Succeeded")


def _get_deprovision_status(k8s: K8sClient, *, name: str) -> str:
    if not k8s.is_present(resource="computeinstance", name=name):
        return "cr_deleted"
    return k8s.get_compute_instance_latest_job_state(name=name, job_type="deprovision", checked=False)


def _wait_for_provision_job(k8s: K8sClient, *, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_latest_job_id(name=name, job_type="provision", checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"provision job for {name}",
    )


def _wait_for_provision_termination(k8s: K8sClient, *, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_latest_job_state(name=name, job_type="provision", checked=False),
        until=lambda v: v in (*TERMINAL_JOB_STATES, ""),
        retries=60,
        delay=1,
        description=f"provision termination for {name}",
    )


def _wait_for_deprovision_completion(k8s: K8sClient, *, name: str) -> str:
    result: str = poll_until(
        fn=lambda: _get_deprovision_status(k8s, name=name),
        until=lambda v: v in (*TERMINAL_JOB_STATES, "cr_deleted"),
        retries=120,
        delay=1,
        description=f"deprovision completion for {name}",
    )
    assert result != "Failed", f"Deprovision job failed for {name}"
    return result


def _verify_no_duplicate_deprovision(k8s: K8sClient, *, name: str) -> None:
    if not k8s.is_present(resource="computeinstance", name=name):
        return
    first_id: str = k8s.get_compute_instance_latest_job_id(name=name, job_type="deprovision", checked=False)
    time.sleep(10)
    second_id: str = k8s.get_compute_instance_latest_job_id(name=name, job_type="deprovision", checked=False)
    assert first_id == second_id, f"Deprovision job ID changed: {first_id} -> {second_id}"


def test_compute_instance_delete_during_provision(
    cli: FulfillmentCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, vm_template: str
) -> None:
    uuid: str = cli.create_compute_instance(template=vm_template)
    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    _wait_for_provision_job(k8s_hub_client, name=ci_name)

    prov_state: str = k8s_hub_client.get_compute_instance_latest_job_state(
        name=ci_name, job_type="provision", checked=False
    )
    deprov_job_id: str = k8s_hub_client.get_compute_instance_latest_job_id(
        name=ci_name, job_type="deprovision", checked=False
    )
    assert prov_state in ("Running", "Pending", "Unknown"), f"Expected provision in progress, got {prov_state}"
    assert deprov_job_id == "", "No deprovision job should exist before deletion"

    cli.delete_compute_instance(uuid=uuid)

    _wait_for_provision_termination(k8s_hub_client, name=ci_name)
    _wait_for_deprovision_completion(k8s_hub_client, name=ci_name)
    _verify_no_duplicate_deprovision(k8s_hub_client, name=ci_name)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    assert uuid not in grpc.list_compute_instance_ids()

    orphan_count: int = k8s_virt_client.count_by_label_all_namespaces(
        resource="virtualmachine", label=f"osac.openshift.io/computeinstance={ci_name}"
    )
    assert orphan_count == 0, f"Found {orphan_count} orphaned VMs for {ci_name}"
