from __future__ import annotations

import subprocess
import time
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.osac_cli import OsacCLI


def _unique_name(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:8]}"


def _wait_cluster_removed(grpc: GRPCClient, cluster_id: str, timeout: int = 30) -> None:
    deadline = time.monotonic() + timeout
    while cluster_id in grpc.list_cluster_ids():
        if time.monotonic() > deadline:
            pytest.fail(f"Timed out waiting for cluster removal: {cluster_id}")
        time.sleep(2)


def test_catalog_item_crud(grpc: GRPCClient, cluster_template: str) -> None:
    name = _unique_name("e2e-cat")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    try:
        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        obj = item["object"]
        assert obj["title"] == name
        assert obj["template"] == cluster_template
        assert obj["published"] is True

        updated_title = _unique_name("e2e-cat-updated")
        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, title=updated_title)

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        assert item["object"]["title"] == updated_title

        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)

        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids()

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected Get to fail after deletion, got: {output}"

        catalog_item_id = ""
    finally:
        if catalog_item_id:
            grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_unpublished_catalog_item_not_visible_in_public_api(grpc: GRPCClient, cluster_template: str) -> None:
    name = _unique_name("e2e-unpub")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=False)
    try:
        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids()

        output, rc = grpc.call_unchecked(service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id})
        assert rc != 0, f"Expected Get to fail for unpublished item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_catalog_item_unpublish_transition(grpc: GRPCClient, cluster_template: str) -> None:
    name = _unique_name("e2e-trans")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    try:
        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()

        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, published=False)

        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids()

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected Get to fail after unpublishing, got: {output}"
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_catalog_item_field_definitions(grpc: GRPCClient, cluster_template: str) -> None:
    field_defs = [
        {
            "path": "spec.network.pod_cidr",
            "display_name": "Pod CIDR",
            "editable": True,
            "default": {"stringValue": "10.128.0.0/14"},
        },
        {
            "path": "spec.network.service_cidr",
            "display_name": "Service CIDR",
            "editable": False,
            "default": {"stringValue": "172.30.0.0/16"},
        },
    ]
    name = _unique_name("e2e-fd")
    catalog_item_id = grpc.create_cluster_catalog_item(
        name=name, template=cluster_template, published=True, field_definitions=field_defs
    )
    try:
        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 2

        pod_fd = next(fd for fd in returned_fds if fd["path"] == "spec.network.pod_cidr")
        assert pod_fd["displayName"] == "Pod CIDR"
        assert pod_fd["editable"] is True

        # editable=false is omitted by protobuf (default value), so we only check displayName
        svc_fd = next(fd for fd in returned_fds if fd["path"] == "spec.network.service_cidr")
        assert svc_fd["displayName"] == "Service CIDR"

        updated_fds = [
            {
                "path": "spec.network.pod_cidr",
                "display_name": "Pod Network CIDR",
                "editable": True,
                "default": {"stringValue": "10.128.0.0/14"},
            },
            {
                "path": "spec.network.service_cidr",
                "display_name": "Service CIDR",
                "editable": False,
                "default": {"stringValue": "172.30.0.0/16"},
            },
        ]
        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, field_definitions=updated_fds)

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 2
        pod_fd = next(fd for fd in returned_fds if fd["path"] == "spec.network.pod_cidr")
        assert pod_fd["displayName"] == "Pod Network CIDR"

        reduced_fds = [
            {
                "path": "spec.network.pod_cidr",
                "display_name": "Pod Network CIDR",
                "editable": True,
                "default": {"stringValue": "10.128.0.0/14"},
            },
        ]
        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, field_definitions=reduced_fds)

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        returned_fds = item["object"].get("fieldDefinitions", [])
        assert len(returned_fds) == 1
        assert returned_fds[0]["path"] == "spec.network.pod_cidr"
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_create_cluster_with_catalog_item(grpc: GRPCClient, cli: OsacCLI, cluster_template: str) -> None:
    name = _unique_name("e2e-cat")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    cluster_id = ""
    try:
        cluster_name = _unique_name("e2e-cluster")
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)

        assert cluster_id in grpc.list_cluster_ids()

        cluster = grpc.get_cluster(cluster_id=cluster_id)
        assert cluster["object"]["spec"]["catalogItem"] == catalog_item_id
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            _wait_cluster_removed(grpc, cluster_id)
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_create_cluster_with_unpublished_catalog_item_fails(
    grpc: GRPCClient, cluster_template: str
) -> None:
    name = _unique_name("e2e-unpub")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=False)
    try:
        output, rc = grpc.call_unchecked(
            service="osac.public.v1.Clusters/Create",
            data={"object": {"spec": {"catalog_item": catalog_item_id}}},
        )
        assert rc != 0, f"Expected create to fail for unpublished catalog item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_delete_catalog_item_blocked_when_referenced(grpc: GRPCClient, cli: OsacCLI, cluster_template: str) -> None:
    name = _unique_name("e2e-ref")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    cluster_id = ""
    try:
        cluster_name = _unique_name("e2e-cluster")
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)

        output, rc = grpc.call_unchecked(
            service="osac.private.v1.ClusterCatalogItems/Delete", data={"id": catalog_item_id}
        )
        assert rc != 0, f"Expected catalog item delete to be blocked, got: {output}"
        assert "referenc" in output.lower() or "in use" in output.lower() or "failed precondition" in output.lower()
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            _wait_cluster_removed(grpc, cluster_id)
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)
