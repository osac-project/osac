from __future__ import annotations

from pathlib import Path

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_cluster_deletion, wait_for_cluster_order_cr, wait_for_cluster_ready
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI


@pytest.fixture
def cluster_order(
    cli: OsacCLI, k8s_hub_client: K8sClient, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
):
    uuid: str = cli.create_cluster(
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )
    co_name: str = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
    yield uuid, co_name
    if k8s_hub_client.is_present(resource="clusterorder", name=co_name):
        cli.delete_cluster(uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)


def test_cluster_order_lifecycle(
    cluster_order: tuple[str, str], grpc: GRPCClient, k8s_hub_client: K8sClient, cli: OsacCLI
) -> None:
    uuid, co_name = cluster_order

    assert uuid in grpc.list_cluster_ids()

    wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

    hc_name: str = k8s_hub_client.get_cluster_order_hosted_cluster_name(name=co_name)
    assert hc_name, f"No HostedCluster name in ClusterOrder {co_name}"

    hc_ns: str = k8s_hub_client.get_cluster_order_namespace(name=co_name)
    assert hc_ns, f"No namespace in ClusterOrder {co_name}"

    cli.delete_cluster(uuid=uuid)
    wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
    assert uuid not in grpc.list_cluster_ids()
