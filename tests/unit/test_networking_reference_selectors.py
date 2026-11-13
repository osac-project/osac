from __future__ import annotations

from itertools import product
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.networking.vmaas import test_vmaas_networking as suite

SELECTORS = list(product(("id", "name"), repeat=3))


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
    return {"client": client, "hub": hub, "network": network, "pool": pool, "vm": vm, "eip": eip, "nat": nat}


@pytest.mark.parametrize(("pool_selector", "network_selector", "eip_selector"), SELECTORS)
def test_nat_reference_requests_and_canonical_ids(
    reference_world: dict[str, Any], pool_selector: str, network_selector: str, eip_selector: str
) -> None:
    """Require the selected request forms and both API/CR allocation before NAT readiness."""
    world = reference_world
    suite._create_nat(
        client=world["client"],
        k8s_hub_client=world["hub"],
        network=world["network"],
        pool=world["pool"],
        run_id="run",
        key="t1",
        pool_selector=pool_selector,
        network_selector=network_selector,
        eip_selector=eip_selector,
    )
    creates = [
        entry.kwargs["data"]["object"]["spec"]
        for entry in world["client"].call.call_args_list
        if entry.kwargs["service"].endswith("/Create")
    ]
    assert creates == [
        {"pool": {pool_selector: world["pool"][pool_selector]}},
        {
            "virtual_network": {network_selector: world["network"][f"vnet_{network_selector}"]},
            "external_ip": {eip_selector: "eip-id" if eip_selector == "id" else "netris-vm-nat-eip-t1-run"},
        },
    ]
    assert world["network"]["nat_id"] == "consumer-id"
    assert world["pool"]["suite_eip_ids"] == {"eip-id"}
    suite.wait_for_external_ip_allocated.assert_called_once()
    suite.stack.wait_for_grpc_external_ip_allocated.assert_called_once_with(world["client"], "eip-id")
    suite.wait_for_nat_gateway_ready.assert_called_once()


@pytest.mark.parametrize(("pool_selector", "eip_selector", "vm_selector"), SELECTORS)
def test_attachment_reference_requests_and_canonical_ids(
    reference_world: dict[str, Any], pool_selector: str, eip_selector: str, vm_selector: str
) -> None:
    """Require each pool, ExternalIP and VM selector to reach the real helper unchanged."""
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
        pool_selector=pool_selector,
        eip_selector=eip_selector,
        vm_selector=vm_selector,
    )
    creates = [
        entry.kwargs["data"]["object"]["spec"]
        for entry in world["client"].call.call_args_list
        if entry.kwargs["service"].endswith("/Create")
    ]
    assert creates == [
        {"pool": {pool_selector: world["pool"][pool_selector]}},
        {
            "external_ip": {eip_selector: "eip-id" if eip_selector == "id" else "netris-vm-t1-a1-eip-run"},
            "compute_instance": {vm_selector: world["vm"][vm_selector]},
        },
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


@pytest.mark.parametrize("selectors", SELECTORS)
def test_nat_rows_prove_traffic_before_cleanup_and_keep_last_row(
    monkeypatch: pytest.MonkeyPatch, selectors: tuple[str, str, str]
) -> None:
    """Preserve the traffic oracle and final-row dependency for all eight NAT combinations."""
    events = Mock()
    for name in ("_assert_no_egress_without_nat", "_create_nat", "_assert_nat_egress", "_drop_nat", "_drop_nat_eip"):
        monkeypatch.setattr(suite, name, getattr(events, name))
    suite.TestVmaasNetworking().test_09_nat_gateway_egress(
        grpc=Mock(),
        k8s_hub_client=Mock(),
        net_test_run_id="run",
        netris_vm_network={"vm_a1": {}},
        netris_external_path={"pool": {}, "t1": {}},
        pool_selector=selectors[0],
        network_selector=selectors[1],
        eip_selector=selectors[2],
    )
    expected = ["_assert_no_egress_without_nat", "_create_nat", "_assert_nat_egress"]
    if selectors != SELECTORS[-1]:
        expected += ["_drop_nat", "_drop_nat_eip"]
    assert [entry[0] for entry in events.mock_calls] == expected


@pytest.mark.parametrize("selectors", SELECTORS)
def test_attachment_rows_prove_traffic_before_cleanup_and_keep_last_row(
    monkeypatch: pytest.MonkeyPatch, selectors: tuple[str, str, str]
) -> None:
    """Release seven proven ingress rows but keep the final attachment for detach checks."""
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
        netris_vm_network={"vm_a1": {}},
        netris_external_path=external,
        pool_selector=selectors[0],
        eip_selector=selectors[1],
        vm_selector=selectors[2],
    )
    assert record["ingress_proven"] is True
    assert [entry[0] for entry in events.mock_calls] == (
        ["attach", "traffic"] if selectors == SELECTORS[-1] else ["attach", "traffic", "cleanup"]
    )
    assert external["ingress"] == ([record] if selectors == SELECTORS[-1] else [])


