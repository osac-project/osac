from __future__ import annotations

import ipaddress
import json
import subprocess
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from tests.e2e.core import runner
from tests.e2e.core.grpc_client import PUBLIC_API
from tests.e2e.vmaas.regression import test_vmaas_vm_lifecycle_demo as scenario


@pytest.fixture
def failed_create(monkeypatch: pytest.MonkeyPatch) -> SimpleNamespace:
    """Reach the real scenario's CLI create, with only backend calls mocked."""
    grpc, private_grpc, cli, private_cli = Mock(), Mock(), Mock(), Mock()
    grpc.with_read_connection_retries.return_value = grpc
    private_grpc.with_read_connection_retries.return_value = private_grpc
    private_grpc.list_network_classes.return_value = [{"id": "class-id", "k8s_manager": "k8s_only"}]
    private_grpc.call.return_value = {"object": {"spec": {"networkClass": {"id": "class-id"}}}}
    cli.describe_instance_type.return_value = "ACTIVE vCPUs: 1 Memory (GiB): 2"
    cli.get.return_value = json.dumps([{"id": "catalog-id"}])
    grpc.create_compute_instance_catalog_item.return_value = "catalog-id"
    grpc.create_virtual_network.return_value = "vn-id"
    grpc.list_virtual_network_ids.return_value = ["vn-id"]
    grpc.get_virtual_network.return_value = {"object": {"status": {"state": "VIRTUAL_NETWORK_STATE_READY"}}}
    grpc.create_subnet.return_value = "subnet-id"
    grpc.list_subnet_ids.return_value = ["subnet-id"]
    grpc.get_subnet.return_value = {
        "object": {
            "metadata": {"name": "e2e-subnet-12345678"},
            "spec": {"virtualNetwork": {"id": "vn-id"}, "ipv4Cidr": "172.27.0.0/24"},
            "status": {"state": "SUBNET_STATE_READY"},
        }
    }
    grpc.create_security_group_with_rules.return_value = "sg-id"
    grpc.list_security_group_ids.return_value = ["sg-id"]
    grpc.get_security_group.side_effect = lambda **kwargs: {
        "object": {
            "spec": {
                "virtualNetwork": {"id": "vn-id"},
                "ingress": grpc.create_security_group_with_rules.call_args.kwargs["ingress"],
                "egress": grpc.create_security_group_with_rules.call_args.kwargs["egress"],
            },
            "status": {"state": "SECURITY_GROUP_STATE_READY"},
        }
    }
    grpc.list_with_filter.return_value = [{"id": "ci-id", "metadata": {"name": "created-vm"}}]
    cli.create_compute_instance_with_catalog_item.side_effect = AssertionError("cannot parse UUID")
    monkeypatch.setattr(scenario, "uuid4", lambda: SimpleNamespace(hex="12345678"))
    monkeypatch.setattr(scenario, "unique_name", lambda prefix: "created-vm")
    monkeypatch.setattr(scenario, "allocate_worker_subnet", lambda: ipaddress.ip_network("172.27.0.0/24"))
    monkeypatch.setattr(runner.time, "sleep", Mock())
    for function in (
        "wait_for_virtual_network_cr",
        "wait_for_virtual_network_ready",
        "wait_for_subnet_cr",
        "wait_for_subnet_ready",
        "wait_for_security_group_cr",
        "wait_for_security_group_ready",
    ):
        monkeypatch.setattr(scenario, function, Mock(return_value="test-cr"))
    resolve_cr = Mock(return_value="ci-cr")
    monkeypatch.setattr(scenario, "wait_for_cr", resolve_cr)
    events = []
    cli.delete_compute_instance.side_effect = lambda **kwargs: events.append("delete VM")
    monkeypatch.setattr(scenario, "wait_for_deletion", lambda **kwargs: events.append("wait CR"))
    monkeypatch.setattr(scenario, "wait_for_grpc_removal", lambda **kwargs: events.append("wait API"))
    monkeypatch.setattr(scenario, "delete_and_wait_for_security_group", lambda *args: events.append("delete SG"))
    monkeypatch.setattr(scenario, "delete_and_wait_for_subnet", lambda *args: events.append("delete Subnet"))
    monkeypatch.setattr(scenario, "delete_and_wait_for_virtual_network", lambda *args: events.append("delete VN"))
    grpc.delete_compute_instance_catalog_item.side_effect = lambda **kwargs: events.append("delete catalog")
    private_grpc.delete_instance_type.side_effect = lambda **kwargs: events.append("delete InstanceType")
    return SimpleNamespace(
        grpc=grpc, private_grpc=private_grpc, cli=cli, private_cli=private_cli, events=events, resolve_cr=resolve_cr
    )


def run_scenario(state: SimpleNamespace) -> None:
    scenario.test_vmaas_vm_lifecycle_demo(
        cli=state.cli,
        private_cli=state.private_cli,
        grpc=state.grpc,
        private_grpc=state.private_grpc,
        k8s_hub_client=Mock(),
        k8s_virt_client=Mock(),
        vm_template="template",
        default_storage_tier="local",
        default_disk_image="image",
        edit_lifecycle=Mock(),
        temporary_scratch_storage=None,
    )


