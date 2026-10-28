from __future__ import annotations

import json
import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core import grpc_client
from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient


@pytest.fixture
def rpc_stub(monkeypatch: pytest.MonkeyPatch) -> tuple[GRPCClient, Mock, Mock]:
    """Replace command execution and backoff with mocks while exercising the real client."""
    test_token = "test-private-token"
    client = GRPCClient(address="fulfillment-api.example.test:443", token=test_token, retry_read_connections=True)
    run = Mock(return_value='{"items": []}')
    sleep = Mock()
    monkeypatch.setattr(grpc_client, "run", run)
    monkeypatch.setattr(grpc_client.time, "sleep", sleep)
    return client, run, sleep


def _dial_timeout(client: GRPCClient) -> subprocess.CalledProcessError:
    """Match the pre-connection failure text that is eligible for safe read retries."""
    return subprocess.CalledProcessError(
        1,
        ["grpcurl", "-H", f"Authorization: Bearer {client.token}"],
        stderr=f'Failed to dial target host "{client.address}": context deadline exceeded\n',
    )


@pytest.mark.parametrize("api", [PUBLIC_API, PRIVATE_API])
@pytest.mark.parametrize("method", ["Get", "List"])
@pytest.mark.parametrize("failed_attempts", [1, 2])
def test_read_retries_only_dial_timeout(
    rpc_stub: tuple[GRPCClient, Mock, Mock],
    caplog: pytest.LogCaptureFixture,
    api: str,
    method: str,
    failed_attempts: int,
) -> None:
    """Require identical requests, bounded backoff and credential-free retry logging."""
    client, run, sleep = rpc_stub
    response = {"object": {"id": "private-resource-id"}}
    run.side_effect = [_dial_timeout(client) for _ in range(failed_attempts)] + [json.dumps(response)]

    assert client.call(service=f"{api}.ComputeInstances/{method}", data={"id": "private-resource-id"}) == response
    assert run.call_count == failed_attempts + 1
    assert all(call.args == run.call_args_list[0].args for call in run.call_args_list)
    assert sleep.call_count == failed_attempts
    assert all(call.args == (2,) for call in sleep.call_args_list)
    assert len(caplog.records) == failed_attempts
    assert "attempt 2/3" in caplog.text
    if failed_attempts == 2:
        assert "attempt 3/3" in caplog.text
    for sensitive in [client.token, client.address, "private-resource-id", "Authorization", "ComputeInstances"]:
        assert sensitive not in caplog.text


@pytest.mark.parametrize("method", ["Get", "List"])
def test_read_dial_timeout_budget_exhaustion_fails(rpc_stub: tuple[GRPCClient, Mock, Mock], method: str) -> None:
    """Propagate the final transport error after exactly three command attempts."""
    client, run, sleep = rpc_stub
    errors = [_dial_timeout(client) for _ in range(3)]
    run.side_effect = errors

    with pytest.raises(subprocess.CalledProcessError) as caught:
        client.call(service=f"{PUBLIC_API}.ComputeInstances/{method}")

    assert caught.value is errors[-1]
    assert run.call_count == 3
    assert sleep.call_count == 2


@pytest.mark.parametrize(
    ("returncode", "stderr"),
    [
        (16, "ERROR:\n  Code: Unauthenticated\n  Message: invalid token"),
        (7, "ERROR:\n  Code: PermissionDenied\n  Message: access denied"),
        (3, "ERROR:\n  Code: InvalidArgument\n  Message: bad request"),
        (5, "ERROR:\n  Code: NotFound\n  Message: absent"),
        (4, "ERROR:\n  Code: DeadlineExceeded\n  Message: context deadline exceeded"),
        (14, "ERROR:\n  Code: Unavailable\n  Message: unavailable"),
        (1, "Failed to query for service descriptor: context deadline exceeded"),
        (1, 'Failed to dial target host "fulfillment-api.example.test:443": connection refused'),
        (1, 'Failed to dial target host "different.example.test:443": context deadline exceeded'),
        (1, 'Failed to dial target host "fulfillment-api.example.test:443": context deadline exceeded\nother error'),
        (1, "unknown failure"),
        (1, ""),
    ],
)
def test_other_read_errors_fail_immediately(
    rpc_stub: tuple[GRPCClient, Mock, Mock], returncode: int, stderr: str
) -> None:
    """Keep RPC statuses, reflection failures and nonmatching dial errors outside retries."""
    client, run, sleep = rpc_stub
    error = subprocess.CalledProcessError(returncode, ["grpcurl"], stderr=stderr)
    run.side_effect = error

    with pytest.raises(subprocess.CalledProcessError) as caught:
        client.call(service=f"{PUBLIC_API}.ComputeInstances/List")

    assert caught.value is error
    assert run.call_count == 1
    sleep.assert_not_called()


@pytest.mark.parametrize("method", ["Create", "Update", "Delete", "Restart", "ListExtra", "GetExtra"])
def test_other_methods_do_not_retry_dial_timeout(rpc_stub: tuple[GRPCClient, Mock, Mock], method: str) -> None:
    """Protect writes and similarly named methods from the Get/List retry policy."""
    client, run, sleep = rpc_stub
    run.side_effect = _dial_timeout(client)

    with pytest.raises(subprocess.CalledProcessError):
        client.call(service=f"{PUBLIC_API}.ComputeInstances/{method}")

    assert run.call_count == 1
    sleep.assert_not_called()