@pytest.mark.parametrize("final_row", [False, True])
@pytest.mark.parametrize("stage", ["eip_cr", "eip_allocation", "nat_create", "nat_ready", "traffic"])
def test_nat_row_failure_releases_partial_resources_except_final(
    reference_world: dict[str, Any], monkeypatch: pytest.MonkeyPatch, stage: str, final_row: bool
) -> None:
    """Clean every known non-final NAT resource even when creation never returns."""
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
    released: list[str] = []

    def drop_nat(client: Mock, hub: Mock, network: dict[str, Any]) -> None:
        """Observe the gateway ID before the real wrapper forgets successful cleanup."""
        if network.get("nat_id"):
            released.append(network["nat_id"])

    def drop_eip(client: Mock, hub: Mock, network: dict[str, Any]) -> None:
        """Observe the allocation ID before the real wrapper forgets it."""
        if network.get("nat_eip_id"):
            released.append(network["nat_eip_id"])

    monkeypatch.setattr(suite, "teardown_nat", drop_nat)
    monkeypatch.setattr(suite, "teardown_nat_eip", drop_eip)
    selector = "name" if final_row else "id"
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_09_nat_gateway_egress(
            grpc=world["client"],
            k8s_hub_client=world["hub"],
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path={"pool": world["pool"], "t1": world["network"]},
            pool_selector=selector,
            network_selector=selector,
            eip_selector=selector,
        )
    assert caught.value is failure
    if final_row:
        assert released == []
        assert world["network"]["nat_eip_id"] == "eip-id"
    else:
        assert released == (["consumer-id", "eip-id"] if stage in {"nat_ready", "traffic"} else ["eip-id"])
        assert not world["network"].get("nat_id") and not world["network"].get("nat_eip_id")


@pytest.mark.parametrize("field", ["nat_id", "nat_eip_id"])
def test_nat_creation_never_overwrites_an_uncleaned_row(reference_world: dict[str, Any], field: str) -> None:
    """Keep earlier cleanup IDs reachable if a previous row could not release them."""
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


@pytest.mark.parametrize("traffic_fails", [False, True])
def test_nat_cleanup_attempts_both_resources_and_preserves_all_errors(
    monkeypatch: pytest.MonkeyPatch, traffic_fails: bool
) -> None:
    """Report cleanup failures together, retaining the traffic failure when present."""
    errors = [RuntimeError("traffic"), RuntimeError("gateway cleanup"), RuntimeError("allocation cleanup")]
    monkeypatch.setattr(suite, "_assert_no_egress_without_nat", Mock())
    monkeypatch.setattr(suite, "_create_nat", Mock())
    monkeypatch.setattr(suite, "_assert_nat_egress", Mock(side_effect=errors[0] if traffic_fails else None))
    gateway = Mock(side_effect=errors[1])
    allocation = Mock(side_effect=errors[2])
    monkeypatch.setattr(suite, "_drop_nat", gateway)
    monkeypatch.setattr(suite, "_drop_nat_eip", allocation)
    with pytest.raises(ExceptionGroup) as caught:
        suite.TestVmaasNetworking().test_09_nat_gateway_egress(
            grpc=Mock(),
            k8s_hub_client=Mock(),
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path={"pool": {}, "t1": {}},
            pool_selector="id",
            network_selector="id",
            eip_selector="id",
        )
    assert list(caught.value.exceptions) == (errors if traffic_fails else errors[1:])
    gateway.assert_called_once()
    allocation.assert_called_once()


@pytest.mark.parametrize("final_row", [False, True])
@pytest.mark.parametrize(
    "stage", ["eip_create", "eip_cr", "eip_allocation", "attachment_create", "attachment_cr", "attribution", "ssh"]
)
def test_ingress_failure_releases_early_ledger_records_except_final(
    reference_world: dict[str, Any], monkeypatch: pytest.MonkeyPatch, stage: str, final_row: bool
) -> None:
    """Discover partial attachment records even when the create helper never returns."""
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
    earlier = {"label": "other-row", "eip_id": "earlier-id"}
    external = {"pool": world["pool"], "ingress": [earlier]}
    selector = "name" if final_row else "id"
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_10_manual_external_ip_ingress(
            grpc=world["client"],
            private_grpc=Mock(),
            k8s_hub_client=world["hub"],
            net_test_run_id="run",
            netris_vm_network={"vm_a1": world["vm"]},
            netris_external_path=external,
            pool_selector=selector,
            eip_selector=selector,
            vm_selector=selector,
        )
    assert caught.value is failure
    if final_row:
        cleanup.assert_not_called()
        assert len(external["ingress"]) == (1 if stage == "eip_create" else 2)
    else:
        expected = [] if stage == "eip_create" else ["eip-id"]
        if stage in {"attachment_cr", "attribution", "ssh"}:
            expected.insert(0, "consumer-id")
        assert [entry.kwargs["resource_id"] for entry in cleanup.call_args_list] == expected
        assert external["ingress"] == [earlier]
    assert earlier == {"label": "other-row", "eip_id": "earlier-id"}


