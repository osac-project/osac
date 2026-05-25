from __future__ import annotations

import json
from pathlib import Path
from uuid import uuid4

import pytest
import yaml

from tests.core.helpers import wait_for_cluster_deletion
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


@pytest.fixture
def cluster_order(
    k8s_hub_client: K8sClient, namespace: str, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
):
    order_name: str = f"co-immut-{uuid4().hex[:8]}"
    template_params: dict[str, str] = {
        "pull_secret": Path(pull_secret_path).read_text().strip(),
        "ssh_public_key": Path(ssh_public_key_path).read_text().strip(),
    }

    manifest: str = yaml.dump(
        {
            "apiVersion": "osac.openshift.io/v1alpha1",
            "kind": "ClusterOrder",
            "metadata": {
                "name": order_name,
                "namespace": namespace,
                "annotations": {"osac.openshift.io/tenant": namespace},
            },
            "spec": {
                "templateID": cluster_template,
                "templateParameters": json.dumps(template_params),
                "nodeRequests": [{"resourceClass": "ci-worker", "numberOfNodes": 1}],
            },
        }
    )

    k8s_hub_client.apply(manifest=manifest)
    yield order_name
    if k8s_hub_client.is_present(resource="clusterorder", name=order_name):
        uuid = k8s_hub_client.get_jsonpath(
            resource="clusterorder", name=order_name,
            jsonpath='{.metadata.labels.osac\\.openshift\\.io/clusterorder-uuid}'
        )
        k8s_hub_client.delete(resource="clusterorder", name=order_name)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=order_name)
        if uuid:
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)


def test_template_id_immutable(cluster_order: str, k8s_hub_client: K8sClient) -> None:
    poll_until(
        fn=lambda: k8s_hub_client.is_present(resource="clusterorder", name=cluster_order),
        until=lambda v: v is True,
        retries=15,
        delay=2,
        description=f"ClusterOrder {cluster_order} exists",
    )

    patch: str = json.dumps({"spec": {"templateID": "osac.templates.bogus_template"}})
    output, rc = k8s_hub_client.patch(resource="clusterorder", name=cluster_order, patch=patch)
    assert rc != 0, "patch to templateID should be rejected by the server"
    assert "immutable" in output.lower() or "templateID" in output, (
        f"patch was rejected but not for immutability: {output}"
    )
