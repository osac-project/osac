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
