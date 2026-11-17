"""Verify the CI guest ingress VIP reservation contract.

The claim registry is the only shared state between concurrent CaaS jobs on the
CI provider network, so these tests pin ownership, retry stability, conflict
resolution, exhaustion, retention and release behavior.
"""

import sys
from pathlib import Path
from unittest.mock import Mock

import pytest
from kubernetes import client
from kubernetes.client.rest import ApiException
from urllib3.exceptions import ConnectTimeoutError, HTTPError, MaxRetryError, ReadTimeoutError

COLLECTIONS_PATH = Path(__file__).resolve().parents[2] / "collections"
sys.path.insert(0, str(COLLECTIONS_PATH))

from ansible_collections.ci.steps.plugins.module_utils import (  # noqa: E402
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


@pytest.mark.parametrize("ip_range", [
    "127.0.0.1-127.0.0.1",
    "0.0.0.0-0.0.0.0",
    "0.0.0.0-0.0.0.3",
    "224.0.0.1-224.0.0.1",
])
def test_unusable_vip_ranges_fail_without_writing_claims(ip_range) -> None:
    store = FakeStore()

    with pytest.raises(claim_utils.InvalidRangeError):
        claim_utils.claim_address(ip_range, owner("uid-a"), store, retry_delay=0)

    assert store.data == {}
    assert store.writes == 0


@pytest.mark.parametrize("ip_range", ["255.255.255.255-255.255.255.255", "255.255.255.254-255.255.255.255"])
def test_limited_broadcast_ranges_fail_before_registry_access(ip_range: str) -> None:
    store = Mock()
    store.read.return_value = ({}, None)

    with pytest.raises(claim_utils.InvalidRangeError, match="limited-broadcast"):
        claim_utils.claim_address(ip_range, owner("uid-a"), store, retry_delay=0)

    store.read.assert_not_called()
    store.write.assert_not_called()


@pytest.mark.parametrize("operation", ["claim", "release"])
@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("namespace", "bad namespace"),
        ("namespace", "bad\tspace"),
        ("namespace", "bad\nspace"),
        ("namespace", "bad/namespace"),
        ("namespace", "two.labels"),
        ("namespace", "Uppercase"),
        ("namespace", "under_score"),
        ("namespace", "-leading"),
        ("namespace", "trailing-"),
        ("namespace", "a" * 64),
        ("name", "bad name"),
        ("name", "bad\tname"),
        ("name", "bad\nname"),
        ("name", "bad/name"),
        ("name", "Uppercase"),
        ("name", "under_score"),
        ("name", "-leading"),
        ("name", "trailing-"),
        ("name", "two..dots"),
        ("name", "trailing."),
        ("name", "a" * 254),
    ],
)
def test_invalid_owner_identity_fails_before_registry_access(operation: str, field: str, value: str) -> None:
    store = Mock()
    store.read.return_value = ({}, None)
    invalid_owner = owner("uid-a", **{field: value})

    with pytest.raises(claim_utils.ClaimError, match=field):
        if operation == "claim":
            claim_utils.claim_address(RANGE, invalid_owner, store, retry_delay=0)
        else:
            claim_utils.release_address(invalid_owner, store, retry_delay=0)

    store.read.assert_not_called()
    store.write.assert_not_called()


@pytest.mark.parametrize(
    ("namespace", "name"), [("a", "0"), ("a" * 63, "a" * 253), ("ci", "order.region.example"), ("ci-0", "order-0")]
)
def test_valid_owner_identity_round_trips_through_claim_and_release(namespace: str, name: str) -> None:
    store = FakeStore()
    identity = owner("uid-a", namespace=namespace, name=name)

    assert claim_utils.claim_address(RANGE, identity, store, retry_delay=0) == ("10.10.0.240", True)
    assert claim_utils.release_address(identity, store, retry_delay=0) == ("10.10.0.240", True)
    assert store.data == {}
    assert store.writes == 2


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


def test_claims_are_retained_until_explicit_release() -> None:
    store = FakeStore({"uid-gone": "10.10.0.240 osac-order-gone/order-gone"})

    ip, changed = claim(store)

    assert (ip, changed) == ("10.10.0.241", True)
    assert store.data["uid-gone"] == "10.10.0.240 osac-order-gone/order-gone"
    assert store.data["uid-a"] == "10.10.0.241 osac-order-a/order-a"


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


def test_config_map_reads_preserve_data_and_resource_version() -> None:
    api = Mock()
    api.read_namespaced_config_map.return_value = client.V1ConfigMap(
        data={"uid-a": "10.10.0.240 osac-order-a/order-a"},
        metadata=client.V1ObjectMeta(resource_version="17"),
    )
    store = claim_utils.ConfigMapClaimStore(api, "aap")

    assert store.read() == ({"uid-a": "10.10.0.240 osac-order-a/order-a"}, "17")
    api.read_namespaced_config_map.assert_called_once_with(
        claim_utils.DEFAULT_REGISTRY_NAME, "aap", _request_timeout=(5, 30)
    )


@pytest.mark.parametrize("status", [403, 404, 500])
def test_only_missing_registries_are_treated_as_empty(status) -> None:
    api = Mock()
    api.read_namespaced_config_map.side_effect = ApiException(status=status)
    store = claim_utils.ConfigMapClaimStore(api, "aap")

    if status == 404:
        assert store.read() == ({}, None)
    else:
        with pytest.raises(claim_utils.ClaimError):
            store.read()
    api.create_namespaced_config_map.assert_not_called()
    api.replace_namespaced_config_map.assert_not_called()


