from __future__ import annotations

import subprocess
from collections.abc import Callable
from types import SimpleNamespace
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.vmaas.regression.external_ip import test_external_ip_pool_capacity as capacity


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    """Advance polling time without sleeping or changing Python's shared time module."""
    elapsed = [0.0]

    def sleep(seconds: float) -> None:
        """Charge each polling delay to the runner-local fake clock."""
        elapsed[0] += seconds

    monkeypatch.setattr(runner, "time", SimpleNamespace(sleep=sleep, monotonic=lambda: elapsed[0]))
    return elapsed


@pytest.fixture(params=["test_release_restores_capacity", "test_pool_deletion_succeeds_after_all_ips_released"])
def consumer(request: pytest.FixtureRequest) -> Callable[..., None]:
    """Exercise each real capacity consumer with the same lifecycle scenarios."""
    return getattr(capacity.TestPoolCapacity(), request.param)


def test_capacity_consumers_wait_for_natural_deletion_before_reading_capacity(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], consumer: Callable[..., None]
) -> None:
    """Allow cleanup beyond 58 seconds while keeping capacity checks after confirmed deletion."""
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = K8sClient(namespace="test", as_system_admin=False)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    releasing_one = consumer.__name__ == "test_release_restores_capacity"
    completed: set[str] = set()
    capacity_reads: list[float] = []
    allocation_sequence = [2, 1, 2] if releasing_one else [1, 0]

    def read_resource(*args: str) -> tuple[str, int]:
        """Model canonical kubectl output until each tracked IP naturally disappears."""
        name = args[3]
        assert args == ("kubectl", "get", "externalips.osac.openshift.io", name, "-n", "test")
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
        return (
            ("resource still present", 0)
            if present
            else (f'Error from server (NotFound): externalips.osac.openshift.io "{name}" not found', 1)
        )

    def read_capacity(*, pool_id: str) -> dict[str, object]:
        """Keep archive propagation separate from confirmed Kubernetes resource deletion."""
        assert pool_id == "pool-id"
        assert completed == ({"cr-first"} if releasing_one else {"cr-first", "cr-last"})
        assert ("ip-first", "cr-first") not in tracked
        if not releasing_one:
            assert not tracked
        capacity_reads.append(clock[0])
        allocated = allocation_sequence[len(capacity_reads) - 1]
        return {"object": {"status": {"total": "2", "allocated": str(allocated), "available": str(2 - allocated)}}}

    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read_resource)
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
    """Preserve every still-present IP when the natural-deletion polling budget expires."""
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = K8sClient(namespace="test", as_system_admin=False)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    releasing_one = consumer.__name__ == "test_release_restores_capacity"

    def read_resource(*args: str) -> tuple[str, int]:
        """Leave the pending IP present after any earlier sibling's confirmed removal."""
        name = args[3]
        assert args == ("kubectl", "get", "externalips.osac.openshift.io", name, "-n", "test")
        if releasing_one or name == "cr-last":
            return "resource still present", 0
        return f'Error from server (NotFound): externalips.osac.openshift.io "{name}" not found', 1

    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read_resource)
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
    """Do not lose tracked resources when the initial API delete request fails."""
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
    k8s.is_absent.assert_not_called()
    private_grpc.get_external_ip_pool.assert_not_called()
    private_grpc.delete_external_ip_pool.assert_not_called()


@pytest.mark.parametrize("absent", [False, True], ids=["present", "canonical-not-found"])
def test_external_ip_deletion_requires_confirmed_absence(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], absent: bool
) -> None:
    """Accept only exact canonical NotFound; a successful read must keep waiting."""
    output = (
        'Error from server (NotFound): externalips.osac.openshift.io "cr-first" not found'
        if absent
        else "resource still present"
    )
    read = Mock(return_value=(output, 1 if absent else 0))
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)
    k8s = K8sClient(namespace="test", as_system_admin=False)

    if absent:
        helpers.wait_for_external_ip_deletion(k8s=k8s, name="cr-first")
    else:
        with pytest.raises(TimeoutError, match="timed out after 595s"):
            helpers.wait_for_external_ip_deletion(k8s=k8s, name="cr-first")

    assert clock[0] == (0 if absent else 595)
    assert read.call_args_list == [
        call("kubectl", "get", "externalips.osac.openshift.io", "cr-first", "-n", "test")
    ] * (1 if absent else 120)


