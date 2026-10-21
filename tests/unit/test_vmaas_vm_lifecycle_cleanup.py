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
    """Exercise the actual lifecycle body through mocked clients and readiness helpers."""
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
    """Exhaust the name-lookup budget before cleaning dependents without deleting a VM."""
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
    """Find a later API result and finish VM deletion before network cleanup."""
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
    """Preserve the primary failure and refuse potentially unrelated VM deletion."""
    failed_create.grpc.list_with_filter.return_value = matches
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    assert "find ComputeInstance after incomplete CLI create" in str(caught.value.exceptions[1])
    failed_create.cli.delete_compute_instance.assert_not_called()


def test_failed_cr_lookup_still_deletes_recovered_api_id(failed_create: SimpleNamespace) -> None:
    """Use a recovered API ID for deletion even when its Kubernetes CR cannot be observed."""
    failed_create.resolve_cr.side_effect = TimeoutError("CR was not observed")
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")
    assert failed_create.events[:2] == ["delete VM", "wait API"]


def test_failure_before_vm_create_needs_no_name_lookup(failed_create: SimpleNamespace) -> None:
    """Avoid searching or deleting VMs when execution never reached their creation."""
    failed_create.private_cli.create_instance_type.side_effect = AssertionError("InstanceType create failed")
    with pytest.raises(AssertionError, match="InstanceType create failed"):
        run_scenario(failed_create)
    failed_create.grpc.list_with_filter.assert_not_called()
    failed_create.cli.delete_compute_instance.assert_not_called()


def test_known_uuid_uses_existing_cleanup_without_name_lookup(failed_create: SimpleNamespace) -> None:
    """Use the retained UUID directly instead of adding a name-based API request."""
    failed_create.cli.create_compute_instance_with_catalog_item.side_effect = None
    failed_create.cli.create_compute_instance_with_catalog_item.return_value = "ci-id"
    failed_create.grpc.get_compute_instance.side_effect = AssertionError("failed after create")
    with pytest.raises(AssertionError, match="failed after create"):
        run_scenario(failed_create)
    failed_create.grpc.list_with_filter.assert_not_called()
    failed_create.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")


def test_lookup_failure_preserves_primary_error_and_redacts_credentials(failed_create: SimpleNamespace) -> None:
    """Keep the original scenario error while sanitizing credentials in the lookup failure."""
    test_secret = "unit-test-secret"
    failed_create.grpc.list_with_filter.side_effect = subprocess.CalledProcessError(
        7, ["grpcurl", "-H", f"Authorization: Bearer {test_secret}"], stderr=test_secret
    )
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], AssertionError)
    assert test_secret not in str(caught.value.exceptions[1])
    failed_create.cli.delete_compute_instance.assert_not_called()
    assert failed_create.events == ["delete SG", "delete Subnet", "delete VN", "delete catalog", "delete InstanceType"]


