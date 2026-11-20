from __future__ import annotations

import json
import os
import uuid
from datetime import UTC, datetime
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind

pytestmark = [
    pytest.mark.regression,
    pytest.mark.skipif(
        not os.environ.get("OSAC_SOURCE_HUB_KUBECONFIG") or not os.environ.get("OSAC_SOURCE_HUB_ID"),
        reason="OSAC_SOURCE_HUB_KUBECONFIG and OSAC_SOURCE_HUB_ID are required for Hub import e2e coverage",
    ),
]


def _condition(object_: dict[str, Any], condition_type: str) -> dict[str, Any]:
    for condition in object_.get("status", {}).get("conditions", []):
        if condition.get("type") == condition_type:
            return condition
    return {}


def _managed_cluster_condition(condition_type: str, status: str) -> dict[str, str]:
    observed_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    return {
        "type": condition_type,
        "status": status,
        "reason": "ManagedClusterImportE2E",
        "message": "ManagedCluster import e2e fixture",
        "lastTransitionTime": observed_at,
    }


def test_import_available_managed_cluster(
    private_cli: OsacCLI, private_grpc: GRPCClient, request: pytest.FixtureRequest
) -> None:
    source_kubeconfig = os.environ.get("OSAC_SOURCE_HUB_KUBECONFIG")
    source_hub_id = os.environ.get("OSAC_SOURCE_HUB_ID")
    source_hub = K8sClient(
        namespace="default",
        kubeconfig=source_kubeconfig,
        as_system_admin=False,
        impersonate=os.environ.get("OSAC_TOKEN_IMPERSONATE") or None,
    )
    managed_cluster_name = f"e2e-import-{uuid.uuid4().hex[:8]}"
    cluster_uid = f"e2e-cluster-uid-{uuid.uuid4().hex}"
    workload_cluster_name = f"e2e-workload-{uuid.uuid4().hex[:8]}"

    def cleanup_source_hub() -> None:
        for resource, name in (("managedcluster", managed_cluster_name), ("namespace", managed_cluster_name)):
            run(*source_hub.base_args(), "delete", resource, name, "--ignore-not-found=true", "--wait=true")

    request.addfinalizer(cleanup_source_hub)

    # These are the ACM objects the real Hub would provide. The controller only
    # needs the registration conditions and the stable identity claim.
    source_hub.apply(
        manifest=json.dumps({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": managed_cluster_name}})
    )
    source_hub.apply(
        manifest=json.dumps(
            {
                "apiVersion": "v1",
                "kind": "List",
                "items": [
                    {
                        "apiVersion": "cluster.open-cluster-management.io/v1",
                        "kind": "ManagedCluster",
                        "metadata": {"name": managed_cluster_name},
                        "spec": {"hubAcceptsClient": True},
                        "status": {
                            "conditions": [
                                _managed_cluster_condition("HubAcceptedManagedCluster", "True"),
                                _managed_cluster_condition("ManagedClusterConditionAvailable", "True"),
                            ],
                            "clusterClaims": [{"name": "id.k8s.io", "value": cluster_uid}],
                        },
                    }
                ],
            }
        )
    )
    run(
        *source_hub.base_args(),
        "patch",
        "managedcluster",
        managed_cluster_name,
        "--subresource=status",
        "--type=merge",
        "-p",
        json.dumps(
            {
                "status": {
                    "conditions": [
                        _managed_cluster_condition("HubAcceptedManagedCluster", "True"),
                        _managed_cluster_condition("ManagedClusterConditionAvailable", "True"),
                    ],
                    "clusterClaims": [{"name": "id.k8s.io", "value": cluster_uid}],
                }
            }
        ),
    )

    try:
        workload_cluster_id = private_cli.create_workload_cluster(
            name=workload_cluster_name,
            source_hub_id=source_hub_id,
            managed_cluster_name=managed_cluster_name,
            supported_workloads=["maas"],
        )

        def get_workload_cluster() -> dict[str, Any]:
            return private_grpc.call(service=f"{PRIVATE_API}.WorkloadClusters/Get", data={"id": workload_cluster_id})[
                "object"
            ]

        registered = poll_until(
            fn=get_workload_cluster,
            until=lambda object_: _condition(object_, "Available").get("status") == "True",
            retries=60,
            delay=5,
            description="WorkloadCluster becomes available after Hub import",
            resource_kind=WaitResourceKind.WORKLOAD_CLUSTER,
            resource_id=workload_cluster_id,
        )
        assert registered["metadata"]["name"] == workload_cluster_name
        assert registered["spec"] == {
            "maas": {"sourceHubId": source_hub_id, "managedClusterName": managed_cluster_name},
            "supportedWorkloads": ["maas"],
        }
        assert registered["status"]["managedClusterUid"] == cluster_uid
        assert _condition(registered, "HubReachable")["status"] == "True"

        cli_listing = private_cli.list_workload_clusters()
        assert workload_cluster_id in cli_listing
        cli_description = private_cli.describe_workload_cluster(workload_cluster_id)
        assert workload_cluster_name in cli_description
        assert managed_cluster_name in cli_description

        managed_cluster_json = json.loads(
            run(*source_hub.base_args(), "get", "managedcluster", managed_cluster_name, "-o", "json")
        )
        assert managed_cluster_json["metadata"]["name"] == managed_cluster_name
        assert managed_cluster_json["status"]["clusterClaims"][0]["value"] == cluster_uid
    finally:
        source_hub.delete(resource="managedcluster", name=managed_cluster_name, wait=True)
        source_hub.delete(resource="namespace", name=managed_cluster_name, wait=True)
