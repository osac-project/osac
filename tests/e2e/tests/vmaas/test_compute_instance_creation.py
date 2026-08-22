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
def test_compute_instance_lifecycle(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    metering: MeteringCollector,
) -> None:
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        name=name, template=vm_template, network_attachments=[{"subnet": default_subnet}]
    )
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    assert uuid in grpc.list_compute_instance_ids()

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    metering.expect("osac.resource.started.v1", resource_id=uuid)
    metering.verify()

    # Verify billing dimensions values match the created resource
    created_event = metering.get_event("osac.resource.created.v1", resource_id=uuid)
    bd = created_event.get("data", {}).get("billing_dimensions", {})
    assert bd.get("instance_type") == cli.default_instance_type, (
        f"billing_dimensions.instance_type mismatch: {bd.get('instance_type')!r} != {cli.default_instance_type!r}"
    )

    # Verify VM exists on virt cluster
    vmi_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    vmi_ts: str = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vmi_ns, compute_instance_name=ci_name)
    assert vmi_ts != "", f"No VMI found on virt cluster for {ci_name}"

    cli.delete_compute_instance(uuid=uuid)
    metering.expect("osac.resource.deleted.v1", resource_id=uuid)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
