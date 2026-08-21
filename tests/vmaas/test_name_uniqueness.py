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


def _grpc_error_message(exc: subprocess.CalledProcessError) -> str:
    combined = (exc.stderr or "") + (exc.stdout or "")
    match = re.search(r"Message:\s*(.+)", combined)
    return match.group(1).strip() if match else ""


class TestVirtualNetworkProjectScopedUniqueness:
    """VirtualNetwork names must be unique within a (tenant, project) scope."""

    def test_duplicate_name_rejected(self, jwt_grpc_tenant1: GRPCClient) -> None:
        vn_name = f"dup-vn-{uuid.uuid4().hex[:8]}"
        vn_id: str | None = None
        try:
            vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.120.0.0/16")
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.121.0.0/16")
            assert_grpc_rejected(exc_info, "AlreadyExists")
            msg = _grpc_error_message(exc_info.value)
            assert "virtual network" in msg.lower(), f"Error should mention 'virtual network', got: {msg}"
        finally:
            if vn_id:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)

    def test_duplicate_name_during_deletion_rejected(
        self, jwt_grpc_tenant1: GRPCClient, k8s_hub_client: K8sClient
    ) -> None:
        vn_name = f"del-dup-{uuid.uuid4().hex[:8]}"
        vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.122.0.0/16")
        cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=cr_name)

        jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.123.0.0/16")
        assert_grpc_rejected(exc_info, "AlreadyExists")

        wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=cr_name)

    def test_same_name_different_tenants_succeeds(
        self, jwt_grpc_tenant1: GRPCClient, jwt_grpc_tenant2: GRPCClient
    ) -> None:
        vn_name = f"cross-t-{uuid.uuid4().hex[:8]}"
        vn_id_t1: str | None = None
        vn_id_t2: str | None = None
        try:
            vn_id_t1 = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.124.0.0/16")
            vn_id_t2 = jwt_grpc_tenant2.create_virtual_network(name=vn_name, ipv4_cidr="10.125.0.0/16")
            assert vn_id_t1 != vn_id_t2
        finally:
            if vn_id_t1:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id_t1)
            if vn_id_t2:
                jwt_grpc_tenant2.delete_virtual_network(vn_id=vn_id_t2)

    @pytest.mark.skip(
        reason="Projects feature not yet implemented - projects must exist before resources can reference them"
    )
    def test_same_name_different_projects_succeeds(self, jwt_grpc_tenant1: GRPCClient) -> None:
        vn_name = f"cross-p-{uuid.uuid4().hex[:8]}"
        vn_id_p1: str | None = None
        vn_id_p2: str | None = None
        try:
            vn_id_p1 = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={
                    "object": {
                        "metadata": {"name": vn_name, "project": "project-alpha"},
                        "spec": {"ipv4_cidr": "10.126.0.0/16"},
                    }
                },
            )["object"]["id"]
            vn_id_p2 = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={
                    "object": {
                        "metadata": {"name": vn_name, "project": "project-beta"},
                        "spec": {"ipv4_cidr": "10.127.0.0/16"},
                    }
                },
            )["object"]["id"]
            assert vn_id_p1 != vn_id_p2
        finally:
            if vn_id_p1:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id_p1)
            if vn_id_p2:
                jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id_p2)


class TestRoleGlobalUniqueness:
    """Roles are globally unique: same name in different tenants is rejected."""

    @pytest.mark.skip(reason="JWT tokens lack permission to create Roles - requires admin credentials")
    def test_duplicate_role_name_different_tenants_rejected(
        self, jwt_grpc_tenant1: GRPCClient, jwt_grpc_tenant2: GRPCClient
    ) -> None:
        role_name = f"e2e-role-{uuid.uuid4().hex[:8]}"
        role_id: str | None = None
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.Roles/Create", data={"object": {"metadata": {"name": role_name}}}
            )
            role_id = response["object"]["id"]

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant2.call(
                    service=f"{PUBLIC_API}.Roles/Create", data={"object": {"metadata": {"name": role_name}}}
                )
            assert_grpc_rejected(exc_info, "AlreadyExists")
        finally:
            if role_id:
                jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.Roles/Delete", data={"id": role_id})


class TestUserTenantScopedUniqueness:
    """User names are tenant-scoped: unique within a tenant, allowed across tenants."""

    @pytest.mark.skip(reason="JWT tokens lack permission to create Users - requires admin credentials")
    def test_duplicate_user_name_same_tenant_rejected(self, jwt_grpc_tenant1: GRPCClient) -> None:
        user_name = f"e2e-user-{uuid.uuid4().hex[:8]}"
        user_id: str | None = None
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.Users/Create", data={"object": {"metadata": {"name": user_name}}}
            )
            user_id = response["object"]["id"]

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.Users/Create", data={"object": {"metadata": {"name": user_name}}}
                )
            assert_grpc_rejected(exc_info, "AlreadyExists")
        finally:
            if user_id:
                jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.Users/Delete", data={"id": user_id})

    @pytest.mark.skip(reason="JWT tokens lack permission to create Users - requires admin credentials")
    def test_same_user_name_different_tenants_succeeds(
        self, jwt_grpc_tenant1: GRPCClient, jwt_grpc_tenant2: GRPCClient
    ) -> None:
        user_name = f"e2e-user-{uuid.uuid4().hex[:8]}"
        user_id_t1: str | None = None
        user_id_t2: str | None = None
        try:
            response_t1 = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.Users/Create", data={"object": {"metadata": {"name": user_name}}}
            )
            user_id_t1 = response_t1["object"]["id"]

            response_t2 = jwt_grpc_tenant2.call(
                service=f"{PUBLIC_API}.Users/Create", data={"object": {"metadata": {"name": user_name}}}
            )
            user_id_t2 = response_t2["object"]["id"]

            assert user_id_t1 != user_id_t2
        finally:
            if user_id_t1:
                jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.Users/Delete", data={"id": user_id_t1})
            if user_id_t2:
                jwt_grpc_tenant2.call(service=f"{PUBLIC_API}.Users/Delete", data={"id": user_id_t2})
