from __future__ import annotations

from pathlib import Path

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_cluster_deletion, wait_for_cluster_order_cr
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI


@pytest.fixture
def cluster_with_explicit_fields(
    cli: OsacCLI, k8s_hub_client: K8sClient, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
):
    uuid: str = cli.create_cluster(
        template=cluster_template, pull_secret_file=pull_secret_path, ssh_public_key_file=ssh_public_key_path
    )
    co_name: str | None = None
    try:
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        yield uuid, co_name
    finally:
        if co_name and k8s_hub_client.is_present(resource="clusterorder", name=co_name):
            cli.delete_cluster(uuid=uuid)
            wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)


def test_cluster_explicit_fields(
    cluster_with_explicit_fields: tuple[str, str], grpc: GRPCClient, k8s_hub_client: K8sClient, ssh_public_key_path: str
) -> None:
    uuid, co_name = cluster_with_explicit_fields

    assert uuid in grpc.list_cluster_ids()

    response = grpc.get_cluster(cluster_id=uuid)
    cluster_spec = response.get("object", {}).get("spec", {})

    assert cluster_spec.get("pullSecret") == "***", "Expected pull_secret to be redacted as '***' in API response"

    expected_ssh_key: str = Path(ssh_public_key_path).read_text().strip()
    assert cluster_spec.get("sshPublicKey") == expected_ssh_key, (
        "Expected ssh_public_key to match the provided file content"
    )

    spec = k8s_hub_client.get_cluster_order_spec(name=co_name)

    assert spec.get("pullSecret") and len(spec["pullSecret"]) > 10, (
        f"Expected pullSecret to be populated on the ClusterOrder CR, got: {spec.get('pullSecret', '')[:20]}"
    )

    assert spec.get("sshPublicKey") == expected_ssh_key, (
        "Expected sshPublicKey on ClusterOrder CR to match the provided file content"
    )

    tp = spec.get("templateParameters", "")
    assert tp in ("", "{}", None), f"Expected templateParameters to be empty, got: {tp}"
