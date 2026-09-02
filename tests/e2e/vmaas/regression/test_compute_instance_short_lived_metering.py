from __future__ import annotations

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_cr, wait_for_deletion, wait_for_grpc_removal
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI


@pytest.mark.metering
def test_short_lived_vm_metering(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    metering: MeteringCollector,
) -> None:
    """Verify metering captures events for a VM created and deleted within 30s (CAP-4).

    A resource existing for 30 seconds must appear in usage data. This validates
    sub-minute billing granularity by creating a VM and immediately deleting it
    without waiting for it to reach Running.
    """
    uuid: str = cli.create_compute_instance(
        name=unique_name("e2e-ci"), template=vm_template, network_attachments=[{"subnet": default_subnet}]
    )
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    cli.delete_compute_instance(uuid=uuid)
    metering.expect("osac.resource.deleted.v1", resource_id=uuid)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
