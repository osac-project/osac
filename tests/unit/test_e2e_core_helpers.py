from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient


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


def test_wait_for_tenant_default_networking_ready_waits_for_all_resources(monkeypatch: pytest.MonkeyPatch) -> None:
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
                        "reason": "AllResourcesReady",
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
