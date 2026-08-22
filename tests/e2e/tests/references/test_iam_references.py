from __future__ import annotations

import contextlib
import logging
import subprocess
from typing import Any
from uuid import uuid4

import pytest

from tests.core.grpc_client import PUBLIC_API, GRPCClient
from tests.core.helpers import assert_grpc_field_violation
from tests.core.osac_cli import OsacCLI

logger = logging.getLogger(__name__)

TENANT_ADMIN_USER = "tenant1_admin"
TENANT_USER = "tenant1_user"
TENANT_ADMIN_ROLE = "tenant-admin"


@pytest.fixture(scope="module", autouse=True)
def _register_test_users(jwt_grpc_tenant1: GRPCClient, jwt_cli_admin: OsacCLI) -> None:
    """Trigger user auto-registration by making authenticated API calls."""
    with contextlib.suppress(subprocess.CalledProcessError):
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.RoleBindings/List")
    jwt_cli_admin.get_unchecked("rolebinding")


def _skip_if_users_not_found(exc: subprocess.CalledProcessError) -> None:
    stderr = exc.stderr or ""
    if "not found" in stderr.lower():
        pytest.skip("Test users not registered in OSAC; Keycloak user sync may be pending")


class TestIAMReferences:
    """OSAC-3114: IAM resource reference tests."""

    def test_role_binding_with_role_and_users_by_name(self, grpc: GRPCClient):
        tag = uuid4().hex[:8]
        rb_name = f"ref-rb-{tag}"

        try:
            rb_id = grpc.create_role_binding(name=rb_name, role_name=TENANT_ADMIN_ROLE, user_names=[TENANT_ADMIN_USER])
        except subprocess.CalledProcessError as exc:
            _skip_if_users_not_found(exc)
            raise
        try:
            response = grpc.get_role_binding(role_binding_id=rb_id)
            spec = response["object"]["spec"]

            role_ref = spec["role"]
            assert role_ref.get("name") == TENANT_ADMIN_ROLE
            assert role_ref.get("id"), "role.id should be auto-populated"

            users = spec["users"]
            assert len(users) >= 1
            user_ref = users[0]
            assert user_ref.get("name") == TENANT_ADMIN_USER
            assert user_ref.get("id"), "user.id should be auto-populated"
        finally:
            try:
                grpc.delete_role_binding(role_binding_id=rb_id)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup role binding %s", rb_id)

    def test_project_membership_by_name(self, grpc: GRPCClient):
        tag = uuid4().hex[:8]
        pm_name = f"ref-pm-{tag}"

        try:
            pm_id = grpc.create_project_membership(name=pm_name, user_names=[TENANT_USER])
        except subprocess.CalledProcessError as exc:
            _skip_if_users_not_found(exc)
            raise
        try:
            response: dict[str, Any] = grpc.call(service=f"{PUBLIC_API}.ProjectMemberships/Get", data={"id": pm_id})
            spec = response["object"]["spec"]

            users = spec["users"]
            assert len(users) >= 1
            user_ref = users[0]
            assert user_ref.get("name") == TENANT_USER
            assert user_ref.get("id"), "user.id should be auto-populated"
        finally:
            try:
                grpc.delete_project_membership(membership_id=pm_id)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup project membership %s", pm_id)

    def test_invalid_role_name_returns_error(self, grpc: GRPCClient):
        tag = uuid4().hex[:8]
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_role_binding(
                name=f"ref-bad-rb-{tag}", role_name="nonexistent-role", user_names=[TENANT_ADMIN_USER]
            )
        assert_grpc_field_violation(exc_info, field_path="role")

    def test_role_binding_with_multiple_users_by_name(self, grpc: GRPCClient):
        tag = uuid4().hex[:8]
        rb_name = f"ref-rb-multi-{tag}"

        try:
            rb_id = grpc.create_role_binding(
                name=rb_name, role_name=TENANT_ADMIN_ROLE, user_names=[TENANT_ADMIN_USER, TENANT_USER]
            )
        except subprocess.CalledProcessError as exc:
            _skip_if_users_not_found(exc)
            raise
        try:
            response = grpc.get_role_binding(role_binding_id=rb_id)
            users = response["object"]["spec"]["users"]
            resolved_names = {u.get("name") for u in users}
            assert TENANT_ADMIN_USER in resolved_names
            assert TENANT_USER in resolved_names
            for u in users:
                assert u.get("id"), f"user.id should be auto-populated for {u.get('name')}"
        finally:
            try:
                grpc.delete_role_binding(role_binding_id=rb_id)
            except subprocess.CalledProcessError:
                logger.warning("Failed to cleanup role binding %s", rb_id)
