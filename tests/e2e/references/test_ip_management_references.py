from __future__ import annotations

import logging
import subprocess
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    assert_grpc_field_violation,
    poll_until,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
)
from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


class TestIPManagementReferences:
    """OSAC-3105: IP management resource reference tests."""

    @pytest.mark.requires_bmaas
    @pytest.mark.requires_vmaas
    def test_external_ip_from_pool_by_name(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, ref_eip_pool: dict[str, str]
    ):
        tag = uuid4().hex[:8]
        eip_name = f"ref-eip-{tag}"

        response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
        )
        eip_id = response["object"]["id"]
        eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        try:
            pool_ref = response["object"]["spec"]["pool"]
            assert pool_ref.get("name") == ref_eip_pool["name"]
            assert pool_ref.get("id") == ref_eip_pool["id"]
        finally:
            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)

    @pytest.mark.requires_bmaas
    def test_nat_gateway_by_name(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        ref_virtual_network: dict[str, str],
        ref_eip_pool: dict[str, str],
        ref_test_run_id: str,
    ):
        tag = uuid4().hex[:8]
        eip_name = f"ref-nat-eip-{tag}"

        eip_response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
        )
        eip_id = eip_response["object"]["id"]
        eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        nat_id: str | None = None
        try:
            wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr_name)
            nat_name = f"ref-nat-{tag}"
            nat_id = grpc.create_nat_gateway(
                name=nat_name, virtual_network_name=ref_virtual_network["name"], external_ip_name=eip_name
            )

            nat_response = grpc.call(service=f"{PUBLIC_API}.NATGateways/Get", data={"id": nat_id})
            spec = nat_response["object"]["spec"]

            vn_ref = spec.get("virtual_network", spec.get("virtualNetwork", {}))
            assert vn_ref.get("name") == ref_virtual_network["name"]
            assert vn_ref.get("id") == ref_virtual_network["id"]

            eip_ref = spec.get("external_ip", spec.get("externalIp", {}))
            assert eip_ref.get("name") == eip_name
            assert eip_ref.get("id") == eip_id
        finally:
            if nat_id:
                try:
                    grpc.delete_nat_gateway(nat_gateway_id=nat_id)
                    poll_until(
                        fn=lambda: (
                            nat_id
                            not in [
                                item["id"]
                                for item in grpc.call(service=f"{PUBLIC_API}.NATGateways/List").get("items", [])
                            ]
                        ),
                        until=lambda gone: gone is True,
                        retries=60,
                        delay=5,
                        description=f"NATGateway {nat_id} deletion",
                    )
                except subprocess.CalledProcessError:
                    logger.warning("Failed to cleanup NATGateway %s", nat_id)
            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)

    @pytest.mark.requires_vmaas
    def test_invalid_attachment_target_returns_field_path(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, ref_eip_pool: dict[str, str]
    ):
        tag = uuid4().hex[:8]
        eip_name = f"ref-att-eip-{tag}"

        eip_response: dict[str, Any] = grpc.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
        )
        eip_id = eip_response["object"]["id"]
        eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
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
            assert_grpc_field_violation(exc_info, field_path="object.spec")
        finally:
            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)

    @pytest.mark.requires_bmaas
    @pytest.mark.requires_vmaas
    def test_cross_tenant_pool_reference(
        self, jwt_grpc_tenant1: GRPCClient, k8s_hub_client: K8sClient, ref_eip_pool: dict[str, str]
    ):
        tag = uuid4().hex[:8]
        eip_name = f"ref-xt-eip-{tag}"

        response: dict[str, Any] = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": eip_name}, "spec": {"pool": {"name": ref_eip_pool["name"]}}}},
        )
        eip_id = response["object"]["id"]
        eip_cr_name = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        try:
            pool_ref = response["object"]["spec"]["pool"]
            assert pool_ref.get("name") == ref_eip_pool["name"]
            assert pool_ref.get("id") == ref_eip_pool["id"]
        finally:
            jwt_grpc_tenant1.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr_name)