def test_external_ip_deletion_accepts_present_then_qualified_not_found(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """Recognize real kubectl's qualified NotFound only after the present IP disappears."""
    read = Mock(
        side_effect=[
            ("resource still present", 0),
            ('Error from server (NotFound): externalips.osac.openshift.io "cr-first" not found', 1),
        ]
    )
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)

    helpers.wait_for_external_ip_deletion(k8s=K8sClient(namespace="test", as_system_admin=False), name="cr-first")

    assert clock[0] == 5
    assert (
        read.call_args_list == [call("kubectl", "get", "externalips.osac.openshift.io", "cr-first", "-n", "test")] * 2
    )


def test_successive_capacity_consumers_do_not_retry_confirmed_deletion(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """Release tracking after qualified absence so the later consumer deletes only remaining IPs."""
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = K8sClient(namespace="test", as_system_admin=False)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    read = Mock(
        side_effect=[
            response
            for name in ["cr-first", "cr-last", "cr-refill"]
            for response in [
                ("resource still present", 0),
                (f'Error from server (NotFound): externalips.osac.openshift.io "{name}" not found', 1),
            ]
        ]
    )
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)
    private_grpc.get_external_ip_pool.side_effect = [
        {"object": {"status": {"total": "2", "allocated": str(allocated), "available": str(2 - allocated)}}}
        for allocated in [1, 2, 0]
    ]
    refill = Mock(return_value=("ip-refill", "cr-refill"))
    monkeypatch.setattr(capacity, "create_ip", refill)
    pool_wait = Mock()
    monkeypatch.setattr(capacity, "wait_for_external_ip_pool_deletion", pool_wait)
    consumers = capacity.TestPoolCapacity()

    consumers.test_release_restores_capacity(
        small_pool=("pool-id", "pool-cr"), grpc=grpc, private_grpc=private_grpc, k8s_hub_client=k8s, created_ips=tracked
    )
    assert tracked == [("ip-last", "cr-last"), ("ip-refill", "cr-refill")]
    consumers.test_pool_deletion_succeeds_after_all_ips_released(
        small_pool=("pool-id", "pool-cr"), grpc=grpc, private_grpc=private_grpc, k8s_hub_client=k8s, created_ips=tracked
    )

    assert tracked == []
    assert clock[0] == 15
    assert grpc.delete_external_ip.call_args_list == [
        call(external_ip_id=name) for name in ["ip-first", "ip-last", "ip-refill"]
    ]
    assert read.call_args_list == [
        call("kubectl", "get", "externalips.osac.openshift.io", name, "-n", "test")
        for name in ["cr-first", "cr-last", "cr-refill"]
        for _ in range(2)
    ]
    refill.assert_called_once_with(grpc, k8s, "pool-id")
    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")
    pool_wait.assert_called_once_with(k8s=k8s, name="pool-cr")


@pytest.mark.parametrize(
    "output",
    [
        "Error from server (Forbidden): externalips is forbidden",
        "Unable to connect to the server: connection refused",
        'Error from server (NotFound): secrets.osac.openshift.io "cr-first" not found',
        'Error from server (NotFound): externalips.other.example "cr-first" not found',
        'Error from server (NotFound): externalips.osac.openshift.io "other-ip" not found',
        'Error from server (NotFound): externalips "cr-first" not found',
    ],
    ids=["forbidden", "connection-error", "wrong-resource", "wrong-group", "wrong-name", "unqualified-resource"],
)
def test_capacity_consumers_preserve_tracking_on_unconfirmed_absence(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], consumer: Callable[..., None], output: str
) -> None:
    """Fail closed on lookup errors without releasing tracking or advancing pool operations."""
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = K8sClient(namespace="test", as_system_admin=False)
    tracked = [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    read = Mock(return_value=(output, 1))
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)
    refill = Mock()
    monkeypatch.setattr(capacity, "create_ip", refill)
    pool_wait = Mock()
    monkeypatch.setattr(capacity, "wait_for_external_ip_pool_deletion", pool_wait)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        consumer(
            small_pool=("pool-id", "pool-cr"),
            grpc=grpc,
            private_grpc=private_grpc,
            k8s_hub_client=k8s,
            created_ips=tracked,
        )

    assert exc_info.value.stderr == output
    assert clock[0] == 0
    assert tracked == [("ip-first", "cr-first"), ("ip-last", "cr-last")]
    read.assert_called_once_with("kubectl", "get", "externalips.osac.openshift.io", "cr-first", "-n", "test")
    private_grpc.get_external_ip_pool.assert_not_called()
    private_grpc.delete_external_ip_pool.assert_not_called()
    refill.assert_not_called()
    pool_wait.assert_not_called()
