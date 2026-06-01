from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
import yaml

from tests.core.helpers import wait_for_cluster_deletion, wait_for_cluster_ready
from tests.core.k8s_client import K8sClient


@pytest.fixture
def cluster_order(
    k8s_hub_client: K8sClient, namespace: str, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
):
    order_name: str = f"co-fields-{uuid4().hex[:8]}"
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
    yield order_name, template_params
    k8s_hub_client.delete(resource="clusterorder", name=order_name, wait=False)
    wait_for_cluster_deletion(k8s=k8s_hub_client, name=order_name)


def test_cluster_order_api_fields(
    cluster_order: tuple[str, dict[str, str]], cluster_template: str, k8s_hub_client: K8sClient
) -> None:
    order_name, template_params = cluster_order

    wait_for_cluster_ready(k8s=k8s_hub_client, name=order_name)

    co: dict[str, Any] = k8s_hub_client.get_json(resource="clusterorder", name=order_name)
    spec: dict[str, Any] = co["spec"]
    assert spec["templateID"] == cluster_template, f"templateID mismatch: {spec['templateID']} != {cluster_template}"
    assert json.loads(spec["templateParameters"]) == template_params, "templateParameters mismatch"
    assert spec["nodeRequests"][0]["resourceClass"] == "ci-worker", "resourceClass mismatch"
    assert spec["nodeRequests"][0]["numberOfNodes"] == 1, "numberOfNodes mismatch"

    status: dict[str, Any] = co.get("status", {})
    assert status.get("phase") == "Ready", f"Expected Ready phase, got {status.get('phase')}"

    cluster_ref: dict[str, Any] = status.get("clusterReference", {})
    assert cluster_ref.get("hostedClusterName"), "Missing hostedClusterName"
    assert cluster_ref.get("namespace"), "Missing namespace"
    assert cluster_ref.get("serviceAccountName"), "Missing serviceAccountName"
    assert cluster_ref.get("roleBindingName"), "Missing roleBindingName"
