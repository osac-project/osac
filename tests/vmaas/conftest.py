from __future__ import annotations

import os
import subprocess
import time
import uuid
from collections.abc import Iterator

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import env

DEFAULT_IT_CORES: int = 2
DEFAULT_IT_MEMORY_GIB: int = 4


@pytest.fixture(scope="session")
def k8s_virt_client(namespace: str) -> K8sClient:
    vm_kubeconfig: str = os.environ["OSAC_VM_KUBECONFIG"]
    return K8sClient(namespace=namespace, kubeconfig=vm_kubeconfig)


@pytest.fixture(scope="session")
def vm_template() -> str:
    return env("OSAC_VM_TEMPLATE", "osac.templates.ocp_virt_vm")


@pytest.fixture(scope="session")
def network_class() -> str:
    return env("OSAC_NETWORK_CLASS", "cudn_net")


@pytest.fixture(scope="session")
def test_run_id() -> str:
    """Unique ID for this test run to avoid resource name conflicts."""
    return str(uuid.uuid4())[:8]


@pytest.fixture(scope="session")
def default_networking(
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    network_class: str,
    test_run_id: str,
) -> dict[str, str]:
    """
    Create default networking resources (VirtualNetwork + Subnet) for VM tests.

    Uses unique names per test run to avoid conflicts with leftover resources
    from interrupted previous runs.

    Returns:
        dict with keys: 'virtual_network_id', 'subnet_id'
    """
    # Track created resources for cleanup on setup failure
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None

    try:
        # Create virtual network with unique name
        vn_name = f"test-vn-{test_run_id}"
        print(f"\nCreating VirtualNetwork: {vn_name}")
        vn_id = grpc.create_virtual_network(
            name=vn_name,
            network_class=network_class,
            ipv4_cidr="10.200.0.0/16",
        )
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        print(f"Waiting for VirtualNetwork {vn_cr_name} to become Ready...")
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)
        print(f"VirtualNetwork {vn_cr_name} is Ready")

        # Create subnet with unique name
        subnet_name = f"test-subnet-{test_run_id}"
        print(f"Creating Subnet: {subnet_name}")
        subnet_id = grpc.create_subnet(
            name=subnet_name,
            virtual_network=vn_id,
            ipv4_cidr="10.200.100.0/24",
        )
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        print(f"Waiting for Subnet {subnet_cr_name} to become Ready...")
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)
        print(f"Subnet {subnet_cr_name} is Ready")

        yield {
            "virtual_network_id": vn_id,
            "virtual_network_cr_name": vn_cr_name,
            "subnet_id": subnet_id,
            "subnet_cr_name": subnet_cr_name,
        }
    except Exception:
        # If setup fails, cleanup any resources that were created
        print(f"\nSetup failed, cleaning up partial resources: {test_run_id}")
        if subnet_id and subnet_cr_name:
            try:
                grpc.delete_subnet(subnet_id=subnet_id)
            except Exception as e:
                print(f"WARNING: Failed to cleanup subnet {subnet_id}: {e}")
        if vn_id and vn_cr_name:
            try:
                grpc.delete_virtual_network(vn_id=vn_id)
            except Exception as e:
                print(f"WARNING: Failed to cleanup virtual network {vn_id}: {e}")
        raise  # Re-raise original exception
    finally:
        # Normal cleanup runs regardless of setup success/failure
        # Only attempt cleanup if resources were successfully created
        if vn_id and vn_cr_name and subnet_id and subnet_cr_name:
            print(f"\nCleaning up test networking resources: {test_run_id}")

            # Delete subnet first
            try:
                print(f"Deleting Subnet {subnet_id}...")
                grpc.delete_subnet(subnet_id=subnet_id)
                wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr_name)
                print(f"Subnet {subnet_id} deleted")
            except Exception as e:
                print(f"WARNING: Failed to delete subnet {subnet_id}: {e}")

            # Delete virtual network
            try:
                print(f"Deleting VirtualNetwork {vn_id}...")
                grpc.delete_virtual_network(vn_id=vn_id)
                wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr_name)
                print(f"VirtualNetwork {vn_id} deleted")
            except Exception as e:
                print(f"WARNING: Failed to delete virtual network {vn_id}: {e}")


@pytest.fixture(scope="session")
def default_subnet(default_networking: dict[str, str]) -> str:
    """Convenience fixture that returns just the subnet ID (for CLI/gRPC API usage)."""
    return default_networking["subnet_id"]


@pytest.fixture(scope="session")
def default_subnet_ref(default_networking: dict[str, str]) -> str:
    """Convenience fixture that returns the subnet CR name (for K8s API usage)."""
    return default_networking["subnet_cr_name"]


@pytest.fixture(scope="session")
def default_instance_type(private_grpc: GRPCClient, test_run_id: str) -> Iterator[str]:
    """Create a default ACTIVE instance type for VM tests; clean up after."""
    it_name = f"e2e-default-it-{test_run_id}"
    private_grpc.create_instance_type(
        name=it_name,
        cores=DEFAULT_IT_CORES,
        memory_gib=DEFAULT_IT_MEMORY_GIB,
        description="Default E2E instance type",
    )
    yield it_name
    try:
        private_grpc.delete_instance_type(name=it_name)
    except subprocess.CalledProcessError as e:
        output = ((e.stdout or "") + (e.stderr or "")).lower()
        if "not found" not in output:
            raise


@pytest.fixture(scope="session", autouse=True)
def _set_cli_default_instance_type(cli: OsacCLI, default_instance_type: str) -> None:
    """Wire the session-scoped default instance type into the shared CLI fixture."""
    cli.default_instance_type = default_instance_type