@pytest.mark.parametrize(
    "error", [subprocess.TimeoutExpired(["grpcurl"], 300), json.JSONDecodeError("invalid JSON", "bad", 0)]
)
def test_other_exception_types_do_not_retry(rpc_stub: tuple[GRPCClient, Mock, Mock], error: Exception) -> None:
    """Leave subprocess timeouts and invalid response JSON visible to the caller."""
    client, run, sleep = rpc_stub
    run.side_effect = error

    with pytest.raises(type(error)) as caught:
        client.call(service=f"{PUBLIC_API}.ComputeInstances/List")

    assert caught.value is error
    assert run.call_count == 1
    sleep.assert_not_called()


def test_successful_read_is_called_once(rpc_stub: tuple[GRPCClient, Mock, Mock]) -> None:
    """Avoid extra requests or backoff when the first response succeeds."""
    client, run, sleep = rpc_stub

    assert client.call(service=f"{PUBLIC_API}.ComputeInstances/List") == {"items": []}
    assert run.call_count == 1
    sleep.assert_not_called()


def test_token_failure_does_not_retry_rpc(rpc_stub: tuple[GRPCClient, Mock, Mock]) -> None:
    """Do not start a command when obtaining its credentials fails."""
    _, run, sleep = rpc_stub
    token_factory = Mock(side_effect=RuntimeError("authentication failed"))
    client = GRPCClient(
        address="fulfillment-api.example.test:443", token_factory=token_factory, retry_read_connections=True
    )

    with pytest.raises(RuntimeError, match="authentication failed"):
        client.call(service=f"{PUBLIC_API}.ComputeInstances/List")

    token_factory.assert_called_once()
    run.assert_not_called()
    sleep.assert_not_called()


def test_token_subprocess_failure_is_outside_retry_policy(rpc_stub: tuple[GRPCClient, Mock, Mock]) -> None:
    """Keep authentication failures outside retries even when their text resembles dial errors."""
    original, run, sleep = rpc_stub
    token_factory = Mock(side_effect=_dial_timeout(original))
    client = GRPCClient(address=original.address, token_factory=token_factory, retry_read_connections=True)

    with pytest.raises(subprocess.CalledProcessError):
        client.call(service=f"{PUBLIC_API}.ComputeInstances/List")

    token_factory.assert_called_once()
    run.assert_not_called()
    sleep.assert_not_called()


@pytest.mark.parametrize("method", ["Get", "List"])
def test_default_client_preserves_fail_fast_behavior(rpc_stub: tuple[GRPCClient, Mock, Mock], method: str) -> None:
    """Require explicit opt-in before retrying either public read method."""
    enabled, run, sleep = rpc_stub
    client = GRPCClient(address=enabled.address, token=enabled.token)
    error = _dial_timeout(client)
    run.side_effect = error

    with pytest.raises(subprocess.CalledProcessError) as caught:
        client.call(service=f"{PUBLIC_API}.ComputeInstances/{method}")

    assert caught.value is error
    assert run.call_count == 1
    sleep.assert_not_called()


def test_retry_client_keeps_original_token_refresh_and_does_not_change_original(
    rpc_stub: tuple[GRPCClient, Mock, Mock], monkeypatch: pytest.MonkeyPatch
) -> None:
    """Refresh credentials between attempts without enabling retries on the original client."""
    enabled, run, sleep = rpc_stub
    token_factory = Mock(side_effect=["first-token", "refreshed-token", "original-refreshed-token"])
    original = GRPCClient(address=enabled.address, token_factory=token_factory)
    client = original.with_read_connection_retries()
    clock = Mock(side_effect=[0.0, 61.0, 62.0])
    monkeypatch.setattr(grpc_client.time, "monotonic", clock)
    error = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr=f'Failed to dial target host "{enabled.address}": context deadline exceeded'
    )
    run.side_effect = [error, '{"items": []}', error]

    assert client.call(service=f"{PUBLIC_API}.ComputeInstances/List") == {"items": []}
    assert "Authorization: Bearer first-token" in run.call_args_list[0].args
    assert "Authorization: Bearer refreshed-token" in run.call_args_list[1].args
    assert token_factory.call_count == 2

    with pytest.raises(subprocess.CalledProcessError) as caught:
        original.call(service=f"{PUBLIC_API}.ComputeInstances/List")

    assert caught.value is error
    assert run.call_count == 3
    assert sleep.call_count == 1


def test_retry_client_preserves_existing_token_cache_age(
    rpc_stub: tuple[GRPCClient, Mock, Mock], monkeypatch: pytest.MonkeyPatch
) -> None:
    """Copy cached credentials with their original age so cloning cannot extend their TTL."""
    enabled, _, _ = rpc_stub
    clock = Mock(return_value=0.0)
    monkeypatch.setattr(grpc_client.time, "monotonic", clock)
    token_factory = Mock(side_effect=["first-token", "refreshed-token"])
    original = GRPCClient(address=enabled.address, token_factory=token_factory)
    assert original.token == "first-token"

    clock.return_value = 59.0
    client = original.with_read_connection_retries()
    assert client.token == "first-token"
    token_factory.assert_called_once()

    clock.return_value = 61.0
    assert client.token == "refreshed-token"
    assert token_factory.call_count == 2
    assert original._cached_token == "first-token"
    assert original._cached_at == 0.0


def test_unchecked_call_preserves_error_without_retry(
    rpc_stub: tuple[GRPCClient, Mock, Mock], monkeypatch: pytest.MonkeyPatch
) -> None:
    """Return unchecked command results verbatim without invoking checked retry logic."""
    client, run, sleep = rpc_stub
    error = _dial_timeout(client)
    unchecked = Mock(return_value=(error.stderr, error.returncode))
    monkeypatch.setattr(grpc_client, "run_unchecked", unchecked)

    assert client.call_unchecked(service=f"{PUBLIC_API}.ComputeInstances/List") == (error.stderr, error.returncode)
    unchecked.assert_called_once()
    run.assert_not_called()
    sleep.assert_not_called()
