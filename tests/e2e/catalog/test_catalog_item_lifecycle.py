from __future__ import annotations

from tests.e2e.catalog.conftest import unique_name
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_cluster_order_cr
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

# Fixed (not randomly generated) so repeated runs reuse the same ClusterVersion
# via GRPCClient.ensure_cluster_version instead of accumulating duplicates on
# the shared cluster. The version *name* ("4.20.0-e2e-catalog-item") is distinct
# from caas/test_cluster_create.py to avoid state races under xdist.
TEST_RELEASE_IMAGE = "quay.io/openshift-release-dev/ocp-release:4.20.0-multi"


def test_catalog_item_crud(grpc: GRPCClient, cluster_template: str) -> None:
    name = unique_name("e2e-cat")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    try:
        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        obj = item["object"]
        assert obj["title"] == name
        assert obj["template"]["name"] == cluster_template
        assert obj["published"] is True

        updated_title = unique_name("e2e-cat-updated")
        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, title=updated_title)

        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)
        assert item["object"]["title"] == updated_title

        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)

        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids()

        output, rc = grpc.call_unchecked(service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id})
        assert rc != 0, f"Expected Get to fail after deletion, got: {output}"

        catalog_item_id = ""
    finally:
        if catalog_item_id:
            grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_unpublished_catalog_item_readable_in_public_api(grpc: GRPCClient, cluster_template: str) -> None:
    name = unique_name("e2e-unpub")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=False)
    try:
        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()
        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids(published_only=True)

        output, rc = grpc.call_unchecked(service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id})
        assert rc == 0, f"Expected Get to read unpublished item, got: {output}"
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_catalog_item_unpublish_transition(grpc: GRPCClient, cluster_template: str) -> None:
    name = unique_name("e2e-trans")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    try:
        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()

        grpc.update_cluster_catalog_item(catalog_item_id=catalog_item_id, published=False)

        assert catalog_item_id in grpc.list_cluster_catalog_item_ids()
        assert catalog_item_id not in grpc.list_cluster_catalog_item_ids(published_only=True)

        output, rc = grpc.call_unchecked(service="osac.public.v1.ClusterCatalogItems/Get", data={"id": catalog_item_id})
        assert rc == 0, f"Expected Get to read item after unpublishing, got: {output}"
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_catalog_item_fields(grpc: GRPCClient, cluster_template: str) -> None:
    fields = {
        "network": {
            "pod_cidr": {"editable": {"default_value": "10.128.0.0/14"}},
            "service_cidr": {"locked": "172.30.0.0/16"},
        }
    }
    catalog_item_id = grpc.create_cluster_catalog_item(
        name=unique_name("e2e-policy"), template=cluster_template, fields=fields
    )
    try:
        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)["object"]
        network = item["fields"]["network"]
        assert network["podCidr"]["editable"]["defaultValue"] == "10.128.0.0/14"
        assert network["serviceCidr"] == {"locked": "172.30.0.0/16"}

        grpc.update_cluster_catalog_item(
            catalog_item_id=catalog_item_id,
            fields={"network": {"pod_cidr": {"editable": {}}, "service_cidr": {"locked": "172.31.0.0/16"}}},
        )
        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)["object"]
        network = item["fields"]["network"]
        assert network["podCidr"] == {"editable": {}}
        assert network["serviceCidr"] == {"locked": "172.31.0.0/16"}

        grpc.update_cluster_catalog_item(
            catalog_item_id=catalog_item_id, fields={"network": {"pod_cidr": {"editable": {}}}}
        )
        item = grpc.get_cluster_catalog_item(catalog_item_id=catalog_item_id)["object"]
        assert item["fields"]["network"] == {"podCidr": {"editable": {}}}
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_create_cluster_with_catalog_item(grpc: GRPCClient, cli: OsacCLI, cluster_template: str) -> None:
    name = unique_name("e2e-cat")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    cluster_id = ""
    try:
        cluster_name = unique_name("e2e-cluster")
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)

        assert cluster_id in grpc.list_cluster_ids()

        cluster = grpc.get_cluster(cluster_id=cluster_id)
        assert cluster["object"]["spec"]["catalogItem"]["id"] == catalog_item_id
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            poll_until(
                fn=lambda: cluster_id not in grpc.list_cluster_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"Cluster {cluster_id} removal from API",
            )
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_create_cluster_with_unpublished_catalog_item_fails(grpc: GRPCClient, cluster_template: str) -> None:
    name = unique_name("e2e-unpub")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=False)
    try:
        output, rc = grpc.call_unchecked(
            service="osac.public.v1.Clusters/Create",
            data={
                "object": {
                    "metadata": {"name": unique_name("e2e-cl-unpub")},
                    "spec": {"catalog_item": {"id": catalog_item_id}},
                }
            },
        )
        assert rc != 0, f"Expected create to fail for unpublished catalog item, got: {output}"
        assert "not published" in output.lower() or "not found" in output.lower()
    finally:
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_cluster_survives_catalog_item_deletion(grpc: GRPCClient, cli: OsacCLI, cluster_template: str) -> None:
    name = unique_name("e2e-ref")
    catalog_item_id = grpc.create_cluster_catalog_item(name=name, template=cluster_template, published=True)
    cluster_id = ""
    catalog_deleted = False
    try:
        cluster_name = unique_name("e2e-cluster")
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)

        output, rc = grpc.call_unchecked(
            service="osac.public.v1.ClusterCatalogItems/Delete", data={"id": catalog_item_id}
        )
        assert rc == 0, f"Expected catalog item deletion to succeed, got: {output}"
        catalog_deleted = True
        persisted = grpc.call(service="osac.public.v1.Clusters/Get", data={"id": cluster_id})["object"]
        assert persisted["spec"]["catalogItem"]["id"] == catalog_item_id
        assert persisted["spec"]["template"]["id"], "Materialized Template must survive catalog deletion"
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            poll_until(
                fn=lambda: cluster_id not in grpc.list_cluster_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"Cluster {cluster_id} removal from API",
            )
        if not catalog_deleted:
            grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)


