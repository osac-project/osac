from __future__ import annotations

import base64
from typing import Any

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cr, wait_for_deletion, wait_for_provision, wait_for_running
from tests.k8s_client import K8sClient

TEST_CORES: int = 2
TEST_MEMORY_GIB: int = 4
TEST_BOOT_DISK_SIZE: int = 20
TEST_IMAGE: str = "quay.io/containerdisks/fedora:latest"
TEST_IMAGE_SOURCE_TYPE: str = "registry"
TEST_RUN_STRATEGY: str = "Always"
TEST_USER_DATA: str = "#cloud-config\npackages:\n  - vim\n"


def test_compute_instance_cli_explicit_fields(cli: FulfillmentCLI, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    uuid: str = cli.create_compute_instance(
        template="osac.templates.ocp_virt_vm",
        cores=TEST_CORES,
        memory_gib=TEST_MEMORY_GIB,
        boot_disk_size=TEST_BOOT_DISK_SIZE,
        image=TEST_IMAGE,
        image_source_type=TEST_IMAGE_SOURCE_TYPE,
        run_strategy=TEST_RUN_STRATEGY,
        user_data_secret_ref=TEST_USER_DATA,
    )

    assert uuid in grpc.list_compute_instance_ids()

    ci_name: str = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

    ci_spec: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
    spec: dict[str, Any] = ci_spec["spec"]
    assert spec["cores"] == TEST_CORES, f"cores mismatch: {spec['cores']} != {TEST_CORES}"
    assert spec["memoryGiB"] == TEST_MEMORY_GIB, f"memoryGiB mismatch: {spec['memoryGiB']} != {TEST_MEMORY_GIB}"
    assert spec["bootDisk"]["sizeGiB"] == TEST_BOOT_DISK_SIZE, (
        f"bootDisk.sizeGiB mismatch: {spec['bootDisk']['sizeGiB']} != {TEST_BOOT_DISK_SIZE}"
    )
    assert spec["image"]["sourceRef"] == TEST_IMAGE, f"image.sourceRef mismatch: {spec['image']['sourceRef']}"
    assert spec["runStrategy"] == TEST_RUN_STRATEGY, f"runStrategy mismatch: {spec['runStrategy']}"

    expected_secret_name: str = f"{uuid}-user-data"
    assert spec["userDataSecretRef"]["name"] == expected_secret_name, (
        f"userDataSecretRef.name mismatch: {spec['userDataSecretRef']['name']} != {expected_secret_name}"
    )

    secret: dict[str, Any] = k8s_hub_client.get_json(resource="secret", name=expected_secret_name)
    expected_b64: str = base64.b64encode(TEST_USER_DATA.encode()).decode()
    assert secret["data"]["userdata"] == expected_b64, "User data Secret content does not match"

    owner_refs: list[dict[str, Any]] = secret["metadata"]["ownerReferences"]
    assert len(owner_refs) > 0, "Secret missing ownerReferences"
    assert owner_refs[0]["kind"] == "ComputeInstance"
    assert owner_refs[0]["name"] == ci_name

    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)

    cli.delete_compute_instance(uuid=uuid)
    wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
