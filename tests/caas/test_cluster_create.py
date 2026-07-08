from __future__ import annotations

from pathlib import Path

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_ready,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI


def test_cluster_create(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_path: str,
    ssh_public_key_path: str,
) -> None:
    uuid = cli.create_cluster(
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )
    co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
    assert uuid in grpc.list_cluster_ids()

    wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

    cli.delete_cluster(uuid=uuid)
    wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
    wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
