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


def test_disabled_no_storage_checks_total_not_only_returned_page() -> None:
    """Fail the no-storage oracle for a nonzero total even when the returned page is empty."""
    from tests.e2e.references.test_networking_references import TestNetworkingReferences

    client = Mock()
    error = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n"
    )

    def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Return the expected create rejection followed by an inconsistent stored total."""
        if service.endswith("/Create"):
            raise error
        return {"total": 1}

    client.call.side_effect = call
    client.list_with_filter.return_value = []
    private = Mock()
    private.list_private_external_ips.return_value = []
    private.get_external_ip_pool.return_value = {"object": {"status": {"allocated": 0, "available": 2}}}
    with pytest.raises(AssertionError):
        TestNetworkingReferences().test_disabled_external_ip_reference_stores_nothing(
            grpc=client,
            private_grpc=private,
            k8s_hub_client=Mock(),
            disabled_reference_pool={"id": "pool-id", "name": "pool-name"},
            ref_test_run_id="run",
            pool_selector="id",
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
    pool = suite.disabled_reference_pool.__wrapped__(private, Mock(), "run")
    next(pool)
    pool.close()
    assert [entry.kwargs["resource_id"] for entry in cleanup.call_args_list] == ["leaked-eip", "pool-id"]
    private.call.assert_called_once_with(
        service=f"{suite.PRIVATE_API}.ExternalIPs/List", data={"filter": 'this.spec.pool.id == "pool-id"'}
    )
    cleanup.call_args_list[0].kwargs["delete"]()
    private.call.assert_called_with(service=f"{suite.PRIVATE_API}.ExternalIPs/Delete", data={"id": "leaked-eip"})


@pytest.mark.parametrize("pool_selector", ["id", "name"])
@pytest.mark.parametrize("change", ["none", "allocation", "allocated", "available", "read-error"])
def test_disabled_snapshot_contract(pool_selector: str, change: str) -> None:
    """Accept unchanged state only; fail allocation/accounting mutations and observer errors."""
    from tests.e2e.references import test_networking_references as suite

    client = Mock()

    def call(*, service: str, data: dict[str, Any]) -> dict[str, Any]:
        """Validate the selected pool reference and model the exact disabled API response."""
        if service.endswith("/Create"):
            assert data["object"]["spec"] == {"pool": {pool_selector: f"pool-{pool_selector}"}}
            raise subprocess.CalledProcessError(
                1, ["grpcurl"], stderr="ERROR:\nCode: FailedPrecondition\nMessage: provider networking is disabled\n"
            )
        return {}

    client.call.side_effect = call
    private = Mock()
    private.list_private_external_ips.side_effect = [[], [{"id": "unexpected"}] if change == "allocation" else []]
    before = {"object": {"status": {"allocated": 0, "available": 2}}}
    after = {
        "object": {"status": {"allocated": int(change == "allocated"), "available": 1 if change == "available" else 2}}
    }
    private.get_external_ip_pool.side_effect = [
        before,
        RuntimeError("read failed") if change == "read-error" else after,
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

    if change == "none":
        run()
    else:
        with pytest.raises(RuntimeError if change == "read-error" else AssertionError):
            run()
