"""Exercise authenticated Ansible HTTP transport against loopback providers."""

import contextlib
import hmac
import importlib.util
import json
import os
import secrets
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace

import pytest

from .test_netris_external_ip_module import (
    COLLECTIONS_PATH,
    MODULE_PATH,
    module_args,
    netris_eip,
)

FIXTURE_PATH = (
    Path(__file__).resolve().parents[1]
    / "integration/targets/netris_external_ip/files/verify_external_ip.py"
)
fixture_spec = importlib.util.spec_from_file_location("netris_fixture", FIXTURE_PATH)
netris_fixture = importlib.util.module_from_spec(fixture_spec)
fixture_spec.loader.exec_module(netris_fixture)


@contextlib.contextmanager
def running_server(server):
    """Serve loopback requests while guaranteeing thread and socket cleanup."""
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


@pytest.fixture
def provider():
    """Expose the shared session-enforcing provider on an ephemeral port."""
    with running_server(netris_fixture.NetrisFixtureServer()) as server:
        yield server


@pytest.fixture
def transport_module(tmp_path, monkeypatch):
    """Supply real fetch_url parameters without proxying loopback traffic."""
    monkeypatch.setenv("no_proxy", "127.0.0.1")
    monkeypatch.setenv("NO_PROXY", "127.0.0.1")
    return SimpleNamespace(params={"use_netrc": False}, tmpdir=str(tmp_path))


def client_for(server, module, session_cookie=None):
    """Build the real client using the fixture session unless overridden."""
    return netris_eip.NetrisSubnetClient(
        module,
        f"http://127.0.0.1:{server.server_port}",
        server.session_cookie if session_cookie is None else session_cookie,
    )


def test_authenticated_get_post_delete(provider, transport_module):
    """Require the initial session on each shared-client HTTP operation."""
    client = client_for(provider, transport_module)

    assert client.list_subnets() == []
    subnet_id = client.create_subnet("transport-reservation", "192.0.2.1/32", 1, 7)
    assert len(client.list_subnets()) == 1
    client.delete_subnet(subnet_id)
    assert client.list_subnets() == []
    assert [call["method"] for call in provider.calls] == [
        "GET", "POST", "GET", "DELETE", "GET"
    ]


@pytest.mark.parametrize("operation", netris_eip.OPERATIONS)
def test_reconcile_uses_authenticated_shared_client(provider, transport_module, operation):
    """Exercise allocation and release reconciliation through real HTTP."""
    request = {
        "reservation_name": "transport-reservation",
        "cidr": "192.0.2.0/29",
        "operation": operation,
    }
    if operation == "external_ip.release":
        provider.commit_subnet({
            "name": request["reservation_name"], "prefix": "192.0.2.1/32",
            "purpose": "nat", "site_id": 1, "tenant_id": 7,
        })
    result = netris_eip.reconcile(
        request, client_for(provider, transport_module), 1, 7, delay_seconds=0
    )

    assert result["changed"] is True
    expected = "192.0.2.1" if operation == "external_ip.allocate" else ""
    assert result["address"] == expected
    assert provider.calls[0]["method"] == "GET"


@pytest.mark.parametrize("method,path,body", [
    ("GET", "/api/v2/ipam/subnets", None),
    ("POST", "/api/v2/ipam/subnet", {}),
    ("DELETE", "/api/v2/ipam/subnet/1", None),
])
@pytest.mark.parametrize("cookie", [
    None, "connect.sid=invalid-test-session", "connect.sid=invalid-\u00e9",
], ids=["missing", "incorrect", "non-ascii"])
def test_fixture_rejects_missing_or_incorrect_cookie(
    provider, transport_module, method, path, body, cookie
):
    """Reject missing, incorrect, and non-ASCII sessions before mutation."""
    headers = {"Cookie": cookie} if cookie is not None else {}
    response, info = netris_eip.fetch_url(
        transport_module, f"http://127.0.0.1:{provider.server_port}{path}",
        method=method, headers=headers, data=json.dumps(body) if body is not None else None,
    )
    if response is not None:
        response.close()
    assert info["status"] == 401
    assert provider.calls == []
    assert provider.subnets == []


@pytest.mark.parametrize("operation", netris_eip.OPERATIONS)
@pytest.mark.parametrize("authenticated", [True, False])
def test_real_module_subprocess_uses_fixture_session(
    provider, transport_module, operation, authenticated
):
    """Verify child-module authentication and sanitized errors without leaks."""
    if operation == "external_ip.release":
        provider.commit_subnet({
            "name": (
                f"{netris_eip.RESERVATION_NAME_PREFIX}"
                f"{module_args()['resource']['metadata']['uid']}"
            ),
            "prefix": "10.20.0.1/32", "purpose": "nat", "site_id": 1, "tenant_id": 7,
        })
    job = SimpleNamespace(server=provider, fixture_url=f"http://127.0.0.1:{provider.server_port}")
    job_vars = netris_fixture.ExternalIPPlaybookTests._job_vars(
        job, module_args()["resource"], pool=False
    )
    args = module_args(
        operation=operation,
        controller_url=job_vars["netris_controller_url"],
        session_cookie=job_vars["netris_session_cookie"],
    )
    if operation == "external_ip.release":
        args["resource"]["metadata"]["deletionTimestamp"] = "2026-01-01T00:00:00Z"
    if not authenticated:
        args["session_cookie"] = secrets.token_hex(16)
    environment = os.environ.copy()
    environment["PYTHONPATH"] = str(COLLECTIONS_PATH)
    process = subprocess.run(
        [sys.executable, str(MODULE_PATH)],
        input=json.dumps({"ANSIBLE_MODULE_ARGS": args}),
        text=True, capture_output=True, env=environment, timeout=30,
    )
    assert process.returncode == (0 if authenticated else 1)
    result = json.loads(process.stdout)
    output = process.stdout + process.stderr
    cookie_leaked = any(value in output for value in (
        provider.session_cookie, args["session_cookie"]
    ))
    assert not cookie_leaked
    if authenticated:
        assert result["changed"] is True
        expected_method = "POST" if operation == "external_ip.allocate" else "DELETE"
        assert any(call["method"] == expected_method for call in provider.calls)
    else:
        assert result["failed"] is True
        assert result["msg"] == f"Netris ExternalIP {operation} failed (provider_error, HTTP 401)"
        assert provider.calls == []


