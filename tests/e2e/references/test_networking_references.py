from __future__ import annotations

import json
import logging
import subprocess
from collections.abc import Iterator

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    assert_grpc_field_violation,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_ready,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.teardown import wait_owned_gone
from tests.e2e.references.assertions import assert_disabled_create_error

logger = logging.getLogger(__name__)


@pytest.fixture
def disabled_reference_mode(private_grpc: GRPCClient) -> None:
    """Require the installed disabled mode before creating a negative-contract pool."""
    try:
        response = private_grpc.call(service=f"{PRIVATE_API}.InstallationSettings/Get", data={}, emit_defaults=True)
    except json.JSONDecodeError:
        pytest.fail("InstallationSettings/Get returned malformed JSON", pytrace=False)
    if not isinstance(response, dict) or type(response.get("networkingProvisioningEnabled")) is not bool:
        pytest.fail("InstallationSettings/Get must return a boolean networkingProvisioningEnabled", pytrace=False)
    if response["networkingProvisioningEnabled"]:
        pytest.skip("Disabled reference contract is not applicable: provider networking provisioning is enabled")


@pytest.fixture
def disabled_reference_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient, ref_test_run_id: str, disabled_reference_mode: None
) -> Iterator[dict[str, str]]:
    """Create a fixture-owned shared pool via private APIs, wait for readiness, then remove children and pool."""
    name = f"ref-disabled-pool-{ref_test_run_id}"
    pool_id = private_grpc.create_external_ip_pool(name=name, cidrs=[str(allocate_worker_subnet(prefix=30))])
    try:
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        poll_until(
            fn=lambda: private_grpc.get_external_ip_pool(pool_id=pool_id)["object"]["status"]["state"],
            until=lambda state: state == "EXTERNAL_IP_POOL_STATE_READY",
            retries=30,
            delay=2,
            description=f"ExternalIPPool {pool_id} API READY",
            resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
            resource_id=pool_id,
        )
        yield {"id": pool_id, "name": name}
    finally:
        stored = private_grpc.call(
            service=f"{PRIVATE_API}.ExternalIPs/List", data={"filter": f'this.spec.pool.id == "{pool_id}"'}
        )
        for item in stored.get("items", []):
            external_ip_id = item["id"]
            wait_owned_gone(
                description=f"Unexpected ExternalIP {external_ip_id} gone",
                api_gone=lambda resource_id=external_ip_id: (
                    resource_id not in {entry["id"] for entry in private_grpc.list_private_external_ips()}
                ),
                cr_gone=lambda resource_id=external_ip_id: (
                    k8s_hub_client.get_external_ip_name(uuid=resource_id, checked=False) == ""
                ),
                delete=lambda resource_id=external_ip_id: private_grpc.call(
                    service=f"{PRIVATE_API}.ExternalIPs/Delete", data={"id": resource_id}
                ),
                resource_kind=WaitResourceKind.EXTERNAL_IP,
                resource_id=external_ip_id,
            )
        wait_owned_gone(
            description=f"ExternalIPPool {pool_id} gone",
            api_gone=lambda: pool_id not in private_grpc.list_external_ip_pool_ids(),
            cr_gone=lambda: k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False) == "",
            delete=lambda: private_grpc.delete_external_ip_pool(pool_id=pool_id),
            resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
            resource_id=pool_id,
        )


