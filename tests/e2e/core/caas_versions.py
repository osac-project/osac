"""DiskImage-backed ClusterVersion selection for positive bare-metal CaaS E2E tests."""

from __future__ import annotations

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.runner import env


def ensure_caas_disk_image_version(private_grpc: GRPCClient) -> str:
    """Resolve a configured version or idempotently provision a backed E2E version.

    Do not repair caller-supplied versions: a bad override is a configuration error.
    The stable fixture version is separate from intentional no-DiskImage negative cases.
    """
    configured = env("OSAC_CLUSTER_VERSION", "")
    if configured:
        version = private_grpc.get_cluster_version(version_id=configured)["object"]
        disk_image = version.get("spec", {}).get("diskImage") or {}
        if not (disk_image.get("id") or disk_image.get("name")):
            raise ValueError(f"OSAC_CLUSTER_VERSION={configured!r} must reference a ClusterVersion with a DiskImage")
        return version["metadata"]["name"]

    disk_image_id = private_grpc.ensure_disk_image(
        name="rhcos-4-22", source_ref=env("OSAC_RHCOS_BMI_IMAGE", "oci://quay.io/rh_ee_rpiccoli/rhcos-bmi:4.22.0")
    )
    version = private_grpc.ensure_cluster_version(
        version="4.22.0-e2e-references",
        image=env("OSAC_TEST_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.22.0-multi"),
        disk_image=disk_image_id,
    )
    return version["name"]
