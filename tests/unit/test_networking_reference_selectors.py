from __future__ import annotations

from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.networking.vmaas import test_vmaas_networking as suite


@pytest.fixture
def reference_world(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """Provide distinct reference forms and mocked provider waits without claiming live allocation."""
    client = Mock()
    network = {"vnet_id": "vn-id", "vnet_name": "vn-name"}
    pool = {"id": "pool-id", "name": "pool-name", "cidr": "198.51.100.0/29"}
    vm = {"id": "vm-id", "name": "vm-name"}
    eip = {"spec": {"pool": {"id": pool["id"]}}}
    nat = {
        "spec": {"virtualNetwork": {"id": "vn-id"}, "externalIp": {"id": "eip-id"}},
        "status": {"state": "NAT_GATEWAY_STATE_READY"},
    }
    client.get_external_ip.return_value = {"object": eip}
    client.get_external_ip_attachment.return_value = {
        "object": {
            "spec": {"externalIp": {"id": "eip-id"}, "computeInstance": {"id": "vm-id"}},
            "status": {"state": "EXTERNAL_IP_ATTACHMENT_STATE_READY"},
        }
    }

    def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Return stable resource IDs and canonical NAT references for request inspection."""
        if service.endswith("/Create"):
            return {"object": {"id": "eip-id" if "ExternalIPs/" in service else "consumer-id"}}
        return {"object": nat}

    client.call.side_effect = call
    hub = Mock()
    hub.get_nat_gateway_name.return_value = "nat-cr"
    for name in (
        "wait_for_external_ip_cr",
        "wait_for_external_ip_allocated",
        "wait_for_nat_gateway_ready",
        "wait_for_external_ip_attachment_cr",
        "wait_for_external_ip_attachment_ready",
    ):
        monkeypatch.setattr(suite, name, Mock(return_value="resource-cr"))
    monkeypatch.setattr(suite.stack, "wait_for_grpc_external_ip_allocated", Mock(return_value="198.51.100.1"))
    monkeypatch.setattr(suite.stack, "external_ip_address", Mock(return_value="198.51.100.1"))
    monkeypatch.setattr(suite.stack, "wait_for_attribution", Mock())
    monkeypatch.setattr(suite, "_wait_for_compute_instance_external_ip", Mock())
    return {"client": client, "hub": hub, "network": network, "pool": pool, "vm": vm, "eip": eip, "nat": nat}


def _creates(client: Mock) -> list[dict[str, Any]]:
    return [
        entry.kwargs["data"]["object"]["spec"]
        for entry in client.call.call_args_list
        if entry.kwargs["service"].endswith("/Create")
    ]


def test_nat_requests_reference_by_id_and_wait_for_api_and_cr_allocation(reference_world: dict[str, Any]) -> None:
    world = reference_world
    suite._create_nat(
        client=world["client"],
        k8s_hub_client=world["hub"],
        network=world["network"],
        pool=world["pool"],
        run_id="run",
        key="t1",
    )
    assert _creates(world["client"]) == [
        {"pool": {"id": "pool-id"}},
        {"virtual_network": {"id": "vn-id"}, "external_ip": {"id": "eip-id"}},
    ]
    assert world["network"]["nat_id"] == "consumer-id"
    assert world["pool"]["suite_eip_ids"] == {"eip-id"}
    suite.wait_for_external_ip_allocated.assert_called_once()
    suite.stack.wait_for_grpc_external_ip_allocated.assert_called_once_with(world["client"], "eip-id")
    suite.wait_for_nat_gateway_ready.assert_called_once()


def test_attachment_requests_reference_by_id_and_wait_for_api_and_cr_allocation(
    reference_world: dict[str, Any],
) -> None:
    world = reference_world
    external = {"pool": world["pool"], "ingress": []}
    record = suite._attach_external_ip(
        client=world["client"],
        private_grpc=Mock(),
        k8s_hub_client=world["hub"],
        external=external,
        vm=world["vm"],
        label="t1-a1",
        run_id="run",
    )
    assert _creates(world["client"]) == [
        {"pool": {"id": "pool-id"}},
        {"external_ip": {"id": "eip-id"}, "compute_instance": {"id": "vm-id"}},
    ]
    assert external["ingress"] == [record]
    assert record["attachment_id"] == "consumer-id"
    suite.wait_for_external_ip_allocated.assert_called_once()
    suite.stack.wait_for_grpc_external_ip_allocated.assert_called_once_with(world["client"], "eip-id")
    suite.wait_for_external_ip_attachment_ready.assert_called_once()


def test_wrong_canonical_pool_fails_without_losing_cleanup_ids(reference_world: dict[str, Any]) -> None:
    """Reject an incorrectly resolved pool while retaining the allocation's cleanup ledger."""
    world = reference_world
    world["eip"]["spec"]["pool"]["id"] = "wrong-pool"
    with pytest.raises(AssertionError):
        suite._create_nat(
            client=world["client"],
            k8s_hub_client=world["hub"],
            network=world["network"],
            pool=world["pool"],
            run_id="run",
            key="t1",
        )
    assert world["network"]["nat_eip_id"] == "eip-id"
    assert world["pool"]["suite_eip_ids"] == {"eip-id"}


@pytest.mark.parametrize("field", ["externalIp", "computeInstance"])
def test_wrong_attachment_target_fails_with_cleanup_record(reference_world: dict[str, Any], field: str) -> None:
    """Reject canonical attachment target mismatches without orphaning either created ID."""
    world = reference_world
    world["client"].get_external_ip_attachment.return_value["object"]["spec"][field]["id"] = "wrong-id"
    external = {"pool": world["pool"], "ingress": []}
    with pytest.raises(AssertionError):
        suite._attach_external_ip(
            client=world["client"],
            private_grpc=Mock(),
            k8s_hub_client=world["hub"],
            external=external,
            vm=world["vm"],
            label="t1-a1",
            run_id="run",
        )
    assert external["ingress"][0]["attachment_id"] == "consumer-id"
    assert external["ingress"][0]["eip_id"] == "eip-id"


@pytest.mark.parametrize("field", ["virtualNetwork", "externalIp"])
def test_wrong_nat_target_fails_with_cleanup_ids(reference_world: dict[str, Any], field: str) -> None:
    """Reject wrong NAT references after recording both consumer and allocation for cleanup."""
    world = reference_world
    world["nat"]["spec"][field]["id"] = "wrong-id"
    with pytest.raises(AssertionError):
        suite._create_nat(
            client=world["client"],
            k8s_hub_client=world["hub"],
            network=world["network"],
            pool=world["pool"],
            run_id="run",
            key="t1",
        )
    assert world["network"]["nat_id"] == "consumer-id"
    assert world["network"]["nat_eip_id"] == "eip-id"


@pytest.mark.parametrize("field", ["nat_id", "nat_eip_id"])
def test_nat_creation_never_overwrites_an_uncleaned_nat(reference_world: dict[str, Any], field: str) -> None:
    """Keep earlier cleanup IDs reachable if they were never released."""
    world = reference_world
    world["network"][field] = "earlier-resource"
    with pytest.raises(AssertionError):
        suite._create_nat(
            client=world["client"],
            k8s_hub_client=world["hub"],
            network=world["network"],
            pool=world["pool"],
            run_id="run",
            key="t1",
        )
    assert world["network"][field] == "earlier-resource"
    world["client"].call.assert_not_called()


def test_nat_egress_checks_no_egress_first_and_keeps_the_nat(monkeypatch: pytest.MonkeyPatch) -> None:
    """The NAT stays for the lifecycle tests that follow; only the fixture finalizer removes it."""
    events = Mock()
    for name in ("_assert_no_egress_without_nat", "_create_nat", "_assert_nat_egress", "_drop_nat", "_drop_nat_eip"):
        monkeypatch.setattr(suite, name, getattr(events, name))
    suite.TestVmaasNetworking().test_09_nat_gateway_egress(
        grpc=Mock(),
        k8s_hub_client=Mock(),
        net_test_run_id="run",
        netris_vm_network={"vm_a1": {}},
        netris_external_path={"pool": {}, "t1": {}},
    )
    assert [entry[0] for entry in events.mock_calls] == [
        "_assert_no_egress_without_nat",
        "_create_nat",
        "_assert_nat_egress",
    ]


def test_nat_no_egress_precondition_failure_creates_nothing(monkeypatch: pytest.MonkeyPatch) -> None:
    failure = RuntimeError("unexpected existing egress")
    monkeypatch.setattr(suite, "_assert_no_egress_without_nat", Mock(side_effect=failure))
    create = Mock()
    monkeypatch.setattr(suite, "_create_nat", create)
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_09_nat_gateway_egress(
            grpc=Mock(),
            k8s_hub_client=Mock(),
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path={"pool": {}, "t1": {}},
        )
    assert caught.value is failure
    create.assert_not_called()


@pytest.mark.parametrize("stage", ["eip_cr", "eip_allocation", "nat_create", "nat_ready", "traffic"])
def test_nat_failure_keeps_every_created_id_for_the_finalizer(
    reference_world: dict[str, Any], monkeypatch: pytest.MonkeyPatch, stage: str
) -> None:
    """A failed NAT step releases nothing itself; the IDs it recorded let the fixture finalizer clean up."""
    world = reference_world
    failure = RuntimeError(f"failed at {stage}")
    monkeypatch.setattr(suite, "_assert_no_egress_without_nat", Mock())
    monkeypatch.setattr(suite, "_assert_nat_egress", Mock(side_effect=failure if stage == "traffic" else None))
    if stage == "eip_cr":
        suite.wait_for_external_ip_cr.side_effect = failure
    elif stage == "eip_allocation":
        suite.stack.wait_for_grpc_external_ip_allocated.side_effect = failure
    elif stage == "nat_ready":
        suite.wait_for_nat_gateway_ready.side_effect = failure
    elif stage == "nat_create":
        original_call = world["client"].call.side_effect

        def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
            """Fail the consumer create after the ExternalIP is already recorded."""
            if ".NATGateways/Create" in service:
                raise failure
            return original_call(service=service, data=data)

        world["client"].call.side_effect = call
    teardown = Mock()
    monkeypatch.setattr(suite, "teardown_nat", teardown)
    monkeypatch.setattr(suite, "teardown_nat_eip", teardown)
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_09_nat_gateway_egress(
            grpc=world["client"],
            k8s_hub_client=world["hub"],
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path={"pool": world["pool"], "t1": world["network"]},
        )
    assert caught.value is failure
    teardown.assert_not_called()
    assert world["network"]["nat_eip_id"] == "eip-id"
    if stage in {"nat_ready", "traffic"}:
        assert world["network"]["nat_id"] == "consumer-id"


def test_ingress_proves_the_path_and_keeps_the_attachment(monkeypatch: pytest.MonkeyPatch) -> None:
    """The attachment stays in the ledger for test_11, marked as proven."""
    record = {"address": "198.51.100.1"}
    external = {"ingress": []}
    events = Mock()

    def attach(**kwargs: object) -> dict[str, Any]:
        """Model the real helper's early ledger insertion before returning."""
        external["ingress"].append(record)
        return record

    events.attach.side_effect = attach
    monkeypatch.setattr(suite, "_attach_external_ip", events.attach)
    monkeypatch.setattr(suite, "_assert_external_ip_ssh", events.traffic)
    monkeypatch.setattr(suite, "_teardown_ingress_record", events.cleanup)
    suite.TestVmaasNetworking().test_10_manual_external_ip_ingress(
        grpc=Mock(),
        private_grpc=Mock(),
        k8s_hub_client=Mock(),
        net_test_run_id="run",
        netris_vm_network={"vm_a1": {"id": "ci-a1"}},
        netris_external_path=external,
    )
    assert record["ingress_proven"] is True
    assert [entry[0] for entry in events.mock_calls] == ["attach", "traffic"]
    assert external["ingress"] == [record]


@pytest.mark.parametrize(
    "stage", ["eip_create", "eip_cr", "eip_allocation", "attachment_create", "attachment_cr", "attribution", "ssh"]
)
def test_ingress_failure_keeps_early_ledger_records_for_the_finalizer(
    reference_world: dict[str, Any], monkeypatch: pytest.MonkeyPatch, stage: str
) -> None:
    """A failed ingress step releases nothing itself; its ledger record lets the fixture finalizer clean up."""
    world = reference_world
    failure = RuntimeError(f"failed at {stage}")
    if stage in {"eip_create", "attachment_create"}:
        original_call = world["client"].call.side_effect

        def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
            """Fail one API create while retaining every earlier successful creation."""
            if (stage == "eip_create" and ".ExternalIPs/Create" in service) or (
                stage == "attachment_create" and ".ExternalIPAttachments/Create" in service
            ):
                raise failure
            return original_call(service=service, data=data)

        world["client"].call.side_effect = call
    elif stage == "eip_cr":
        suite.wait_for_external_ip_cr.side_effect = failure
    elif stage == "eip_allocation":
        suite.stack.wait_for_grpc_external_ip_allocated.side_effect = failure
    elif stage == "attachment_cr":
        suite.wait_for_external_ip_attachment_cr.side_effect = failure
    elif stage == "attribution":
        suite.stack.wait_for_attribution.side_effect = failure
    monkeypatch.setattr(suite, "_assert_external_ip_ssh", Mock(side_effect=failure if stage == "ssh" else None))
    cleanup = Mock()
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)
    earlier = {"label": "other", "eip_id": "earlier-id"}
    external = {"pool": world["pool"], "ingress": [earlier]}
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_10_manual_external_ip_ingress(
            grpc=world["client"],
            private_grpc=Mock(),
            k8s_hub_client=world["hub"],
            net_test_run_id="run",
            netris_vm_network={"vm_a1": world["vm"]},
            netris_external_path=external,
        )
    assert caught.value is failure
    cleanup.assert_not_called()
    assert len(external["ingress"]) == (1 if stage == "eip_create" else 2)
    assert earlier == {"label": "other", "eip_id": "earlier-id"}


