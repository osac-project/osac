from __future__ import annotations

import subprocess
from unittest.mock import Mock, call

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import StuckState
from tests.e2e.vmaas import networking_lifecycle_helpers as lifecycle


@pytest.fixture
def network_calls(monkeypatch: pytest.MonkeyPatch) -> Mock:
    calls = Mock()
    calls.attach_mock(Mock(spec=GRPCClient), "grpc")
    calls.attach_mock(Mock(spec=K8sClient), "k8s")
    calls.grpc.create_virtual_network.return_value = "vn-id"
    calls.grpc.get_virtual_network.return_value = {"object": {"status": {"state": "VIRTUAL_NETWORK_STATE_READY"}}}
    calls.grpc.list_virtual_network_ids.return_value = []
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", Mock())
    for operation in ("cr", "ready", "deletion"):
        name = f"wait_for_virtual_network_{operation}"
        wait = Mock(return_value="vn-cr" if operation == "cr" else None)
        calls.attach_mock(wait, name)
        monkeypatch.setattr(lifecycle, name, wait)
    return calls


def test_virtual_network_waits_for_cr_then_api_ready_before_returning(network_calls: Mock) -> None:
    network_calls.grpc.get_virtual_network.side_effect = [
        {"object": {"status": {"state": state}}}
        for state in ("VIRTUAL_NETWORK_STATE_PENDING", "VIRTUAL_NETWORK_STATE_READY")
    ]

    assert lifecycle.create_and_wait_for_virtual_network(
        network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16"
    ) == ("vn-id", "vn-cr")

    assert network_calls.mock_calls == [
        call.grpc.create_virtual_network(name="network", ipv4_cidr="172.19.0.0/16"),
        call.wait_for_virtual_network_cr(k8s=network_calls.k8s, uuid="vn-id"),
        call.wait_for_virtual_network_ready(k8s=network_calls.k8s, name="vn-cr"),
        call.grpc.get_virtual_network(vn_id="vn-id"),
        call.grpc.get_virtual_network(vn_id="vn-id"),
    ]


def test_virtual_network_api_timeout_cleans_up_before_raising(network_calls: Mock) -> None:
    network_calls.grpc.get_virtual_network.return_value = {
        "object": {"status": {"state": "VIRTUAL_NETWORK_STATE_PENDING"}}
    }

    with pytest.raises(StuckState, match="VirtualNetwork vn-id gRPC READY"):
        lifecycle.create_and_wait_for_virtual_network(network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16")

    assert network_calls.grpc.get_virtual_network.call_count == 60
    assert network_calls.mock_calls[-3:] == [
        call.grpc.delete_virtual_network(vn_id="vn-id"),
        call.wait_for_virtual_network_deletion(k8s=network_calls.k8s, name="vn-cr"),
        call.grpc.list_virtual_network_ids(),
    ]
    network_calls.grpc.create_subnet.assert_not_called()
    network_calls.grpc.create_security_group.assert_not_called()


@pytest.mark.parametrize("wait", ["cr", "ready"])
@pytest.mark.parametrize("cleanup_fails", [False, True])
def test_virtual_network_cr_failure_preserves_error_and_cleans_up(
    network_calls: Mock, wait: str, cleanup_fails: bool
) -> None:
    error = RuntimeError("CR wait failed")
    getattr(network_calls, f"wait_for_virtual_network_{wait}").side_effect = error
    if cleanup_fails:
        network_calls.grpc.delete_virtual_network.side_effect = RuntimeError("cleanup failed")

    with pytest.raises(RuntimeError, match="CR wait failed") as raised:
        lifecycle.create_and_wait_for_virtual_network(network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16")

    assert raised.value is error
    network_calls.grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-id")
    network_calls.grpc.get_virtual_network.assert_not_called()
    if wait == "ready" and not cleanup_fails:
        network_calls.wait_for_virtual_network_deletion.assert_called_once_with(k8s=network_calls.k8s, name="vn-cr")
    else:
        network_calls.wait_for_virtual_network_deletion.assert_not_called()
    if not cleanup_fails:
        network_calls.grpc.list_virtual_network_ids.assert_called_once_with()


@pytest.mark.parametrize("cleanup_step", ["delete", "cr_deletion", "api_deletion"])
def test_virtual_network_api_failure_survives_cleanup_failure(network_calls: Mock, cleanup_step: str) -> None:
    error = RuntimeError("API wait failed")
    network_calls.grpc.get_virtual_network.side_effect = error
    cleanup = {
        "delete": network_calls.grpc.delete_virtual_network,
        "cr_deletion": network_calls.wait_for_virtual_network_deletion,
        "api_deletion": network_calls.grpc.list_virtual_network_ids,
    }[cleanup_step]
    cleanup.side_effect = RuntimeError("cleanup failed")

    with pytest.raises(RuntimeError, match="API wait failed") as raised:
        lifecycle.create_and_wait_for_virtual_network(network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16")

    assert raised.value is error
    cleanup.assert_called_once()
    network_calls.grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-id")


def test_virtual_network_create_failure_does_not_delete_unknown_resource(network_calls: Mock) -> None:
    error = RuntimeError("create failed")
    network_calls.grpc.create_virtual_network.side_effect = error

    with pytest.raises(RuntimeError, match="create failed") as raised:
        lifecycle.create_and_wait_for_virtual_network(network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16")

    assert raised.value is error
    network_calls.wait_for_virtual_network_cr.assert_not_called()
    network_calls.grpc.delete_virtual_network.assert_not_called()


def test_virtual_network_retries_unavailable_and_missing_api_status(network_calls: Mock) -> None:
    network_calls.grpc.get_virtual_network.side_effect = [
        subprocess.CalledProcessError(1, "grpc"),
        {},
        {"object": {"status": {"state": "VIRTUAL_NETWORK_STATE_READY"}}},
    ]

    assert lifecycle.create_and_wait_for_virtual_network(
        network_calls.grpc, network_calls.k8s, "network", "172.19.0.0/16"
    ) == ("vn-id", "vn-cr")

    assert network_calls.grpc.get_virtual_network.call_count == 3
    network_calls.grpc.delete_virtual_network.assert_not_called()
