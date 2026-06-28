from __future__ import annotations

import base64
import contextlib
import json
import logging
import queue
import ssl
import subprocess
import threading
import time
from collections.abc import Callable, Iterator
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

logger = logging.getLogger(__name__)

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


# ---------------------------------------------------------------------------
# Transport helpers
# ---------------------------------------------------------------------------


def _ws_url(fulfillment_address: str) -> str:
    """Build the WebSocket console proxy URL from the fulfillment address."""
    host: str = fulfillment_address.rsplit(":", 1)[0]
    return f"wss://{host}{CONSOLE_WS_PATH}"


def _ws_connect(url: str, ticket: str, timeout: int = 30) -> websocket.WebSocket:
    """Open a WebSocket connection to the console proxy with the given ticket."""
    logger.info("WS connecting to %s (timeout=%ds)", url, timeout)
    ws = websocket.create_connection(
        url,
        header={"Authorization": f"Bearer {ticket}"},
        sslopt={"cert_reqs": ssl.CERT_NONE},
        subprotocols=["binary"],
        timeout=timeout,
    )
    logger.info("WS connected")
    return ws


def _ws_recv(ws: websocket.WebSocket, timeout: float) -> str | None:
    """Read one WebSocket message, returning decoded text or None on timeout."""
    ws.settimeout(timeout)
    try:
        data = ws.recv()
        if isinstance(data, bytes):
            return data.decode(errors="replace")
        return data if data else None
    except websocket.WebSocketTimeoutException:
        return None


def _ws_try_connect(url: str, ticket: str) -> bool:
    """Try to open a WebSocket connection. Returns True if successful."""
    try:
        ws = _ws_connect(url, ticket, timeout=10)
        ws.close()
        return True
    except Exception as exc:
        logger.info("WS try-connect failed: %s: %s", type(exc).__name__, exc)
        return False


