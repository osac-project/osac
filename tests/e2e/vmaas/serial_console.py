"""The tenant-facing WebSocket transport for ComputeInstance serial consoles."""

from __future__ import annotations

import ssl
import time
from collections.abc import Callable, Iterator

import websocket

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind


def wait_for_console_ready(grpc: GRPCClient, instance: str) -> None:
    # The console resolver also checks Fulfillment's state, which can lag the hub CR.
    poll_until(
        fn=lambda: grpc.get_compute_instance(ci_id=instance)["object"].get("status", {}).get("state"),
        until=lambda state: state == "COMPUTE_INSTANCE_STATE_RUNNING",
        description=f"ComputeInstance {instance} Running in Fulfillment for console access",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance,
    )


def open_serial_console(grpc: GRPCClient, address: str, instance: str) -> websocket.WebSocket:
    __tracebackhide__ = True
    session = grpc.create_console_session(
        resource_type="CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE", resource_id=instance, console_type="CONSOLE_TYPE_SERIAL"
    )
    return ws_connect(ws_url(address), session["ticket"])


def ws_url(fulfillment_address: str) -> str:
    host = fulfillment_address.rsplit(":", 1)[0]
    return f"wss://{host}/api/fulfillment/v1/console_sessions/connect"


def ws_connect(url: str, ticket: str, timeout: int = 30) -> websocket.WebSocket:
    __tracebackhide__ = True  # this frame contains a single-use console credential
    return websocket.create_connection(
        url,
        header={"Authorization": f"Bearer {ticket}"},
        sslopt={"cert_reqs": ssl.CERT_NONE},
        subprotocols=["binary"],
        timeout=timeout,
    )


def ws_recv(ws: websocket.WebSocket, timeout: float) -> str | None:
    """Return guest output, an empty string on close, or None on timeout."""
    ws.settimeout(timeout)
    try:
        data = ws.recv()
    except websocket.WebSocketTimeoutException:
        return None
    return data.decode(errors="replace") if isinstance(data, bytes) else data


def console_output(send: Callable[[bytes], None], recv: Callable[[float], str | None]) -> Iterator[str]:
    """Yield recent serial text across frame boundaries for up to five minutes.

    A blank Enter redisplays a prompt that predates the connection. Send it
    again on an idle console, never the text the caller is looking for.
    """
    deadline = time.monotonic() + 300
    tail = "\n"
    while (remaining := deadline - time.monotonic()) > 0:
        chunk = recv(min(5, remaining))
        if chunk == "":
            break
        if chunk is None:
            send(b"\r")
            continue
        output = tail + chunk.replace("\r", "")
        yield output
        tail = output[-8192:]
