from __future__ import annotations

from itertools import product
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.networking.vmaas import test_vmaas_networking as suite

SELECTORS = list(product(("id", "name"), repeat=3))


@pytest.fixture
def reference_world(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
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
    record = {"address": "198.51.100.1"}
    external = {"ingress": [record]}
    events = Mock()
    events.attach.return_value = record
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
