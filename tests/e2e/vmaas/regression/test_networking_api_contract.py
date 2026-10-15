from __future__ import annotations

import subprocess
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_method_unavailable, assert_grpc_rejected

pytestmark = pytest.mark.regression


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


def test_public_ipv6_networking_is_rejected_and_not_persisted(jwt_grpc_tenant1_admin: GRPCClient) -> None:
    name = f"ipv6-contract-vn-{uuid4().hex[:8]}"
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.VirtualNetworks/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"ipv4_cidr": "10.250.0.0/16", "ipv6_cidr": "2001:db8::/64"},
                }
            },
        )

    assert_grpc_rejected(exc_info, "InvalidArgument")
    assert not jwt_grpc_tenant1_admin.list_with_filter(
        service=f"{PUBLIC_API}.VirtualNetworks/List", filter_expr=f'this.metadata.name == "{name}"'
    )


@pytest.mark.parametrize(
    ("cidrs", "ip_family"),
    [
        (["198.51.100.0/30", "198.51.100.4/30"], "IP_FAMILY_IPV4"),
        (["2001:db8::/126"], "IP_FAMILY_IPV6"),
    ],
)
def test_external_ip_pool_requires_one_ipv4_cidr(
    private_grpc: GRPCClient, cidrs: list[str], ip_family: str
) -> None:
    name = f"ipv4-pool-contract-{uuid4().hex[:8]}"
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        private_grpc.call(
            service=f"{PRIVATE_API}.ExternalIPPools/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"cidrs": cidrs, "ip_family": ip_family},
                }
            },
        )

    assert_grpc_rejected(exc_info, "InvalidArgument")
    assert not private_grpc.list_with_filter(
        service=f"{PRIVATE_API}.ExternalIPPools/List", filter_expr=f'this.metadata.name == "{name}"'
    )
