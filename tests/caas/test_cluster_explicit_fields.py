from __future__ import annotations

import json
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
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
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
    api_params = cluster_spec.get("templateParameters", {})

    assert "pull_secret" in api_params, "Expected pull_secret in API templateParameters"
    pull_secret_val = api_params["pull_secret"].get("value", "")
    assert pull_secret_val == "***" or len(pull_secret_val) > 10, (
        "Expected pull_secret to be redacted or populated in API response"
    )

    expected_ssh_key: str = Path(ssh_public_key_path).read_text().strip()
    assert api_params.get("ssh_public_key", {}).get("value") == expected_ssh_key, (
        "Expected ssh_public_key in API templateParameters to match the provided file content"
    )

    spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
    cr_params = json.loads(spec.get("templateParameters", "{}"))

    assert cr_params.get("pull_secret") and len(cr_params["pull_secret"]) > 10, (
        f"Expected pull_secret in CR templateParameters, got: {str(cr_params.get('pull_secret', ''))[:20]}"
    )

    assert cr_params.get("ssh_public_key") == expected_ssh_key, (
        "Expected ssh_public_key in CR templateParameters to match the provided file content"
    )
