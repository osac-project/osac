from __future__ import annotations

import subprocess
import tempfile
import uuid
from collections.abc import Generator, Iterator
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import bmi_instance_type_for_tests, registered_ssh_key_secret
from tests.e2e.core.inventory import InventoryBackend, make_inventory_backend
from tests.e2e.core.k8s_client import K8sClient, bmh_kubeconfig
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env

BMI_DISK_IMAGE_SOURCE_REF = "oci://quay.io/osac-project/fedora-cloud-bmi:44"


@pytest.fixture(scope="session")
def bmi_template() -> str:
    return env("OSAC_BMI_TEMPLATE", "bm-host-provisioning")


@pytest.fixture(scope="session")
def bmi_instance_type(private_grpc: GRPCClient, bmi_template: str) -> Generator[str, None, None]:
    yield from bmi_instance_type_for_tests(
        private_grpc, template_name=bmi_template, configured=env("OSAC_BMI_INSTANCE_TYPE", "").strip()
    )


@pytest.fixture(scope="session")
def bmh_namespace() -> str:
    return env("OSAC_BMH_NAMESPACE", "host-inventory")


@pytest.fixture(scope="session")
def inventory_backend(k8s_hub_client: K8sClient, namespace: str) -> Generator[InventoryBackend, None, None]:
    """Inventory backend selected by OSAC_INVENTORY_BACKEND (default metal3).

    On netbox:
    - connection settings come from the deployed BMF configuration (Secrets
      in the OSAC namespace),
    - ``OSAC_NETBOX_*`` env vars are debug overrides only.
    """
    backend = make_inventory_backend(k8s=k8s_hub_client, namespace=namespace)
    yield backend
    backend.close()


@pytest.fixture(scope="session")
def inventory_hosts(inventory_backend: InventoryBackend) -> Iterator[None]:
    """Assert the pre-staged inventory has an available host.

    Both backends stage their inventory before the suite starts — never
    the tests or the OSAC operators:
    - metal3: BMHs enrolled by the lab/CI infrastructure (the netris
      Ansible backend) or the platform admin.
    - netbox: devices staged by the provider reconcile
      (setup-virtual-netbox-devices.sh).
    """
    inventory_backend.ensure_available_hosts()
    yield


@pytest.fixture(scope="session")
def k8s_bmh_client(namespace: str, k8s_hub_client: K8sClient) -> K8sClient:
    """Kubernetes client for the cluster that owns the BareMetalHost inventory.

    OSAC_BMH_KUBECONFIG identifies it explicitly; in a vcluster run the hub is
    the vcluster while the BMH inventory stays on the outer cluster, so this is
    the outer kubeconfig. Unset keeps the hub client, which is how a
    single-cluster installation runs.
    """
    kubeconfig: str | None = bmh_kubeconfig()
    if kubeconfig is None:
        return k8s_hub_client
    return K8sClient(namespace=namespace, kubeconfig=kubeconfig)


@pytest.fixture(scope="session")
def test_run_id() -> str:
    return str(uuid.uuid4())[:8]


@pytest.fixture(scope="session")
def ssh_public_key() -> Generator[str, None, None]:
    with tempfile.TemporaryDirectory() as tmpdir:
        key_path = Path(tmpdir) / "bmi-test-key"
        subprocess.run(
            ["ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "bmi-e2e-test"],
            capture_output=True,
            check=True,
        )
        yield (key_path.with_suffix(".pub")).read_text().strip()


@pytest.fixture(scope="session")
def bmi_disk_image(private_grpc: GRPCClient, test_run_id: str) -> Generator[str, None, None]:
    """Create a default DiskImage for BMaaS E2E tests and clean it up afterward."""
    di_name = f"e2e-bmi-di-{test_run_id}"
    di_id = private_grpc.create_disk_image(
        name=di_name,
        source_ref=BMI_DISK_IMAGE_SOURCE_REF,
        architecture=["ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64"],
        api=PRIVATE_API,
    )

    yield di_name

    try:
        private_grpc.delete_disk_image(disk_image_id=di_id, api=PRIVATE_API)
    except Exception as e:
        print(f"WARNING: Failed to delete disk image {di_id}: {e}")


@pytest.fixture(scope="session")
def ssh_key_secret(jwt_cli_user: OsacCLI, ssh_public_key: str, test_run_id: str) -> Iterator[str]:
    """Register the generated public key as the tenant Secret used by BMaaS tests."""
    with registered_ssh_key_secret(jwt_cli_user, ssh_public_key, f"e2e-bmaas-key-{test_run_id}") as name:
        yield name


@pytest.fixture(scope="session")
def catalog_item(
    grpc: GRPCClient,
    bmi_template: str,
    bmi_instance_type: str,
    test_run_id: str,
    bmi_disk_image: str,
    ssh_key_secret: str,
) -> Generator[str, None, None]:
    name = f"e2e-bmaas-{test_run_id}"
    print(f"\nCreating BareMetalInstanceCatalogItem: {name}")
    item_id: str = grpc.create_baremetal_instance_catalog_item(
        name=name,
        title=f"E2E BMaaS Test ({test_run_id})",
        description="Temporary catalog item for BMaaS E2E tests",
        template=bmi_template,
        fields={
            "ssh_key": {"editable": {"default_value": {"name": ssh_key_secret}}},
            "disk_image": {"editable": {"default_value": {"name": bmi_disk_image}}},
            "instance_type": {"editable": {"default_value": {"name": bmi_instance_type, "shared": True}}},
        },
    )
    print(f"CatalogItem created: {item_id}")

    yield item_id

    try:
        print(f"\nDeleting BareMetalInstanceCatalogItem {item_id}...")
        grpc.delete_baremetal_instance_catalog_item(item_id=item_id)
        print(f"CatalogItem {item_id} deleted")
    except Exception as e:
        print(f"WARNING: Failed to delete catalog item {item_id}: {e}")
