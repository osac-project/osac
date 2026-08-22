from __future__ import annotations

import logging
import subprocess

import pytest

from tests.core.grpc_client import PUBLIC_API, GRPCClient
from tests.core.helpers import (
    assert_grpc_field_violation,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
)
from tests.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)


class TestNetworkingReferences:
    """OSAC-3095: Networking resource reference tests."""

    def test_create_virtual_network_with_network_class_by_name(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, ref_network_class: str, ref_test_run_id: str
    ):
        vn_name = f"ref-vn-nc-{ref_test_run_id}"
        response = grpc.call(
            service=f"{PUBLIC_API}.VirtualNetworks/Create",
            data={
                "object": {
                    "metadata": {"name": vn_name},
                    "spec": {"network_class": {"name": ref_network_class}, "ipv4_cidr": "10.211.0.0/16"},
                }
            },
        )
        vn_id = response["object"]["id"]
        try:
            vn = grpc.get_virtual_network(vn_id=vn_id)
            nc_ref = vn["object"]["spec"].get("network_class", vn["object"]["spec"].get("networkClass", {}))
            assert nc_ref.get("name") == ref_network_class or nc_ref.get("id"), (
                "network_class reference should contain name or resolved id"
            )
        finally:
            grpc.delete_virtual_network(vn_id=vn_id)
            try:
                vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
                wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for virtual network %s", vn_id)

    def test_create_subnet_with_virtual_network_by_name(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, ref_virtual_network: dict[str, str], ref_test_run_id: str
    ):
        subnet_name = f"ref-sub-vn-{ref_test_run_id}"
        response = grpc.call(
            service=f"{PUBLIC_API}.Subnets/Create",
            data={
                "object": {
                    "metadata": {"name": subnet_name},
                    "spec": {"virtual_network": {"name": ref_virtual_network["name"]}, "ipv4_cidr": "10.210.200.0/24"},
                }
            },
        )
        subnet_id = response["object"]["id"]
        try:
            subnet = grpc.call(service=f"{PUBLIC_API}.Subnets/Get", data={"id": subnet_id})
            vn_ref = subnet["object"]["spec"].get("virtual_network", subnet["object"]["spec"].get("virtualNetwork", {}))
            assert vn_ref.get("name") == ref_virtual_network["name"] or vn_ref.get("id") == ref_virtual_network["id"]
        finally:
            grpc.delete_subnet(subnet_id=subnet_id)
            try:
                subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
                wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for subnet %s", subnet_id)

    def test_create_security_group_with_virtual_network_by_name(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, ref_virtual_network: dict[str, str], ref_test_run_id: str
    ):
        sg_name = f"ref-sg-vn-{ref_test_run_id}"
        response = grpc.call(
            service=f"{PUBLIC_API}.SecurityGroups/Create",
            data={
                "object": {
                    "metadata": {"name": sg_name},
                    "spec": {"virtual_network": {"name": ref_virtual_network["name"]}},
                }
            },
        )
        sg_id = response["object"]["id"]
        try:
            sg = grpc.call(service=f"{PUBLIC_API}.SecurityGroups/Get", data={"id": sg_id})
            vn_ref = sg["object"]["spec"].get("virtual_network", sg["object"]["spec"].get("virtualNetwork", {}))
            assert vn_ref.get("name") == ref_virtual_network["name"] or vn_ref.get("id") == ref_virtual_network["id"]
        finally:
            grpc.delete_security_group(sg_id=sg_id)
            try:
                sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
                wait_for_security_group_deletion(k8s=k8s_hub_client, name=sg_cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for security group %s", sg_id)

    def test_invalid_virtual_network_reference_returns_field_path(self, grpc: GRPCClient, ref_test_run_id: str):
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.Subnets/Create",
                data={
                    "object": {
                        "metadata": {"name": f"ref-bad-vn-{ref_test_run_id}"},
                        "spec": {"virtual_network": {"name": "nonexistent-vn"}, "ipv4_cidr": "10.212.0.0/24"},
                    }
                },
            )
        assert_grpc_field_violation(exc_info, field_path="spec.virtual_network")

    def test_cel_filter_by_virtual_network_name(
        self, grpc: GRPCClient, ref_subnet: dict[str, str], ref_virtual_network: dict[str, str]
    ):
        vn_name = ref_virtual_network["name"]
        items = grpc.list_with_filter(
            service=f"{PUBLIC_API}.Subnets/List", filter_expr=f'this.spec.virtual_network.name == "{vn_name}"'
        )
        found_ids = [item["id"] for item in items]
        assert ref_subnet["id"] in found_ids, (
            f"Expected subnet {ref_subnet['id']} in filter results for VN name '{vn_name}', got {found_ids}"
        )

    def test_cross_tenant_network_class_reference(
        self, jwt_grpc_tenant1: GRPCClient, k8s_hub_client: K8sClient, ref_network_class: str, ref_test_run_id: str
    ):
        vn_name = f"ref-vn-xt-{ref_test_run_id}"
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.VirtualNetworks/Create",
            data={
                "object": {
                    "metadata": {"name": vn_name},
                    "spec": {"network_class": {"name": ref_network_class}, "ipv4_cidr": "10.213.0.0/16"},
                }
            },
        )
        vn_id = response["object"]["id"]
        try:
            vn = jwt_grpc_tenant1.get_virtual_network(vn_id=vn_id)
            nc_ref = vn["object"]["spec"].get("network_class", vn["object"]["spec"].get("networkClass", {}))
            assert nc_ref.get("name") == ref_network_class or nc_ref.get("id"), (
                "network_class reference should contain name or resolved id for cross-tenant reference"
            )
        finally:
            jwt_grpc_tenant1.delete_virtual_network(vn_id=vn_id)
            try:
                vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
                wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for virtual network %s", vn_id)
