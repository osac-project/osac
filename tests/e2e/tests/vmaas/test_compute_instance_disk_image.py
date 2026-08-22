from __future__ import annotations

import subprocess
from typing import Any
from uuid import uuid4

import pytest

from tests.core.grpc_client import PUBLIC_API, GRPCClient
from tests.core.helpers import (
    assert_grpc_rejected,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient

SOURCE_REF = "quay.io/containerdisks/fedora:41"


def _unique_name(prefix: str = "e2e-cidi") -> str:
    return f"{prefix}-{uuid4().hex[:8]}"


def test_compute_instance_with_disk_image(
    grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str, k8s_hub_client: K8sClient
) -> None:
    """AC-1 / TC-FR7-01: Create CI with DiskImage reference, verify VM runs with correct image."""
    di_name = _unique_name("e2e-di")
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
            instance_type=default_instance_type,
            name=_unique_name("e2e-ci"),
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


def test_obsolete_disk_image_blocks_creation(
    grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str
) -> None:
    """AC-2 / TC-FR7-04: OBSOLETE DiskImage blocks ComputeInstance creation."""
    di_name = _unique_name("e2e-di")
    di_id: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED")
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_OBSOLETE")

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_compute_instance_with_disk_image(
                template=vm_template,
                disk_image_name=di_name,
                subnet_ids=[default_subnet],
                instance_type=default_instance_type,
            )
        assert_grpc_rejected(exc_info, "FailedPrecondition")
    finally:
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_deprecated_disk_image_allows_creation_with_warning(
    grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str
) -> None:
    """AC-3 / TC-FR7-05: DEPRECATED DiskImage allows creation with warning."""
    di_name = _unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED")

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            instance_type=default_instance_type,
            name=_unique_name("e2e-ci"),
        )
        ci_id = response["object"]["id"]
        assert ci_id, "CI creation with DEPRECATED DiskImage should succeed"

        warnings: list[str] = response.get("warnings", [])
        assert any("deprecated" in w.lower() for w in warnings), (
            f"Response should contain deprecation warning, got: {warnings}"
        )
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_template_disk_image_default(
    grpc: GRPCClient, private_grpc: GRPCClient, default_subnet: str, default_instance_type: str
) -> None:
    """AC-4 / TC-FR8-01: Template disk_image default applied when user omits disk_image."""
    di_name = _unique_name("e2e-di")
    di_id: str | None = None
    template_id: str | None = None
    ci_id: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        template_name = _unique_name("e2e-tmpl")
        template_id = private_grpc.create_compute_instance_template(
            name=template_name,
            title="E2E DiskImage default test",
            description="Template with disk_image in spec_defaults",
            spec_defaults={"disk_image": {"name": di_name}},
        )

        attachments = [{"subnet": {"id": default_subnet}}]
        # disk_image is deliberately omitted here — it must be inherited from the
        # template's spec_defaults. boot_disk/run_strategy are supplied directly
        # because the custom template intentionally defaults only disk_image.
        spec: dict[str, Any] = {
            "template": {"name": template_name},
            "instance_type": {"name": default_instance_type},
            "network_attachments": attachments,
            "boot_disk": {"size_gib": 20},
            "run_strategy": "Always",
        }
        response = grpc.call(
            service=f"{PUBLIC_API}.ComputeInstances/Create",
            data={"object": {"metadata": {"name": _unique_name("e2e-ci")}, "spec": spec}},
        )
        ci_id = response["object"]["id"]
        assert ci_id, "CI creation from template should succeed"

        ci_obj = grpc.get_compute_instance(ci_id=ci_id)
        disk_image_ref = ci_obj["object"]["spec"].get("diskImage", {})
        assert disk_image_ref.get("id") == di_id, (
            f"CI should inherit disk_image from template default, got: {disk_image_ref}"
        )
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if template_id is not None:
            private_grpc.delete_compute_instance_template(template_id=template_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_deletion_protection(
    grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str, k8s_hub_client: K8sClient
) -> None:
    """AC-5 / TC-FR12-01 + TC-FR12-04: Deletion protection lifecycle."""
    di_name = _unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None
    ci_name: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            instance_type=default_instance_type,
            name=_unique_name("e2e-ci"),
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