@pytest.mark.parametrize("failure", ["parse", "timeout"])
def test_accepted_create_is_cleaned_when_cli_does_not_return_uuid(failed_create: SimpleNamespace, failure: str) -> None:
    """A lost create response must not skip VM deletion or reorder dependents."""
    if failure == "timeout":
        failed_create.cli.create_compute_instance_with_catalog_item.side_effect = subprocess.TimeoutExpired("osac", 300)
        expected = RuntimeError
    else:
        expected = AssertionError
    with pytest.raises(expected):
        run_scenario(failed_create)

    assert failed_create.cli.create_compute_instance_with_catalog_item.call_args.kwargs["name"] == "created-vm"
    failed_create.grpc.list_with_filter.assert_called_once_with(
        service=f"{PUBLIC_API}.ComputeInstances/List", filter_expr='this.metadata.name == "created-vm"'
    )
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")
    assert failed_create.events == [
        "delete VM",
        "wait CR",
        "wait API",
        "delete SG",
        "delete Subnet",
        "delete VN",
        "delete catalog",
        "delete InstanceType",
    ]


def test_rejected_create_does_not_delete_another_vm(failed_create: SimpleNamespace) -> None:
    failed_create.grpc.list_with_filter.return_value = []
    with pytest.raises(AssertionError, match="cannot parse UUID"):
        run_scenario(failed_create)
    failed_create.cli.delete_compute_instance.assert_not_called()
    assert failed_create.grpc.list_with_filter.call_count == 60
    assert runner.time.sleep.call_count == 59
    runner.time.sleep.assert_called_with(2)
    assert failed_create.events == ["delete SG", "delete Subnet", "delete VN", "delete catalog", "delete InstanceType"]


@pytest.mark.parametrize("empty_results", [1, 3])
def test_delayed_create_visibility_is_cleaned(failed_create: SimpleNamespace, empty_results: int) -> None:
    failed_create.grpc.list_with_filter.side_effect = [
        *[[] for _ in range(empty_results)],
        [{"id": "ci-id", "metadata": {"name": "created-vm"}}],
    ]
    with pytest.raises(AssertionError, match="cannot parse UUID"):
        run_scenario(failed_create)
    assert failed_create.grpc.list_with_filter.call_count == empty_results + 1
    assert runner.time.sleep.call_count == empty_results
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")
    assert failed_create.events[:4] == ["delete VM", "wait CR", "wait API", "delete SG"]


@pytest.mark.parametrize(
    "matches",
    [
        [{"id": "other-id", "metadata": {"name": "other-vm"}}],
        [{"id": "ci-id", "metadata": {"name": "created-vm"}}, {"id": "other-id", "metadata": {"name": "created-vm"}}],
    ],
)
def test_ambiguous_or_wrong_name_is_never_deleted(failed_create: SimpleNamespace, matches: list[dict]) -> None:
    failed_create.grpc.list_with_filter.return_value = matches
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    assert "find ComputeInstance after incomplete CLI create" in str(caught.value.exceptions[1])
    failed_create.cli.delete_compute_instance.assert_not_called()


def test_failed_cr_lookup_still_deletes_recovered_api_id(failed_create: SimpleNamespace) -> None:
    failed_create.resolve_cr.side_effect = TimeoutError("CR was not observed")
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")
    assert failed_create.events[:2] == ["delete VM", "wait API"]


def test_failure_before_vm_create_needs_no_name_lookup(failed_create: SimpleNamespace) -> None:
    failed_create.private_cli.create_instance_type.side_effect = AssertionError("InstanceType create failed")
    with pytest.raises(AssertionError, match="InstanceType create failed"):
        run_scenario(failed_create)
    failed_create.grpc.list_with_filter.assert_not_called()
    failed_create.cli.delete_compute_instance.assert_not_called()


def test_known_uuid_uses_existing_cleanup_without_name_lookup(failed_create: SimpleNamespace) -> None:
    failed_create.cli.create_compute_instance_with_catalog_item.side_effect = None
    failed_create.cli.create_compute_instance_with_catalog_item.return_value = "ci-id"
    failed_create.grpc.get_compute_instance.side_effect = AssertionError("failed after create")
    with pytest.raises(AssertionError, match="failed after create"):
        run_scenario(failed_create)
    failed_create.grpc.list_with_filter.assert_not_called()
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")


def test_lookup_failure_preserves_primary_error_and_redacts_credentials(failed_create: SimpleNamespace) -> None:
    failed_create.grpc.list_with_filter.side_effect = subprocess.CalledProcessError(
        7, ["grpcurl", "-H", "Authorization: Bearer unit-test-secret"], stderr="unit-test-secret"
    )
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    assert "unit-test-secret" not in str(caught.value.exceptions[1])
    failed_create.cli.delete_compute_instance.assert_not_called()
    assert failed_create.events == ["delete SG", "delete Subnet", "delete VN", "delete catalog", "delete InstanceType"]
