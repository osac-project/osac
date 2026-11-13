from __future__ import annotations

from inspect import unwrap
from unittest.mock import Mock, call

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import StuckState
from tests.e2e.vmaas.sanity import test_metering_billing as billing


@pytest.fixture
def network_calls(monkeypatch: pytest.MonkeyPatch) -> Mock:
    calls = Mock()
    calls.attach_mock(Mock(spec=GRPCClient), "grpc")
    calls.attach_mock(Mock(spec=K8sClient), "k8s")
    calls.grpc.create_virtual_network.return_value = "vn-id"
    calls.grpc.create_subnet.return_value = "subnet-id"
    calls.grpc.create_security_group_with_rules.return_value = "sg-id"
    calls.grpc.get_virtual_network.return_value = {"object": {"status": {"state": "VIRTUAL_NETWORK_STATE_PENDING"}}}
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", Mock())
    for resource in ("virtual_network", "subnet", "security_group"):
        for operation in ("cr", "ready", "deletion"):
            name = f"wait_for_{resource}_{operation}"
            wait = Mock(return_value=f"{resource}-cr" if operation == "cr" else None)
            calls.attach_mock(wait, name)
            monkeypatch.setattr(billing, name, wait)
    for resource in ("subnet", "security_group"):
        name = f"wait_for_grpc_{resource}_ready"
        wait = Mock()
        calls.attach_mock(wait, name)
        monkeypatch.setattr(billing, name, wait)
    return calls


def test_network_attachment_waits_for_api_vn_before_children_and_cleans_up(network_calls: Mock) -> None:
    network_calls.grpc.get_virtual_network.side_effect = [
        {"object": {"status": {"state": state}}}
        for state in ("VIRTUAL_NETWORK_STATE_PENDING", "VIRTUAL_NETWORK_STATE_READY")
    ]
    attachment = unwrap(billing.metering_network_attachment)(network_calls.grpc, network_calls.k8s, "project")
    try:
        assert next(attachment) == {"subnet": "subnet-id", "security_groups": ["sg-id"]}
        assert [event[0] for event in network_calls.mock_calls] == [
            "grpc.create_virtual_network",
            "wait_for_virtual_network_cr",
            "wait_for_virtual_network_ready",
            "grpc.get_virtual_network",
            "grpc.get_virtual_network",
            "grpc.create_subnet",
            "wait_for_subnet_cr",
            "wait_for_subnet_ready",
            "wait_for_grpc_subnet_ready",
            "grpc.create_security_group_with_rules",
            "wait_for_security_group_cr",
            "wait_for_security_group_ready",
            "wait_for_grpc_security_group_ready",
        ]
        assert network_calls.grpc.get_virtual_network.call_args_list == [call(vn_id="vn-id")] * 2
        assert network_calls.grpc.create_subnet.call_args.kwargs["virtual_network"] == "vn-id"
        assert network_calls.grpc.create_security_group_with_rules.call_args.kwargs["virtual_network"] == "vn-id"
        network_calls.grpc.delete_virtual_network.assert_not_called()
    finally:
        attachment.close()
    assert network_calls.mock_calls[-6:] == [
        call.grpc.delete_security_group(sg_id="sg-id"),
        call.wait_for_security_group_deletion(k8s=network_calls.k8s, name="security_group-cr"),
        call.grpc.delete_subnet(subnet_id="subnet-id"),
        call.wait_for_subnet_deletion(k8s=network_calls.k8s, name="subnet-cr"),
        call.grpc.delete_virtual_network(vn_id="vn-id"),
        call.wait_for_virtual_network_deletion(k8s=network_calls.k8s, name="virtual_network-cr"),
    ]


def test_network_attachment_api_vn_timeout_creates_no_children_and_cleans_up(network_calls: Mock) -> None:
    attachment = unwrap(billing.metering_network_attachment)(network_calls.grpc, network_calls.k8s, "project")
    try:
        with pytest.raises(StuckState, match="VirtualNetwork vn-id gRPC READY"):
            next(attachment)
    finally:
        attachment.close()
    assert network_calls.grpc.get_virtual_network.call_count == 60
    network_calls.grpc.create_subnet.assert_not_called()
    network_calls.grpc.create_security_group_with_rules.assert_not_called()
    network_calls.grpc.delete_subnet.assert_not_called()
    network_calls.grpc.delete_security_group.assert_not_called()
    network_calls.grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-id")
    network_calls.wait_for_virtual_network_deletion.assert_called_once_with(
        k8s=network_calls.k8s, name="virtual_network-cr"
    )
