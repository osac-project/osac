from __future__ import annotations

import ipaddress
import subprocess
from unittest.mock import Mock, call

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


def test_ref_eip_pool_teardown_removes_dependent_ips_before_pool(monkeypatch: pytest.MonkeyPatch) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    wait_for_ip_deletion = Mock()
    wait_for_pool_deletion = Mock()
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_deletion", wait_for_ip_deletion)
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_deletion", wait_for_pool_deletion)

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.call.side_effect = [
        {
            "items": [
                {"id": "unrelated-ip", "spec": {"pool": {"id": "another-pool"}}},
                {"id": "ip-id-one", "spec": {"pool": {"id": "pool-id"}}},
            ],
            "nextPageToken": "next-page",
        },
        {"items": [{"id": "ip-id-two", "spec": {"pool": {"id": "pool-id"}}}]},
    ]
    k8s = Mock()
    k8s.get_external_ip_name.side_effect = ["ip-one-cr", "ip-two-cr"]
    k8s.is_present.return_value = True

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, k8s, "test-run")
    next(generator)

    with pytest.raises(StopIteration):
        next(generator)

    assert private_grpc.method_calls == [
        call.create_external_ip_pool(name="ref-eip-pool-test-run", cidrs=["172.27.0.0/24"]),
        call.call(service="osac.private.v1.ExternalIPs/List", data=None),
        call.call(service="osac.private.v1.ExternalIPs/List", data={"pageToken": "next-page"}),
        call.delete_external_ip(external_ip_id="ip-id-one"),
        call.delete_external_ip(external_ip_id="ip-id-two"),
        call.delete_external_ip_pool(pool_id="pool-id"),
    ]
    assert wait_for_ip_deletion.call_args_list == [call(k8s=k8s, name="ip-one-cr"), call(k8s=k8s, name="ip-two-cr")]
    wait_for_pool_deletion.assert_called_once_with(k8s=k8s, name="pool-cr")


def test_ref_eip_pool_teardown_propagates_pool_deletion_error(monkeypatch: pytest.MonkeyPatch) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.call.return_value = {"items": []}
    private_grpc.delete_external_ip_pool.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="Authorization: Bearer secret-token"
    )

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, Mock(), "test-run")
    next(generator)

    with pytest.raises(subprocess.CalledProcessError, match="grpcurl"):
        next(generator)


def test_ref_eip_pool_teardown_attempts_pool_deletion_after_child_cleanup_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    cleanup_error = RuntimeError("child cleanup failed")
    pool_error = subprocess.CalledProcessError(1, ["grpcurl"], stderr="Code: Internal")
    monkeypatch.setattr(references_conftest, "_cleanup_ref_pool_external_ips", Mock(side_effect=cleanup_error))

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.delete_external_ip_pool.side_effect = pool_error

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, Mock(), "test-run")
    next(generator)

    with pytest.raises(ExceptionGroup) as exc_info:
        next(generator)

    assert exc_info.value.exceptions == (cleanup_error, pool_error)
    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")


def test_ref_eip_pool_teardown_resolves_cr_name_after_setup_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(references_conftest, "allocate_worker_subnet", lambda: ipaddress.IPv4Network("172.27.0.0/24"))
    monkeypatch.setattr(
        references_conftest, "wait_for_external_ip_pool_cr", Mock(side_effect=TimeoutError("CR not found"))
    )
    monkeypatch.setattr(references_conftest, "_cleanup_ref_pool_external_ips", lambda *_: None)
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