@pytest.mark.parametrize("failure", ["timeout", "decode"])
@pytest.mark.parametrize(
    ("create_method", "collection", "creation_name", "delete_helper", "expected_events"),
    [
        (
            "create_compute_instance_catalog_item",
            "ComputeInstanceCatalogItems",
            "e2e-catalog-12345678",
            None,
            ["delete catalog", "delete InstanceType"],
        ),
        (
            "create_virtual_network",
            "VirtualNetworks",
            "e2e-vnet-12345678",
            "delete_and_wait_for_virtual_network",
            ["delete VN", "delete catalog", "delete InstanceType"],
        ),
        (
            "create_subnet",
            "Subnets",
            "e2e-subnet-12345678",
            "delete_and_wait_for_subnet",
            ["delete Subnet", "delete VN", "delete catalog", "delete InstanceType"],
        ),
        (
            "create_security_group_with_rules",
            "SecurityGroups",
            "e2e-sg-12345678",
            "delete_and_wait_for_security_group",
            ["delete SG", "delete Subnet", "delete VN", "delete catalog", "delete InstanceType"],
        ),
    ],
)
def test_incomplete_rpc_create_recovers_delayed_id_before_cleanup(
    failed_create: SimpleNamespace,
    monkeypatch: pytest.MonkeyPatch,
    failure: str,
    create_method: str,
    collection: str,
    creation_name: str,
    delete_helper: str | None,
    expected_events: list[str],
) -> None:
    """A lost or invalid create response must preserve reverse dependency deletion."""
    error = (
        subprocess.TimeoutExpired("grpcurl", 300)
        if failure == "timeout"
        else json.JSONDecodeError("invalid response", "", 0)
    )
    getattr(failed_create.grpc, create_method).side_effect = error
    failed_create.grpc.list_with_filter.side_effect = [
        [],
        [],
        [{"id": "recovered-id", "metadata": {"name": creation_name}}],
    ]
    delete = Mock(side_effect=lambda *args: failed_create.events.append(expected_events[0]))
    if delete_helper:
        monkeypatch.setattr(scenario, delete_helper, delete)

    with pytest.raises(RuntimeError if failure == "timeout" else json.JSONDecodeError):
        run_scenario(failed_create)

    assert failed_create.grpc.list_with_filter.call_count == 3
    assert all(
        call.kwargs
        == {"service": f"{PUBLIC_API}.{collection}/List", "filter_expr": f'this.metadata.name == "{creation_name}"'}
        for call in failed_create.grpc.list_with_filter.call_args_list
    )
    assert getattr(failed_create.grpc, create_method).call_args.kwargs["name"] == creation_name
    assert failed_create.events == expected_events
    if delete_helper:
        assert delete.call_args.args[2:] == ("recovered-id", None)
    else:
        failed_create.grpc.delete_compute_instance_catalog_item.assert_called_once_with(catalog_item_id="recovered-id")
    failed_create.cli.create_compute_instance_with_catalog_item.assert_not_called()
    failed_create.cli.delete_compute_instance.assert_not_called()


def test_rejected_rpc_create_exhausts_lookup_without_deleting(failed_create: SimpleNamespace) -> None:
    """An absent catalog item consumes the bounded lookup budget without inventing an ID."""
    failed_create.grpc.create_compute_instance_catalog_item.side_effect = subprocess.TimeoutExpired("grpcurl", 300)
    failed_create.grpc.list_with_filter.return_value = []
    with pytest.raises(RuntimeError, match="lifecycle command failed"):
        run_scenario(failed_create)
    assert failed_create.grpc.list_with_filter.call_count == 60
    assert runner.time.sleep.call_count == 59
    failed_create.grpc.delete_compute_instance_catalog_item.assert_not_called()
    assert failed_create.events == ["delete InstanceType"]


@pytest.mark.parametrize(
    "matches",
    [
        [{"id": "other-id", "metadata": {"name": "other-item"}}],
        [
            {"id": "first-id", "metadata": {"name": "e2e-catalog-12345678"}},
            {"id": "second-id", "metadata": {"name": "e2e-catalog-12345678"}},
        ],
    ],
)
def test_rpc_recovery_refuses_ambiguous_or_wrong_name(failed_create: SimpleNamespace, matches: list[dict]) -> None:
    """Retain the primary timeout while refusing to delete a mismatched or nonunique result."""
    failed_create.grpc.create_compute_instance_catalog_item.side_effect = subprocess.TimeoutExpired("grpcurl", 300)
    failed_create.grpc.list_with_filter.return_value = matches
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], RuntimeError)
    assert "find ComputeInstanceCatalogItems after incomplete gRPC create" in str(caught.value.exceptions[1])
    failed_create.grpc.delete_compute_instance_catalog_item.assert_not_called()
    assert failed_create.events == ["delete InstanceType"]


def test_rpc_recovery_reports_safe_lookup_error_and_continues_cleanup(failed_create: SimpleNamespace) -> None:
    """Keep lookup credentials out of diagnostics while cleaning earlier known resources."""
    test_secret = "unit-test-secret"
    failed_create.grpc.create_compute_instance_catalog_item.side_effect = subprocess.TimeoutExpired("grpcurl", 300)
    failed_create.grpc.list_with_filter.side_effect = subprocess.CalledProcessError(
        7, ["grpcurl", "-H", f"Authorization: Bearer {test_secret}"], stderr=test_secret
    )
    with pytest.raises(BaseExceptionGroup) as caught:
        run_scenario(failed_create)
    assert isinstance(caught.value.exceptions[0], RuntimeError)
    assert test_secret not in str(caught.value.exceptions[1])
    failed_create.grpc.delete_compute_instance_catalog_item.assert_not_called()
    assert failed_create.events == ["delete InstanceType"]
