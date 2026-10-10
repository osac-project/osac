from __future__ import annotations

import logging
import subprocess
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient

pytestmark = pytest.mark.sanity

logger = logging.getLogger(__name__)


class TestSelfSubjectAccessReviewPermissions:
    """Verify permission check results match actual operation authorization."""

    def test_tenant_admin_can_check_permitted_tenant_operation(
        self, jwt_grpc_tenant1_admin: GRPCClient, private_grpc: GRPCClient
    ):
        """AC1: Tenant Admin can check a permitted tenant-scoped management operation."""
        # Setup: ensure tenant exists
        tenant = "tenant1"

        # Check permission to create VirtualNetwork in own tenant
        response = jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": tenant, "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.VirtualNetworks", "method": "Create"},
                }
            },
        )

        # Verify: tenant admin is allowed
        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed is True, (
            f"Tenant Admin check failed: role=tenant_admin, service=VirtualNetworks, "
            f"method=Create, tenant={tenant}, expected=allowed"
        )

        # Verify actual operation succeeds
        tag = uuid4().hex[:8]
        vn_name = f"e2e-ssar-vn-{tag}"
        vn_id = None
        try:
            vn_id = jwt_grpc_tenant1_admin.create_virtual_network(name=vn_name, ipv4_cidr="10.100.0.0/16")
            assert vn_id, "VirtualNetwork creation should succeed when permission check returned allowed=true"
        finally:
            # Cleanup
            if vn_id:
                try:
                    jwt_grpc_tenant1_admin.delete_virtual_network(vn_id=vn_id)
                except subprocess.CalledProcessError:
                    pass  # Cleanup failure is non-fatal

    def test_tenant_user_denied_for_higher_role_operation(self, jwt_grpc_tenant1: GRPCClient):
        """AC1: Client (tenant user) is denied operations reserved for higher roles."""
        # Check permission to delete User (tenant-admin-only operation)
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.Users", "method": "Delete"},
                }
            },
        )

        # Verify: tenant user (non-admin) is denied
        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed is False, (
            f"Client check failed: role=tenant_user, service=Users, method=Delete, "
            f"tenant=tenant1, expected=denied (only tenant_admin can manage users)"
        )

    def test_tenant_user_checking_infrastructure_operation(self, jwt_grpc_tenant1: GRPCClient):
        """AC2: Tenant user checks an infrastructure operation (List method on own tenant resources)."""
        # Check permission to list ComputeInstances in own tenant
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.ComputeInstances", "method": "List"},
                }
            },
        )

        # Verify: tenant user can list resources in their own tenant
        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed is True, (
            f"Tenant user check failed: role=tenant_user, service=ComputeInstances, "
            f"method=List, tenant=tenant1, expected=allowed"
        )

        # Verify actual operation succeeds
        instances = jwt_grpc_tenant1.list_compute_instance_ids()
        # No assertion on content - just verify the call doesn't raise PermissionDenied
        assert isinstance(instances, list), "List should succeed when permission check returned allowed=true"

    def test_tenant_user_denied_for_other_tenant(self, jwt_grpc_tenant1: GRPCClient):
        """AC2: Caller outside a tenant is denied operations on that tenant."""
        # tenant1_user checks permission to create VirtualNetwork in tenant2
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "tenant2", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.VirtualNetworks", "method": "Create"},
                }
            },
        )

        # Verify: user from tenant1 cannot operate on tenant2 resources
        assert "object" in response, f"Expected response.object, got: {response}"
        assert "status" in response["object"], f"Expected response.object.status, got: {response['object']}"
        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed is False, (
            f"Cross-tenant check failed: role=tenant_user, service=VirtualNetworks, "
            f"method=Create, tenant=tenant2, expected=denied (user belongs to tenant1)"
        )

        # Verify denial reason does NOT disclose tenant2 name (information disclosure prevention)
        reason = status.get("reason", "")
        assert "tenant2" not in reason.lower(), (
            f"Denial reason leaked cross-tenant information: {reason}. "
            f"Reason should not reveal tenant names from other tenants."
        )

    def test_unauthenticated_request_rejected(self, fulfillment_address: str):
        """AC2: Unauthenticated access is rejected."""
        # Create a GRPCClient with no token
        anon_client = GRPCClient(address=fulfillment_address, token="")

        # Attempt to check permission without authentication
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            anon_client.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "osac.public.v1.Clusters", "method": "Create"},
                    }
                },
            )

        # Verify: returns Unauthenticated error
        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "Unauthenticated" in output or "unauthenticated" in output, (
            f"Unauthenticated request should fail with Unauthenticated error. Got: {output}"
        )



