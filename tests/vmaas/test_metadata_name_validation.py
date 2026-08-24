from __future__ import annotations

import re
import subprocess
import uuid

import pytest

from tests.core.grpc_client import PUBLIC_API, GRPCClient
from tests.core.helpers import (
    assert_grpc_rejected,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


def _grpc_error_message(exc: subprocess.CalledProcessError) -> str:
    combined = (exc.stderr or "") + (exc.stdout or "")
    match = re.search(r"Message:\s*(.+)", combined)
    return match.group(1).strip() if match else ""


class TestVirtualNetworkNameValidation:
    """Validates Metadata.name enforcement on VirtualNetwork (tenant-scoped)."""

    def test_create_without_name(self, grpc: GRPCClient) -> None:
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={"object": {"spec": {"ipv4_cidr": "10.100.0.0/16"}}},
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        grpc_msg = _grpc_error_message(exc_info.value)
        assert "metadata is required" in grpc_msg.lower(), "gRPC rejection should reference metadata is required"

    @pytest.mark.parametrize(
        "invalid_name",
        ["Test-VNet", "test_vnet!", "-starts-with-hyphen", "ends-with-hyphen-", "has spaces", "ALLCAPS"],
        ids=["uppercase-mixed", "special-chars", "leading-hyphen", "trailing-hyphen", "spaces", "all-uppercase"],
    )
    def test_create_with_invalid_name(self, grpc: GRPCClient, invalid_name: str) -> None:
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={"object": {"metadata": {"name": invalid_name}, "spec": {"ipv4_cidr": "10.100.0.0/16"}}},
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")

    def test_create_with_valid_dns_name(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        vn_name = f"test-valid-dns-{uuid.uuid4().hex[:8]}"
        vn_id: str = grpc.create_virtual_network(name=vn_name, ipv4_cidr="10.110.0.0/16")
        cr_name: str | None = None
        try:
            cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
            wait_for_virtual_network_ready(k8s=k8s_hub_client, name=cr_name)

            vn: dict = grpc.get_virtual_network(vn_id=vn_id)
            assert vn["object"]["metadata"]["name"] == vn_name
        finally:
            grpc.delete_virtual_network(vn_id=vn_id)
            if cr_name:
                wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=cr_name)
            poll_until(
                fn=lambda: vn_id not in grpc.list_virtual_network_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"VirtualNetwork {vn_id} removal from API",
            )
