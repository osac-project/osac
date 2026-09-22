from __future__ import annotations

import subprocess
import uuid

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_method_unavailable

pytestmark = pytest.mark.regression


class TestVirtualNetworkCrudOnly:
    """Public VirtualNetwork supports create/read/delete, but no Update operation."""

    def test_update_method_is_not_exposed(self, jwt_grpc_tenant1: GRPCClient) -> None:
        vn_name = f"crud-only-{uuid.uuid4().hex[:8]}"
        vn_id: str | None = None
        try:
            vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.130.0.0/16")

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.VirtualNetworks/Update",
                    data={"object": {"id": vn_id, "metadata": {"name": f"renamed-{vn_name}"}}},
                )
            assert_grpc_method_unavailable(
                exc_info, service=f"{PUBLIC_API}.VirtualNetworks", method="Update"
            )
        finally:
            if vn_id:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)
