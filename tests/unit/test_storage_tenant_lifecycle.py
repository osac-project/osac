"""Storage scenarios must own tenant creation and cleanup through Fulfillment."""

from __future__ import annotations

import inspect
import subprocess
import traceback
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.storage import test_caas_cluster_storage as caas
from tests.e2e.storage import test_tenant_storage_lifecycle as tenant
from tests.e2e.storage.helpers import delete_storage_tenant


@pytest.mark.parametrize("scenario", ["tenant", "caas"])
@pytest.mark.parametrize("failure_stage", ["", "provisioning", "creation"])
@pytest.mark.parametrize("cleanup_stuck", [False, True])
def test_storage_scenario_uses_tenant_api_and_cleans_up(
    monkeypatch: pytest.MonkeyPatch, scenario: str, failure_stage: str, cleanup_stuck: bool
) -> None:
    module = tenant if scenario == "tenant" else caas
    run_scenario = (
        tenant.test_tenant_storage_lifecycle if scenario == "tenant" else caas.test_caas_cluster_storage_lifecycle
    )
    k8s = Mock(spec=K8sClient, namespace="test-hub")
    grpc = Mock(spec=GRPCClient)
    grpc.find_tenant_id.return_value = "tenant-uuid"
    grpc.call_unchecked.return_value = ("ERROR:\n  Code: NotFound\n  Message: Tenant not found", 1)
    if cleanup_stuck:
        grpc.call_unchecked.return_value = ('{"object": {"id": "tenant-uuid"}}', 0)
    k8s.is_absent.return_value = True
    monkeypatch.setattr(runner.time, "sleep", Mock())
    cli = Mock(spec=OsacCLI)
    cli.create_cluster.return_value = "cluster-uuid"
    verification = Mock(side_effect=AssertionError("provisioning failed") if failure_stage == "provisioning" else None)
    if failure_stage == "creation":
        create = grpc.ensure_tenant if scenario == "tenant" else cli.create_cluster
        create.side_effect = RuntimeError("creation failed")
    monkeypatch.setattr(module, "_verify_provisioning", verification)
    monkeypatch.setattr(module, "_verify_teardown", Mock())
    if scenario == "caas":
        monkeypatch.setattr(caas, "wait_for_tenant_cr", Mock())
        monkeypatch.setattr(caas, "wait_for_tenant_condition", Mock())
        monkeypatch.setattr(caas, "wait_for_cluster_order_cr", Mock(return_value="cluster-order"))
        monkeypatch.setattr(caas, "wait_for_cluster_ready", Mock())
        monkeypatch.setattr(caas, "wait_for_tenant_deletion", Mock())
        monkeypatch.delenv("OSAC_FULFILLMENT_TRUST_E2E", raising=False)
        k8s.patch.return_value = ("", 0)
    fixtures = {
        "k8s_hub_client": k8s,
        "private_grpc": grpc,
        "storage_config_namespace": "storage-config",
        "cli": cli,
        "cluster_template": "cluster-template",
        "pull_secret_name": "pull-secret",
        "ssh_public_key_path": "/test/id.pub",
    }
    # Use the real scenario's fixture signature, including the pre-fix signature.
    kwargs = {name: fixtures[name] for name in inspect.signature(run_scenario).parameters}
    if cleanup_stuck:
        with pytest.raises(TimeoutError):
            run_scenario(**kwargs)
    elif failure_stage == "provisioning":
        with pytest.raises(AssertionError, match="provisioning failed"):
            run_scenario(**kwargs)
    elif failure_stage == "creation":
        with pytest.raises(RuntimeError, match="creation failed"):
            run_scenario(**kwargs)
    else:
        run_scenario(**kwargs)

    grpc.ensure_tenant.assert_called_once()
    tenant_name = grpc.ensure_tenant.call_args.kwargs["name"]
    if failure_stage == "creation":
        verification.assert_not_called()
        cli.delete_cluster.assert_not_called()
    else:
        assert verification.call_args.kwargs["tenant_name"] == tenant_name
    grpc.find_tenant_id.assert_called_with(name=tenant_name)
    grpc.call.assert_called_once_with(service="osac.private.v1.Tenants/Delete", data={"id": "tenant-uuid"})
    grpc.call_unchecked.assert_called_with(service="osac.private.v1.Tenants/Get", data={"id": "tenant-uuid"})
    if not cleanup_stuck:
        k8s.is_absent.assert_any_call(resource="namespaces", name=tenant_name)
    k8s.apply.assert_not_called()
    k8s.delete.assert_not_called()