@pytest.mark.parametrize("ssh_fails", [False, True])
def test_ingress_cleanup_is_safe_when_record_already_removed(
    reference_world: dict[str, Any], monkeypatch: pytest.MonkeyPatch, ssh_fails: bool
) -> None:
    """Clean a returned record without requiring that it still be in the shared ledger."""
    world = reference_world
    external = {"pool": world["pool"], "ingress": []}
    failure = RuntimeError("ssh failed")

    def ssh(**kwargs: object) -> None:
        """Model another cleanup path already removing the returned ledger entry."""
        external["ingress"].clear()
        if ssh_fails:
            raise failure

    monkeypatch.setattr(suite, "_assert_external_ip_ssh", ssh)
    cleanup = Mock()
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)

    def run() -> None:
        """Run one non-final real helper path with mocked provider boundaries."""
        suite.TestVmaasNetworking().test_10_manual_external_ip_ingress(
            grpc=world["client"],
            private_grpc=Mock(),
            k8s_hub_client=world["hub"],
            net_test_run_id="run",
            netris_vm_network={"vm_a1": world["vm"]},
            netris_external_path=external,
            pool_selector="id",
            eip_selector="id",
            vm_selector="id",
        )

    if ssh_fails:
        with pytest.raises(RuntimeError) as caught:
            run()
        assert caught.value is failure
    else:
        run()
    assert [entry.kwargs["resource_id"] for entry in cleanup.call_args_list] == ["consumer-id", "eip-id"]
    assert external["ingress"] == []


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


def test_ingress_row_preserves_original_failure_and_failed_cleanup_ledger(monkeypatch: pytest.MonkeyPatch) -> None:
    """Do not mask a partial-create error or drop the record when its cleanup also fails."""
    record = {"label": "row", "eip_id": "eip-id"}
    external = {"ingress": []}
    failure = RuntimeError("partial create")
    cleanup_failure = RuntimeError("cleanup")

    def attach(**kwargs: object) -> dict[str, Any]:
        """Publish a partial record before failing, like the real allocation helper."""
        external["ingress"].append(record)
        raise failure

    monkeypatch.setattr(suite, "_attach_external_ip", attach)
    monkeypatch.setattr(suite, "_teardown_ingress_record", Mock(side_effect=cleanup_failure))
    with pytest.raises(ExceptionGroup) as caught:
        suite.TestVmaasNetworking().test_10_manual_external_ip_ingress(
            grpc=Mock(),
            private_grpc=Mock(),
            k8s_hub_client=Mock(),
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path=external,
            pool_selector="id",
            eip_selector="id",
            vm_selector="id",
        )
    assert list(caught.value.exceptions) == [failure, cleanup_failure]
    assert external["ingress"] == [record]


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


def test_nat_no_egress_precondition_does_not_enter_row_cleanup(monkeypatch: pytest.MonkeyPatch) -> None:
    """Leave prior state intact when the no-NAT precondition fails before any row creation."""
    failure = RuntimeError("unexpected existing egress")
    monkeypatch.setattr(suite, "_assert_no_egress_without_nat", Mock(side_effect=failure))
    create = Mock()
    gateway = Mock()
    allocation = Mock()
    monkeypatch.setattr(suite, "_create_nat", create)
    monkeypatch.setattr(suite, "_drop_nat", gateway)
    monkeypatch.setattr(suite, "_drop_nat_eip", allocation)
    with pytest.raises(RuntimeError) as caught:
        suite.TestVmaasNetworking().test_09_nat_gateway_egress(
            grpc=Mock(),
            k8s_hub_client=Mock(),
            net_test_run_id="run",
            netris_vm_network={"vm_a1": {}},
            netris_external_path={"pool": {}, "t1": {}},
            pool_selector="id",
            network_selector="id",
            eip_selector="id",
        )
    assert caught.value is failure
    create.assert_not_called()
    gateway.assert_not_called()
    allocation.assert_not_called()