def test_ingress_teardown_attempts_each_resource_and_retains_failed_ids(monkeypatch: pytest.MonkeyPatch) -> None:
    """Keep both failed resource IDs for fixture retry and expose both cleanup errors."""
    errors = [RuntimeError("attachment cleanup"), RuntimeError("allocation cleanup")]
    cleanup = Mock(side_effect=errors)
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)
    record = {"label": "row", "client": Mock(), "attachment_id": "attachment-id", "eip_id": "eip-id"}
    with pytest.raises(ExceptionGroup) as caught:
        suite._teardown_ingress_record(record, Mock())
    assert list(caught.value.exceptions) == errors
    assert cleanup.call_count == 2
    assert record["attachment_id"] == "attachment-id" and record["eip_id"] == "eip-id"


@pytest.mark.parametrize("failed_field", ["attachment_id", "eip_id"])
def test_ingress_teardown_retries_only_ids_not_successfully_removed(
    monkeypatch: pytest.MonkeyPatch, failed_field: str
) -> None:
    """Forget successful deletions while keeping failed IDs available for a later fixture retry."""
    failure = RuntimeError("resource still exists")
    fields = ["attachment_id", "eip_id"]
    cleanup = Mock(side_effect=[failure if field == failed_field else None for field in fields])
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)
    record = {"label": "row", "client": Mock(), "attachment_id": "attachment-id", "eip_id": "eip-id"}
    with pytest.raises(RuntimeError) as caught:
        suite._teardown_ingress_record(record, Mock())
    assert caught.value is failure
    assert record[failed_field]
    assert record[next(field for field in fields if field != failed_field)] == ""
    cleanup.side_effect = None
    suite._teardown_ingress_record(record, Mock())
    assert cleanup.call_count == 3
    assert record["attachment_id"] == record["eip_id"] == ""
