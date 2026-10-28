from __future__ import annotations

import json

import pytest

from tests.e2e.bmaas.conftest import BMI_DISK_IMAGE_SOURCE_REF
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient

pytestmark = pytest.mark.sanity


def test_catalog_blocks_disk_image_architecture_removal(
    private_grpc: GRPCClient, catalog_item: str, bmi_disk_image: str
) -> None:
    images = private_grpc.call(
        service=f"{PRIVATE_API}.DiskImages/List", data={"filter": f"this.metadata.name == {json.dumps(bmi_disk_image)}"}
    ).get("items", [])
    assert len(images) == 1
    image = images[0]
    remaining = ["ARCHITECTURE_ARM64"]
    assert set(image["spec"]["architecture"]) == {"ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64"}
    update = {
        "object": {"id": image["id"], "spec": {"architecture": remaining}},
        "updateMask": {"paths": ["spec.architecture"]},
    }
    output, code = private_grpc.call_unchecked(service=f"{PRIVATE_API}.DiskImages/Update", data=update)
    assert code != 0, "An architecture needed by the catalog was removed"
    if "FailedPrecondition" not in output or "DISK_IMAGE_ARCHITECTURE_IN_USE" not in output:
        pytest.fail("Unexpected catalog architecture dependency error")
    if catalog_item in output or BMI_DISK_IMAGE_SOURCE_REF in output:
        pytest.fail("Dependency error exposed a dependent identity or image source")
    unchanged = private_grpc.call(service=f"{PRIVATE_API}.DiskImages/Get", data={"id": image["id"]})["object"]
    assert set(unchanged["spec"]["architecture"]) == set(image["spec"]["architecture"])
