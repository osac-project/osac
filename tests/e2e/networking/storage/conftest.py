from __future__ import annotations

import os
from collections.abc import Iterator
from functools import partial

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import registered_ssh_key_secret
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env
from tests.e2e.networking.storage.endpoint import StorageEndpoint, from_env, log_line_count
from tests.e2e.networking.vmaas import guest_ssh, stack
from tests.e2e.vmaas.conftest import default_disk_image, default_instance_type, test_run_id  # noqa: F401

DEFAULT_SUBNET_NAME = "default-ipv4"
DEFAULT_SG_NAME = "default"
DEFAULT_NAT_NAME = "default-nat"


@pytest.fixture(scope="session")
def storage_endpoint() -> StorageEndpoint:
    return from_env(os.environ)


@pytest.fixture(scope="session")
def storage_log_start(storage_endpoint: StorageEndpoint) -> int:
    """Line count before any test mounts, so positive assertions cannot see an earlier mount."""
    return log_line_count(storage_endpoint)


def _default_network(client: GRPCClient) -> dict[str, str]:
    subnets = client.call(service=f"{PUBLIC_API}.Subnets/List").get("items", [])
    subnet = next((item for item in subnets if item.get("metadata", {}).get("name") == DEFAULT_SUBNET_NAME), None)
    if subnet is None:
        raise RuntimeError(f"No {DEFAULT_SUBNET_NAME} Subnet; tenant default networking is not ready")
    groups = client.call(service=f"{PUBLIC_API}.SecurityGroups/List").get("items", [])
    group = next(
        (
            item
            for item in groups
            if item.get("metadata", {}).get("name") == DEFAULT_SG_NAME
            and item.get("spec", {}).get("virtual_network", {}).get("id")
            == subnet.get("spec", {}).get("virtual_network", {}).get("id")
        ),
        None,
    )
    if group is None:
        raise RuntimeError(f"No {DEFAULT_SG_NAME} SecurityGroup; tenant default networking is not ready")
    return {
        "label": "default",
        "vnet_id": subnet["spec"]["virtual_network"]["id"],
        "sub_a_id": subnet["id"],
        "subnet_cidr": subnet["spec"]["ipv4_cidr"],
        "sg_id": group["id"],
    }


@pytest.fixture(scope="module")
def tenant1_default_network(grpc: GRPCClient) -> dict[str, str]:
    return _default_network(grpc)


@pytest.fixture(scope="module")
def tenant2_default_network(jwt_grpc_tenant2_admin: GRPCClient) -> dict[str, str]:
    return _default_network(jwt_grpc_tenant2_admin)


def default_nat_address(client: GRPCClient) -> str:
    items = client.call(service=f"{PUBLIC_API}.ExternalIPs/List").get("items", [])
    external_ip = next((item for item in items if item.get("metadata", {}).get("name") == DEFAULT_NAT_NAME), None)
    address = (external_ip or {}).get("status", {}).get("address", "")
    if not address:
        raise RuntimeError(f"{DEFAULT_NAT_NAME} ExternalIP has no allocated address")
    return address


@pytest.fixture(scope="session")
def storage_identity_file() -> str:
    return env("OSAC_BMI_SSH_IDENTITY", "/root/.ssh/id_rsa")


@pytest.fixture(scope="module")
def storage_key_secrets(
    cli: OsacCLI, jwt_cli_tenant2_admin: OsacCLI, net_ssh_public_key: str, net_test_run_id: str
) -> Iterator[dict[str, str]]:
    with (
        registered_ssh_key_secret(cli, net_ssh_public_key, f"e2e-storage-key-{net_test_run_id}") as tenant1,
        registered_ssh_key_secret(
            jwt_cli_tenant2_admin, net_ssh_public_key, f"e2e-storage-key-t2-{net_test_run_id}"
        ) as tenant2,
    ):
        yield {"tenant1": tenant1, "tenant2": tenant2}


@pytest.fixture(scope="module")
def storage_vms(
    cli: OsacCLI,
    jwt_cli_tenant2_admin: OsacCLI,
    grpc: GRPCClient,
    jwt_grpc_tenant2_admin: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_instance_type: str,  # noqa: F811
    default_disk_image: str,  # noqa: F811
    default_storage_tier: str,
    tenant1_default_network: dict[str, str],
    tenant2_default_network: dict[str, str],
    storage_key_secrets: dict[str, str],
    storage_identity_file: str,
    net_test_run_id: str,
) -> Iterator[dict[str, dict[str, str]]]:
    """One VM on each tenant's default network, created from the defaults onboarding made."""
    instances: list[dict[str, str]] = []
    state: dict[str, dict[str, str]] = {}
    stack.register_guest_snapshot("storage-guests", instances, storage_identity_file)

    created = stack.run_parallel(
        [
            partial(
                stack.create_vm,
                cli=cli,
                k8s_hub_client=k8s_hub_client,
                instances=instances,
                name=f"storage-vm-t1-{net_test_run_id}",
                network=tenant1_default_network,
                sg_id=tenant1_default_network["sg_id"],
                key_secret=storage_key_secrets["tenant1"],
                vm_template=vm_template,
                instance_type=default_instance_type,
                disk_image=default_disk_image,
                storage_tier=default_storage_tier,
            ),
            partial(
                stack.create_vm,
                cli=jwt_cli_tenant2_admin,
                k8s_hub_client=k8s_hub_client,
                instances=instances,
                name=f"storage-vm-t2-{net_test_run_id}",
                network=tenant2_default_network,
                sg_id=tenant2_default_network["sg_id"],
                key_secret=storage_key_secrets["tenant2"],
                vm_template=vm_template,
                instance_type=default_instance_type,
                disk_image=default_disk_image,
                storage_tier=default_storage_tier,
            ),
        ]
    )
    state.update(tenant1=created[0], tenant2=created[1])

    try:
        stack.run_parallel(
            [
                partial(
                    stack.wait_for_vm,
                    k8s_hub_client=k8s_hub_client,
                    k8s_virt_client=k8s_virt_client,
                    instance=vm,
                    identity_file=storage_identity_file,
                )
                for vm in state.values()
            ]
        )
        yield state
    finally:
        stack.run_stage(
            [
                (
                    f"ComputeInstance {vm['id']}",
                    partial(stack.delete_vm, client=grpc, k8s_hub_client=k8s_hub_client, instance=vm),
                )
                for name, vm in state.items()
                if name == "tenant1"
            ]
            + [
                (
                    f"ComputeInstance {vm['id']}",
                    partial(stack.delete_vm, client=jwt_grpc_tenant2_admin, k8s_hub_client=k8s_hub_client, instance=vm),
                )
                for name, vm in state.items()
                if name == "tenant2"
            ],
            errors=[],
        )
        guest_ssh.remove_helpers_for(tenant1_default_network["vnet_id"])
        guest_ssh.remove_helpers_for(tenant2_default_network["vnet_id"])
