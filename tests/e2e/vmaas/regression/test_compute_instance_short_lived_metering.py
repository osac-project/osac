from __future__ import annotations

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_deletion, wait_for_grpc_removal
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.regression


@pytest.mark.metering
def test_short_lived_vm_metering(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    metering: MeteringCollector,
) -> None:
    """Verify a VM created and deleted within 30s is never billed (CAP-4).

    The VM is deleted while it is still provisioning, so it never reaches a
    billable state and no heartbeat may arrive for it.
    """
    uuid: str = cli.create_compute_instance(
        name=unique_name("e2e-ci"), template=vm_template, network_attachments=[default_network_attachment]
    )

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    cli.delete_compute_instance(uuid=uuid)

    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    wait_for_grpc_removal(grpc=grpc, uuid=uuid)

    metering.assert_no_events(HEARTBEAT_TYPE, resource_id=uuid, since="2000-01-01T00:00:00+00:00", within=30)
