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
            break
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

        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)
        catalog_item_id = ""

        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids()
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
    grpc: GRPCClient, cli: OsacCLI, cluster_template: str
) -> None:
    name = _unique_name("e2e-unpub")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=False)
    try:
        cluster_name = _unique_name("e2e-cluster")
        with pytest.raises(subprocess.CalledProcessError):
            cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


@pytest.mark.xfail(reason="Server does not yet block deletion of referenced catalog items")
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
        assert rc != 0, f"Expected delete to be blocked, got: {output}"
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            _wait_cluster_removed(grpc, cluster_id)
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)