@pytest.mark.parametrize("version", [None, "17"])
def test_config_map_writes_use_create_or_compare_and_swap(version) -> None:
    api = Mock()
    store = claim_utils.ConfigMapClaimStore(api, "aap")
    data = {"uid-a": "10.10.0.240 osac-order-a/order-a"}

    store.write(data, version)

    method = (
        api.create_namespaced_config_map if version is None
        else api.replace_namespaced_config_map
    )
    body = method.call_args.args[-1]
    assert method.call_args.kwargs == {"_request_timeout": (5, 30)}
    assert body["data"] == data
    assert body["metadata"]["labels"] == {claim_utils.CLAIM_LABEL: "true"}
    if version is None:
        assert "resourceVersion" not in body["metadata"]
        assert method.call_args.args[0] == "aap"
        api.replace_namespaced_config_map.assert_not_called()
    else:
        assert body["metadata"]["resourceVersion"] == version
        assert method.call_args.args[:2] == (claim_utils.DEFAULT_REGISTRY_NAME, "aap")
        api.create_namespaced_config_map.assert_not_called()


@pytest.mark.parametrize("version", [None, "17"])
@pytest.mark.parametrize("status", [403, 409, 500])
def test_config_map_write_conflicts_are_retryable_and_other_errors_fail_closed(
    version, status
) -> None:
    api = Mock()
    method = (
        api.create_namespaced_config_map if version is None
        else api.replace_namespaced_config_map
    )
    method.side_effect = ApiException(status=status)
    store = claim_utils.ConfigMapClaimStore(api, "aap")

    error = claim_utils.ClaimConflictError if status == 409 else claim_utils.ClaimError
    with pytest.raises(error):
        store.write({}, version)


@pytest.mark.parametrize("operation", ["claim", "release", "create", "replace"])
@pytest.mark.parametrize(
    "error",
    [
        ReadTimeoutError(None, "/registry", "sensitive transport details"),
        ConnectTimeoutError("sensitive transport details"),
        MaxRetryError(None, "/registry", reason=OSError("sensitive transport details")),
    ],
    ids=["read-timeout", "connect-timeout", "max-retry"],
)
def test_registry_transport_failures_fail_closed_without_additional_requests(operation: str, error: HTTPError) -> None:
    api = Mock()
    method = {
        "claim": api.read_namespaced_config_map,
        "release": api.read_namespaced_config_map,
        "create": api.create_namespaced_config_map,
        "replace": api.replace_namespaced_config_map,
    }[operation]
    method.side_effect = error
    store = claim_utils.ConfigMapClaimStore(api, "aap")

    with pytest.raises(claim_utils.ClaimError) as caught:
        if operation == "claim":
            claim_utils.claim_address(RANGE, owner("uid-a"), store, retry_delay=0)
        elif operation == "release":
            claim_utils.release_address(owner("uid-a"), store, retry_delay=0)
        else:
            store.write({"uid-b": "10.10.0.241 osac-order-b/order-b"}, None if operation == "create" else "17")

    assert not isinstance(caught.value, claim_utils.ClaimConflictError)
    assert "aap/osac-ci-guest-ingress-vip-claims" in str(caught.value)
    assert type(error).__name__ in str(caught.value)
    assert "sensitive transport details" not in str(caught.value)
    assert caught.value.__cause__ is error
    assert len(api.mock_calls) == 1


@pytest.mark.parametrize("registry_exists", [False, True])
def test_committed_write_timeout_is_recovered_by_rereading_the_owner_claim(registry_exists: bool) -> None:
    api = Mock()
    data = {"uid-other": "10.10.0.240 osac-order-b/order-b"} if registry_exists else {}
    version = "17" if registry_exists else None

    def read(*args: object, **kwargs: object) -> client.V1ConfigMap:
        if version is None:
            raise ApiException(status=404)
        return client.V1ConfigMap(data=dict(data), metadata=client.V1ObjectMeta(resource_version=version))

    def commit_then_timeout(*args: object, **kwargs: object) -> None:
        nonlocal version
        body = args[-1]
        assert isinstance(body, dict)
        assert body["metadata"].get("resourceVersion") == version
        data.clear()
        data.update(body["data"])
        version = "18"
        raise ReadTimeoutError(None, "/registry", "response lost after commit")

    api.read_namespaced_config_map.side_effect = read
    method = api.replace_namespaced_config_map if registry_exists else api.create_namespaced_config_map
    method.side_effect = commit_then_timeout
    store = claim_utils.ConfigMapClaimStore(api, "aap")
    expected = "10.10.0.241" if registry_exists else "10.10.0.240"

    with pytest.raises(claim_utils.ClaimError, match="ReadTimeoutError"):
        claim_utils.claim_address(RANGE, owner("uid-a"), store, retry_delay=0)

    assert api.read_namespaced_config_map.call_count == 1
    assert method.call_count == 1
    assert data["uid-a"] == f"{expected} osac-order-a/order-a"
    if registry_exists:
        assert data["uid-other"] == "10.10.0.240 osac-order-b/order-b"
    assert claim_utils.claim_address(RANGE, owner("uid-a"), store, retry_delay=0) == (expected, False)
    assert api.read_namespaced_config_map.call_count == 2
    assert method.call_count == 1
    assert len(api.mock_calls) == 3
