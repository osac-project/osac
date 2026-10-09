from __future__ import annotations

import contextlib
import ipaddress
import logging
import re
import ssl
import time
from collections.abc import Callable

import websocket

from tests.e2e.core.grpc_client import GRPCClient

logger = logging.getLogger(__name__)

CONSOLE_WS_PATH = "/api/fulfillment/v1/console_sessions/connect"
CONSOLE_USER = "fedora"

_LOGIN_TIMEOUT_S = 300.0
_AUTH_TIMEOUT_S = 60.0
_PING_TIMEOUT_S = 40.0
_ENTER_INTERVAL_S = 15.0


def user_data(*, password: str, username: str = CONSOLE_USER) -> str:
    """Cloud-init userdata that sets a serial-console password for guest ping."""
    return (
        "#cloud-config\n"
        "ssh_pwauth: true\n"
        "chpasswd:\n"
        "  expire: false\n"
        "  users:\n"
        f"    - name: {username}\n"
        f"      password: {password}\n"
        "      type: text\n"
    )


def ping(
    *, grpc: GRPCClient, fulfillment_address: str, vm_id: str, dest_ip: str, password: str, username: str = CONSOLE_USER
) -> bool:
    """Log in on the VM serial console and run ``ping -c 3 -W 2`` to dest_ip.

    Opens a new console ticket for each call and closes the session afterwards
    (tickets are single-use; a second concurrent session is rejected).
    """
    dest_ip = _canonical_dest_ip(dest_ip)
    session = grpc.create_console_session(
        resource_type="CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE", resource_id=vm_id, console_type="CONSOLE_TYPE_SERIAL"
    )
    ticket = session["ticket"]
    url = _ws_url(fulfillment_address)
    ws = _ws_connect(url, ticket)

    def recv(timeout: float, _ws: websocket.WebSocket = ws) -> str | None:
        """Read one serial-console WebSocket message, or None on timeout."""
        return _ws_recv(_ws, timeout)

    send = ws.send_binary
    try:
        if _wait_for_login_prompt(send=send, recv=recv, username=username) == "login":
            _authenticate(send=send, recv=recv, username=username, password=password)
        return _run_ping(send=send, recv=recv, dest_ip=dest_ip)
    finally:
        with contextlib.suppress(Exception):
            send(b"logout\n")
            _collect_until(recv=recv, timeout=5.0, done=_has_login_prompt)
        with contextlib.suppress(Exception):
            ws.close()
            ws.shutdown()


def _ws_url(fulfillment_address: str) -> str:
    """Build the WebSocket console-proxy URL from a host:port fulfillment address."""
    host: str = fulfillment_address.rsplit(":", 1)[0]
    return f"wss://{host}{CONSOLE_WS_PATH}"


def _ws_connect(url: str, ticket: str, timeout: int = 30) -> websocket.WebSocket:
    """Open a binary WebSocket to the console proxy using the session ticket."""
    return websocket.create_connection(
        url,
        header={"Authorization": f"Bearer {ticket}"},
        sslopt={"cert_reqs": ssl.CERT_NONE},
        subprotocols=["binary"],
        timeout=timeout,
    )


def _ws_recv(ws: websocket.WebSocket, timeout: float) -> str | None:
    """Return decoded console bytes, or None if the read times out."""
    ws.settimeout(timeout)
    try:
        data = ws.recv()
        if isinstance(data, bytes):
            return data.decode(errors="replace")
        return data if data else None
    except websocket.WebSocketTimeoutException:
        return None


def _wait_for_login_prompt(
    *,
    send: Callable[[bytes], None],
    recv: Callable[[float], str | None],
    username: str = CONSOLE_USER,
    timeout: float = _LOGIN_TIMEOUT_S,
    enter_interval: float = _ENTER_INTERVAL_S,
) -> str:
    """Return ``login`` when a login prompt appears, or ``shell`` if already logged in."""
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
            logger.info("Received %d bytes waiting for login prompt", len(chunk))
            if _has_login_prompt(accumulated):
                return "login"
            if _logged_in(accumulated, username):
                return "shell"
    raise AssertionError(
        f"Console did not show login prompt within {timeout:.0f}s. Received {len(accumulated)} bytes total."
    )


def _has_login_prompt(text: str) -> bool:
    """Return True when accumulated console text ends with a login prompt."""
    normalized = text.replace("\r", "")
    return bool(re.search(r"(?im)^[^\n]*login:\s*$", normalized))


def _authenticate(
    *,
    send: Callable[[bytes], None],
    recv: Callable[[float], str | None],
    username: str,
    password: str,
    timeout: float = _AUTH_TIMEOUT_S,
) -> None:
    """Submit guest credentials at the serial login and password prompts."""
    send(f"{username}\n".encode())
    accumulated = _collect_until(recv=recv, timeout=timeout, done=lambda text: "password" in text.lower())
    if "password" not in accumulated.lower():
        raise AssertionError(f"Console did not prompt for password ({len(accumulated)} bytes, password_prompt=false)")
    send(f"{password}\n".encode())
    accumulated += _collect_until(recv=recv, timeout=timeout, done=lambda text: _logged_in(text, username))
    if not _logged_in(accumulated, username):
        login_incorrect = "login incorrect" in accumulated.lower()
        raise AssertionError(
            f"Console login failed for {username} ({len(accumulated)} bytes, login_incorrect={login_incorrect})"
        )


def _logged_in(text: str, username: str) -> bool:
    """Return True when console output looks like a logged-in shell prompt."""
    lower = text.lower()
    if "login incorrect" in lower:
        return False
    return "$" in text or "#" in text or f"{username}@" in lower


def _canonical_dest_ip(dest_ip: str) -> str:
    """Return a canonical unscoped IP string, or raise ValueError if dest_ip is unsafe."""
    try:
        addr = ipaddress.ip_address(dest_ip.strip())
    except ValueError as exc:
        raise ValueError(f"invalid dest_ip {dest_ip!r}") from exc
    if isinstance(addr, ipaddress.IPv6Address) and addr.scope_id:
        raise ValueError(f"invalid dest_ip {dest_ip!r}: IPv6 scope IDs are not allowed")
    return format(addr)


def _run_ping(
    *,
    send: Callable[[bytes], None],
    recv: Callable[[float], str | None],
    dest_ip: str,
    timeout: float = _PING_TIMEOUT_S,
) -> bool:
    """Run guest ping to dest_ip and return True when PING_RC is 0."""
    dest_ip = _canonical_dest_ip(dest_ip)
    send(f"ping -c 3 -W 2 {dest_ip}; echo PING_RC:$?\n".encode())
    accumulated = _collect_until(recv=recv, timeout=timeout, done=lambda text: bool(re.search(r"PING_RC:\d+", text)))
    match = re.search(r"PING_RC:(\d+)", accumulated)
    if not match:
        logger.info("Guest ping did not report PING_RC (%d bytes)", len(accumulated))
        raise AssertionError(f"Guest ping did not report PING_RC ({len(accumulated)} bytes)")
    ok = match.group(1) == "0"
    logger.info("Guest ping PING_RC=%s success=%s", match.group(1), ok)
    return ok


def _collect_until(*, recv: Callable[[float], str | None], timeout: float, done: Callable[[str], bool]) -> str:
    """Read console output until done(text) is true or timeout elapses."""
    accumulated = ""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        remaining = deadline - time.monotonic()
        chunk = recv(min(remaining, 5.0))
        if chunk:
            accumulated += chunk
            if done(accumulated):
                return accumulated
    return accumulated