class TestSelfSubjectAccessReviewValidation:
    """Verify service and method validation against public API contract."""

    def test_unknown_service_returns_invalid_argument(self, jwt_grpc_tenant1_admin: GRPCClient):
        """AC3: Unknown services return InvalidArgument."""
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "osac.public.v1.NonExistentService", "method": "Create"},
                    }
                },
            )

        # Verify: returns InvalidArgument with unknown service message
        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "InvalidArgument" in output or "invalid" in output.lower(), (
            f"Unknown service should return InvalidArgument error. Got: {output}"
        )
        assert "unknown service" in output.lower() or "NonExistentService" in output, (
            f"Error should mention unknown service. Got: {output}"
        )

    def test_unsupported_method_returns_invalid_argument(self, jwt_grpc_tenant1_admin: GRPCClient):
        """AC3: Unsupported methods for a service return InvalidArgument."""
        # ExternalIPPools is read-only in the public API - Create is not supported
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "osac.public.v1.ExternalIPPools", "method": "Create"},
                    }
                },
            )

        # Verify: returns InvalidArgument with unsupported method message
        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "InvalidArgument" in output or "invalid" in output.lower(), (
            f"Unsupported method should return InvalidArgument error. Got: {output}"
        )
        assert "not supported" in output.lower() or "method" in output.lower(), (
            f"Error should mention method not supported. Got: {output}"
        )

    def test_private_service_rejected(self, jwt_grpc_tenant1_admin: GRPCClient):
        """Verify that private API services are rejected (only osac.public.v1.* supported)."""
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "osac.private.v1.Tenants", "method": "Create"},
                    }
                },
            )

        # Verify: validation fails (pattern constraint requires osac.public.v1.*)
        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "InvalidArgument" in output or "invalid" in output.lower(), (
            f"Private service should be rejected by validation. Got: {output}"
        )


class TestSelfSubjectAccessReviewMultipleServices:
    """Verify permission checks work across different OSAC services."""

    @pytest.mark.parametrize(
        "service,method,expected_allowed",
        [
            ("osac.public.v1.Clusters", "List", True),  # Tenant admin can list
            ("osac.public.v1.VirtualNetworks", "Create", True),  # Tenant admin can create networking
            ("osac.public.v1.Subnets", "Delete", True),  # Tenant admin can delete networking
            ("osac.public.v1.SecurityGroups", "Delete", True),  # Tenant admin can delete networking
            ("osac.public.v1.ComputeInstances", "Update", True),  # Tenant admin can update instances
            ("osac.public.v1.ExternalIPPools", "List", True),  # Anyone can list (read-only resource)
            ("osac.public.v1.BareMetalInstances", "Update", True),  # Tenant admin can update BMI
        ],
    )
    def test_service_method_combinations(
        self, jwt_grpc_tenant1_admin: GRPCClient, service: str, method: str, expected_allowed: bool
    ):
        """Verify permission checks across multiple public service/method combinations."""
        response = jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": service, "method": method},
                }
            },
        )

        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed == expected_allowed, (
            f"Permission check failed: role=tenant_admin, service={service}, "
            f"method={method}, tenant=tenant1, expected={'allowed' if expected_allowed else 'denied'}, "
            f"got={'allowed' if allowed else 'denied'}"
        )


class TestSelfSubjectAccessReviewAdvisoryNature:
    """Verify that permission checks are advisory and re-evaluated on actual operations."""

    def test_permission_check_is_advisory_snapshot(self, jwt_grpc_tenant1_admin: GRPCClient):
        """AC4: Verify advisory behavior - permission checks are snapshots, not guarantees.

        NOTE: This test verifies the advisory contract by checking permissions and then
        performing the actual operation. The permission may change between check and operation
        (e.g., if an admin revokes roles via Keycloak), demonstrating that checks are advisory.

        Full permission revocation testing requires Keycloak admin access and is documented
        for QE manual testing in fulfillment-service/it/SELF_SUBJECT_ACCESS_REVIEW_TESTS.md.
        """
        tenant = "tenant1"

        # Step 1: Check permission to list VirtualNetworks
        check_response = jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": tenant, "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.VirtualNetworks", "method": "List"},
                }
            },
        )

        # Step 2: Verify permission check returns allowed
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        status = check_response["object"]["status"]
        allowed = status.get("allowed", False)
        assert allowed is True, (
            "Permission check should return allowed=true for tenant admin listing VirtualNetworks"
        )

        # Step 3: Perform actual operation - authorization is re-evaluated
        # If roles were revoked between check and operation, this would fail
        vn_ids = jwt_grpc_tenant1_admin.list_virtual_network_ids()
        assert isinstance(vn_ids, list), (
            "Actual operation should succeed. Note: if authorization changed between check and "
            "operation (e.g., role revoked), operation would fail - demonstrating advisory nature."
        )

        # Advisory contract: permission check result was valid at time of check,
        # but actual operations independently re-evaluate authorization


