from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.core.helpers import (
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_ready,
)
from tests.core.grpc_client import GRPCClient
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI


@pytest.fixture(scope="module")
def ready_cluster(
    cli: OsacCLI, k8s_hub_client: K8sClient, grpc: GRPCClient,
    cluster_template: str, pull_secret_path: str, ssh_public_key_path: str,
):
    uuid: str = cli.create_cluster(
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )
    co_name: str = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
    try:
        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)
    except Exception:
        if k8s_hub_client.is_present(resource="clusterorder", name=co_name):
            cli.delete_cluster(uuid=uuid)
            wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
        raise
    yield uuid, co_name
    if k8s_hub_client.is_present(resource="clusterorder", name=co_name):
        cli.delete_cluster(uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)


def test_get_kubeconfig(ready_cluster: tuple[str, str], cli: OsacCLI) -> None:
    uuid, _ = ready_cluster
    output: str = cli.get_cluster_credential("kubeconfig", uuid=uuid)
    assert output, "kubeconfig output should not be empty"
    kubeconfig = yaml.safe_load(output)
    assert "clusters" in kubeconfig, "kubeconfig should have clusters key"
    assert len(kubeconfig["clusters"]) > 0, "kubeconfig should have at least one cluster"
    server: str = kubeconfig["clusters"][0]["cluster"]["server"]
    assert server.startswith("https://"), f"server URL should start with https://, got: {server}"


def test_get_password(ready_cluster: tuple[str, str], cli: OsacCLI) -> None:
    uuid, _ = ready_cluster
    output: str = cli.get_cluster_credential("password", uuid=uuid)
    assert output, "password output should not be empty"
    assert len(output.strip()) > 0, "password should be a non-empty string"
