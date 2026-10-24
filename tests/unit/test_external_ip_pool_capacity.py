from __future__ import annotations

from collections.abc import Callable
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.regression.external_ip import test_external_ip_pool_capacity as capacity


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    elapsed = [0.0]

    def sleep(seconds: float) -> None:
        elapsed[0] += seconds

    monkeypatch.setattr(runner.time, "sleep", sleep)
    monkeypatch.setattr(runner.time, "monotonic", lambda: elapsed[0])
    return elapsed


@pytest.fixture(params=["test_release_restores_capacity", "test_pool_deletion_succeeds_after_all_ips_released"])
def consumer(request: pytest.FixtureRequest) -> Callable[..., None]:
    return getattr(capacity.TestPoolCapacity(), request.param)


def test_capacity_consumers_wait_for_natural_deletion_before_reading_capacity(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], consumer: Callable[..., None]
) -> None:
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    releasing_one = consumer.__name__ == "test_release_restores_capacity"
    completed: set[str] = set()
    capacity_reads: list[float] = []
    allocation_sequence = [2, 1, 2] if releasing_one else [1, 0]

    def is_present(*, resource: str, name: str) -> bool:
        assert resource == "externalip"
        expected_ip = ("ip-first", "cr-first") if name == "cr-first" else ("ip-last", "cr-last")
        assert expected_ip in tracked
        assert grpc.delete_external_ip.call_args_list == (
            [call(external_ip_id="ip-first")]
            if releasing_one
            else [call(external_ip_id="ip-first"), call(external_ip_id="ip-last")]
        )
        present = clock[0] < (62 if releasing_one or name == "cr-last" else 32)
        if not present:
            completed.add(name)
        return present

    def read_capacity(*, pool_id: str) -> dict[str, object]:
        assert pool_id == "pool-id"
        assert completed == ({"cr-first"} if releasing_one else {"cr-first", "cr-last"})
        assert ("ip-first", "cr-first") not in tracked
        if not releasing_one:
            assert not tracked
        capacity_reads.append(clock[0])
        allocated = allocation_sequence[len(capacity_reads) - 1]
        return {"object": {"status": {"total": "2", "allocated": str(allocated), "available": str(2 - allocated)}}}

    k8s.is_present.side_effect = is_present
    private_grpc.get_external_ip_pool.side_effect = read_capacity
    monkeypatch.setattr(capacity, "create_ip", Mock(return_value=("ip-refill", "cr-refill")))
    pool_wait = Mock()
    monkeypatch.setattr(capacity, "wait_for_external_ip_pool_deletion", pool_wait)

    consumer(
        small_pool=("pool-id", "pool-cr"), grpc=grpc, private_grpc=private_grpc, k8s_hub_client=k8s, created_ips=tracked
    )

    assert capacity_reads[:2] == [65, 67]
    if releasing_one:
        assert tracked == [("ip-last", "cr-last"), ("ip-refill", "cr-refill")]
        private_grpc.delete_external_ip_pool.assert_not_called()
        pool_wait.assert_not_called()
    else:
        assert tracked == []
        private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")
        pool_wait.assert_called_once_with(k8s=k8s, name="pool-cr")


def test_capacity_consumers_retain_pending_ips_on_natural_deletion_timeout(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], consumer: Callable[..., None]
) -> None:
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    releasing_one = consumer.__name__ == "test_release_restores_capacity"

    def is_present(*, resource: str, name: str) -> bool:
        assert resource == "externalip"
        return releasing_one or name == "cr-last"

    k8s.is_present.side_effect = is_present
    refill = Mock()
    monkeypatch.setattr(capacity, "create_ip", refill)
    pool_wait = Mock()
    monkeypatch.setattr(capacity, "wait_for_external_ip_pool_deletion", pool_wait)

    with pytest.raises(TimeoutError, match="timed out after 595s"):
        consumer(
            small_pool=("pool-id", "pool-cr"),
            grpc=grpc,
            private_grpc=private_grpc,
            k8s_hub_client=k8s,
            created_ips=tracked,
        )

    assert clock[0] == 595
    assert tracked == (
        [("ip-first", "cr-first"), ("ip-last", "cr-last")] if releasing_one else [("ip-last", "cr-last")]
    )
    private_grpc.get_external_ip_pool.assert_not_called()
    private_grpc.delete_external_ip_pool.assert_not_called()
    refill.assert_not_called()
    pool_wait.assert_not_called()


def test_capacity_consumers_retain_tracking_when_delete_request_fails(consumer: Callable[..., None]) -> None:
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    grpc.delete_external_ip.side_effect = TimeoutError("delete request timed out")

    with pytest.raises(TimeoutError, match="delete request timed out"):
        consumer(
            small_pool=("pool-id", "pool-cr"),
            grpc=grpc,
            private_grpc=private_grpc,
            k8s_hub_client=k8s,
            created_ips=tracked,
        )

    assert tracked == [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    k8s.is_present.assert_not_called()
    private_grpc.get_external_ip_pool.assert_not_called()
    private_grpc.delete_external_ip_pool.assert_not_called()
