from __future__ import annotations

import json
import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.wait_kinds import WaitResourceKind


def test_wait_for_new_vmi_retries_lookup_errors_until_timestamp_changes(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_vmi_creation_timestamp.side_effect = [
        "original",
        subprocess.CalledProcessError(returncode=1, cmd="kubectl", stderr="array index out of bounds"),
        "",
        "recreated",
    ]
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    result = helpers.wait_for_new_vmi(
        k8s=k8s, vmi_namespace="vm-namespace", compute_instance_name="compute-instance", initial_timestamp="original"
    )

    assert result == "recreated"
    assert k8s.get_vmi_creation_timestamp.call_count == 4


def test_wait_helpers_pass_resource_identity_to_poll_until(monkeypatch: pytest.MonkeyPatch) -> None:
    poll = Mock(return_value=True)
    monkeypatch.setattr(helpers, "poll_until", poll)

    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True
    helpers.wait_for_tenant_cr(k8s=k8s, name="tenant-one")

    grpc = Mock(spec=GRPCClient)
    grpc.list_baremetal_instance_ids.return_value = ["bmi-one"]
    helpers.wait_for_bmi_grpc_removal(grpc=grpc, uuid="bmi-one")

    tenant_call, bmi_call = poll.call_args_list
    assert tenant_call.kwargs["resource_kind"] is WaitResourceKind.TENANT
    assert tenant_call.kwargs["resource_id"] == "tenant-one"
    assert bmi_call.kwargs["resource_kind"] is WaitResourceKind.BARE_METAL_INSTANCE
    assert bmi_call.kwargs["resource_id"] == "bmi-one"


def test_delete_instance_type_if_present_ignores_not_found() -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.delete_instance_type.side_effect = subprocess.CalledProcessError(
        returncode=1, cmd="grpcurl", stderr="instance type not found"
    )

    helpers.delete_instance_type_if_present(grpc=grpc, name="test-instance-type")


def test_delete_instance_type_if_present_propagates_other_errors() -> None:
    grpc = Mock(spec=GRPCClient)
    error = subprocess.CalledProcessError(returncode=1, cmd="grpcurl", stderr="permission denied")
    grpc.delete_instance_type.side_effect = error

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        helpers.delete_instance_type_if_present(grpc=grpc, name="test-instance-type")

    assert exc_info.value is error


@pytest.mark.parametrize("success_reason", ["AllResourcesReady", "ProvisioningDisabled"])
def test_wait_for_tenant_default_networking_ready_waits_for_all_resources(
    monkeypatch: pytest.MonkeyPatch, success_reason: str
) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.get_tenant_by_name.side_effect = [
        {
            "status": {
                "conditions": [
                    {
                        "type": "TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY",
                        "status": "CONDITION_STATUS_FALSE",
                        "reason": "ResourcesPending",
                        "message": "SecurityGroup/default",
                    }
                ]
            }
        },
        {
            "status": {
                "conditions": [
                    {
                        "type": "TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY",
                        "status": "CONDITION_STATUS_TRUE",
                        "reason": success_reason,
                        "message": "All defaults are ready",
                    }
                ]
            }
        },
    ]
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    helpers.wait_for_tenant_default_networking_ready(grpc=grpc, tenant_name="tenant1")

    assert grpc.get_tenant_by_name.call_count == 2
    grpc.get_tenant_by_name.assert_called_with(name="tenant1")


@pytest.mark.parametrize(
    ("reason", "status"), [("NoDefaultNetworking", "True"), ("ResourceFailed", "False"), ("ReservedTenant", "True")]
)
def test_wait_for_tenant_default_networking_ready_fails_on_terminal_conditions(reason: str, status: str) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.get_tenant_by_name.return_value = {
        "status": {
            "conditions": [
                {
                    "type": "TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY",
                    "status": f"CONDITION_STATUS_{status.upper()}",
                    "reason": reason,
                    "message": "fixture networking state",
                }
            ]
        }
    }

    with pytest.raises(RuntimeError, match=reason) as exc_info:
        helpers.wait_for_tenant_default_networking_ready(grpc=grpc, tenant_name="tenant1")

    assert "fixture networking state" in str(exc_info.value)
    assert grpc.get_tenant_by_name.call_count == 1


def test_wait_for_tenant_default_networking_ready_times_out_with_missing_condition(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.get_tenant_by_name.return_value = {"status": {"conditions": []}}
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    with pytest.raises(TimeoutError, match="last status=<missing>, reason=<missing>, message=<no message>"):
        helpers.wait_for_tenant_default_networking_ready(grpc=grpc, tenant_name="tenant1")

    assert grpc.get_tenant_by_name.call_count == 120


def test_wait_for_tenant_default_networking_ready_does_not_swallow_grpc_errors() -> None:
    grpc = Mock(spec=GRPCClient)
    error = subprocess.CalledProcessError(returncode=1, cmd="grpcurl", stderr="permission denied")
    grpc.get_tenant_by_name.side_effect = error

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        helpers.wait_for_tenant_default_networking_ready(grpc=grpc, tenant_name="tenant1")

    assert exc_info.value is error
    assert grpc.get_tenant_by_name.call_count == 1


def test_wait_for_tenant_default_networking_ready_narrows_the_accepted_reason(monkeypatch: pytest.MonkeyPatch) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.get_tenant_by_name.return_value = {
        "status": {
            "conditions": [
                {
                    "type": "TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY",
                    "status": "CONDITION_STATUS_TRUE",
                    "reason": "AllResourcesReady",
                    "message": "All defaults are ready",
                }
            ]
        }
    }
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    with pytest.raises(TimeoutError, match=r"\['ProvisioningDisabled'\]"):
        helpers.wait_for_tenant_default_networking_ready(
            grpc=grpc, tenant_name="tenant1", expected_reasons={"ProvisioningDisabled"}
        )


@pytest.mark.parametrize("installed", [True, False])
def test_read_installed_networking_mode_returns_the_stored_boolean(installed: bool) -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.call.return_value = {"networkingProvisioningEnabled": installed}

    assert helpers.read_installed_networking_mode(private_grpc=grpc) is installed
    grpc.call.assert_called_once_with(service="osac.private.v1.InstallationSettings/Get", data={}, emit_defaults=True)


def test_read_installed_networking_mode_rejects_a_missing_boolean() -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.call.return_value = {}

    with pytest.raises(RuntimeError, match="boolean networkingProvisioningEnabled"):
        helpers.read_installed_networking_mode(private_grpc=grpc)


def test_read_installed_networking_mode_rejects_malformed_json() -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.call.side_effect = json.JSONDecodeError("not json", "", 0)

    with pytest.raises(RuntimeError, match="malformed JSON"):
        helpers.read_installed_networking_mode(private_grpc=grpc)


def test_read_installed_networking_mode_reports_rpc_failure() -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.call.side_effect = subprocess.CalledProcessError(returncode=1, cmd="grpcurl", stderr="permission denied")

    with pytest.raises(RuntimeError, match="permission denied"):
        helpers.read_installed_networking_mode(private_grpc=grpc)


def test_assert_no_network_jobs_fails_on_a_recorded_launch() -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_status_jobs.return_value = [{"id": "job-1"}]

    with pytest.raises(AssertionError, match="no provider networking job"):
        helpers.assert_no_network_jobs(k8s=k8s, resource="virtualnetwork", name="vn", context="test")

    k8s.get_status_jobs.return_value = []
    helpers.assert_no_network_jobs(k8s=k8s, resource="virtualnetwork", name="vn", context="test")


def test_assert_no_network_manager_jobs_ignores_the_workload_provision_job() -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_status_jobs.return_value = [{"task": "provision", "manager": "none", "state": "Succeeded"}]

    helpers.assert_no_network_manager_jobs(k8s=k8s, resource="computeinstance", name="ci", context="test")

    k8s.get_status_jobs.return_value = [
        {"task": "provision", "manager": "none", "state": "Succeeded"},
        {"task": "apply_network_attachment", "manager": "fabric", "state": "Succeeded"},
    ]
    with pytest.raises(AssertionError, match="no networking manager task"):
        helpers.assert_no_network_manager_jobs(k8s=k8s, resource="computeinstance", name="ci", context="test")


def test_assert_no_network_attachment_matches_only_the_target() -> None:
    k8s = Mock(spec=K8sClient)
    k8s.list_json_all_namespaces.return_value = {
        "items": [{"metadata": {"name": "na-other"}, "spec": {"target": {"kind": "ComputeInstance", "id": "other"}}}]
    }
    helpers.assert_no_network_attachment(k8s=k8s, target_kind="ComputeInstance", target_id="ci-1")

    k8s.list_json_all_namespaces.return_value = {
        "items": [{"metadata": {"name": "na-mine"}, "spec": {"target": {"kind": "ComputeInstance", "id": "ci-1"}}}]
    }
    with pytest.raises(AssertionError, match="na-mine"):
        helpers.assert_no_network_attachment(k8s=k8s, target_kind="ComputeInstance", target_id="ci-1")


def test_assert_no_default_external_access_rejects_reserved_names() -> None:
    grpc = Mock(spec=GRPCClient)
    grpc.call.return_value = {"items": [{"metadata": {"name": "default-nat"}}]}

    with pytest.raises(AssertionError, match="default-nat"):
        helpers.assert_no_default_external_access(private_grpc=grpc)

    grpc.call.return_value = {"items": [{"metadata": {"name": "tenant-owned-eip"}}]}
    helpers.assert_no_default_external_access(private_grpc=grpc)


def test_k8s_client_reads_condition_and_jobs(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = K8sClient(namespace="osac")
    payload = {
        "status": {
            "conditions": [{"type": "Ready", "status": "True", "reason": "ProvisioningDisabled"}],
            "jobs": [{"id": "job-1"}],
        }
    }
    monkeypatch.setattr(k8s, "get_json", Mock(return_value=payload))

    condition = k8s.get_condition(resource="virtualnetwork", name="vn", condition_type="Ready")
    assert condition["reason"] == "ProvisioningDisabled"
    assert k8s.get_condition(resource="virtualnetwork", name="vn", condition_type="Other") == {}
    assert k8s.get_status_jobs(resource="virtualnetwork", name="vn") == [{"id": "job-1"}]
