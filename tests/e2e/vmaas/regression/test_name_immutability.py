from __future__ import annotations

import subprocess
import uuid

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected


def _grpc_error_output(exc: subprocess.CalledProcessError) -> str:
    return (exc.stderr or "") + (exc.stdout or "")


class TestVirtualNetworkImmutability:
    """Immutable fields on VirtualNetwork cannot be changed after creation."""

    def test_update_name_rejected(self, jwt_grpc_tenant1: GRPCClient) -> None:
        vn_name = f"imm-name-{uuid.uuid4().hex[:8]}"
        vn_id: str | None = None
        try:
            vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.130.0.0/16")

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.VirtualNetworks/Update",
                    data={
                        "object": {
                            "id": vn_id,
                            "metadata": {"name": f"renamed-{vn_name}"},
                            "spec": {"ipv4_cidr": "10.130.0.0/16"},
                        },
                        "update_mask": {"paths": ["metadata.name"]},
                    },
                )
            assert_grpc_rejected(exc_info, "InvalidArgument")
            output = _grpc_error_output(exc_info.value)
            assert "metadata.name" in output.lower() or "immutable" in output.lower(), (
                f"Error should mention 'metadata.name' or 'immutable', got: {output}"
            )
        finally:
            if vn_id:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)

    @pytest.mark.skip(
        reason=(
            "Cannot test tenant/project immutability - referencing non-existent tenant "
            "fails with PermissionDenied before immutability check"
        )
    )
    @pytest.mark.parametrize(
        ("field_path", "field_key", "field_value"),
        [("metadata.tenant", "tenant", "different-tenant"), ("metadata.project", "project", "different-project")],
        ids=["tenant", "project"],
    )
    def test_update_tenant_or_project_rejected(
        self, jwt_grpc_tenant1: GRPCClient, field_path: str, field_key: str, field_value: str
    ) -> None:
        vn_name = f"imm-tp-{uuid.uuid4().hex[:8]}"
        vn_id: str | None = None
        try:
            vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.131.0.0/16")

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.VirtualNetworks/Update",
                    data={
                        "object": {
                            "id": vn_id,
                            "metadata": {"name": vn_name, field_key: field_value},
                            "spec": {"ipv4_cidr": "10.131.0.0/16"},
                        },
                        "update_mask": {"paths": [field_path]},
                    },
                )
            assert_grpc_rejected(exc_info, "InvalidArgument")
            output = _grpc_error_output(exc_info.value)
            assert "immutable" in output.lower() or field_path in output.lower(), (
                f"Error should mention 'immutable' or '{field_path}', got: {output}"
            )
        finally:
            if vn_id:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)
