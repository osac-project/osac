from __future__ import annotations

import subprocess
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import assert_grpc_rejected

SOURCE_REF = "quay.io/containerdisks/fedora:41"


def _unique_name(prefix: str = "e2e-di") -> str:
    return f"{prefix}-{uuid4().hex[:8]}"


def test_disk_image_crud_provider_admin(grpc: GRPCClient) -> None:
    """AC-1: Provider Admin registers a global DiskImage, lists and gets it.
    AC-7: Delete DiskImage succeeds when unreferenced."""
    di_id: str | None = None

    try:
        di_id = grpc.create_disk_image(
            name=_unique_name(),
            source_ref=SOURCE_REF,
            guest_os_family="GUEST_OS_FAMILY_LINUX",
            architecture=["ARCHITECTURE_AMD64"],
        )
        assert di_id, "create_disk_image should return a non-empty ID"

        assert di_id in grpc.list_disk_image_ids(), "DiskImage should appear in list after create"

        obj = grpc.get_disk_image(disk_image_id=di_id)
        spec = obj["object"]["spec"]
        assert spec["sourceRef"] == SOURCE_REF
        assert spec["guestOsFamily"] == "GUEST_OS_FAMILY_LINUX"
        assert "ARCHITECTURE_AMD64" in spec["architecture"]
        assert spec.get("lifecycle", "DISK_IMAGE_LIFECYCLE_AVAILABLE") == "DISK_IMAGE_LIFECYCLE_AVAILABLE"

        deleted_id = di_id
        grpc.delete_disk_image(disk_image_id=di_id)
        di_id = None

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.get_disk_image(disk_image_id=deleted_id)
        assert_grpc_rejected(exc_info, "NotFound")
    finally:
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_tenant_admin(jwt_grpc_tenant1_admin: GRPCClient) -> None:
    """AC-2: Tenant Admin registers a tenant-scoped DiskImage."""
    di_id: str | None = None

    try:
        di_id = jwt_grpc_tenant1_admin.create_disk_image(
            name=_unique_name(), source_ref=SOURCE_REF, architecture=["ARCHITECTURE_AMD64"]
        )
        assert di_id, "Tenant Admin should be able to create a tenant-scoped DiskImage"

        obj = jwt_grpc_tenant1_admin.get_disk_image(disk_image_id=di_id)
        tenant = obj["object"].get("metadata", {}).get("tenant", "")
        assert tenant, "Tenant-scoped DiskImage should have metadata.tenant set"

        assert di_id in jwt_grpc_tenant1_admin.list_disk_image_ids(), (
            "Tenant Admin should see their own DiskImage in list"
        )
    finally:
        if di_id is not None:
            jwt_grpc_tenant1_admin.delete_disk_image(disk_image_id=di_id)


def test_disk_image_tenant_user(jwt_grpc_tenant1: GRPCClient) -> None:
    """AC-3: Tenant User registers a tenant-scoped DiskImage."""
    di_id: str | None = None

    try:
        di_id = jwt_grpc_tenant1.create_disk_image(
            name=_unique_name(), source_ref=SOURCE_REF, architecture=["ARCHITECTURE_AMD64"]
        )
        assert di_id, "Tenant User should be able to create a tenant-scoped DiskImage"

        obj = jwt_grpc_tenant1.get_disk_image(disk_image_id=di_id)
        tenant = obj["object"].get("metadata", {}).get("tenant", "")
        assert tenant, "Tenant-scoped DiskImage should have metadata.tenant set"
    finally:
        if di_id is not None:
            jwt_grpc_tenant1.delete_disk_image(disk_image_id=di_id)


