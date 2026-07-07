from __future__ import annotations

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.runner import poll_until


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
        assert obj["template"] == compute_instance_template
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


def test_unpublished_compute_instance_catalog_item_not_visible_in_public_api(
    grpc: GRPCClient, compute_instance_template: str
) -> None:
    name = unique_name("e2e-ci-unpub")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=False
    )
    try:
        assert catalog_item_id not in grpc.list_compute_instance_catalog_item_ids()

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected Get to fail for unpublished item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_catalog_item_unpublish_transition(
    grpc: GRPCClient, compute_instance_template: str
) -> None:
    name = unique_name("e2e-ci-trans")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    try:
        assert catalog_item_id in grpc.list_compute_instance_catalog_item_ids()

        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, published=False)

        assert catalog_item_id not in grpc.list_compute_instance_catalog_item_ids()

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected Get to fail after unpublishing, got: {output}"
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_compute_instance_catalog_item_field_definitions(
    grpc: GRPCClient, compute_instance_template: str
) -> None:
    field_defs = [
        {
            "path": "spec.instance_type",
            "display_name": "Instance Type",
            "editable": True,
            "default": {"stringValue": "standard-2x4"},
        },
    ]
    name = unique_name("e2e-ci-fd")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True, field_definitions=field_defs
    )
    try:
        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 1

        it_fd = next(fd for fd in returned_fds if fd["path"] == "spec.instance_type")
        assert it_fd["displayName"] == "Instance Type"
        assert it_fd["editable"] is True

        updated_fds = [
            {
                "path": "spec.instance_type",
                "display_name": "VM Size",
                "editable": True,
                "default": {"stringValue": "standard-2x4"},
            },
        ]
        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, field_definitions=updated_fds)

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 1
        it_fd = next(fd for fd in returned_fds if fd["path"] == "spec.instance_type")
        assert it_fd["displayName"] == "VM Size"

        updated_fds_v2 = [
            {
                "path": "spec.instance_type",
                "display_name": "VM Size",
                "editable": False,
                "default": {"stringValue": "standard-4x8"},
            },
        ]
        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, field_definitions=updated_fds_v2)

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 1
        it_fd = next(fd for fd in returned_fds if fd["path"] == "spec.instance_type")
        assert it_fd["displayName"] == "VM Size"
        assert it_fd.get("editable", False) is False
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_create_compute_instance_with_catalog_item(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    name = unique_name("e2e-ci-cat")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    ci_id = ""
    try:
        ci_id = grpc.create_compute_instance(catalog_item=catalog_item_id, subnet_ids=[default_subnet_id])

        assert ci_id in grpc.list_compute_instance_ids()

        ci = grpc.get_compute_instance(ci_id=ci_id)
        assert ci["object"]["spec"]["catalogItem"] == catalog_item_id
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
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    name = unique_name("e2e-ci-unpub")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=False
    )
    try:
        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ComputeInstances/Create",
            data={"object": {"spec": {"catalog_item": catalog_item_id, "network_attachments": [{"subnet": default_subnet_id}]}}},
        )
        assert rc != 0, f"Expected create to fail for unpublished catalog item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_delete_compute_instance_catalog_item_blocked_when_referenced(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    name = unique_name("e2e-ci-ref")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True
    )
    ci_id = ""
    try:
        ci_id = grpc.create_compute_instance(catalog_item=catalog_item_id, subnet_ids=[default_subnet_id])

        output, rc = grpc.call_unchecked(
            service="osac.private.v1.ComputeInstanceCatalogItems/Delete", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected catalog item delete to be blocked, got: {output}"
        assert "referenc" in output.lower() or "in use" in output.lower() or "failed precondition" in output.lower()
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