def _grpc_popen(address: str, ticket: str) -> subprocess.Popen:
    """Start a gRPC console stream subprocess. Caller must manage stdin/lifecycle."""
    return subprocess.Popen(
        ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {ticket}", "-d", "@", address, CONSOLE_GRPC_SERVICE],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


class _GrpcSession:
    """Interactive gRPC console session via grpcurl subprocess."""

    def __init__(self, address: str, ticket: str) -> None:
        self._proc = _grpc_popen(address, ticket)
        self._output_q: queue.Queue[str | None] = queue.Queue()
        self._connected = threading.Event()
        self._reader = threading.Thread(target=self._read_stdout, daemon=True)
        self._reader.start()

    def _read_stdout(self) -> None:
        assert self._proc.stdout is not None
        json_buf = ""
        for raw_line in self._proc.stdout:
            line = raw_line.decode()
            json_buf += line
            if line.strip() == "}":
                try:
                    obj = json.loads(json_buf)
                    json_buf = ""
                    if "output" in obj and "data" in obj["output"]:
                        self._output_q.put(base64.b64decode(obj["output"]["data"]).decode(errors="replace"))
                    elif "status" in obj:
                        msg = obj["status"].get("message", "")
                        logger.info("gRPC status: %s", msg)
                        if msg == "Connected":
                            self._connected.set()
                except json.JSONDecodeError:
                    pass  # incomplete object, keep accumulating lines
        self._output_q.put(None)

    def send(self, data: bytes) -> None:
        assert self._proc.stdin is not None
        encoded = base64.b64encode(data).decode()
        msg = json.dumps({"input": {"data": encoded}}) + "\n"
        self._proc.stdin.write(msg.encode())
        self._proc.stdin.flush()

    def recv(self, timeout: float) -> str | None:
        """Read next decoded console output chunk. Returns None on timeout/EOF."""
        try:
            return self._output_q.get(timeout=timeout)
        except queue.Empty:
            return None

    def wait_connected(self, timeout: float = 10.0) -> bool:
        """Block until the gRPC session reports Connected. Returns False on timeout."""
        return self._connected.wait(timeout=timeout)

    @property
    def alive(self) -> bool:
        return self._proc.poll() is None

    def close(self) -> None:
        with contextlib.suppress(OSError):
            if self._proc.stdin and not self._proc.stdin.closed:
                self._proc.stdin.close()
        self._proc.stdin = None  # prevent communicate() from flushing closed stdin
        with contextlib.suppress(OSError):
            self._proc.kill()
        self._proc.communicate()


def _grpc_stream(address: str, ticket: str) -> tuple[str, str, int]:
    """Open a gRPC console stream via grpcurl, close stdin, and collect output.

    Returns (stdout, stderr, returncode).
    """
    logger.info("gRPC stream opening to %s", address)
    proc = _grpc_popen(address, ticket)
    try:
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
    stdout_str, stderr_str = stdout.decode(), stderr.decode()
    logger.info(
        "gRPC stream result: rc=%d, stdout=%d bytes, stderr=%d bytes", proc.returncode, len(stdout_str), len(stderr_str)
    )
    if stdout_str:
        logger.info("gRPC stdout: %s%s", stdout_str[:1024], "..." if len(stdout_str) > 1024 else "")
    if stderr_str:
        logger.info("gRPC stderr: %s%s", stderr_str[:1024], "..." if len(stderr_str) > 1024 else "")
    return stdout_str, stderr_str, proc.returncode


def _create_ticket(grpc: GRPCClient, vm_uuid: str, *, client_id: str = "") -> dict[str, Any]:
    """Create a serial console session and return the session object."""
    logger.info("Creating console ticket for VM %s", vm_uuid)
    result = grpc.create_console_session(
        resource_type="CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE",
        resource_id=vm_uuid,
        console_type="CONSOLE_TYPE_SERIAL",
        client_id=client_id,
    )
    logger.info("Ticket created, expires at %s", result.get("expiresAt", "unknown"))
    return result


# ---------------------------------------------------------------------------
# Shared interactive console helper
# ---------------------------------------------------------------------------


def _wait_for_login_prompt(
    *,
    send: Callable[[bytes], None],
    recv: Callable[[float], str | None],
    timeout: float = 300.0,
    enter_interval: float = 15.0,
) -> str:
    """Send enter periodically and wait for 'login' prompt in console output.

    Returns accumulated console output. Raises AssertionError on timeout.
    """
    accumulated = ""
    deadline = time.monotonic() + timeout
    next_enter = time.monotonic()

    while time.monotonic() < deadline:
        if time.monotonic() >= next_enter:
            send(b"\n")
            logger.info("Sent enter to console")
            next_enter = time.monotonic() + enter_interval

        remaining = deadline - time.monotonic()
        chunk = recv(min(remaining, 5.0))

        if chunk:
            accumulated += chunk
            truncated = chunk[:1024]
            logger.info("Received %d bytes: %s%s", len(chunk), truncated, "..." if len(chunk) > 1024 else "")
            if "login" in accumulated.lower():
                logger.info("Found login prompt (%d bytes total)", len(accumulated))
                return accumulated

    raise AssertionError(
        f"Console did not show login prompt within {timeout:.0f}s. Received {len(accumulated)} bytes total."
    )


# ---------------------------------------------------------------------------
# Positive tests, WebSocket transport
# ---------------------------------------------------------------------------


def test_console_serial_websocket(console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str) -> None:
    """Connect to the serial console via WebSocket and verify interactive login prompt."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    url: str = _ws_url(fulfillment_address)
    ws = _ws_connect(url, session["ticket"])
    try:
        _wait_for_login_prompt(send=ws.send_binary, recv=lambda t, _ws=ws: _ws_recv(_ws, t))
    finally:
        with contextlib.suppress(Exception):
            ws.close()


# ---------------------------------------------------------------------------
# Positive tests, gRPC stream transport
# ---------------------------------------------------------------------------


def test_console_serial_grpc_stream(console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str) -> None:
    """Connect to the serial console via gRPC bidi stream and verify interactive login prompt."""
    session: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    gs = _GrpcSession(fulfillment_address, session["ticket"])
    try:
        _wait_for_login_prompt(send=gs.send, recv=gs.recv)
    finally:
        gs.close()


# ---------------------------------------------------------------------------
# Ticket reuse, single-use JTI enforcement
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
        logger.info("Ticket reuse attempt %d: %s", i + 1, "success" if success else "rejected")

    assert results[0] is True, "First use of ticket must succeed"
    assert all(r is False for r in results[1:]), f"Subsequent uses must fail (JTI single-use), got: {results}"


# ---------------------------------------------------------------------------
# Concurrent session, only one session per resource
# ---------------------------------------------------------------------------


def test_console_concurrent_session_rejected(
    console_vm: dict[str, str], grpc: GRPCClient, fulfillment_address: str
) -> None:
    """A second connection to the same resource must be rejected while one is active."""
    session1: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])
    session2: dict[str, Any] = _create_ticket(grpc, console_vm["uuid"])

    gs1 = _GrpcSession(fulfillment_address, session1["ticket"])
    try:
        assert gs1.wait_connected(), "First session failed to connect"
        assert gs1.alive, "First session should still be running"

        stdout, stderr, rc = _grpc_stream(fulfillment_address, session2["ticket"])
        combined: str = stdout + stderr
        logger.info("Concurrent session2 rc=%d", rc)
        assert rc != 0, f"Second connection should fail, got: {combined!r}"
        assert "FailedPrecondition" in combined or "session already active" in combined, (
            f"Expected FailedPrecondition or 'session already active', got: {combined!r}"
        )

        assert gs1.alive, "First session died while second was rejected"
    finally:
        gs1.close()


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

    logger.info("Ticket expires at %s, waiting %.0fs past expiry", expires_at_str, wait_seconds)
    time.sleep(wait_seconds)

    url: str = _ws_url(fulfillment_address)
    success: bool = _ws_try_connect(url, ticket)
    logger.info("Expired ticket connect result: %s", success)
    assert success is False, "Expired ticket must be rejected"


# ---------------------------------------------------------------------------
# Negative tests, no running VM required
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
