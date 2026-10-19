from __future__ import annotations

import contextlib
import subprocess
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    assert_grpc_rejected,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_nat_gateway_deletion,
    wait_for_nat_gateway_ready,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression

NAT_GATEWAYS = f"{PUBLIC_API}.NATGateways"


def _expect_not_found(grpc: GRPCClient, *, service: str, resource_id: str) -> None:
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        grpc.call(service=f"{service}/Get", data={"id": resource_id})
    assert_grpc_rejected(exc_info, "NotFound")


def _nat_gateway_ids(grpc: GRPCClient) -> list[str]:
    response: dict = grpc.call(service=f"{NAT_GATEWAYS}/List")
    return [item["id"] for item in response.get("items", [])]


def _has_fabric_manager(private_grpc: GRPCClient) -> bool:
    """NATGateways require a fabric manager; k8s-only environments cannot provision them."""
    for network_class in private_grpc.list_network_classes():
        spec = network_class.get("spec", {})
        if spec.get("fabricManager") or spec.get("fabric_manager"):
            return True
    return False


def test_networking_resource_lifecycle(
    jwt_grpc_tenant1: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient
) -> None:
    """The shared networking resources follow the accepted lifecycle on the deployed stack.

    Creates a tenant-scoped graph in readiness order (VirtualNetwork, Subnet,
    SecurityGroup, then the provider-owned ExternalIPPool, ExternalIP, and — where
    the environment's NetworkClass has a fabric manager — NATGateway), proves the
    dependency guards reject out-of-order deletion, replaces a Subnet with a
    changed spec, and deletes the graph in reverse dependency order confirming
    NotFound on every read after removal and on a second Delete (the retry
    contract). NATGateways are skipped in k8s-only environments (their deployed
    lifecycle is covered by the BMaaS suite). The ExternalIPAttachment path and
    the VM attachment-immutability journey live in
    test_compute_network_immutability.py; NetworkClass is a provider-owned
    singleton covered by the unit suites and is never mutated here.
    """
    suffix = uuid4().hex[:8]
    vn_name = f"res-life-vn-{suffix}"
    sg_name = f"res-life-sg-{suffix}"
    subnet_name = f"res-life-sub-{suffix}"
    pool_name = f"res-life-pool-{suffix}"
    eip_name = f"res-life-eip-{suffix}"
    natgw_name = f"res-life-natgw-{suffix}"

    vn_id: str | None = None
    vn_cr: str | None = None
    sg_id: str | None = None
    sg_cr: str | None = None
    subnet_id: str | None = None
    subnet_cr: str | None = None
    pool_id: str | None = None
    pool_cr: str | None = None
    eip_id: str | None = None
    eip_cr: str | None = None
    natgw_id: str | None = None
    natgw_cr: str | None = None

    try:
        # Readiness order: VirtualNetwork, then its dependents. The Subnet is
        # readied before the SecurityGroup: both CI failures left the SG CR
        # un-Ready when it was created before any subnet existed in the VN,
        # while every SG created after a Ready subnet reached Ready.
        vn_id = jwt_grpc_tenant1.create_virtual_network(name=vn_name, ipv4_cidr="10.101.0.0/16")
        vn_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr)

        subnet_id = jwt_grpc_tenant1.create_subnet(name=subnet_name, virtual_network=vn_id, ipv4_cidr="10.101.1.0/24")
        subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)

        # Replacement: the Subnet is deleted and recreated with a changed spec
        # before the SecurityGroup exists — SecurityGroups reference the
        # VirtualNetwork and block Subnet deletion for their whole lifetime, so
        # the replacement cannot happen after the SG is created. Reads and a
        # second Delete return NotFound after removal.
        delete_and_wait_for_subnet(jwt_grpc_tenant1, k8s_hub_client, subnet_id, subnet_cr)
        deleted_subnet_id = subnet_id
        subnet_id = None
        subnet_cr = None
        _expect_not_found(jwt_grpc_tenant1, service=f"{PUBLIC_API}.Subnets", resource_id=deleted_subnet_id)
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.Subnets/Delete", data={"id": deleted_subnet_id})
        assert_grpc_rejected(exc_info, "NotFound")

        subnet_id = jwt_grpc_tenant1.create_subnet(
            name=f"{subnet_name}-r", virtual_network=vn_id, ipv4_cidr="10.101.2.0/24"
        )
        subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)

        sg_id = jwt_grpc_tenant1.create_security_group(name=sg_name, virtual_network=vn_id)
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)

        # Dependency guard: the VirtualNetwork cannot be deleted while the Subnet is active.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.VirtualNetworks/Delete", data={"id": vn_id})
        assert_grpc_rejected(exc_info, "FailedPrecondition")

        # The pool is provider-owned and created through the private API; the
        # tenant user's ExternalIP references it through the public API.
        pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(allocate_worker_subnet(prefix=24))])
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)

        eip_id = jwt_grpc_tenant1.create_external_ip(name=eip_name, pool=pool_id)
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)

        # NATGateways require a fabric manager. k8s-only environments (e.g. the
        # VMaaS CI profile, which clears the singleton NetworkClass's fabric
        # manager) reject NATGateway creation by design (OSAC-4875 dispatcher
        # blocking), so the NATGateway leg only runs where the fabric exists.
        # Its deployed lifecycle is covered by the BMaaS suite (create/delete
        # phases); this journey covers the five resources this environment owns.
        has_fabric = _has_fabric_manager(private_grpc)
        if has_fabric:
            natgw_id = jwt_grpc_tenant1.create_nat_gateway(
                name=natgw_name, virtual_network_name=vn_name, external_ip_name=eip_name
            )
            # The operator creates the CR asynchronously, so poll for the CR name
            # (mirroring the BMaaS suite) instead of reading it directly after Create.
            natgw_cr = poll_until(
                fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=natgw_id, checked=False),
                until=lambda name: name != "",
                retries=30,
                delay=2,
                description=f"NATGateway CR for {natgw_id}",
            )
            wait_for_nat_gateway_ready(k8s=k8s_hub_client, name=natgw_cr)
        else:
            print("NetworkClass has no fabric manager; skipping the NATGateway leg")

        # List/Get surface per resource, same identity as created.
        assert vn_id in jwt_grpc_tenant1.list_virtual_network_ids()
        assert sg_id in jwt_grpc_tenant1.list_security_group_ids()
        assert subnet_id in jwt_grpc_tenant1.list_subnet_ids()
        assert eip_id in jwt_grpc_tenant1.list_external_ip_ids()

        assert jwt_grpc_tenant1.get_virtual_network(vn_id=vn_id)["object"]["metadata"]["name"] == vn_name
        assert jwt_grpc_tenant1.get_subnet(subnet_id=subnet_id)["object"]["metadata"]["name"] == f"{subnet_name}-r"
        assert jwt_grpc_tenant1.get_security_group(sg_id=sg_id)["object"]["metadata"]["name"] == sg_name
        assert jwt_grpc_tenant1.get_external_ip(external_ip_id=eip_id)["object"]["metadata"]["name"] == eip_name

        # NATGateways require a fabric manager (checked before Create above); the
        # ExternalIP dependency guard only applies while a NATGateway references it.
        if has_fabric:
            assert natgw_id in _nat_gateway_ids(jwt_grpc_tenant1)

            # Dependency guard: the ExternalIP cannot be deleted while the NATGateway references it.
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.ExternalIPs/Delete", data={"id": eip_id})
            assert_grpc_rejected(exc_info, "FailedPrecondition")

        # Reverse dependency order, confirming NotFound reads after each removal.
        if has_fabric:
            jwt_grpc_tenant1.call(service=f"{NAT_GATEWAYS}/Delete", data={"id": natgw_id})
            wait_for_nat_gateway_deletion(k8s=k8s_hub_client, name=natgw_cr)
            _expect_not_found(jwt_grpc_tenant1, service=NAT_GATEWAYS, resource_id=natgw_id)
            natgw_id = None
            natgw_cr = None

        jwt_grpc_tenant1.delete_external_ip(external_ip_id=eip_id)
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
        _expect_not_found(jwt_grpc_tenant1, service=f"{PUBLIC_API}.ExternalIPs", resource_id=eip_id)
        eip_id = None
        eip_cr = None

        delete_and_wait_for_security_group(jwt_grpc_tenant1, k8s_hub_client, sg_id, sg_cr)
        sg_id = None
        sg_cr = None

        delete_and_wait_for_subnet(jwt_grpc_tenant1, k8s_hub_client, subnet_id, subnet_cr)
        subnet_id = None
        subnet_cr = None

        delete_and_wait_for_virtual_network(jwt_grpc_tenant1, k8s_hub_client, vn_id, vn_cr)
        vn_id = None
        vn_cr = None

        private_grpc.delete_external_ip_pool(pool_id=pool_id)
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr)
        pool_id = None
        pool_cr = None
    finally:
        # Best-effort reverse-order cleanup; tolerate removal errors from a partial run.
        if natgw_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                jwt_grpc_tenant1.call(service=f"{NAT_GATEWAYS}/Delete", data={"id": natgw_id})
                if natgw_cr is not None:
                    wait_for_nat_gateway_deletion(k8s=k8s_hub_client, name=natgw_cr)
        if eip_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                jwt_grpc_tenant1.delete_external_ip(external_ip_id=eip_id)
                if eip_cr is not None:
                    wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
        if sg_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                delete_and_wait_for_security_group(jwt_grpc_tenant1, k8s_hub_client, sg_id, sg_cr)
        if subnet_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                delete_and_wait_for_subnet(jwt_grpc_tenant1, k8s_hub_client, subnet_id, subnet_cr)
        if vn_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                delete_and_wait_for_virtual_network(jwt_grpc_tenant1, k8s_hub_client, vn_id, vn_cr)
        if pool_id is not None:
            with contextlib.suppress(subprocess.CalledProcessError):
                private_grpc.delete_external_ip_pool(pool_id=pool_id)
                if pool_cr is not None:
                    wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr)
