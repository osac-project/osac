from __future__ import annotations

import subprocess
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.references.assertions import assert_disabled_create_error


@pytest.mark.parametrize("stream", ["stderr", "output"])
def test_disabled_create_error_accepts_exact_grpcurl_status(stream: str) -> None:
    """Accept the canonical disabled status from either captured subprocess stream."""
    error = subprocess.CalledProcessError(
        1,
        ["grpcurl", "sensitive-command"],
        **{stream: "ERROR:\n  Code: FailedPrecondition\n  Message: provider networking is disabled\n"},
    )
    assert_disabled_create_error(error)


@pytest.mark.parametrize(
    "output",
    [
        "",
        "connection refused",
        "Code: Unavailable\nMessage: provider networking is disabled",
        "Code: FailedPreconditionExtra\nMessage: provider networking is disabled",
        "Code: FailedPrecondition\nMessage: pool is not ready",
        "Code: FailedPrecondition\nMessage: provider networking is disabled unexpectedly",
        "Code: FailedPrecondition\nMessage: provider networking is",
        "Message: provider networking is disabled",
        "Code: FailedPrecondition",
        "Code: FailedPrecondition\nMessage: provider networking is disabled\nMessage: other",
        "transport failed\nERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled",
        "ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\nconnection refused",
        "ERROR:\nMessage: provider networking is disabled\nCode: FailedPrecondition",
    ],
)
def test_disabled_create_error_rejects_other_failures_without_leaking(output: str) -> None:
    """Reject incomplete or unrelated errors without printing captured commands or output."""
    error = subprocess.CalledProcessError(1, ["grpcurl", "sensitive-command"], stderr=output)
    with pytest.raises(AssertionError) as failure:
        assert_disabled_create_error(error)
    assert "sensitive-command" not in str(failure.value)
    if output:
        assert output not in str(failure.value)


def test_disabled_create_rejects_unrelated_output_alongside_status() -> None:
    """Do not accept a correct status when the other stream contains a transport failure."""
    error = subprocess.CalledProcessError(
        1,
        ["grpcurl"],
        output="connection refused",
        stderr="ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n",
    )
    with pytest.raises(AssertionError):
        assert_disabled_create_error(error)


