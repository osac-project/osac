"""Verify the CI guest ingress VIP reservation contract.

The claim registry is the only shared state between concurrent CaaS jobs on the
CI provider network, so these tests pin ownership, retry stability, conflict
resolution, exhaustion, retention and release behavior.
"""

import sys
from pathlib import Path

import pytest

COLLECTIONS_PATH = Path(__file__).resolve().parents[2] / "collections"
sys.path.insert(0, str(COLLECTIONS_PATH))

from ansible_collections.osac.service.plugins.module_utils import (  # noqa: E402
    ingress_vip_claim as claim_utils,
)

RANGE = "10.10.0.240-10.10.0.243"


def owner(uid, namespace="osac-order-a", name="order-a"):
    return {"uid": uid, "namespace": namespace, "name": name}


class FakeStore:
    """In-memory claim registry with resourceVersion and injected conflicts."""

    def __init__(self, data=None):
        self.data = dict(data or {})
        self.resource_version = "1" if self.data else None
        self.writes = 0
        self._concurrent_writer = None

    def read(self):
        return dict(self.data), self.resource_version

    def write(self, data, resource_version):
        if self._concurrent_writer is not None:
            self.data.update(self._concurrent_writer)
            self._concurrent_writer = None
            self.resource_version = f"rv-{self.writes + 1}"
            self.writes += 1
            raise claim_utils.ClaimConflictError("registry changed during the claim")
        if resource_version != self.resource_version:
            raise claim_utils.ClaimConflictError("registry changed during the claim")
        self.data = dict(data)
        self.writes += 1
        self.resource_version = f"rv-{self.writes}"

    def lose_next_write(self, data):
        self._concurrent_writer = dict(data)


def claim(store, uid="uid-a", **kwargs):
    return claim_utils.claim_address(
        RANGE,
        owner(uid, **kwargs.pop("owner_fields", {})),
        store,
        retry_delay=0,
        **kwargs,
    )


def test_range_accepts_canonical_and_rejects_invalid_input() -> None:
    assert claim_utils.parse_range("10.10.0.240-10.10.0.243") == [
        "10.10.0.240",
        "10.10.0.241",
        "10.10.0.242",
        "10.10.0.243",
    ]

    for invalid in (
        "",
        "10.10.0.240",
        "10.10.0.243-10.10.0.240",
        "10.10.0.240-10.10.1.1",
        "10.10.0.0/28",
        "10.10.0.x-10.10.0.243",
    ):
        with pytest.raises(claim_utils.InvalidRangeError):
            claim_utils.parse_range(invalid)

    # A provider may reserve a single address for the whole CI guest fleet.
    assert claim_utils.parse_range("10.10.0.240-10.10.0.240") == ["10.10.0.240"]


def test_first_claim_takes_the_first_address_and_writes_once() -> None:
    store = FakeStore()

    ip, changed = claim(store)

    assert (ip, changed) == ("10.10.0.240", True)
    assert store.data == {"uid-a": "10.10.0.240 osac-order-a/order-a"}
    assert store.writes == 1


def test_repeated_and_concurrent_retries_reuse_the_same_address() -> None:
    store = FakeStore()
    first_ip, _ = claim(store)

    for _ in range(3):
        ip, changed = claim(store)
        assert (ip, changed) == (first_ip, False)

    assert store.writes == 1


def test_distinct_clusters_get_distinct_addresses() -> None:
    store = FakeStore()

    addresses = {claim(store, uid=f"uid-{index}")[0] for index in range(4)}

    assert addresses == {"10.10.0.240", "10.10.0.241", "10.10.0.242", "10.10.0.243"}


def test_losing_a_write_race_retries_and_takes_another_address() -> None:
    store = FakeStore()
    store.lose_next_write({"uid-other": "10.10.0.240 osac-order-b/order-b"})

    ip, changed = claim(store)

    assert (ip, changed) == ("10.10.0.241", True)
    assert store.data == {
        "uid-other": "10.10.0.240 osac-order-b/order-b",
        "uid-a": "10.10.0.241 osac-order-a/order-a",
    }