class RedirectHandler(BaseHTTPRequestHandler):
    """Expose redirect targets without retaining or logging session values."""

    def log_message(self, *args):
        """Keep synthetic request details out of server logs."""
        pass

    def handle_request(self):
        """Record transport facts and redirect only authenticated requests."""
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length) if length else b""
        authenticated = hmac.compare_digest(
            self.headers.get("Cookie", "").encode("utf-8"),
            f"connect.sid={self.server.session_cookie}".encode("utf-8"),
        )
        self.server.observations.append({
            "method": self.command,
            "authenticated": authenticated,
            "cookie_present": self.headers.get("Cookie") is not None,
            "content_type": self.headers.get("Content-Type"),
            "body": body,
        })
        if self.path == "/start" and authenticated:
            self.send_response(self.server.redirect_status)
            self.send_header("Location", self.server.destination)
            if self.server.response_cookies in ("session", "both"):
                self.send_header(
                    "Set-Cookie", f"connect.sid={self.server.minted_session}; Path=/; HttpOnly"
                )
            if self.server.response_cookies in ("other", "both"):
                self.send_header("Set-Cookie", f"probe={self.server.minted_other}; Path=/")
            payload = b""
        else:
            self.send_response(200 if self.path == "/landing" else 401)
            payload = b"{}"
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    do_GET = handle_request
    do_POST = handle_request
    do_DELETE = handle_request


@pytest.mark.parametrize("cookie", [
    None, "connect.sid=invalid-test-session", "connect.sid=invalid-\u00e9",
], ids=["missing", "incorrect", "non-ascii"])
def test_redirect_handler_rejects_invalid_session(transport_module, cookie):
    """Return 401, not a redirect or server error, for invalid header text."""
    with running_server(ThreadingHTTPServer(("127.0.0.1", 0), RedirectHandler)) as server:
        server.session_cookie = secrets.token_hex(16)
        server.observations = []
        response, info = netris_eip.fetch_url(
            transport_module, f"http://127.0.0.1:{server.server_port}/start",
            headers={"Cookie": cookie} if cookie is not None else {},
        )
        if response is not None:
            response.close()
        assert info["status"] == 401
        assert len(server.observations) == 1
        assert not server.observations[0]["authenticated"]


@pytest.mark.parametrize("method,status,policy,followed", [
    ("GET", status, "urllib2", True) for status in (301, 302, 303, 307, 308)
] + [
    (method, status, policy, policy == "all")
    for method in ("POST", "DELETE")
    for status in (307, 308)
    for policy in ("urllib2", "all")
])
@pytest.mark.parametrize("cross_origin", [False, True])
@pytest.mark.parametrize("response_cookies", ["none", "session", "other", "both"])
def test_real_redirect_transport_never_forwards_cookie(
    transport_module, method, status, policy, followed, cross_origin, response_cookies
):
    """Exclude supplied and response cookies without changing redirect policy."""
    with contextlib.ExitStack() as stack:
        origin = stack.enter_context(running_server(ThreadingHTTPServer(
            ("127.0.0.1", 0), RedirectHandler
        )))
        target = origin
        if cross_origin:
            target = stack.enter_context(running_server(ThreadingHTTPServer(
                ("127.0.0.1", 0), RedirectHandler
            )))
        for server in {origin, target}:
            server.session_cookie = secrets.token_hex(16)
            server.observations = []
        origin.redirect_status = status
        origin.destination = f"http://127.0.0.1:{target.server_port}/landing"
        origin.response_cookies = response_cookies
        origin.minted_session = secrets.token_hex(16)
        origin.minted_other = secrets.token_hex(16)
        client = client_for(origin, transport_module)
        transport_module.params["follow_redirects"] = policy
        body = {"probe": True} if method == "POST" else None
        if followed:
            assert client._request(method, "/start", body) == {}
        else:
            with pytest.raises(netris_eip.NetrisExternalIPError) as raised:
                client._request(method, "/start", body)
            assert raised.value.http_status == status
        initial = origin.observations[0]
        assert initial["authenticated"]
        assert initial["content_type"] == ("application/json" if body is not None else None)
        if body is not None:
            assert json.loads(initial["body"]) == body
        observations = origin.observations + (target.observations if cross_origin else [])
        assert len(observations) == (2 if followed else 1)
        if followed:
            assert not observations[1]["cookie_present"]
            assert observations[1]["method"] == method
            assert observations[1]["body"] == initial["body"]
            assert observations[1]["content_type"] == initial["content_type"]