@pytest.mark.parametrize("pool_selector", ["id", "name"])
@pytest.mark.parametrize("stored", [{"total": 1}, {"items": [{"id": "unexpected"}], "total": 0}])
def test_disabled_no_storage_checks_total_not_only_returned_page(pool_selector: str, stored: dict[str, Any]) -> None:
    """Reject either nonempty named records or a nonzero total for both pool selectors."""
    from tests.e2e.references.test_networking_references import TestNetworkingReferences

    client = Mock()
    error = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n"
    )

    def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Return the expected create rejection followed by an inconsistent stored total."""
        if service.endswith("/Create"):
            raise error
        return stored

    client.call.side_effect = call
    client.list_with_filter.return_value = []
    private = Mock()
    private.call.return_value = {"items": []}
    private.get_external_ip_pool.return_value = {"object": {"status": {"allocated": 0, "available": 2}}}
    with pytest.raises(AssertionError):
        TestNetworkingReferences().test_disabled_external_ip_reference_stores_nothing(
            grpc=client,
            private_grpc=private,
            k8s_hub_client=Mock(),
            disabled_reference_pool={"id": "pool-id", "name": "pool-name"},
            ref_test_run_id="run",
            pool_selector=pool_selector,
        )


def test_disabled_pool_cleanup_releases_unexpected_stored_external_ip(monkeypatch: pytest.MonkeyPatch) -> None:
    """Release an unexpectedly persisted child before naturally deleting its owned pool."""
    from tests.e2e.references import test_networking_references as suite

    private = Mock()
    private.create_external_ip_pool.return_value = "pool-id"
    private.get_external_ip_pool.return_value = {"object": {"status": {"state": "EXTERNAL_IP_POOL_STATE_READY"}}}
    private.call.return_value = {"items": [{"id": "leaked-eip"}]}
    monkeypatch.setattr(suite, "wait_for_external_ip_pool_cr", Mock(return_value="pool-cr"))
    monkeypatch.setattr(suite, "wait_for_external_ip_pool_ready", Mock())
    cleanup = Mock()
    monkeypatch.setattr(suite, "wait_owned_gone", cleanup)
    pool = suite.disabled_reference_pool.__wrapped__(private, Mock(), "run", None)
    next(pool)
    pool.close()
    assert [entry.kwargs["resource_id"] for entry in cleanup.call_args_list] == ["leaked-eip", "pool-id"]
    private.call.assert_called_once_with(
        service=f"{suite.PRIVATE_API}.ExternalIPs/List", data={"filter": 'this.spec.pool.id == "pool-id"'}
    )
    cleanup.call_args_list[0].kwargs["delete"]()
    private.call.assert_called_with(service=f"{suite.PRIVATE_API}.ExternalIPs/Delete", data={"id": "leaked-eip"})


@pytest.mark.parametrize("pool_selector", ["id", "name"])
@pytest.mark.parametrize(
    "change",
    [
        "none",
        "unrelated-deletion",
        "unrelated-insertion",
        "allocation",
        "deletion",
        "allocated",
        "available",
        "list-read-before",
        "list-read-after",
        "pool-read-before",
        "pool-read-after",
    ],
)
def test_disabled_snapshot_contract(pool_selector: str, change: str) -> None:
    """Ignore other pools while rejecting fixture-pool mutations and propagating observer errors."""
    from tests.e2e.references import test_networking_references as suite

    client = Mock()

    def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Validate the selected pool reference and model the exact disabled API response."""
        if service.endswith("/Create"):
            assert service == f"{suite.PUBLIC_API}.ExternalIPs/Create"
            assert data["object"]["spec"] == {"pool": {pool_selector: f"pool-{pool_selector}"}}
            raise subprocess.CalledProcessError(
                1, ["grpcurl"], stderr="ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n"
            )
        assert service == f"{suite.PUBLIC_API}.ExternalIPs/List"
        assert data == {"filter": 'this.metadata.name == "ref-disabled-eip-' + pool_selector + '-run"'}
        return {"items": [], "total": 0}

    client.call.side_effect = call
    private = Mock()
    owned = {"id": "fixture-eip", "spec": {"pool": {"id": "pool-id"}}}
    unrelated = {"id": "unrelated-eip", "spec": {"pool": {"id": "other-pool-id"}}}
    initial = [owned, unrelated]
    final = [owned] if change == "unrelated-deletion" else list(initial)
    if change in ("allocation", "unrelated-insertion"):
        pool_id = "pool-id" if change == "allocation" else "other-pool-id"
        final.append({"id": "new-eip", "spec": {"pool": {"id": pool_id}}})
    if change == "deletion":
        final.remove(owned)
    snapshots = iter([initial, final])
    read_count = 0

    def read_records() -> list[dict[str, Any]]:
        """Model global API data changing between the real test method's two reads."""
        nonlocal read_count
        read_count += 1
        if change == ("list-read-before" if read_count == 1 else "list-read-after"):
            raise RuntimeError("list read failed")
        return next(snapshots)

    def private_call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Require the actual API's canonical pool-ID filter and model its matching response."""
        assert service == f"{suite.PRIVATE_API}.ExternalIPs/List"
        assert data == {"filter": 'this.spec.pool.id == "pool-id"'}
        return {"items": [item for item in read_records() if item["spec"]["pool"]["id"] == "pool-id"]}

    private.call.side_effect = private_call
    private.list_private_external_ips.side_effect = read_records
    before = {"object": {"status": {"allocated": 1, "available": 2}}}
    after = {
        "object": {
            "status": {"allocated": 2 if change == "allocated" else 1, "available": 1 if change == "available" else 2}
        }
    }
    private.get_external_ip_pool.side_effect = [
        RuntimeError("pool read failed") if change == "pool-read-before" else before,
        RuntimeError("pool read failed") if change == "pool-read-after" else after,
    ]

    def run() -> None:
        """Exercise the deployed-test oracle with deterministic before/after API snapshots."""
        suite.TestNetworkingReferences().test_disabled_external_ip_reference_stores_nothing(
            grpc=client,
            private_grpc=private,
            k8s_hub_client=Mock(),
            disabled_reference_pool={"id": "pool-id", "name": "pool-name"},
            ref_test_run_id="run",
            pool_selector=pool_selector,
        )

    if change in ("none", "unrelated-deletion", "unrelated-insertion"):
        run()
    else:
        with pytest.raises(RuntimeError if "read-" in change else AssertionError):
            run()
    private.list_private_external_ips.assert_not_called()
    expected_reads = 1 if change in ("list-read-before", "pool-read-before") else 2
    assert private.call.call_count == expected_reads
    for entry in private.call.call_args_list:
        assert entry.kwargs == {
            "service": f"{suite.PRIVATE_API}.ExternalIPs/List",
            "data": {"filter": 'this.spec.pool.id == "pool-id"'},
        }
