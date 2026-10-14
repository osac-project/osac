from __future__ import annotations

import ipaddress
import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core import helpers
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.references import conftest as references_conftest
from tests.e2e.references import test_ip_management_references as ip_management_references


def test_allocate_worker_subnet_keeps_prefix_ranges_disjoint(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(helpers.allocate_worker_subnet, "_counters", {}, raising=False)

    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw3")
    pool_subnet = helpers.allocate_worker_subnet(prefix=24)

    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw0")
    external_ip_subnet = helpers.allocate_worker_subnet(prefix=30)

    assert pool_subnet == ipaddress.IPv4Network("172.27.96.0/24")
    assert external_ip_subnet == ipaddress.IPv4Network("172.27.128.0/30")
    assert not pool_subnet.overlaps(external_ip_subnet)


def test_allocate_worker_subnet_rejects_worker_outside_reserved_24_range(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(helpers.allocate_worker_subnet, "_counters", {}, raising=False)
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw4")

    with pytest.raises(RuntimeError, match="outside the reserved /24 address space"):
        helpers.allocate_worker_subnet(prefix=24)


def _patch_ref_eip_pool_setup(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(references_conftest, "allocate_worker_subnet", lambda: ipaddress.IPv4Network("172.27.0.0/24"))
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_cr", lambda **_: "pool-cr")
    for helper_name in ("wait_for_external_ip_pool_ready", "wait_for_external_ip_pool_grpc_ready"):
        monkeypatch.setattr(references_conftest, helper_name, lambda **_: None)


def test_ref_eip_pool_teardown_does_not_sweep_allocations_when_pool_delete_fails(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    pool_error = subprocess.CalledProcessError(1, ["grpcurl"], stderr="Code: FailedPrecondition: pool is in use")
    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.delete_external_ip_pool.side_effect = pool_error
    k8s = Mock()

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, k8s, "test-run")
    next(generator)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        next(generator)

    assert exc_info.value is pool_error
    private_grpc.create_external_ip_pool.assert_called_once_with(name="ref-eip-pool-test-run", cidrs=["172.27.0.0/24"])
    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")
    private_grpc.call.assert_not_called()
    private_grpc.delete_external_ip.assert_not_called()
    k8s.get_external_ip_name.assert_not_called()
    k8s.get_external_ip_pool_name.assert_not_called()


def test_ref_eip_pool_teardown_resolves_cr_name_after_setup_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(references_conftest, "allocate_worker_subnet", lambda: ipaddress.IPv4Network("172.27.0.0/24"))
    monkeypatch.setattr(
        references_conftest, "wait_for_external_ip_pool_cr", Mock(side_effect=TimeoutError("CR not found"))
    )
    wait_for_pool_deletion = Mock()
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_deletion", wait_for_pool_deletion)

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    k8s = Mock()
    k8s.get_external_ip_pool_name.return_value = "pool-cr"
    k8s.is_present.return_value = True

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, k8s, "test-run")

    with pytest.raises(TimeoutError, match="CR not found"):
        next(generator)

    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")
    k8s.get_external_ip_pool_name.assert_called_once_with(uuid="pool-id", checked=False)
    wait_for_pool_deletion.assert_called_once_with(k8s=k8s, name="pool-cr")


def test_nat_gateway_reference_waits_for_external_ip_allocation_and_deletion(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[str] = []
    k8s = Mock()
    grpc = Mock(spec=GRPCClient)
    grpc.call.side_effect = [
        {"object": {"id": "external-ip-id"}},
        {
            "object": {
                "spec": {
                    "virtualNetwork": {"id": "virtual-network-id", "name": "virtual-network"},
                    "externalIp": {"id": "external-ip-id", "name": "ref-nat-eip-test-tag"},
                }
            }
        },
        {"items": []},
    ]
    grpc.create_nat_gateway.side_effect = lambda **_: events.append("nat-created") or "nat-id"
    grpc.delete_external_ip.side_effect = lambda **_: events.append("external-ip-delete-requested")

    def wait_for_cr(**_: object) -> str:
        events.append("external-ip-cr-created")
        return "external-ip-cr"

    def wait_for_allocation(**_: object) -> None:
        events.append("external-ip-allocated")

    def wait_for_deletion(**_: object) -> None:
        events.append("external-ip-deleted")

    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_cr", wait_for_cr)
    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_allocated", wait_for_allocation)
    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_deletion", wait_for_deletion)
    monkeypatch.setattr(ip_management_references, "uuid4", lambda: Mock(hex="test-tag"))

    ip_management_references.TestIPManagementReferences().test_nat_gateway_by_name(
        grpc=grpc,
        k8s_hub_client=k8s,
        ref_virtual_network={"id": "virtual-network-id", "name": "virtual-network"},
        ref_eip_pool={"id": "pool-id", "name": "pool"},
        ref_test_run_id="test-run",
    )

    assert events == [
        "external-ip-cr-created",
        "external-ip-allocated",
        "nat-created",
        "external-ip-delete-requested",
        "external-ip-deleted",
    ]
