from __future__ import annotations

import subprocess

import pytest

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import assert_grpc_rejected, wait_for_grpc_removal

SOURCE_REF = "quay.io/containerdisks/fedora:41"


def test_catalog_item_disk_image_default_applied(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    """AC-2 / TC-FR9-01: CatalogItem disk_image field_definition default is applied to the ComputeInstance."""
    di_id: str | None = None
    catalog_item_id: str | None = None
    ci_id: str | None = None

    try:
        di_name = unique_name("e2e-cidi-di")
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        # The default references the DiskImage by NAME; the apply path is the prefix-less
        # "disk_image", which the server interprets as a DiskImage name.
        # network_attachments must also be declared: with field_definitions present, the server
        # (applyFieldDefinitions) allowlists only catalog_item, template, and the declared fd
        # paths, then rejects any other spec leaf. The CI-create below sends network_attachments
        # (the subnet), so it must be a declared field or the create is rejected InvalidArgument.
        field_defs = [
            {"path": "disk_image", "display_name": "Disk Image", "editable": True, "default": di_name},
            {"path": "network_attachments", "display_name": "Network", "editable": True},
        ]
        catalog_item_id = grpc.create_compute_instance_catalog_item(
            name=unique_name("e2e-cidi-cat"),
            template=compute_instance_template,
            published=True,
            field_definitions=field_defs,
        )

        # disk_image is deliberately omitted — it must be inherited from the catalog default.
        ci_id = grpc.create_compute_instance(
            name=unique_name("e2e-cidi-ci"), catalog_item=catalog_item_id, subnet_ids=[default_subnet_id]
        )

        ci = grpc.get_compute_instance(ci_id=ci_id)
        disk_image_ref = ci["object"]["spec"].get("diskImage", {})
        assert disk_image_ref.get("id") == di_id, (
            f"CI should inherit disk_image from the catalog default, got: {disk_image_ref}"
        )
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if catalog_item_id is not None:
            grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_deletion_protection_catalog_item(grpc: GRPCClient, compute_instance_template: str) -> None:
    """AC-4 / TC-FR12-03: Cannot delete a DiskImage referenced by a CatalogItem field_definition."""
    di_id: str | None = None
    catalog_item_id: str | None = None

    try:
        di_name = unique_name("e2e-cidi-di")
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        # Same field_definition shape as the default-application test: prefix-less
        # "disk_image" + DiskImage name.
        field_defs = [
            {"path": "disk_image", "display_name": "Disk Image", "editable": True, "default": di_name}
        ]
        catalog_item_id = grpc.create_compute_instance_catalog_item(
            name=unique_name("e2e-cidi-cat"),
            template=compute_instance_template,
            published=True,
            field_definitions=field_defs,
        )

        # Deletion is blocked while the catalog item references the disk image.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.delete_disk_image(disk_image_id=di_id)
        assert_grpc_rejected(exc_info, "FailedPrecondition")
        combined = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "catalog item" in combined.lower(), (
            f"Error should identify the referencing catalog item, got: {combined.strip()}"
        )

        # Remove the referrer, then deletion succeeds — protection is reference-bound, not permanent.
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        catalog_item_id = None
        deleted_di_id = di_id
        grpc.delete_disk_image(disk_image_id=di_id)
        di_id = None

        # A successful Delete RPC does not prove absence — confirm the DiskImage is actually gone.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.get_disk_image(disk_image_id=deleted_di_id)
        assert_grpc_rejected(exc_info, "NotFound")
    finally:
        if catalog_item_id is not None:
            grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)
