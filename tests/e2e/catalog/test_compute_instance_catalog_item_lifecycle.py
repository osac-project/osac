from __future__ import annotations

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.runner import poll_until


def test_compute_instance_catalog_item_crud(grpc: GRPCClient, compute_instance_template: str) -> None:
    name = unique_name("e2e-ci-cat")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    try:
        assert catalog_item_id in grpc.list_compute_instance_catalog_item_ids()

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        obj = item["object"]
        assert obj["title"] == name
        assert obj["template"]["name"] == compute_instance_template
        assert obj["published"] is True

        updated_title = unique_name("e2e-ci-cat-updated")
        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, title=updated_title)

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        assert item["object"]["title"] == updated_title

        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)

        assert catalog_item_id not in grpc.list_compute_instance_catalog_item_ids()

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected Get to fail after deletion, got: {output}"

        catalog_item_id = ""
    finally:
        if catalog_item_id:
            grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_unpublished_compute_instance_catalog_item_readable_in_public_api(
    grpc: GRPCClient, compute_instance_template: str
) -> None:
    name = unique_name("e2e-ci-unpub")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=False
    )
    try:
        assert catalog_item_id in grpc.list_compute_instance_catalog_item_ids()
        assert catalog_item_id not in grpc.list_compute_instance_catalog_item_ids(published_only=True)

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc == 0, f"Expected Get to read unpublished item, got: {output}"
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_catalog_item_unpublish_transition(grpc: GRPCClient, compute_instance_template: str) -> None:
    name = unique_name("e2e-ci-trans")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    try:
        assert catalog_item_id in grpc.list_compute_instance_catalog_item_ids()

        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, published=False)

        assert catalog_item_id in grpc.list_compute_instance_catalog_item_ids()
        assert catalog_item_id not in grpc.list_compute_instance_catalog_item_ids(published_only=True)

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc == 0, f"Expected Get to read item after unpublishing, got: {output}"
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_catalog_item_fields(grpc: GRPCClient, compute_instance_template: str) -> None:
    initial_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"
    locked_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBe5EVW4cHjAFNa8jMJQqLGBJENvJRfH+Q2lOjFr93vd other@example.com"
    fields = {"ssh_public_key": {"editable": {"default_value": initial_key}}}
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=unique_name("e2e-ci-policy"), template=compute_instance_template, fields=fields
    )
    try:
        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)["object"]
        assert item["fields"]["sshPublicKey"]["editable"]["defaultValue"] == initial_key

        grpc.update_compute_instance_catalog_item(
            catalog_item_id=catalog_item_id, fields={"ssh_public_key": {"locked": locked_key}}
        )
        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)["object"]
        assert item["fields"]["sshPublicKey"] == {"locked": locked_key}

        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, fields={})
        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)["object"]
        assert not item.get("fields")
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_create_compute_instance_with_catalog_item(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str, default_storage_tier: str
) -> None:
    name = unique_name("e2e-ci-cat")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    ci_id = ""
    try:
        ci_name = unique_name("e2e-ci")
        ci_id = grpc.create_compute_instance(
            name=ci_name,
            catalog_item=catalog_item_id,
            subnet_ids=[default_subnet_id],
            boot_disk_storage_tier=default_storage_tier,
        )

        assert ci_id in grpc.list_compute_instance_ids()

        ci = grpc.get_compute_instance(ci_id=ci_id)
        assert ci["object"]["spec"]["catalogItem"]["id"] == catalog_item_id
    finally:
        if ci_id:
            grpc.delete_compute_instance(ci_id=ci_id)
            poll_until(
                fn=lambda: ci_id not in grpc.list_compute_instance_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"ComputeInstance {ci_id} removal from API",
            )
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_create_compute_instance_with_unpublished_catalog_item_fails(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str, default_storage_tier: str
) -> None:
    name = unique_name("e2e-ci-unpub")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=False
    )
    try:
        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstances/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-ci-unpub-create")},
                    "spec": {
                        "catalog_item": {"id": catalog_item_id},
                        "boot_disk": {"storage_tier": {"name": default_storage_tier}},
                        "network_attachments": [{"subnet": {"id": default_subnet_id}}],
                    },
                }
            },
        )
        assert rc != 0, f"Expected create to fail for unpublished catalog item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_survives_catalog_item_deletion(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str, default_storage_tier: str
) -> None:
    name = unique_name("e2e-ci-ref")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    ci_id = ""
    catalog_deleted = False
    try:
        ci_name = unique_name("e2e-ci")
        ci_id = grpc.create_compute_instance(
            name=ci_name,
            catalog_item=catalog_item_id,
            subnet_ids=[default_subnet_id],
            boot_disk_storage_tier=default_storage_tier,
        )

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Delete", data={"id": catalog_item_id}
        )
        assert rc == 0, f"Expected catalog item deletion to succeed, got: {output}"
        catalog_deleted = True
        persisted = grpc.call(service="osac.public.v1.ComputeInstances/Get", data={"id": ci_id})["object"]
        assert persisted["spec"]["catalogItem"]["id"] == catalog_item_id
        assert persisted["spec"]["template"]["id"], "Materialized Template must survive catalog deletion"
    finally:
        if ci_id:
            grpc.delete_compute_instance(ci_id=ci_id)
            poll_until(
                fn=lambda: ci_id not in grpc.list_compute_instance_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"ComputeInstance {ci_id} removal from API",
            )
        if not catalog_deleted:
            grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