def test_create_cluster_with_catalog_item_version(
    grpc: GRPCClient, private_grpc: GRPCClient, cli: OsacCLI, k8s_hub_client: K8sClient, cluster_template: str
) -> None:
    """Verify the catalog-item version resolution path: a field policy
    default for version overrides the template's own spec_defaults.version."""
    version = private_grpc.ensure_cluster_version(version="4.20.0-e2e-catalog-item", image=TEST_RELEASE_IMAGE)

    name = unique_name("e2e-cv-cat")
    catalog_item_id = grpc.create_cluster_catalog_item(
        name=name,
        template=cluster_template,
        published=True,
        fields={"version": {"editable": {"default_value": {"name": version["name"], "shared": True}}}},
    )
    cluster_id = ""
    try:
        cluster_name = unique_name("e2e-cv-cluster")
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=catalog_item_id, name=cluster_name)

        cluster = grpc.get_cluster(cluster_id=cluster_id)
        assert cluster["object"]["spec"]["version"]["name"] == version["name"]

        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_id)
        release_image = poll_until(
            fn=lambda: k8s_hub_client.get_cluster_order_spec(name=co_name).get("releaseImage", ""),
            until=lambda v: v != "",
            retries=30,
            delay=5,
            description=f"{co_name} ClusterOrder releaseImage resolution",
        )
        assert release_image == TEST_RELEASE_IMAGE
    finally:
        if cluster_id:
            cli.delete_cluster(uuid=cluster_id)
            poll_until(
                fn=lambda: cluster_id not in grpc.list_cluster_ids(),
                until=lambda v: v is True,
                retries=30,
                delay=5,
                description=f"Cluster {cluster_id} removal from API",
            )
        grpc.delete_cluster_catalog_item(catalog_item_id=catalog_item_id)
