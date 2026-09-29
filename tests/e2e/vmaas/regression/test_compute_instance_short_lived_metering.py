from __future__ import annotations

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.regression


@pytest.mark.metering
def test_short_lived_vm_metering(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    metering: MeteringCollector,
) -> None:
    """Verify metering captures created, started, and deleted events across the full lifecycle.

    Waits for the ComputeInstance to reach Running before deleting so the
    controller can emit all expected metering events (created, started,
    deleted).  The quick-delete edge case (deleting before Running) is
    tracked separately.
    """
    uuid: str = cli.create_compute_instance(
        name=unique_name("e2e-ci"), template=vm_template, network_attachments=[{"subnet": default_subnet}]
    )
    metering.expect("osac.resource.created.v1", resource_id=uuid)

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    metering.expect("osac.resource.started.v1", resource_id=uuid)

    cli.delete_compute_instance(uuid=uuid)
    metering.expect("osac.resource.deleted.v1", resource_id=uuid, timeout=180)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)
