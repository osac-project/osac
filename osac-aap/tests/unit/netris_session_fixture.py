"""A session-enforcing Netris IPAM provider for the ExternalIP transport tests.

Serves the subnet list, create and delete endpoints on a loopback port and
requires the exact per-run ``connect.sid`` cookie on every request. It records
only the method and path of accepted requests, never bodies or cookies.
Rejected requests change nothing and are not recorded.
"""

import hmac
import json
import secrets
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class NetrisFixtureHandler(BaseHTTPRequestHandler):
    """Serve the Netris IPAM transport."""

    def _respond(self, status, data=None):
        body = json.dumps(data if data is not None else {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _record(self, method, path=None):
        with self.server.lock:
            self.server.calls.append({"method": method, "path": path or self.path.split("?")[0]})

    def _read_body(self):
        length = int(self.headers.get("Content-Length", 0))
        try:
            body = json.loads(self.rfile.read(length)) if length else {}
        except (json.JSONDecodeError, ValueError):
            return None
        return body if isinstance(body, dict) else {}

    def _authorized(self):
        if hmac.compare_digest(
            self.headers.get("Cookie", "").encode("utf-8"),
            f"connect.sid={self.server.session_cookie}".encode("utf-8"),
        ):
            return True
        self._respond(401, {"message": "authentication required"})
        return False

    def do_GET(self):
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if path != "/api/v2/ipam/subnets":
            self._respond(404, {"message": "unexpected GET %s" % path})
            return
        self._record("GET")
        with self.server.lock:
            data = [
                {
                    "id": s["id"], "name": s["name"], "prefix": s["prefix"], "purpose": s["purpose"],
                    "sites": [{"id": s["site_id"]}], "tenant": {"id": s["tenant_id"]},
                }
                for s in self.server.subnets
            ]
        self._respond(200, {"data": data})

    def do_POST(self):
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if path not in ("/api/v2/ipam/subnet", "/api/v2/ipam/subnets"):
            self._respond(404, {"message": "unexpected POST %s" % path})
            return
        self._record("POST")
        body = self._read_body()
        if body is None:
            self._respond(400, {"message": "invalid subnet body"})
            return
        record = self.server.commit_subnet(body)
        if record is None:
            self._respond(400, {"message": "subnet already exists"})
            return
        self._respond(201, {"data": {"id": record["id"]}})

    def do_DELETE(self):
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if not path.startswith("/api/v2/ipam/subnet/"):
            self._respond(404, {"message": "unexpected DELETE %s" % path})
            return
        self._record("DELETE", path)
        subnet_id = path.rsplit("/", 1)[-1]
        with self.server.lock:
            self.server.subnets = [s for s in self.server.subnets if str(s.get("id")) != subnet_id]
        self._respond(204, {"message": "provider delete result"})

    def log_message(self, format, *args):
        """Keep request details and test-only credentials out of logs."""


class NetrisFixtureServer(ThreadingHTTPServer):
    """Hold the provider store, the session and the request log."""

    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), NetrisFixtureHandler)
        self.session_cookie = secrets.token_hex(16)
        self.lock = threading.Lock()
        self.subnets = []
        self.calls = []
        self.next_subnet_id = 1

    def commit_subnet(self, body):
        """Commit one create, or return None on a duplicate name or /32 in the same site and tenant."""
        sites = body.get("sites") or []
        site_id = sites[0].get("id") if sites and isinstance(sites[0], dict) else body.get("site_id")
        tenant = body.get("tenant")
        tenant_id = tenant.get("id") if isinstance(tenant, dict) else body.get("tenant_id")
        with self.lock:
            for subnet in self.subnets:
                if subnet.get("name") == body.get("name"):
                    return None
                if (
                    subnet.get("prefix") == body.get("prefix")
                    and subnet.get("site_id") == site_id
                    and subnet.get("tenant_id") == tenant_id
                ):
                    return None
            record = {
                "id": self.next_subnet_id, "name": body.get("name"), "prefix": body.get("prefix"),
                "purpose": body.get("purpose"), "site_id": site_id, "tenant_id": tenant_id,
            }
            self.next_subnet_id += 1
            self.subnets.append(record)
            return record

    def handle_error(self, request, client_address):
        """Keep dropped-connection noise out of the test output."""
