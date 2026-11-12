from __future__ import annotations

import contextlib
import os

import pytest

from tests.e2e.core.fulfillment_trust import assert_management_tls
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    assert_no_network_attachment,
    assert_no_network_manager_jobs,
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
from tests.e2e.core.wait_kinds import WaitResourceKind

pytestmark = pytest.mark.regression


@pytest.mark.metering
def test_compute_instance_lifecycle(
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
    ci_name: str | None = None

    def cleanup() -> None:
        cli.delete_compute_instance(uuid=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=uuid)

    try:
        assert uuid in grpc.list_compute_instance_ids()

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client)

        # The VM was created without a project, so its heartbeats must carry no
        # project dimension: the engine emits only the facts a resource carries.
        metering.expect_no_project()
        metering.expect(HEARTBEAT_TYPE, resource_id=uuid, timeout=180, resource_kind=WaitResourceKind.COMPUTE_INSTANCE)
        metering.verify()

        # Verify billing dimensions match the running resource: the heartbeat's
        # dimensions name the instance type actually running. The first cached
        # heartbeat can predate the running-type fact and stay cached after its
        # reversal and the typed replacement arrive, so wait for the heartbeat
        # that carries the type instead of inspecting the cached one.
        heartbeat_event = metering.heartbeat_with_dimensions(
            uuid,
            {"instance_type": cli.default_instance_type},
            timeout=180,
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        )
        bd = heartbeat_event.get("data", {}).get("billing_dimensions", {})
        assert bd.get("instance_type") == cli.default_instance_type, (
            f"billing_dimensions.instance_type mismatch: {bd.get('instance_type')!r} != {cli.default_instance_type!r}"
        )

        # Verify VM exists on virt cluster
        vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vmi_ns, compute_instance_name=ci_name)
        assert vmi_ts != "", f"No VMI found on virt cluster for {ci_name}"
    except BaseException:
        with contextlib.suppress(Exception):
            cleanup()
        raise
    else:
        cleanup()


def test_compute_instance_lifecycle_without_provider_networking(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    provider_networking_disabled: None,
) -> None:
    """A VM runs on the disabled fallback, which claims no provider work.

    The workload keeps its normal lifecycle and readiness gates; NetworkReady is
    True with reason ProvisioningDisabled, the operator records no networking job,
    and the workload service creates no NetworkAttachment.
    """
    name = unique_name("e2e-ci-disabled")
    uuid: str = cli.create_compute_instance(
        name=name, template=vm_template, network_attachments=[default_network_attachment]
    )
    ci_name: str | None = None

    def cleanup() -> None:
        cli.delete_compute_instance(uuid=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=uuid)

    try:
        assert uuid in grpc.list_compute_instance_ids()

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        network_ready = k8s_hub_client.get_condition(
            resource="computeinstance", name=ci_name, condition_type="NetworkReady"
        )
        assert network_ready.get("status") == "True", (
            f"NetworkReady must be True on the disabled fallback, got {network_ready}"
        )
        assert network_ready.get("reason") == "ProvisioningDisabled", (
            f"NetworkReady reason {network_ready.get('reason')!r}, expected 'ProvisioningDisabled': {network_ready}"
        )
        assert_no_network_manager_jobs(
            k8s=k8s_hub_client, resource="computeinstance", name=ci_name, context=f"ComputeInstance {ci_name}"
        )
        compute_status: dict[str, object] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name).get(
            "status", {}
        )
        assert not compute_status.get("computeNetworkAttachmentStatuses"), (
            f"provider networking is disabled, but {ci_name} reports attachment statuses "
            f"{compute_status.get('computeNetworkAttachmentStatuses')}"
        )
        assert_no_network_attachment(k8s=k8s_hub_client, target_kind="ComputeInstance", target_id=uuid)
    except BaseException:
        with contextlib.suppress(Exception):
            cleanup()
        raise
    else:
        cleanup()
