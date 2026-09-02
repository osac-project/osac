from __future__ import annotations

import subprocess
from typing import Any

import pytest

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected, wait_for_cr, wait_for_deletion, wait_for_provision
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI


def verify_datavolume_storage_classes(
    k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, ci_name: str, cr: dict[str, Any]
) -> None:
    """Verify that DataVolumes were created with correct StorageClasses.

    NOTE: Commented out in all tests until osac PR #257 (OSAC-3632) merges.
    AAP currently uses global _requested_storage_tier and ignores per-disk storageTier fields.
    After PR #257 merges, uncomment the verify_datavolume_storage_classes() calls in tests.
    """
    _ = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)

    # Verify boot disk
    boot_tier = cr["spec"]["bootDisk"]["storageTier"]
    expected_boot_sc = k8s_hub_client.get_storage_class_for_tier(tier_name=boot_tier)
    boot_dv_name = f"{ci_name}-root-disk"
    actual_boot_sc = k8s_virt_client.get_datavolume_storage_class(name=boot_dv_name)
    assert actual_boot_sc == expected_boot_sc, (
        f"Boot disk StorageClass mismatch: {actual_boot_sc!r} != {expected_boot_sc!r}"
    )

    # Verify additional disks (1-indexed)
    for idx, disk in enumerate(cr.get("spec", {}).get("additionalDisks", [])):
        tier = disk["storageTier"]
        expected_sc = k8s_hub_client.get_storage_class_for_tier(tier_name=tier)

        dv_name = f"{ci_name}-disk-{idx + 1}"
        actual_sc = k8s_virt_client.get_datavolume_storage_class(name=dv_name)
        assert actual_sc == expected_sc, (
            f"Additional disk {idx} StorageClass mismatch: {actual_sc!r} != {expected_sc!r}"
        )


def test_compute_instance_explicit_boot_disk_tier(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    default_instance_type: str,
) -> None:
    """Verify that explicit boot disk storage tier is persisted correctly."""
    name = unique_name("e2e-ci-tier")
    uuid = cli.create_compute_instance(
        name=name,
        template=vm_template,
        instance_type=default_instance_type,
        network_attachments=[{"subnet": default_subnet}],
        boot_disk_storage_tier=default_storage_tier,
    )

    ci_name = None
    try:
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)

        # Verify CR has the correct storage tier
        cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        assert cr["spec"]["bootDisk"]["storageTier"] == default_storage_tier, (
            f"Boot disk tier mismatch: {cr['spec']['bootDisk']['storageTier']!r} != {default_storage_tier!r}"
        )

        # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
        # verify_datavolume_storage_classes(
        #     k8s_hub_client=k8s_hub_client,
        #     k8s_virt_client=k8s_virt_client,
        #     ci_name=ci_name,
        #     cr=cr,
        # )
    finally:
        grpc.delete_compute_instance(ci_id=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)


def test_compute_instance_multiple_disks_different_tiers(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
) -> None:
    """Verify that boot disk and additional disks can use different storage tiers independently."""
    fast_tier = additional_storage_tiers["fast"]["name"]
    archive_tier = additional_storage_tiers["archive"]["name"]

    name = unique_name("e2e-ci-multi-tier")
    uuid = cli.create_compute_instance(
        name=name,
        template=vm_template,
        instance_type=default_instance_type,
        network_attachments=[{"subnet": default_subnet}],
        boot_disk_storage_tier=fast_tier,
        additional_disks=[
            {"size_gib": 1, "storage_tier": default_storage_tier},
            {"size_gib": 2, "storage_tier": archive_tier},
        ],
    )

    ci_name = None
    try:
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)

        # Verify CR has the correct storage tiers
        cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        assert cr["spec"]["bootDisk"]["storageTier"] == fast_tier
        assert len(cr["spec"]["additionalDisks"]) == 2
        assert cr["spec"]["additionalDisks"][0]["storageTier"] == default_storage_tier
        assert cr["spec"]["additionalDisks"][1]["storageTier"] == archive_tier

        # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
        # verify_datavolume_storage_classes(
        #     k8s_hub_client=k8s_hub_client,
        #     k8s_virt_client=k8s_virt_client,
        #     ci_name=ci_name,
        #     cr=cr,
        # )
    finally:
        grpc.delete_compute_instance(ci_id=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)