class TestSelfSubjectAccessReviewInformationDisclosure:
    """Verify that denial reasons do not leak cross-tenant information."""

    def test_denial_reason_does_not_disclose_tenant_names(self, jwt_grpc_tenant1: GRPCClient):
        """AC2/Security: Verify denial reasons do not reveal tenant names from other tenants."""
        # tenant1_user checks permission on tenant2 (cross-tenant access)
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "tenant2", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.Subnets", "method": "Create"},
                }
            },
        )

        # Verify: permission denied
        status = response["object"]["status"]
        # Proto3 JSON omits default values - when allowed is false (default), it may be omitted
        allowed = status.get("allowed", False)
        assert allowed is False, "Cross-tenant operation should be denied"

        # Verify: denial reason does NOT contain tenant names
        reason = status.get("reason", "")
        # SAFE reasons: empty string, "insufficient permissions", generic denial
        # UNSAFE reasons: "user is not a member of tenant tenant2", "tenant tenant2 does not exist"
        assert "tenant2" not in reason.lower(), (
            f"Denial reason leaked tenant name 'tenant2': {reason}. "
            f"Safe reasons are empty or generic (e.g., 'insufficient permissions')."
        )
        assert "tenant1" not in reason.lower(), (
            f"Denial reason leaked tenant name 'tenant1': {reason}. "
            f"Safe reasons should not reveal any tenant names."
        )

    def test_actual_operation_error_does_not_disclose_tenant_names(self, jwt_grpc_tenant1: GRPCClient):
        """Verify actual operation errors also do not reveal cross-tenant information."""
        # Attempt to create VirtualNetwork in tenant2 as tenant1_user
        tag = uuid4().hex[:8]
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.VirtualNetworks/Create",
                data={
                    "object": {
                        "metadata": {"name": f"cross-tenant-{tag}", "tenant": "tenant2"},
                        "spec": {"ipv4_cidr": "10.200.0.0/16"},
                    }
                },
            )

        # Verify: error message does not reveal tenant2 name
        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "tenant2" not in output.lower(), (
            f"Actual operation error leaked tenant name 'tenant2': {output}. "
            f"Errors should not reveal cross-tenant information."
        )


class TestSelfSubjectAccessReviewInputValidation:
    """Verify request validation for malformed inputs."""

    def test_empty_service_rejected(self, jwt_grpc_tenant1_admin: GRPCClient):
        """Verify empty service field is rejected by validation."""
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "", "method": "Create"},
                    }
                },
            )

        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "InvalidArgument" in output or "invalid" in output.lower(), (
            f"Empty service should be rejected. Got: {output}"
        )

    def test_empty_method_rejected(self, jwt_grpc_tenant1_admin: GRPCClient):
        """Verify empty method field is rejected by validation."""
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
                data={
                    "object": {
                        "metadata": {"tenant": "tenant1", "name": f"ssar-{uuid4().hex[:8]}"},
                        "spec": {"service": "osac.public.v1.Clusters", "method": ""},
                    }
                },
            )

        output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "InvalidArgument" in output or "invalid" in output.lower(), (
            f"Empty method should be rejected. Got: {output}"
        )

    def test_malformed_tenant_rejected(self, jwt_grpc_tenant1_admin: GRPCClient):
        """Verify malformed tenant name is rejected during tenant validation.

        Note: Tenant field validation happens in tenancy logic, not at proto validation level.
        Malformed tenants are accepted by proto validation but rejected during authorization,
        resulting in allowed=false response rather than InvalidArgument error.
        """
        response = jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.SelfSubjectAccessReviews/Create",
            data={
                "object": {
                    "metadata": {"tenant": "INVALID$TENANT", "name": f"ssar-{uuid4().hex[:8]}"},
                    "spec": {"service": "osac.public.v1.Clusters", "method": "Create"},
                }
            },
        )

        # Verify: malformed tenant is rejected (allowed=false)
        status = response["object"]["status"]
        allowed = status.get("allowed", False)
        assert allowed is False, (
            f"Malformed tenant should be rejected. Expected allowed=false, got: {status}"
        )

        # Verify: reason indicates the tenant issue
        reason = status.get("reason", "")
        assert reason != "", "Rejection reason should be provided for malformed tenant"
