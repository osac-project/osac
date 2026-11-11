from __future__ import annotations

from unittest.mock import Mock, call

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import CommandFailed, StuckState
from tests.e2e.vmaas.sanity import test_jwt_auth_smoke as jwt_smoke


@pytest.fixture
def lifecycle_clients(monkeypatch: pytest.MonkeyPatch) -> tuple[Mock, Mock, Mock]:
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    sleep = Mock()
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", sleep)
    grpc.create_virtual_network.return_value = "vn-id"
    grpc.get_virtual_network.return_value = {"object": {"status": {"state": "VIRTUAL_NETWORK_STATE_READY"}}}
    grpc.create_subnet.return_value = "subnet-id"
    grpc.get_subnet.return_value = {"object": {"status": {"state": "SUBNET_STATE_READY"}}}
    grpc.create_security_group.return_value = "sg-id"
    grpc.list_security_group_ids.return_value = ["sg-id"]
    grpc.list_subnet_ids.return_value = []
    k8s.get_virtual_network_name.return_value = "vn-cr"
    k8s.get_subnet_name.return_value = "subnet-cr"
    k8s.get_security_group_name.return_value = "sg-cr"
    k8s.is_present.return_value = False
    return grpc, k8s, sleep


def test_jwt_security_group_cleanup_waits_for_api_absence(lifecycle_clients: tuple[Mock, Mock, Mock]) -> None:
    grpc, k8s, sleep = lifecycle_clients

    def list_security_groups() -> list[str]:
        if grpc.list_security_group_ids.call_count == 1:
            return ["sg-id"]
        grpc.delete_security_group.assert_called_once_with(sg_id="sg-id")
        k8s.is_present.assert_called_once_with(resource="securitygroups.osac.openshift.io", name="sg-cr")
        grpc.delete_subnet.assert_not_called()
        grpc.delete_virtual_network.assert_not_called()
        return ["sg-id"] if grpc.list_security_group_ids.call_count < 4 else []

    def delete_subnet(*, subnet_id: str) -> None:
        assert subnet_id == "subnet-id"
        assert grpc.list_security_group_ids.call_count == 4

    def list_subnets() -> list[str]:
        grpc.delete_subnet.assert_called_once_with(subnet_id="subnet-id")
        k8s.is_present.assert_any_call(resource="subnets.osac.openshift.io", name="subnet-cr")
        grpc.delete_virtual_network.assert_not_called()
        return ["subnet-id"] if grpc.list_subnet_ids.call_count == 1 else []

    def delete_virtual_network(*, vn_id: str) -> None:
        assert vn_id == "vn-id"
        assert grpc.list_security_group_ids.call_count == 4
        assert grpc.list_subnet_ids.call_count == 2

    grpc.list_security_group_ids.side_effect = list_security_groups
    grpc.delete_subnet.side_effect = delete_subnet
    grpc.list_subnet_ids.side_effect = list_subnets
    grpc.delete_virtual_network.side_effect = delete_virtual_network

    jwt_smoke.test_jwt_security_group_lifecycle(grpc, k8s)

    grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-id")
    assert sleep.call_args_list == [call(5), call(5), call(2)]
    assert k8s.is_present.call_args_list == [
        call(resource="securitygroups.osac.openshift.io", name="sg-cr"),
        call(resource="subnets.osac.openshift.io", name="subnet-cr"),
        call(resource="virtualnetwork", name="vn-cr"),
    ]


def test_jwt_security_group_cleanup_times_out_before_parent_deletion(
    lifecycle_clients: tuple[Mock, Mock, Mock],
) -> None:
    grpc, k8s, sleep = lifecycle_clients

    with pytest.raises(StuckState, match="SecurityGroup sg-id removal from API") as failure:
        jwt_smoke.test_jwt_security_group_lifecycle(grpc, k8s)

    assert failure.value.last_value == "False"
    assert grpc.list_security_group_ids.call_count == 31
    assert sleep.call_args_list == [call(5)] * 29
    k8s.is_present.assert_called_once_with(resource="securitygroups.osac.openshift.io", name="sg-cr")
    grpc.delete_security_group.assert_called_once_with(sg_id="sg-id")
    grpc.delete_subnet.assert_not_called()
    grpc.delete_virtual_network.assert_not_called()
    grpc.list_subnet_ids.assert_not_called()


def test_jwt_security_group_cleanup_propagates_api_list_error(lifecycle_clients: tuple[Mock, Mock, Mock]) -> None:
    grpc, k8s, sleep = lifecycle_clients
    error = CommandFailed(7, ["grpcurl", "SecurityGroups/List"], stderr="Code: PermissionDenied")
    grpc.list_security_group_ids.side_effect = [["sg-id"], error]

    with pytest.raises(CommandFailed, match="PermissionDenied") as failure:
        jwt_smoke.test_jwt_security_group_lifecycle(grpc, k8s)

    assert failure.value is error
    assert grpc.list_security_group_ids.call_count == 2
    k8s.is_present.assert_called_once_with(resource="securitygroups.osac.openshift.io", name="sg-cr")
    grpc.delete_security_group.assert_called_once_with(sg_id="sg-id")
    grpc.delete_subnet.assert_not_called()
    grpc.delete_virtual_network.assert_not_called()
    grpc.list_subnet_ids.assert_not_called()
    sleep.assert_not_called()
