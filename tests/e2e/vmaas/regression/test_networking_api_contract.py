from __future__ import annotations

import subprocess

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_method_unavailable

pytestmark = pytest.mark.regression


@pytest.mark.parametrize(
    "service", ["VirtualNetworks", "Subnets", "SecurityGroups", "ExternalIPs", "ExternalIPAttachments", "NATGateways"]
)
def test_public_networking_update_is_not_exposed(jwt_grpc_tenant1: GRPCClient, service: str) -> None:
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.{service}/Update", data={"object": {"id": "osac-5373-contract-probe"}}
        )

    assert_grpc_method_unavailable(exc_info, service=f"{PUBLIC_API}.{service}", method="Update")