def test_compute_instance_nonexistent_tier_rejected(
    private_grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str, default_disk_image: str
) -> None:
    """Verify that requesting a nonexistent storage tier returns INVALID_ARGUMENT."""
    nonexistent_tier = f"nonexistent-tier-{unique_name('test')}"

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        private_grpc.call(
            service="osac.private.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-bad-tier")},
                    "spec": {
                        "template": {"name": vm_template},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": nonexistent_tier},
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )

    assert_grpc_rejected(exc_info, "InvalidArgument")
    error_lower = str(exc_info.value.stderr).lower()
    assert any(term in error_lower for term in ["not found", "does not exist", "notfound"]), (
        f"Expected not-found/does-not-exist error, got: {exc_info.value.stderr}"
    )


def test_compute_instance_tier_immutability(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
) -> None:
    """Verify that storage tier cannot be changed after ComputeInstance creation."""
    fast_tier = additional_storage_tiers["fast"]["name"]

    name = unique_name("e2e-ci-immutable")
    uuid = cli.create_compute_instance(
        name=name,
        template=vm_template,
        instance_type=default_instance_type,
        network_attachments=[{"subnet": default_subnet}],
        boot_disk_storage_tier=default_storage_tier,
    )

    ci_name = None
    try:
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)

        # Verify CR has the correct storage tier
        cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        assert cr["spec"]["bootDisk"]["storageTier"] == default_storage_tier

        # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
        # verify_datavolume_storage_classes(
        #     k8s_hub_client=k8s_hub_client,
        #     k8s_virt_client=k8s_virt_client,
        #     ci_name=ci_name,
        #     cr=cr,
        # )

        # Attempt to update the storage tier via K8s
        import json

        patch_json = json.dumps({"spec": {"bootDisk": {"storageTier": fast_tier}}})
        output, rc = k8s_hub_client.patch(resource="computeinstance", name=ci_name, patch=patch_json)

        # Verify patch was rejected
        assert rc != 0, "storage tier should be immutable"

        # Verify error mentions immutability
        error_output = output.lower()
        assert "immutable" in error_output or "invalid" in error_output, f"Expected immutability error, got: {output}"
    finally:
        grpc.delete_compute_instance(ci_id=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)


@pytest.mark.skip(
    reason="Requires mandatory storage_tier validation (follow-up osac PR after this test infrastructure lands)"
)
def test_compute_instance_boot_disk_tier_required(
    private_grpc: GRPCClient, vm_template: str, default_subnet: str, default_instance_type: str, default_disk_image: str
) -> None:
    """Verify that missing boot disk tier returns INVALID_ARGUMENT.

    Skipped until follow-up PR makes storage_tier mandatory.
    """
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        private_grpc.call(
            service="osac.private.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-no-tier")},
                    "spec": {
                        "template": {"name": vm_template},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20},  # No storage_tier
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )

    assert_grpc_rejected(exc_info, "InvalidArgument")
    assert "storage_tier is required" in str(exc_info.value.stderr).lower()


