from __future__ import annotations

import json
import os
import uuid
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind

pytestmark = pytest.mark.regression


def _condition(object_: dict[str, Any], condition_type: str) -> dict[str, Any]:
    return next(condition for condition in object_["status"]["conditions"] if condition["type"] == condition_type)


def test_import_available_managed_cluster(
    private_cli: OsacCLI,
    private_grpc: GRPCClient,
    request: pytest.FixtureRequest,
) -> None:
    source_kubeconfig = os.environ.get("OSAC_SOURCE_HUB_KUBECONFIG")
    if not source_kubeconfig:
        pytest.skip("OSAC_SOURCE_HUB_KUBECONFIG is required for Hub import e2e coverage")

    source_hub = K8sClient(
        namespace="default",
        kubeconfig=source_kubeconfig,
        as_system_admin=False,
        impersonate=os.environ.get("OSAC_TOKEN_IMPERSONATE") or None,
    )
    hub_credential_kubeconfig = os.environ.get("OSAC_SOURCE_HUB_CREDENTIAL_KUBECONFIG", source_kubeconfig)
    managed_cluster_name = f"e2e-import-{uuid.uuid4().hex[:8]}"
    cluster_uid = f"e2e-cluster-uid-{uuid.uuid4().hex}"
    hub_id = str(uuid.uuid4())
    registered_hub_id = ""
    workload_cluster_name = f"e2e-workload-{uuid.uuid4().hex[:8]}"
    workload_cluster_id = ""

    def cleanup_source_hub() -> None:
        for resource, name in (("managedcluster", managed_cluster_name), ("namespace", managed_cluster_name)):
            run(env("OSAC_OC_PATH", "oc"), "--kubeconfig", source_kubeconfig, "delete", resource, name, "--ignore-not-found=true", "--wait=true")

    request.addfinalizer(cleanup_source_hub)

    # These are the ACM objects the real Hub would provide. The controller only
    # needs the registration conditions and the stable identity claim.
    source_hub.apply(
        manifest=json.dumps(
            {
                "apiVersion": "v1",
                "kind": "Namespace",
                "metadata": {"name": managed_cluster_name},
            }
        )
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
                        "status": {
                            "conditions": [
                                {"type": "HubAcceptedManagedCluster", "status": "True"},
                                {"type": "ManagedClusterConditionAvailable", "status": "True"},
                            ],
                            "clusterClaims": [{"name": "id.k8s.io", "value": cluster_uid}],
                        },
                    },
                ],
            }
        )
    )
    run(
        env("OSAC_OC_PATH", "oc"),
        "--kubeconfig",
        source_kubeconfig,
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
                        {"type": "HubAcceptedManagedCluster", "status": "True"},
                        {"type": "ManagedClusterConditionAvailable", "status": "True"},
                    ],
                    "clusterClaims": [{"name": "id.k8s.io", "value": cluster_uid}],
                }
            }
        ),
    )

    try:
        registered_hub_id = private_cli.create_hub(hub_id=hub_id, kubeconfig=hub_credential_kubeconfig)
        workload_cluster_id = private_cli.create_workload_cluster(
            name=workload_cluster_name,
            source_hub_id=registered_hub_id,
            managed_cluster_name=managed_cluster_name,
            supported_workloads=["maas"],
        )

        def get_workload_cluster() -> dict[str, Any]:
            return private_grpc.call(
                service=f"{PRIVATE_API}.WorkloadClusters/Get", data={"id": workload_cluster_id}
            )["object"]

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
            "sourceHubId": registered_hub_id,
            "managedClusterName": managed_cluster_name,
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
            run(
                env("OSAC_OC_PATH", "oc"),
                "--kubeconfig",
                source_kubeconfig,
                "get",
                "managedcluster",
                managed_cluster_name,
                "-o",
                "json",
            )
        )
        assert managed_cluster_json["metadata"]["name"] == managed_cluster_name
        assert managed_cluster_json["status"]["clusterClaims"][0]["value"] == cluster_uid
    finally:
        if workload_cluster_id:
            private_grpc.call(service=f"{PRIVATE_API}.WorkloadClusters/Delete", data={"id": workload_cluster_id})
        if registered_hub_id:
            private_grpc.call(service=f"{PRIVATE_API}.Hubs/Delete", data={"id": registered_hub_id})
        source_hub.delete(resource="managedcluster", name=managed_cluster_name, wait=True)
        source_hub.delete(resource="namespace", name=managed_cluster_name, wait=True)
