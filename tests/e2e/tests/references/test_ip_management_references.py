from __future__ import annotations

import logging
import subprocess
from typing import Any
from uuid import uuid4

import pytest

from tests.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.core.helpers import assert_grpc_field_violation

logger = logging.getLogger(__name__)


@pytest.fixture(scope="module")
def ref_eip_pool(private_grpc: GRPCClient) -> str:
    response: dict[str, Any] = private_grpc.call(service=f"{PRIVATE_API}.ExternalIPPools/List")
    items = response.get("items", [])
    if not items:
        pytest.skip("No ExternalIPPools found; deploy at least one pool")
    return items[0]["metadata"]["name"]


@pytest.fixture(scope="module")
def ref_eip_pool_id(private_grpc: GRPCClient, ref_eip_pool: str) -> str:
    items = private_grpc.list_with_filter(
        service=f"{PRIVATE_API}.ExternalIPPools/List", filter_expr=f'this.metadata.name == "{ref_eip_pool}"'
    )
    assert items, f"ExternalIPPool '{ref_eip_pool}' not found"
    return items[0]["id"]


class TestIPManagementReferences:
    """OSAC-3105: IP management resource reference tests."""

    def test_external_ip_from_pool_by_name(self, grpc: GRPCClient, ref_eip_pool: str, ref_eip_pool_id: str):
        tag = uuid4().hex[:8]
        eip_name = f"ref-eip-{tag}"

        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool}}}},
        )
        eip_id = response["object"]["id"]
        try:
            pool_ref = response["object"]["spec"]["pool"]
            assert pool_ref.get("name") == ref_eip_pool
            assert pool_ref.get("id") == ref_eip_pool_id
        finally:
            grpc.delete_external_ip(external_ip_id=eip_id)

    def test_nat_gateway_by_name(
        self, grpc: GRPCClient, ref_virtual_network: dict[str, str], ref_eip_pool: str, ref_test_run_id: str
    ):
        tag = uuid4().hex[:8]
        eip_name = f"ref-nat-eip-{tag}"

        eip_response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool}}}},
        )
        eip_id = eip_response["object"]["id"]
        nat_id: str | None = None
        try:
            nat_name = f"ref-nat-{tag}"
            nat_id = grpc.create_nat_gateway(
                name=nat_name, virtual_network_name=ref_virtual_network["name"], external_ip_name=eip_name
            )

            nat_response = grpc.call(service=f"{PUBLIC_API}.NATGateways/Get", data={"id": nat_id})
            spec = nat_response["object"]["spec"]

            vn_ref = spec["virtual_network"]
            assert vn_ref.get("name") == ref_virtual_network["name"]
            assert vn_ref.get("id") == ref_virtual_network["id"]

            eip_ref = spec["external_ip"]
            assert eip_ref.get("name") == eip_name
            assert eip_ref.get("id") == eip_id
        finally:
            if nat_id:
                try:
                    grpc.delete_nat_gateway(nat_gateway_id=nat_id)
                except subprocess.CalledProcessError:
                    logger.warning("Failed to cleanup NATGateway %s", nat_id)
            grpc.delete_external_ip(external_ip_id=eip_id)

    def test_invalid_attachment_target_returns_field_path(self, grpc: GRPCClient, ref_eip_pool: str):
        tag = uuid4().hex[:8]
        eip_name = f"ref-att-eip-{tag}"

        eip_response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool}}}},
        )
        eip_id = eip_response["object"]["id"]
        try:
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                grpc.call(
                    service=f"{PUBLIC_API}.ExternalIPAttachments/Create",
                    data={
                        "object": {
                            "metadata": {"name": f"ref-att-bad-{tag}"},
                            "spec": {"external_ip": {"name": eip_name}, "compute_instance": {"name": "nonexistent-ci"}},
                        }
                    },
                )
            assert_grpc_field_violation(exc_info, field_path="compute_instance")
        finally:
            grpc.delete_external_ip(external_ip_id=eip_id)

    def test_cross_tenant_pool_reference(self, jwt_grpc_tenant1: GRPCClient, ref_eip_pool: str, ref_eip_pool_id: str):
        tag = uuid4().hex[:8]
        eip_name = f"ref-xt-eip-{tag}"

        response: dict[str, Any] = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool}}}},
        )
        eip_id = response["object"]["id"]
        try:
            pool_ref = response["object"]["spec"]["pool"]
            assert pool_ref.get("name") == ref_eip_pool
            assert pool_ref.get("id") == ref_eip_pool_id
        finally:
            jwt_grpc_tenant1.delete_external_ip(external_ip_id=eip_id)
