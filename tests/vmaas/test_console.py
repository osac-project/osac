from __future__ import annotations

import base64
import json
import ssl
import subprocess
import time
from collections.abc import Iterator
from datetime import UTC, datetime
from typing import Any

import pytest
import websocket

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    assert_grpc_rejected,
    wait_for_cr,
    wait_for_deletion,
    wait_for_provision,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI

CONSOLE_WS_PATH = "/api/fulfillment/v1/console_sessions/connect"
CONSOLE_GRPC_SERVICE = "osac.public.v1.ConsoleProxy/Connect"


@pytest.fixture(scope="module")
def console_vm(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_subnet: str,
) -> Iterator[dict[str, str]]:
    """Create a single compute instance for all console tests in this module."""
    print("\nCreating console test VM...")
    uuid: str = cli.create_compute_instance(template=vm_template, network_attachments=[{"subnet": default_subnet}])
    ci_name: str | None = None
    try:
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
        print(f"Waiting for {ci_name} to provision and reach Running...")
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        print(f"Console test VM {ci_name} is Running")

        yield {"uuid": uuid, "name": ci_name}
    finally:
        print(f"\nCleaning up console test VM {uuid}...")
        try:
            cli.delete_compute_instance(uuid=uuid)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        except Exception as e:
            print(f"WARNING: Failed to cleanup console VM {uuid}: {e}")


def _ws_url(fulfillment_address: str) -> str:
    """Build the WebSocket console proxy URL from the fulfillment address."""
    host: str = fulfillment_address.rsplit(":", 1)[0]
    return f"wss://{host}{CONSOLE_WS_PATH}"


def _ws_connect(url: str, ticket: str, timeout: int = 30) -> websocket.WebSocket:
    """Open a WebSocket connection to the console proxy with the given ticket."""
    return websocket.create_connection(
        url,
        header={"Authorization": f"Bearer {ticket}"},
        sslopt={"cert_reqs": ssl.CERT_NONE},
        subprotocols=["binary"],
        timeout=timeout,
    )


def _ws_try_connect(url: str, ticket: str) -> bool:
    """Try to open a WebSocket connection. Returns True if successful."""
    try:
        ws = _ws_connect(url, ticket, timeout=10)
        ws.close()
        return True
    except Exception as exc:
        print(f"    _ws_try_connect failed: {type(exc).__name__}: {exc}")
        return False


def _grpc_popen(address: str, ticket: str) -> subprocess.Popen:
    """Start a gRPC console stream subprocess. Caller must manage stdin/lifecycle."""
    return subprocess.Popen(
        ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {ticket}", "-d", "@", address, CONSOLE_GRPC_SERVICE],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def _grpc_stream(
    address: str, ticket: str, *, wait: float = 5, input_data: bytes | None = None
) -> tuple[str, str, int]:
    """Open a gRPC console stream via grpcurl, optionally send data, then close.

    Returns (stdout, stderr, returncode).
    """
    proc = _grpc_popen(address, ticket)
    try:
        time.sleep(wait)

        if input_data is not None:
            encoded: str = base64.b64encode(input_data).decode()
            msg = json.dumps({"input": {"data": encoded}}) + "\n"
            proc.stdin.write(msg.encode())  # type: ignore[union-attr]
            proc.stdin.flush()  # type: ignore[union-attr]
            time.sleep(2)

        proc.stdin.close()  # type: ignore[union-attr]
        proc.stdin = None  # prevent communicate() from flushing closed stdin
        stdout, stderr = proc.communicate(timeout=30)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.communicate()
        raise
    except BaseException:
        proc.kill()
        proc.communicate()
        raise
    return stdout.decode(), stderr.decode(), proc.returncode


def _create_ticket(grpc: GRPCClient, vm_uuid: str, *, client_id: str = "") -> dict[str, Any]:
    """Create a serial console session and return the session object."""
    return grpc.create_console_session(
        resource_type="CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE",
        resource_id=vm_uuid,
        console_type="CONSOLE_TYPE_SERIAL",
        client_id=client_id,
    )


# ---------------------------------------------------------------------------
# Positive tests — WebSocket transport
# ---------------------------------------------------------------------------


