from __future__ import annotations

import subprocess
import tempfile
import uuid
from collections.abc import Generator
from pathlib import Path

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.runner import env


@pytest.fixture(scope="session")
def bmi_template() -> str:
    return env("OSAC_BMI_TEMPLATE", "osac.templates.bm_host_provisioning")


@pytest.fixture(scope="session")
def bmh_namespace() -> str:
    return env("OSAC_BMH_NAMESPACE", "host-inventory")


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
def catalog_item(private_grpc: GRPCClient, bmi_template: str, test_run_id: str) -> Generator[str, None, None]:
    name = f"e2e-bmaas-{test_run_id}"
    print(f"\nCreating BareMetalInstanceCatalogItem: {name}")
    item_id: str = private_grpc.create_baremetal_instance_catalog_item(
        name=name,
        title=f"E2E BMaaS Test ({test_run_id})",
        description="Temporary catalog item for BMaaS E2E tests",
        template=bmi_template,
        field_definitions=[{"path": "ssh_public_key", "display_name": "SSH Public Key", "editable": True}],
    )
    print(f"CatalogItem created: {item_id}")

    yield item_id

    try:
        print(f"\nDeleting BareMetalInstanceCatalogItem {item_id}...")
        private_grpc.delete_baremetal_instance_catalog_item(item_id=item_id)
        print(f"CatalogItem {item_id} deleted")
    except Exception as e:
        print(f"WARNING: Failed to delete catalog item {item_id}: {e}")
