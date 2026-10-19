from __future__ import annotations

import contextlib
import subprocess

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    assert_grpc_rejected,
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.vmaas.networking_lifecycle_helpers import delete_and_wait_for_security_group, delete_and_wait_for_subnet

pytestmark = pytest.mark.regression


def _attachments_of(instance: dict) -> list:
    spec = instance.get("spec", {})
    return spec.get("networkAttachments") or spec.get("network_attachments") or []


def _same_attachment_payload(subnet_id: str, sg_id: str) -> dict:
    return {"subnet": {"id": subnet_id}, "security_groups": [{"id": sg_id}]}


def test_compute_instance_network_attachment_immutability(
    grpc: GRPCClient,
    cli: OsacCLI,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_networking: dict[str, str],
    default_network_attachment: dict[str, object],
    vm_template: str,
    test_run_id: str,
) -> None:
    """A running VM's resolved network attachment is immutable on the deployed stack.

    Creates a VM with the resolved attachment, waits for the VMI IP feedback, and
    rejects changed SecurityGroup, subnet, and auto-external-access updates through
    the public API with the persisted attachment, ComputeInstance CR spec, and
    provisioning job evidence unchanged after every rejection. A genuine same-value
    no-op retry is accepted without triggering provisioning. A manual
    ExternalIPAttachment completes the sixth resource's lifecycle and blocks
    ExternalIP deletion while referenced. Tenant-scoped rejection and the
    auto-external-ip allocation path are covered at the Fulfillment API boundary by
    the unit suites and by the BMaaS auto-EIP journey; this journey proves the
    composed deployed behavior through public entry points.
    """
    vn_id = default_networking["virtual_network_id"]
    subnet_id = default_networking["subnet_id"]
    sg_id = default_networking["security_group_id"]

    vm_name = f"net-immutable-vm-{test_run_id}"
    vm_id: str | None = None
    vm_cr: str | None = None
    sg2_id: str | None = None
    sg2_cr: str | None = None
    subnet2_id: str | None = None
    subnet2_cr: str | None = None
    pool_id: str | None = None
    pool_cr: str | None = None
    eip_id: str | None = None
    eip_cr: str | None = None
    attach_id: str | None = None
    attach_cr: str | None = None

    try:
        # Targets for the changed-value rejection attempts.
        sg2_id = grpc.create_security_group(name=f"net-immutable-sg-{test_run_id}", virtual_network=vn_id)
        sg2_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg2_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg2_cr)

        subnet2_id = grpc.create_subnet(
            name=f"net-immutable-sub-{test_run_id}", virtual_network=vn_id, ipv4_cidr="10.200.200.0/24"
        )
        subnet2_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet2_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet2_cr)

        # Create the VM with the resolved attachment and wait for IP feedback.
        vm_id = cli.create_compute_instance(
            name=vm_name, template=vm_template, network_attachments=[default_network_attachment]
        )
        vm_cr = wait_for_cr(k8s=k8s_hub_client, uuid=vm_id)
        wait_for_provision(k8s=k8s_hub_client, name=vm_cr)
        wait_for_running(k8s=k8s_hub_client, name=vm_cr)
        vm_namespace = k8s_hub_client.get_compute_instance_vm_namespace(name=vm_cr)
        wait_for_vmi_ip(k8s=k8s_hub_client, vmi_namespace=vm_namespace, compute_instance_name=vm_cr)

        # Snapshot the API, CR, and provider-job evidence before any rejected update.
        instance_before = grpc.get_compute_instance(ci_id=vm_id)["object"]
        attachments_before = _attachments_of(instance_before)
        assert attachments_before, "created VM has no network attachments"
        assert attachments_before[0].get("subnet", {}).get("id") == subnet_id, "created VM missing expected subnet"
        cr_before = k8s_hub_client.get_json(resource="computeinstance", name=vm_cr).get("spec", {})
        job_before = k8s_hub_client.get_compute_instance_latest_job_id(name=vm_cr, job_type="provision", checked=False)

        def assert_unchanged() -> None:
            """The persisted attachment, CR spec, and provider job did not move."""
            instance_after = grpc.get_compute_instance(ci_id=vm_id)["object"]
            # Compare the complete attachment list so appended or otherwise
            # changed attachments are detected, not just the first entry.
            assert _attachments_of(instance_after) == attachments_before, (
                f"network attachments changed: {attachments_before} -> {_attachments_of(instance_after)}"
            )
            assert instance_after.get("spec", {}).get("autoExternalIpAttachment") == instance_before.get(
                "spec", {}
            ).get("autoExternalIpAttachment"), "auto_external_ip_attachment changed"
            cr_after = k8s_hub_client.get_json(resource="computeinstance", name=vm_cr).get("spec", {})
            assert cr_after == cr_before, "ComputeInstance CR spec changed"
            job_after = k8s_hub_client.get_compute_instance_latest_job_id(
                name=vm_cr, job_type="provision", checked=False
            )
            assert job_after == job_before, "a new provisioning job was submitted after the rejected update"

        def attempt_attachment_update(*, attachments: list[dict]) -> subprocess.CalledProcessError:
            """Send a parent-mask attachment update and assert the InvalidArgument rejection."""
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                grpc.call(
                    service=f"{PUBLIC_API}.ComputeInstances/Update",
                    data={
                        "object": {"id": vm_id, "spec": {"network_attachments": attachments}},
                        "updateMask": {"paths": ["spec.network_attachments"]},
                    },
                )
            assert_grpc_rejected(exc_info, "InvalidArgument")
            assert_unchanged()
            return exc_info.value

        # Changed SecurityGroup replacement is rejected; the stored attachment is unchanged.
        attempt_attachment_update(attachments=[_same_attachment_payload(subnet_id, sg2_id)])

        # Changed subnet is rejected; the stored attachment is unchanged.
        attempt_attachment_update(attachments=[{"subnet": {"id": subnet2_id}, "security_groups": [{"id": sg_id}]}])

        # Flipping auto_external_ip_attachment after creation is rejected.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.ComputeInstances/Update",
                data={
                    "object": {"id": vm_id, "spec": {"auto_external_ip_attachment": True}},
                    "updateMask": {"paths": ["spec.auto_external_ip_attachment"]},
                },
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        assert_unchanged()

        # A genuine same-value no-op retry is accepted and triggers no provisioning.
        grpc.call(
            service=f"{PUBLIC_API}.ComputeInstances/Update",
            data={
                "object": {"id": vm_id, "spec": {"network_attachments": [_same_attachment_payload(subnet_id, sg_id)]}},
                "updateMask": {"paths": ["spec.network_attachments"]},
            },
        )
        assert_unchanged()

        # Sixth resource: a manual ExternalIPAttachment bound to the running VM.
        pool_id = private_grpc.create_external_ip_pool(
            name=f"net-immutable-pool-{test_run_id}", cidrs=[str(allocate_worker_subnet(prefix=24))]
        )
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)

        eip_id = grpc.create_external_ip(name=f"net-immutable-eip-{test_run_id}", pool=pool_id)
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)

        attach_id = grpc.create_external_ip_attachment(
            name=f"net-immutable-attach-{test_run_id}", external_ip=eip_id, compute_instance=vm_id
        )
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attach_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)

        # Dependency guard: the ExternalIP cannot be deleted while the attachment references it.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(service=f"{PUBLIC_API}.ExternalIPs/Delete", data={"id": eip_id})
        assert_grpc_rejected(exc_info, "FailedPrecondition")

        # Retry contract: the second public Delete of the attachment returns NotFound.
        grpc.delete_external_ip_attachment(attachment_id=attach_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attach_cr)
        deleted_attach_id = attach_id
        attach_id = None
        attach_cr = None
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(service=f"{PUBLIC_API}.ExternalIPAttachments/Delete", data={"id": deleted_attach_id})
        assert_grpc_rejected(exc_info, "NotFound")

        # The workload-referenced Subnet delete guard (proved at the deployed API tier).
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(service=f"{PUBLIC_API}.Subnets/Delete", data={"id": subnet_id})
        assert_grpc_rejected(exc_info, "FailedPrecondition")
    finally:
        # Best-effort cleanup in reverse dependency order; tolerate removal errors.
        if attach_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                grpc.delete_external_ip_attachment(attachment_id=attach_id)
                if attach_cr is not None:
                    wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attach_cr)
        if vm_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                grpc.delete_compute_instance(ci_id=vm_id)
                if vm_cr is not None:
                    wait_for_deletion(k8s=k8s_hub_client, name=vm_cr)
                wait_for_grpc_removal(grpc=grpc, uuid=vm_id)
        if eip_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                grpc.delete_external_ip(external_ip_id=eip_id)
                if eip_cr is not None:
                    wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
        if pool_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                private_grpc.delete_external_ip_pool(pool_id=pool_id)
                if pool_cr is not None:
                    wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr)
        if subnet2_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet2_id, subnet2_cr)
        if sg2_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                delete_and_wait_for_security_group(grpc, k8s_hub_client, sg2_id, sg2_cr)
