from __future__ import annotations

import re
import subprocess

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_method_unavailable, assert_grpc_rejected

pytestmark = pytest.mark.regression

PUBLIC_NETWORKING_SERVICES = ["VirtualNetworks", "Subnets", "ExternalIPs", "ExternalIPAttachments", "NATGateways"]


# SecurityGroups.Update is a scoped exception for rule updates (OSAC-5679).
@pytest.mark.parametrize(
    "service", ["VirtualNetworks", "Subnets", "ExternalIPs", "ExternalIPAttachments", "NATGateways"]
)
def test_public_networking_update_is_not_exposed(jwt_grpc_tenant1: GRPCClient, service: str) -> None:
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.{service}/Update", data={"object": {"id": "osac-5373-contract-probe"}}
        )

    assert_grpc_method_unavailable(exc_info, service=f"{PUBLIC_API}.{service}", method="Update")


def test_public_security_groups_scoped_update_rejects_invalid_input(jwt_grpc_tenant1: GRPCClient) -> None:
    """SecurityGroups.Update exists as a rule-only exception (OSAC-5679).

    The RPC resolves in the descriptor and rejects an empty object before
    persistence; rule-edit journeys live in the SecurityGroup lifecycle suites.
    """
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.SecurityGroups/Update", data={"object": {}})
    assert_grpc_rejected(exc_info, "InvalidArgument")


@pytest.mark.parametrize("service", PUBLIC_NETWORKING_SERVICES)
def test_public_networking_exposes_lifecycle_methods(jwt_grpc_tenant1: GRPCClient, service: str) -> None:
    """The accepted operation surface is Create/List/Get/Delete.

    Read methods and the guarded Delete respond to a tenant caller, Create rejects an
    empty object before persistence, and Update stays absent from the descriptor.
    Positive Create/Get/Delete journeys live in the lifecycle suites; this probe only
    proves which methods the public descriptor exposes.
    """
    unique_probe = "osac-5374-contract-probe"

    response: dict = jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.{service}/List")
    # A successful empty List omits the repeated items field in protojson
    # output, so an empty dict is a valid empty list result.
    assert isinstance(response.get("items", []), list)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.{service}/Get", data={"id": unique_probe})
    assert_grpc_rejected(exc_info, "NotFound")

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.{service}/Delete", data={"id": unique_probe})
    assert_grpc_rejected(exc_info, "NotFound")

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.{service}/Create", data={"object": {}})
    assert_grpc_rejected(exc_info, "InvalidArgument")

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.{service}/Update", data={"object": {"id": unique_probe}})
    assert_grpc_method_unavailable(exc_info, service=f"{PUBLIC_API}.{service}", method="Update")


def test_public_external_ip_pool_exposes_only_read_methods(jwt_grpc_tenant1_admin: GRPCClient) -> None:
    """ExternalIPPool is provider-owned: its public surface is List/Get only."""
    unique_probe = "osac-5374-contract-probe"

    response: dict = jwt_grpc_tenant1_admin.call(service=f"{PUBLIC_API}.ExternalIPPools/List")
    # A successful empty List omits the repeated items field in protojson
    # output, so an empty dict is a valid empty list result.
    assert isinstance(response.get("items", []), list)

    for method, data in [
        ("Create", {"object": {"metadata": {"name": unique_probe}}}),
        ("Update", {"object": {"id": unique_probe}}),
        ("Delete", {"id": unique_probe}),
    ]:
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1_admin.call(service=f"{PUBLIC_API}.ExternalIPPools/{method}", data=data)
        assert_grpc_method_unavailable(exc_info, service=f"{PUBLIC_API}.ExternalIPPools", method=method)


def test_public_network_classes_service_is_private_only(jwt_grpc_tenant1_admin: GRPCClient) -> None:
    """NetworkClass has no public service: the provider/admin private API owns its lifecycle."""
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1_admin.call(service=f"{PUBLIC_API}.NetworkClasses/List")

    combined = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
    # A missing service fails at descriptor resolution before any server-side gRPC
    # status is produced, so no "Code:" block may appear; a missing method on an
    # existing service would report "does not include a method named", which must
    # not be the failure shape here.
    assert not re.search(r"Code:\s*\w+", combined), f"expected no server-side status, got: {combined.strip()}"
    assert "does not include a method named" not in combined, (
        f"expected a missing-service resolution failure, not a missing-method error, got: {combined.strip()}"
    )
    assert "NetworkClasses" in combined, f"expected a descriptor-resolution failure, got: {combined.strip()}"
