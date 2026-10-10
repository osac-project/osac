from __future__ import annotations

import subprocess
import uuid

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected, grpc_error_message
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_virtual_network,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression


class TestVirtualNetworkNameValidation:
    """Validates Metadata.name enforcement on VirtualNetwork (tenant-scoped)."""

    def test_create_without_name(self, grpc: GRPCClient) -> None:
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={"object": {"spec": {"ipv4_cidr": "10.100.0.0/16"}}},
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        grpc_msg = grpc_error_message(exc_info.value)
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
        vn_id, cr_name = create_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_name, "10.110.0.0/16")
        try:
            vn: dict = grpc.get_virtual_network(vn_id=vn_id)
            assert vn["object"]["metadata"]["name"] == vn_name
        finally:
            if cr_name:
                delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, cr_name)