def test_console_serial_websocket(console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str) -> None:
    """Connect to the serial console via WebSocket, verify bidirectional data."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    ticket: str = session["ticket"]
    assert ticket != "", "Ticket must not be empty"
    assert "expiresAt" in session, "Session must include expiresAt"

    url: str = _ws_url(fulfillment_address)
    ws = _ws_connect(url, ticket)
    try:
        data = ws.recv()
        assert data, "Expected serial console output after connect"

        ws.send(b"\n")
        more = ws.recv()
        assert more, "Expected response after sending input"
    finally:
        ws.close()


# ---------------------------------------------------------------------------
# Positive tests — gRPC stream transport
# ---------------------------------------------------------------------------


def test_console_serial_grpc_stream(console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str) -> None:
    """Connect to the serial console via gRPC bidi stream, verify data flows."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    ticket: str = session["ticket"]

    stdout, stderr, rc = _grpc_stream(fulfillment_address, ticket, input_data=b"\n")
    assert rc == 0, f"gRPC stream failed: rc={rc}, stderr={stderr!r}"
    assert "output" in stdout, f"Expected console output, got: {stdout!r}"


# ---------------------------------------------------------------------------
# Ticket reuse — single-use JTI enforcement
# ---------------------------------------------------------------------------


def test_console_ticket_reuse_rejected(console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str) -> None:
    """A ticket should work exactly once. 4 subsequent attempts must all fail."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    ticket: str = session["ticket"]
    url: str = _ws_url(fulfillment_address)

    results: list[bool] = []
    for i in range(5):
        success: bool = _ws_try_connect(url, ticket)
        results.append(success)
        print(f"  Attempt {i + 1}: {'success' if success else 'rejected'}")
        if success:
            time.sleep(1)

    assert results[0] is True, "First use of ticket must succeed"
    assert all(r is False for r in results[1:]), f"Subsequent uses must fail (JTI single-use), got: {results}"


# ---------------------------------------------------------------------------
# Concurrent session — only one session per resource
# ---------------------------------------------------------------------------


def test_console_concurrent_session_rejected(
    console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str
) -> None:
    """A second connection to the same resource must be rejected while one is active."""
    session1: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    session2: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])

    # Use gRPC for session1 — grpcurl keeps the bidi stream alive as long as
    # stdin is open, unlike WebSocket which can close if the backend disconnects.
    proc1 = _grpc_popen(fulfillment_address, session1["ticket"])
    try:
        time.sleep(2)
        assert proc1.poll() is None, "First session should still be running"

        stdout, stderr, rc = _grpc_stream(fulfillment_address, session2["ticket"], wait=3)
        combined: str = stdout + stderr
        assert rc != 0, f"Second connection should fail, got: {combined!r}"
        assert "FailedPrecondition" in combined or "session already active" in combined, (
            f"Expected FailedPrecondition or 'session already active', got: {combined!r}"
        )

        assert proc1.poll() is None, "First session died while second was rejected"
    finally:
        proc1.kill()
        proc1.communicate()


# ---------------------------------------------------------------------------
# Expired ticket
# ---------------------------------------------------------------------------


def test_console_expired_ticket_rejected(
    console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str
) -> None:
    """A ticket used after its expiresAt must be rejected."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    ticket: str = session["ticket"]
    expires_at_str: str = session["expiresAt"]

    expires_at = datetime.fromisoformat(expires_at_str.replace("Z", "+00:00"))
    seconds_until_expiry: float = (expires_at - datetime.now(tz=UTC)).total_seconds()
    wait_seconds: float = min(60.0, max(0.0, seconds_until_expiry) + 15.0)

    print(f"Ticket expires at {expires_at_str}, waiting {wait_seconds:.0f}s...")
    time.sleep(wait_seconds)

    url: str = _ws_url(fulfillment_address)
    success: bool = _ws_try_connect(url, ticket)
    assert success is False, "Expired ticket must be rejected"


# ---------------------------------------------------------------------------
# Negative tests — no running VM required
# ---------------------------------------------------------------------------


def test_console_session_nonexistent_vm(grpc: GRPCClient) -> None:
    """Creating a console session for a non-existent VM should fail."""
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        grpc.create_console_session(
            resource_type="CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE",
            resource_id="00000000-0000-0000-0000-000000000000",
            console_type="CONSOLE_TYPE_SERIAL",
        )
    assert_grpc_rejected(exc_info, "NotFound")


def test_console_invalid_ticket_websocket(fulfillment_address: str) -> None:
    """Connecting with a garbage ticket should fail the handshake."""
    url: str = _ws_url(fulfillment_address)
    with pytest.raises(websocket.WebSocketException):
        _ws_connect(url, "not-a-valid-ticket", timeout=10)
