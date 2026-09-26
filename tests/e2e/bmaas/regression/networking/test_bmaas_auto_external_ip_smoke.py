"""Lightweight BMaaS auto ExternalIP e2e smoke (OSAC-4984).

Prerequisites:
  - BMaaS enabled with BMH inventory
  - READY ExternalIPPool (or create via suite fixtures)
  - Fabric / NetworkClass configured so ExternalIPAttachment can become Ready
  - Env: OSAC_BMI_AUTO_EIP_CATALOG_ITEM (default ci-bm-auto-eip),
    OSAC_BMI_CATALOG_ITEM, OSAC_BMI_SSH_PUBLIC_KEY, OSAC_BMH_* as for
    tests/e2e/bmaas/regression/networking/

Deep Pending metadata, capacity math, and fail-closed paths are covered by
fulfillment-service/it/it_bmi_auto_external_ip_test.go — not here.
"""

from __future__ import annotations

from typing import Any, ClassVar

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = [pytest.mark.regression, pytest.mark.requires_bmaas]

_BMI_RUNNING_RETRIES = 90


def _require(state: dict[str, Any], *keys: str) -> None:
    missing = [k for k in keys if k not in state]
    if missing:
        pytest.skip(f"Missing prerequisite state: {', '.join(missing)}")


@pytest.mark.usefixtures("bmi_disk_image")
class TestBmaasAutoExternalIPSmoke:
    """Create BMI with auto ExternalIP → Ready → delete GC."""

    state: ClassVar[dict[str, Any]] = {}

    def test_00_create_bmi_with_auto_eip(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        auto_eip_catalog_item_name: str,
        bmi_disk_image: str,
        net_ssh_public_key: str,
        net_test_run_id: str,
    ) -> None:
        bmi_name = f"autoeip-smoke-{net_test_run_id}"
        bmi_id = cli.create_baremetal_instance(
            name=bmi_name,
            catalog_item=auto_eip_catalog_item_name,
            ssh_key=net_ssh_public_key,
            disk_image=bmi_disk_image,
            external_ip_attachment=True,
        )
        cr_name = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id, retries=_BMI_RUNNING_RETRIES)
        self.__class__.state.update(bmi_id=bmi_id, bmi_name=bmi_name, bmi_cr=cr_name)
        print(f"Created BMI {bmi_name}: {bmi_id}")

    def test_10_auto_attachment_ready(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        _require(self.state, "bmi_id")
        bmi_id = self.state["bmi_id"]

        def find_auto_attachment() -> dict[str, Any] | None:
            attachments = grpc.call(service="osac.public.v1.ExternalIPAttachments/List")
            for item in attachments.get("items", []):
                if item.get("spec", {}).get("baremetalInstance", {}).get("id") == bmi_id:
                    return item
            return None

        attachment = poll_until(
            fn=find_auto_attachment,
            until=lambda a: a is not None,
            retries=60,
            delay=5,
            description="auto ExternalIPAttachment for BMI",
        )
        assert attachment is not None
        attach_id = attachment["id"]
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attach_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)

        eip_id = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
        assert eip_id, "Auto attachment missing ExternalIP reference"
        eip = grpc.get_external_ip(external_ip_id=eip_id)
        address = eip.get("object", {}).get("status", {}).get("address", "")
        assert address, "Auto ExternalIP should have an allocated address when Ready"

        self.__class__.state.update(auto_attach_id=attach_id, auto_eip_id=eip_id, auto_ext_addr=address)
        print(f"Auto EIP Ready: {address} (attachment={attach_id})")

    def test_20_delete_bmi_garbage_collects_auto_eip(
        self, cli: OsacCLI, grpc: GRPCClient, k8s_hub_client: K8sClient
    ) -> None:
        _require(self.state, "bmi_id", "bmi_cr", "auto_attach_id", "auto_eip_id")
        bmi_id = self.state["bmi_id"]
        cli.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=self.state["bmi_cr"])
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)

        poll_until(
            fn=lambda: self.state["auto_attach_id"] not in grpc.list_external_ip_attachment_ids(),
            until=lambda gone: gone is True,
            retries=30,
            delay=5,
            description="auto ExternalIPAttachment garbage collection",
        )
        poll_until(
            fn=lambda: self.state["auto_eip_id"] not in grpc.list_external_ip_ids(),
            until=lambda gone: gone is True,
            retries=30,
            delay=5,
            description="auto ExternalIP garbage collection",
        )
        print("Auto EIP and attachment garbage collected after BMI delete")
