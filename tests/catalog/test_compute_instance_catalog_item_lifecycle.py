from __future__ import annotations

import time
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient


def _unique_name(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:8]}"


def _wait_compute_instance_removed(grpc: GRPCClient, ci_id: str, timeout: int = 30) -> None:
    deadline = time.monotonic() + timeout
    while ci_id in grpc.list_compute_instance_ids():
        if time.monotonic() > deadline:
            pytest.fail(f"Timed out waiting for compute instance removal: {ci_id}")
        time.sleep(2)


def test_compute_instance_catalog_item_crud(grpc: GRPCClient, compute_instance_template: str) -> None:
    name = _unique_name("e2e-ci-cat")
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

        updated_title = _unique_name("e2e-ci-cat-updated")
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
    name = _unique_name("e2e-ci-unpub")
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
    name = _unique_name("e2e-ci-trans")
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
            "path": "spec.cpu_cores",
            "display_name": "CPU Cores",
            "editable": True,
            "default": {"numberValue": 4},
        },
        {
            "path": "spec.memory_gb",
            "display_name": "Memory GB",
            "editable": False,
            "default": {"numberValue": 16},
        },
    ]
    name = _unique_name("e2e-ci-fd")
    catalog_item_id = grpc.create_compute_instance_catalog_item(
        name=name, template=compute_instance_template, published=True, field_definitions=field_defs
    )
    try:
        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 2

        cpu_fd = next(fd for fd in returned_fds if fd["path"] == "spec.cpu_cores")
        assert cpu_fd["displayName"] == "CPU Cores"
        assert cpu_fd["editable"] is True

        # editable=false is omitted by protobuf (default value), so we only check displayName
        mem_fd = next(fd for fd in returned_fds if fd["path"] == "spec.memory_gb")
        assert mem_fd["displayName"] == "Memory GB"

        updated_fds = [
            {
                "path": "spec.cpu_cores",
                "display_name": "Number of CPU Cores",
                "editable": True,
                "default": {"numberValue": 4},
            },
            {
                "path": "spec.memory_gb",
                "display_name": "Memory GB",
                "editable": False,
                "default": {"numberValue": 16},
            },
        ]
        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, field_definitions=updated_fds)

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 2
        cpu_fd = next(fd for fd in returned_fds if fd["path"] == "spec.cpu_cores")
        assert cpu_fd["displayName"] == "Number of CPU Cores"

        reduced_fds = [
            {
                "path": "spec.cpu_cores",
                "display_name": "Number of CPU Cores",
                "editable": True,
                "default": {"numberValue": 4},
            },
        ]
        grpc.update_compute_instance_catalog_item(catalog_item_id=catalog_item_id, field_definitions=reduced_fds)

        item = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 1
        assert returned_fds[0]["path"] == "spec.cpu_cores"
    finally:
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_create_compute_instance_with_catalog_item(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    name = _unique_name("e2e-ci-cat")
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
            _wait_compute_instance_removed(grpc, ci_id)
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)


def test_create_compute_instance_with_unpublished_catalog_item_fails(
    grpc: GRPCClient, compute_instance_template: str, default_subnet_id: str
) -> None:
    name = _unique_name("e2e-ci-unpub")
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
    name = _unique_name("e2e-ci-ref")
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
            _wait_compute_instance_removed(grpc, ci_id)
        grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_item_id)