class TestNetworkingReferences:
    """OSAC-3095: Networking resource reference tests."""

    @pytest.mark.reference_networking
    @pytest.mark.parametrize("pool_selector", ["id", "name"])
    def test_disabled_external_ip_reference_stores_nothing(
        self,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        disabled_reference_pool: dict[str, str],
        ref_test_run_id: str,
        pool_selector: str,
    ) -> None:
        """Reject either pool reference without storage, allocation-set or accounting changes."""
        pool = disabled_reference_pool
        name = f"ref-disabled-eip-{pool_selector}-{ref_test_run_id}"

        def snapshot() -> tuple[set[str], tuple[int, int]]:
            """Read the fixture pool's allocation IDs and counters, propagating read failures."""
            stored = private_grpc.call(
                service=f"{PRIVATE_API}.ExternalIPs/List", data={"filter": f'this.spec.pool.id == "{pool["id"]}"'}
            )
            ids = {item["id"] for item in stored.get("items", [])}
            status = private_grpc.get_external_ip_pool(pool_id=pool["id"])["object"]["status"]
            return ids, (int(status.get("allocated", 0)), int(status.get("available", 0)))

        before = snapshot()
        unexpected_id = ""
        try:
            with pytest.raises(subprocess.CalledProcessError) as failure:
                response = grpc.call(
                    service=f"{PUBLIC_API}.ExternalIPs/Create",
                    data={
                        "object": {"metadata": {"name": name}, "spec": {"pool": {pool_selector: pool[pool_selector]}}}
                    },
                )
                unexpected_id = response["object"]["id"]
            assert_disabled_create_error(failure.value)
            stored = grpc.call(
                service=f"{PUBLIC_API}.ExternalIPs/List", data={"filter": f'this.metadata.name == "{name}"'}
            )
            assert not stored.get("items", []) and int(stored.get("total", 0)) == 0, (
                "Rejected create stored an ExternalIP"
            )
            assert snapshot() == before, "Rejected create changed the observed ExternalIPs or pool accounting"
        finally:
            if unexpected_id:
                wait_owned_gone(
                    description=f"Unexpected ExternalIP {unexpected_id} gone",
                    api_gone=lambda: unexpected_id not in grpc.list_external_ip_ids(),
                    cr_gone=lambda: k8s_hub_client.get_external_ip_name(uuid=unexpected_id, checked=False) == "",
                    delete=lambda: grpc.delete_external_ip(external_ip_id=unexpected_id),
                    resource_kind=WaitResourceKind.EXTERNAL_IP,
                    resource_id=unexpected_id,
                )

    @pytest.mark.reference_networking
    def test_create_subnet_with_virtual_network_by_name(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        networking_ref_virtual_network: dict[str, str],
        ref_test_run_id: str,
    ):
        subnet_name = f"ref-sub-vn-{ref_test_run_id}"
        response = grpc.call(
            service=f"{PUBLIC_API}.Subnets/Create",
            data={
                "object": {
                    "metadata": {"name": subnet_name},
                    "spec": {
                        "virtual_network": {"name": networking_ref_virtual_network["name"]},
                        "ipv4_cidr": "172.26.200.0/24",
                    },
                }
            },
        )
        subnet_id = response["object"]["id"]
        try:
            subnet = grpc.call(service=f"{PUBLIC_API}.Subnets/Get", data={"id": subnet_id})
            vn_ref = subnet["object"]["spec"].get("virtual_network", subnet["object"]["spec"].get("virtualNetwork", {}))
            assert (
                vn_ref.get("name") == networking_ref_virtual_network["name"]
                or vn_ref.get("id") == networking_ref_virtual_network["id"]
            )
        finally:
            grpc.delete_subnet(subnet_id=subnet_id)
            try:
                subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
                wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr_name)
            except (subprocess.CalledProcessError, AssertionError, TimeoutError):
                logger.warning("Cleanup wait failed for subnet %s", subnet_id)

    @pytest.mark.reference_networking
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

    @pytest.mark.reference_networking
    def test_invalid_virtual_network_reference_returns_field_path(self, grpc: GRPCClient, ref_test_run_id: str):
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.Subnets/Create",
                data={
                    "object": {
                        "metadata": {"name": f"ref-bad-vn-{ref_test_run_id}"},
                        "spec": {"virtual_network": {"name": "nonexistent-vn"}, "ipv4_cidr": "172.28.0.0/24"},
                    }
                },
            )
        assert_grpc_field_violation(exc_info, field_path="spec.virtual_network")

    @pytest.mark.reference_networking
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
