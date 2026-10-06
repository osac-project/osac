from __future__ import annotations

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.runner import env

pytestmark = pytest.mark.regression

TEST_RELEASE_IMAGE = env("OSAC_TEST_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.22.0-multi")


def test_cluster_create_rejected_without_disk_image_for_baremetal_workers(
    grpc: GRPCClient, private_grpc: GRPCClient, cluster_template: str
) -> None:
    """BM-backed clusters reject an explicitly selected ClusterVersion without a DiskImage."""
    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )
    version = private_grpc.ensure_cluster_version(
        version=unique_name("4.22.0-e2e-no-disk-image"), image=TEST_RELEASE_IMAGE
    )

    try:
        output, rc = grpc.call_unchecked(
            service="osac.public.v1.Clusters/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-cluster-no-disk-image")},
                    "spec": {
                        "template": {"name": cluster_template, "shared": True},
                        "version": {"name": version["name"], "shared": True},
                        "nodeSets": {"workers": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm"}}},
                    },
                }
            },
        )

        assert rc != 0, f"Expected Cluster create to reject a ClusterVersion without a DiskImage, got: {output}"
        assert "Code: FailedPrecondition" in output, f"Expected FailedPrecondition, got: {output}"
        assert version["name"] in output, f"Expected error for the selected ClusterVersion, got: {output}"
        assert "does not have a disk image attached" in output.lower(), (
            f"Expected shared ClusterVersion DiskImage validation error, got: {output}"
        )
    finally:
        private_grpc.call_unchecked(service="osac.private.v1.ClusterVersions/Delete", data={"id": version["id"]})
