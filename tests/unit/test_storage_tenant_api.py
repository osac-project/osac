"""Storage scenario setup and cleanup must pass through the Tenants API."""

from __future__ import annotations

from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.storage import test_caas_cluster_storage as caas
from tests.e2e.storage import test_tenant_storage_lifecycle as tenant


@pytest.mark.parametrize("scenario", ["caas", "tenant"])
@pytest.mark.parametrize("provisioning_fails", [False, True])
def test_storage_tenant_uses_api_and_cleans_up_after_provisioning(
    monkeypatch: pytest.MonkeyPatch, provisioning_fails: bool, scenario: str
) -> None:
    requests: list[tuple[str, dict[str, Any] | None]] = []
    client = GRPCClient(address="api.example.test:443")
    tenant_name = ""

    def api_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        nonlocal tenant_name
        requests.append((service, data))
        if service == "osac.private.v1.Tenants/Create":
            assert data is not None
            tenant_name = data["object"]["metadata"]["name"]
            return {"object": {"id": "tenant-api-uuid", "metadata": {"name": tenant_name}}}
        if service == "osac.private.v1.Tenants/List":
            return {"items": [{"id": "tenant-api-uuid", "metadata": {"name": tenant_name}}], "total": 1}
        return {}

    monkeypatch.setattr(client, "call", api_call)
    monkeypatch.setattr(client, "ensure_bare_metal_instance_type", Mock())
    k8s = Mock(spec=K8sClient)
    k8s.namespace = "hub"
    k8s.apply.side_effect = AssertionError("Tenant setup must use the OSAC API")
    k8s.delete.side_effect = AssertionError("Tenant cleanup must use the OSAC API")
    k8s.patch.return_value = ("", 0)
    cli = Mock(spec=OsacCLI)
    cli.create_cluster.return_value = "cluster-uuid"
    module = caas if scenario == "caas" else tenant
    for name in ("wait_for_tenant_cr", "wait_for_tenant_condition", "wait_for_tenant_deletion"):
        monkeypatch.setattr(module, name, Mock())
    for name in ("wait_for_cluster_ready",):
        monkeypatch.setattr(caas, name, Mock())
    monkeypatch.setattr(caas, "wait_for_cluster_order_cr", Mock(return_value="cluster-order"))
    monkeypatch.setattr(module, "_verify_teardown", Mock())
    monkeypatch.setattr(
        module,
        "_verify_provisioning",
        Mock(side_effect=RuntimeError("storage unavailable") if provisioning_fails else None),
    )
    monkeypatch.delenv("OSAC_FULFILLMENT_TRUST_E2E", raising=False)

    def run_scenario() -> None:
        if scenario == "tenant":
            tenant.test_tenant_storage_lifecycle(
                k8s_hub_client=k8s, private_grpc=client, storage_config_namespace="storage-config"
            )
            return
        caas.test_caas_cluster_storage_lifecycle(
            k8s_hub_client=k8s,
            cli=cli,
            private_grpc=client,
            cluster_template="template",
            pull_secret_name="pull-secret",
            ssh_public_key_path="/unused/test.pub",
        )

    if provisioning_fails:
        with pytest.raises(RuntimeError, match="storage unavailable"):
            run_scenario()
    else:
        run_scenario()

    tenant_requests = [(service, data) for service, data in requests if ".Tenants/" in service]
    assert tenant_name.startswith("test-caas-storage-" if scenario == "caas" else "test-storage-")
    assert tenant_requests[0] == ("osac.private.v1.Tenants/Create", {"object": {"metadata": {"name": tenant_name}}})
    assert tenant_requests[-1] == ("osac.private.v1.Tenants/Delete", {"id": "tenant-api-uuid"})
    k8s.apply.assert_not_called()
    k8s.delete.assert_not_called()
    if scenario == "caas":
        cli.delete_cluster.assert_called_once_with(uuid="cluster-uuid")
