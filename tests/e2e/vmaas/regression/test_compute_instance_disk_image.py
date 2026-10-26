from __future__ import annotations

import subprocess

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    assert_grpc_rejected,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient

pytestmark = pytest.mark.regression

SOURCE_REF = "quay.io/containerdisks/fedora:41"


def test_compute_instance_with_disk_image(
    grpc: GRPCClient,
    vm_template: str,
    default_subnet: str,
    default_security_group: str,
    default_instance_type: str,
    k8s_hub_client: K8sClient,
    default_storage_tier: str,
) -> None:
    """AC-1 / TC-FR7-01: Create CI with DiskImage reference, verify VM runs with correct image."""
    di_name = unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None
    ci_name: str | None = None

    try:
        di_id = grpc.create_disk_image(
            name=di_name,
            source_ref=SOURCE_REF,
            guest_os_family="GUEST_OS_FAMILY_LINUX",
            architecture=["ARCHITECTURE_AMD64"],
        )
        assert di_id, "create_disk_image should return a non-empty ID"

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            security_group_ids=[default_security_group],
            instance_type=default_instance_type,
            name=unique_name("e2e-ci"),
            boot_disk_storage_tier=default_storage_tier,
        )
        ci_id = response["object"]["id"]
        assert ci_id, "create_compute_instance should return a non-empty ID"

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        ci_obj = grpc.get_compute_instance(ci_id=ci_id)
        assert ci_obj["object"]["spec"]["diskImage"]["id"] == di_id

        source_ref = k8s_hub_client.get_jsonpath(
            resource="computeinstance", name=ci_name, jsonpath="{.spec.image.sourceRef}"
        )
        assert source_ref == SOURCE_REF, f"CRD sourceRef should be {SOURCE_REF}, got {source_ref}"

        guest_os = k8s_hub_client.get_jsonpath(
            resource="computeinstance", name=ci_name, jsonpath="{.spec.guestOSFamily}"
        )
        assert guest_os == "linux", f"CRD guestOSFamily should be 'linux', got {guest_os}"
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_deletion_protection(
    grpc: GRPCClient,
    vm_template: str,
    default_subnet: str,
    default_security_group: str,
    default_instance_type: str,
    k8s_hub_client: K8sClient,
    default_storage_tier: str,
) -> None:
    """AC-5 / TC-FR12-01 + TC-FR12-04: Deletion protection lifecycle."""
    di_name = unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None
    ci_name: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            security_group_ids=[default_security_group],
            instance_type=default_instance_type,
            name=unique_name("e2e-ci"),
            boot_disk_storage_tier=default_storage_tier,
        )
        ci_id = response["object"]["id"]
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.delete_disk_image(disk_image_id=di_id)
        assert_grpc_rejected(exc_info, "FailedPrecondition")

        grpc.delete_compute_instance(ci_id=ci_id)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        ci_id = None

        deleted_di_id = di_id
        grpc.delete_disk_image(disk_image_id=di_id)
        di_id = None

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.get_disk_image(disk_image_id=deleted_di_id)
        assert_grpc_rejected(exc_info, "NotFound")
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)