@pytest.mark.parametrize(
    "output",
    [
        "ERROR:\n  Code: Unavailable\n  Message: service unavailable",
        "ERROR:\n  Code: PermissionDenied\n  Message: NotFound is not the status",
    ],
)
@pytest.mark.parametrize("failing_service", ["Signal", "Get"])
def test_tenant_cleanup_does_not_mistake_api_errors_for_absence(output: str, failing_service: str) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.find_tenant_id.return_value = "tenant-uuid"
    grpc.call_unchecked.side_effect = lambda **kwargs: (
        (output, 1) if kwargs["service"].endswith(f"/{failing_service}") else ("{}", 0)
    )
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.return_value = True
    with pytest.raises(RuntimeError, match=r"cleanup (lookup|signal) failed"):
        delete_storage_tenant(grpc=grpc, k8s=k8s, tenant_name="test-tenant")


def test_tenant_cleanup_requires_namespace_removal(monkeypatch: pytest.MonkeyPatch) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.find_tenant_id.return_value = ""
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.return_value = False
    monkeypatch.setattr(runner.time, "sleep", Mock())
    with pytest.raises(TimeoutError):
        delete_storage_tenant(grpc=grpc, k8s=k8s, tenant_name="test-tenant")
    grpc.call.assert_not_called()
    k8s.delete.assert_not_called()


@pytest.mark.parametrize("times_out", [False, True])
def test_tenant_cleanup_sanitizes_command_failures(times_out: bool) -> None:
    secret = "sentinel-bearer-token"
    command = ["grpcurl", "-H", f"Authorization: Bearer {secret}"]
    failure = subprocess.TimeoutExpired(command, 300) if times_out else subprocess.CalledProcessError(1, command)
    grpc = Mock(spec=GRPCClient)
    grpc.find_tenant_id.side_effect = failure
    with pytest.raises(RuntimeError, match="cleanup request failed") as captured:
        delete_storage_tenant(grpc=grpc, k8s=Mock(spec=K8sClient), tenant_name="test-tenant")
    assert secret not in "".join(traceback.format_exception(captured.value))


def test_tenant_cleanup_signals_only_after_storage_cr_removal(monkeypatch: pytest.MonkeyPatch) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.find_tenant_id.return_value = "tenant-id"
    grpc.call_unchecked.return_value = ("ERROR:\n  Code: NotFound\n  Message: Tenant not found", 1)
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.side_effect = [False, True, True]
    observed = Mock()
    observed.attach_mock(grpc, "grpc")
    observed.attach_mock(k8s, "k8s")
    monkeypatch.setattr(runner.time, "sleep", Mock())

    delete_storage_tenant(grpc=grpc, k8s=k8s, tenant_name="test-tenant")

    assert observed.mock_calls == [
        call.grpc.find_tenant_id(name="test-tenant"),
        call.grpc.call(service="osac.private.v1.Tenants/Delete", data={"id": "tenant-id"}),
        call.k8s.is_absent(resource="tenant", name="test-tenant"),
        call.k8s.is_absent(resource="tenant", name="test-tenant"),
        call.grpc.call_unchecked(service="osac.private.v1.Tenants/Signal", data={"id": "tenant-id"}),
        call.grpc.call_unchecked(service="osac.private.v1.Tenants/Get", data={"id": "tenant-id"}),
        call.k8s.is_absent(resource="namespaces", name="test-tenant"),
    ]