@pytest.mark.skip(
    reason="Requires mandatory storage_tier validation (follow-up osac PR after this test infrastructure lands)"
)
def test_compute_instance_additional_disk_tier_required(
    private_grpc: GRPCClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that missing additional disk tier returns INVALID_ARGUMENT.

    Skipped until follow-up PR makes storage_tier mandatory.
    """
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        private_grpc.call(
            service="osac.private.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-add-disk-no-tier")},
                    "spec": {
                        "template": {"name": vm_template},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": default_storage_tier},
                        "additional_disks": [
                            {"size_gib": 50}  # No storage_tier
                        ],
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )

    assert_grpc_rejected(exc_info, "InvalidArgument")
    assert "additional_disks[0].storage_tier is required" in str(exc_info.value.stderr).lower()


def test_compute_instance_boot_disk_tier_from_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that boot disk tier is resolved from CatalogItem FieldDefinition default."""
    field_defs = [
        {
            "path": "boot_disk.storage_tier",
            "display_name": "Boot Disk Storage Tier",
            "editable": True,
            "default": default_storage_tier,
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-tier"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI using catalog item WITHOUT specifying boot_disk_storage_tier
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-cat-def")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20},  # No storage_tier
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify CatalogItem default was applied
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert cr["spec"]["bootDisk"]["storageTier"] == default_storage_tier
            assert len(cr["spec"].get("additionalDisks", [])) == 0

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


@pytest.mark.skip(
    reason="Custom-template CI provisioning is not e2e-testable: spec.templateID is used verbatim "
    "by AAP as the Ansible role to include, and only shipped templates (e.g. ocp-virt-vm) have a "
    "backing role, so a CI created from a custom template can neither provision nor delete "
    "(hangs wait_for_provision, then teardown). Unskip when the backend supports provisionable "
    "custom templates."
)
def test_compute_instance_boot_disk_tier_from_template_default() -> None:
    """Verify a ComputeInstance resolves its boot disk storage_tier from Template spec_defaults.

    Intent: create a ComputeInstanceTemplate with spec_defaults.boot_disk.storage_tier, then
    create a CI referencing it WITHOUT a boot_disk, and assert the CR's spec.bootDisk.storageTier
    (and sizeGiB) are inherited from the template — and the VM provisions.

    Why skipped: a custom template cannot drive real VM provisioning in e2e. Its spec.templateID
    is written verbatim into the CR and used by AAP as the role name to include
    (playbook_osac_create_compute_instance.yml), and only AAP-published templates have a backing
    role, so the CI can neither provision nor delete. This constraint is documented on the sibling
    test tests/e2e/vmaas/test_compute_instance_disk_image.py::test_template_disk_image_default, which
    hit the same limitation and works around it by asserting on the template object only.
    """


def test_compute_instance_user_tier_overrides_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that user-provided tier overrides CatalogItem default."""
    fast_tier = additional_storage_tiers["fast"]["name"]

    field_defs = [
        {
            "path": "boot_disk.storage_tier",
            "display_name": "Boot Disk Storage Tier",
            "editable": True,
            "default": default_storage_tier,
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-override"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI with explicit storage_tier (should override default)
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-override")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": fast_tier},  # Override
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify user value was used, not CatalogItem default
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert cr["spec"]["bootDisk"]["storageTier"] == fast_tier
            assert len(cr["spec"].get("additionalDisks", [])) == 0

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_explicit_additional_disks_without_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that explicit additional disks work when CatalogItem has no additional_disks default."""
    fast_tier = additional_storage_tiers["fast"]["name"]
    archive_tier = additional_storage_tiers["archive"]["name"]

    # CatalogItem with boot_disk.storage_tier default ONLY (no additional_disks default)
    field_defs = [
        {
            "path": "boot_disk.storage_tier",
            "display_name": "Boot Disk Storage Tier",
            "editable": True,
            "default": default_storage_tier,
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "additional_disks", "display_name": "Additional Disks", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-no-add-def"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI with explicit additional_disks (CatalogItem has no default for this field)
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-explicit-add")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20},  # Uses CatalogItem default tier
                        "additional_disks": [
                            {"size_gib": 5, "storage_tier": fast_tier},
                            {"size_gib": 10, "storage_tier": archive_tier},
                        ],
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify boot disk got CatalogItem default, additional disks got user values
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert cr["spec"]["bootDisk"]["storageTier"] == default_storage_tier
            assert len(cr["spec"]["additionalDisks"]) == 2
            assert cr["spec"]["additionalDisks"][0]["sizeGiB"] == 5
            assert cr["spec"]["additionalDisks"][0]["storageTier"] == fast_tier
            assert cr["spec"]["additionalDisks"][1]["sizeGiB"] == 10
            assert cr["spec"]["additionalDisks"][1]["storageTier"] == archive_tier

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_additional_disks_from_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that additional disks are defaulted from CatalogItem FieldDefinition."""
    fast_tier = additional_storage_tiers["fast"]["name"]

    field_defs = [
        {
            "path": "additional_disks",
            "display_name": "Additional Disks",
            "editable": True,
            "default": [{"size_gib": 10, "storage_tier": fast_tier}],
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "boot_disk.storage_tier", "display_name": "Boot Disk Storage Tier", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-add-disks"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI WITHOUT specifying additional_disks (should use default)
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-add-def")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": default_storage_tier},
                        # No additional_disks specified
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify CatalogItem default was applied
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert len(cr["spec"]["additionalDisks"]) == 1
            assert cr["spec"]["additionalDisks"][0]["sizeGiB"] == 10
            assert cr["spec"]["additionalDisks"][0]["storageTier"] == fast_tier

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_user_additional_disks_override_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that user-provided additional disks replace CatalogItem default entirely."""
    fast_tier = additional_storage_tiers["fast"]["name"]
    archive_tier = additional_storage_tiers["archive"]["name"]

    field_defs = [
        {
            "path": "additional_disks",
            "display_name": "Additional Disks",
            "editable": True,
            "default": [{"size_gib": 10, "storage_tier": fast_tier}],
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "boot_disk.storage_tier", "display_name": "Boot Disk Storage Tier", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-add-override"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI with explicit additional_disks (should replace default)
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-add-override")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": default_storage_tier},
                        "additional_disks": [  # Override
                            {"size_gib": 10, "storage_tier": archive_tier}
                        ],
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify user value was used (1 disk with archive tier), not default (1 disk with fast tier)
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert len(cr["spec"]["additionalDisks"]) == 1
            assert cr["spec"]["additionalDisks"][0]["sizeGiB"] == 10
            assert cr["spec"]["additionalDisks"][0]["storageTier"] == archive_tier

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


@pytest.mark.skip(
    reason="OSAC-4356: empty additional_disks: [] does not opt out of the CatalogItem default (backend gap)"
)
def test_compute_instance_empty_additional_disks_opts_out_of_catalog_item_default(
    private_grpc: GRPCClient,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
    default_storage_tier: str,
    additional_storage_tiers: dict[str, dict[str, str]],
    default_instance_type: str,
    default_disk_image: str,
) -> None:
    """Verify that empty additional disks array opts out of CatalogItem default."""
    fast_tier = additional_storage_tiers["fast"]["name"]

    field_defs = [
        {
            "path": "additional_disks",
            "display_name": "Additional Disks",
            "editable": True,
            "default": [{"size_gib": 10, "storage_tier": fast_tier}],
        },
        {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size", "editable": True},
        {"path": "boot_disk.storage_tier", "display_name": "Boot Disk Storage Tier", "editable": True},
        {"path": "network_attachments", "display_name": "Network Attachments", "editable": True},
        {"path": "disk_image", "display_name": "Disk Image", "editable": True},
        {"path": "instance_type", "display_name": "Instance Type", "editable": True},
        {"path": "run_strategy", "display_name": "Run Strategy", "editable": True},
    ]
    catalog_item_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-cat-add-empty"), template=vm_template, published=True, field_definitions=field_defs
    )

    try:
        # Create CI with explicit empty additional_disks array
        ci_obj = grpc.call(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-add-empty")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "instance_type": {"name": default_instance_type},
                        "boot_disk": {"size_gib": 20, "storage_tier": default_storage_tier},
                        "additional_disks": [],  # Explicit empty array
                        "network_attachments": [{"subnet": {"id": default_subnet}}],
                        "disk_image": {"name": default_disk_image},
                        "run_strategy": "Always",
                    },
                }
            },
        )
        uuid = ci_obj["object"]["id"]

        ci_name = None
        try:
            ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_provision(k8s=k8s_hub_client, name=ci_name)

            # Verify no additional disks were created
            cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
            assert len(cr["spec"].get("additionalDisks", [])) == 0

            # E2E: Verify DataVolume StorageClass (commented out until osac PR #257)
            # verify_datavolume_storage_classes(
            #     k8s_hub_client=k8s_hub_client,
            #     k8s_virt_client=k8s_virt_client,
            #     ci_name=ci_name,
            #     cr=cr,
            # )
        finally:
            grpc.delete_compute_instance(ci_id=uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