def test_persistent_conflict_is_reported_as_retryable() -> None:
    store = FakeStore()

    def always_conflict(data, resource_version):
        raise claim_utils.ClaimConflictError("registry busy")

    store.write = always_conflict

    with pytest.raises(claim_utils.ClaimConflictError):
        claim_utils.claim_address(RANGE, owner("uid-a"), store, max_attempts=3, retry_delay=0)


def test_exhausted_range_fails_with_addresses_and_claims() -> None:
    store = FakeStore(
        {
            "uid-1": "10.10.0.240 ocac/order-1",
            "uid-2": "10.10.0.241 osac/order-2",
            "uid-3": "10.10.0.242 osac/order-3",
            "uid-4": "10.10.0.243 osac/order-4",
        }
    )

    with pytest.raises(claim_utils.ClaimExhaustedError) as error:
        claim(store)

    message = str(error.value)
    assert "10.10.0.240-10.10.0.243" in message
    assert "4" in message
    assert store.writes == 0


def test_address_outside_the_reserved_range_is_refused() -> None:
    store = FakeStore({"uid-a": "10.10.0.99 osac-order-a/order-a"})

    with pytest.raises(claim_utils.ClaimError):
        claim(store)


def test_same_owner_uid_recorded_for_another_order_is_refused() -> None:
    store = FakeStore({"uid-a": "10.10.0.240 osac-order-a/other-order"})

    with pytest.raises(claim_utils.ForeignClaimError):
        claim(store)

    assert store.writes == 0


def test_claim_is_retained_when_a_later_step_of_the_same_job_fails() -> None:
    store = FakeStore()
    ip, _ = claim(store)

    with pytest.raises(claim_utils.ClaimExhaustedError):
        # The range holds a single address, so nothing is free for another order.
        claim_utils.claim_address(
            "10.10.0.240-10.10.0.240",
            owner("uid-b", namespace="osac-order-b", name="order-b"),
            store,
            retry_delay=0,
        )

    assert store.data["uid-a"] == f"{ip} osac-order-a/order-a"
    assert claim(store)[0] == ip


def test_stale_claims_of_deleted_orders_are_reclaimed() -> None:
    store = FakeStore({"uid-gone": "10.10.0.240 osac-order-gone/order-gone"})

    ip, changed = claim(
        store, resolve_owner_alive=lambda uid, namespace, name: uid != "uid-gone"
    )

    assert (ip, changed) == ("10.10.0.240", True)
    assert "uid-gone" not in store.data
    assert store.data["uid-a"] == "10.10.0.240 osac-order-a/order-a"


def test_release_removes_only_the_owning_claim_and_is_idempotent() -> None:
    store = FakeStore(
        {
            "uid-a": "10.10.0.240 osac-order-a/order-a",
            "uid-b": "10.10.0.241 osac-order-b/order-b",
        }
    )

    assert claim_utils.release_address(owner("uid-a"), store, retry_delay=0) == (
        "10.10.0.240",
        True,
    )
    assert store.data == {"uid-b": "10.10.0.241 osac-order-b/order-b"}

    assert claim_utils.release_address(owner("uid-a"), store, retry_delay=0) == (
        None,
        False,
    )
    assert store.writes == 1


def test_release_refuses_a_claim_recorded_for_another_order() -> None:
    store = FakeStore({"uid-a": "10.10.0.240 osac-order-a/other-order"})

    with pytest.raises(claim_utils.ForeignClaimError):
        claim_utils.release_address(owner("uid-a"), store, retry_delay=0)

    assert "uid-a" in store.data


def test_release_retries_a_conflicting_write() -> None:
    store = FakeStore({"uid-a": "10.10.0.240 osac-order-a/order-a"})
    store.lose_next_write({"uid-c": "10.10.0.241 osac-order-c/order-c"})

    assert claim_utils.release_address(owner("uid-a"), store, retry_delay=0) == (
        "10.10.0.240",
        True,
    )
    assert store.data == {"uid-c": "10.10.0.241 osac-order-c/order-c"}


def test_registry_records_are_validated_before_use() -> None:
    broken = FakeStore({"uid-x": "not-an-address"})

    with pytest.raises(claim_utils.ClaimError):
        claim(broken)
