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
    wait_for_tenant_condition,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import env

DEFAULT_IT_CORES: int = 2
DEFAULT_IT_MEMORY_GIB: int = 4

# Shared DiskImage source for CLI-based ComputeInstance scaffolding. Pinned (not
# :latest) to match the source_ref the gRPC AC tests provision and for CI stability.
DEFAULT_DISK_IMAGE_SOURCE_REF: str = "quay.io/containerdisks/fedora:41"


@pytest.fixture(scope="session")
def k8s_virt_client(namespace: str) -> K8sClient:
    vm_kubeconfig: str = os.environ["OSAC_VM_KUBECONFIG"]
    return K8sClient(namespace=namespace, kubeconfig=vm_kubeconfig)


@pytest.fixture(scope="session")
def vm_template() -> str:
    return env("OSAC_VM_TEMPLATE", "ocp-virt-vm")


@pytest.fixture(scope="session")
def test_run_id() -> str:
    """Unique ID for this test run to avoid resource name conflicts."""
    return str(uuid.uuid4())[:8]


@pytest.fixture(scope="session")
def default_networking(grpc: GRPCClient, k8s_hub_client: K8sClient, test_run_id: str) -> dict[str, str]:
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
        vn_id = grpc.create_virtual_network(name=vn_name, ipv4_cidr="10.200.0.0/16")
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        print(f"Waiting for VirtualNetwork {vn_cr_name} to become Ready...")
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)
        print(f"VirtualNetwork {vn_cr_name} is Ready")

        # Create subnet with unique name
        subnet_name = f"test-subnet-{test_run_id}"
        print(f"Creating Subnet: {subnet_name}")
        subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=vn_id, ipv4_cidr="10.200.100.0/24")
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
        name=it_name, cores=DEFAULT_IT_CORES, memory_gib=DEFAULT_IT_MEMORY_GIB, description="Default E2E instance type"
    )
    yield it_name
    try:
        private_grpc.delete_instance_type(name=it_name)
    except subprocess.CalledProcessError as e:
        output = ((e.stdout or "") + (e.stderr or "")).lower()
        if "not found" not in output:
            raise


@pytest.fixture(scope="session")
def default_disk_image(grpc: GRPCClient, test_run_id: str) -> Iterator[str]:
    """Create a default AVAILABLE Linux DiskImage for CLI-based VM tests; clean up after.

    Mirrors ``default_instance_type`` but diverges in two API-driven ways:
    DiskImages/Create is a PUBLIC API (uses ``grpc``, not ``private_grpc``), and a
    DiskImage carries its own UUID distinct from its name — so we capture the id
    returned at create and delete by id, while yielding the name the CLI's
    ``--disk-image`` flag needs.
    """
    di_name = f"e2e-default-di-{test_run_id}"
    di_id = grpc.create_disk_image(
        name=di_name,
        source_ref=DEFAULT_DISK_IMAGE_SOURCE_REF,
        guest_os_family="GUEST_OS_FAMILY_LINUX",
        architecture=["ARCHITECTURE_AMD64"],
    )
    yield di_name
    try:
        grpc.delete_disk_image(disk_image_id=di_id)
    except subprocess.CalledProcessError as e:
        output = ((e.stdout or "") + (e.stderr or "")).lower()
        # Tolerate only an already-deleted DiskImage; surface in-use/failedprecondition (leaked CI).
        if "not found" not in output:
            raise


@pytest.fixture(scope="session")
def default_storage_tier() -> str:
    """
    Reference installer-provided storage tier (matches network_class pattern).

    Defaults to "local" tier created by osac-installer when lvms.enabled=true.
    """
    return env("OSAC_STORAGE_TIER", "local")


@pytest.fixture(scope="session")
def additional_storage_tiers(private_grpc: GRPCClient, test_run_id: str) -> Iterator[dict[str, dict[str, str]]]:
    """
    Create additional storage tiers on the real backend for multi-tier tests.

    Uses the installer-provided "local" backend (real LVMS infrastructure).
    """
    # Get the real backend ID from the "local" tier
    local_tier = private_grpc.get_storage_tier(name="local")
    real_backend_id = local_tier["spec"]["backends"][0]["backendId"]

    created_tiers: dict[str, dict[str, str]] = {}

    try:
        # Create fast tier on REAL backend
        fast_name = f"e2e-tier-fast-{test_run_id}"
        fast_id = private_grpc.create_storage_tier(name=fast_name, backend_id=real_backend_id)
        created_tiers["fast"] = {"name": fast_name, "id": fast_id}

        # Create archive tier on REAL backend
        archive_name = f"e2e-tier-archive-{test_run_id}"
        archive_id = private_grpc.create_storage_tier(name=archive_name, backend_id=real_backend_id)
        created_tiers["archive"] = {"name": archive_name, "id": archive_id}

        yield created_tiers
    finally:
        # Cleanup tiers (but NOT the backend - it's real infrastructure)
        for tier_info in created_tiers.values():
            try:
                private_grpc.delete_storage_tier(tier_id=tier_info["id"])
            except subprocess.CalledProcessError as e:
                output = ((e.stdout or "") + (e.stderr or "")).lower()
                if "not found" not in output:
                    raise


@pytest.fixture(scope="session", autouse=True)
def _wait_for_tenant_storage_ready(k8s_hub_client: K8sClient, namespace: str) -> None:
    """Wait for ClusterStorageReady=True before any VMaaS tests if storage is configured.

    When storageFulfillment is enabled, the storage controller triggers AAP jobs to
    create per-tenant StorageClasses. Those jobs consume AAP executor slots. Compute
    instance provisioning jobs that run while storage jobs are still queued time out
    after 600s waiting for an executor. This fixture waits for storage setup to
    complete before tests begin, eliminating the queue contention.

    Polls for up to 30s to detect whether the ClusterStorageReady condition exists;
    if it never appears the environment has no storage configured and we skip the wait.
    """
    condition: str = ""
    for attempt in range(6):  # 6 x 5s sleeps, 7 reads total
        condition = k8s_hub_client.get_tenant_condition_status(
            name=namespace, condition_type="ClusterStorageReady", checked=False
        )
        if condition:
            break
        if attempt < 5:
            time.sleep(5)
    else:
        # Final read after the last sleep — avoids missing the condition
        # if it appears during the delay after the 6th check.
        time.sleep(5)
        condition = k8s_hub_client.get_tenant_condition_status(
            name=namespace, condition_type="ClusterStorageReady", checked=False
        )

    if not condition or condition == "True":
        return

    wait_for_tenant_condition(k8s=k8s_hub_client, name=namespace, condition_type="ClusterStorageReady")


@pytest.fixture(scope="session", autouse=True)
def _set_cli_default_instance_type(cli: OsacCLI, default_instance_type: str) -> None:
    """Wire the session-scoped default instance type into the shared CLI fixture."""
    cli.default_instance_type = default_instance_type


@pytest.fixture(scope="session", autouse=True)
def _set_cli_default_disk_image(cli: OsacCLI, default_disk_image: str) -> None:
    """Wire the session-scoped default disk image into the shared CLI fixture."""
    cli.default_disk_image = default_disk_image


@pytest.fixture(scope="session", autouse=True)
def _set_cli_default_storage_tier(cli: OsacCLI, default_storage_tier: str) -> None:
    """Wire the session-scoped default storage tier into the shared CLI fixture."""
    cli.default_storage_tier = default_storage_tier