def test_disk_image_deprecation_lifecycle(grpc: GRPCClient) -> None:
    """AC-4: Deprecation workflow — deprecate, verify listing, obsolete, verify hidden."""
    di_id: str | None = None

    try:
        di_id = grpc.create_disk_image(name=_unique_name(), source_ref=SOURCE_REF)

        # Deprecate
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED")
        obj = grpc.get_disk_image(disk_image_id=di_id)
        spec = obj["object"]["spec"]
        assert spec["lifecycle"] == "DISK_IMAGE_LIFECYCLE_DEPRECATED"
        assert spec.get("deprecation", {}).get("deprecationTimestamp"), (
            "deprecation_timestamp should be auto-set on deprecation"
        )

        # Deprecated images still visible in default list
        assert di_id in grpc.list_disk_image_ids(), "DEPRECATED DiskImage should still appear in default list"

        # Obsolete
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_OBSOLETE")
        obj = grpc.get_disk_image(disk_image_id=di_id)
        spec = obj["object"]["spec"]
        assert spec["lifecycle"] == "DISK_IMAGE_LIFECYCLE_OBSOLETE"
        assert spec.get("deprecation", {}).get("obsolescenceTimestamp"), (
            "obsolescence_timestamp should be auto-set on obsolescence"
        )

        # Obsolete images hidden from default list
        assert di_id not in grpc.list_disk_image_ids(), "OBSOLETE DiskImage should be excluded from default list"
    finally:
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_reactivation(grpc: GRPCClient) -> None:
    """AC-5: Reactivation — obsolete image reactivated, visible in default list again."""
    di_id: str | None = None

    try:
        di_id = grpc.create_disk_image(name=_unique_name(), source_ref=SOURCE_REF)

        # Drive to OBSOLETE
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED")
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_OBSOLETE")
        assert di_id not in grpc.list_disk_image_ids(), "OBSOLETE should be hidden"

        # Reactivate
        grpc.update_disk_image_lifecycle(disk_image_id=di_id, lifecycle="DISK_IMAGE_LIFECYCLE_AVAILABLE")
        obj = grpc.get_disk_image(disk_image_id=di_id)
        spec = obj["object"]["spec"]
        assert spec["lifecycle"] == "DISK_IMAGE_LIFECYCLE_AVAILABLE"
        dep = spec.get("deprecation", {})
        assert not dep.get("deprecationTimestamp") and not dep.get("obsolescenceTimestamp"), (
            "deprecation timestamps should be cleared on reactivation"
        )

        assert di_id in grpc.list_disk_image_ids(), "Reactivated DiskImage should be visible in default list"
    finally:
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_disk_image_tenant_isolation(jwt_grpc_tenant1: GRPCClient, jwt_grpc_tenant2: GRPCClient) -> None:
    """AC-6: Tenant isolation — Tenant B cannot see/access Tenant A's images."""
    di_id: str | None = None

    try:
        di_id = jwt_grpc_tenant1.create_disk_image(name=_unique_name(), source_ref=SOURCE_REF)

        # Tenant 1 can see it
        assert di_id in jwt_grpc_tenant1.list_disk_image_ids(), "Tenant 1 should see its own DiskImage"

        # Tenant 2 cannot see it in list
        assert di_id not in jwt_grpc_tenant2.list_disk_image_ids(), (
            "Tenant 2 should NOT see Tenant 1's DiskImage in list"
        )

        # Tenant 2 cannot get it by ID
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            jwt_grpc_tenant2.get_disk_image(disk_image_id=di_id)
        assert_grpc_rejected(exc_info, "NotFound")
    finally:
        if di_id is not None:
            jwt_grpc_tenant1.delete_disk_image(disk_image_id=di_id)


def test_disk_image_obsolete_filtered_list(grpc: GRPCClient) -> None:
    """AC-4: OBSOLETE images excluded from default list but visible with explicit filter."""
    di_available_id: str | None = None
    di_obsolete_id: str | None = None

    try:
        di_available_id = grpc.create_disk_image(name=_unique_name("e2e-di-avail"), source_ref=SOURCE_REF)
        di_obsolete_id = grpc.create_disk_image(name=_unique_name("e2e-di-obs"), source_ref=SOURCE_REF)

        grpc.update_disk_image_lifecycle(disk_image_id=di_obsolete_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED")
        grpc.update_disk_image_lifecycle(disk_image_id=di_obsolete_id, lifecycle="DISK_IMAGE_LIFECYCLE_OBSOLETE")

        # Default list: available visible, obsolete hidden
        default_ids = grpc.list_disk_image_ids()
        assert di_available_id in default_ids, "AVAILABLE DiskImage should be in default list"
        assert di_obsolete_id not in default_ids, "OBSOLETE DiskImage should be excluded from default list"

        # Explicit filter: obsolete visible
        # 3 = DISK_IMAGE_LIFECYCLE_OBSOLETE (CEL uses proto enum numeric values)
        obsolete_ids = grpc.list_disk_image_ids(filter_expr="this.spec.lifecycle == 3")
        assert di_obsolete_id in obsolete_ids, "OBSOLETE DiskImage should be visible with explicit lifecycle filter"
    finally:
        if di_obsolete_id is not None:
            grpc.delete_disk_image(disk_image_id=di_obsolete_id)
        if di_available_id is not None:
            grpc.delete_disk_image(disk_image_id=di_available_id)
