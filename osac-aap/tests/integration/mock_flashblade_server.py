"""Mock Pure Storage FlashBlade REST API server for storage integration tests.

Simulates the subset of the FlashBlade REST API that the vendored
``purestorage.flashblade`` modules exercise during tenant setup: realms,
servers, NFS export policies and file systems.

Unlike the VAST mock, the client here is the ``py-pure-client`` SDK rather than
plain ``uri`` calls, so three things are mandatory:

* TLS -- the SDK always builds an ``https://`` target.
* ``GET /api/api_version`` -- version negotiation happens before anything else.
  It must advertise a version the installed SDK also supports.
* ``POST /api/login`` -- must return an ``x-auth-token`` response header, which
  the SDK then replays on every subsequent call.

Any ``GET`` under ``/api/<version>/`` that names no known resource returns an
empty collection rather than 404.  ``purefb_info`` gathers far more than the
test cares about (object-store policies, SMB policies, fleets, ...), and an
error on any one of them fails the whole module.

Usage:
    python3 mock_flashblade_server.py <port> --tls --cert <path> --key <path>
"""

import argparse
import json
import re
import ssl
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn
from urllib.parse import parse_qs, urlparse

# Advertised to the SDK during negotiation. Must be >= the highest version any
# feature gate in the modules checks: realms need 2.19, servers 2.21, fleet
# context 2.17.
API_VERSIONS = [
    "2.0", "2.1", "2.2", "2.3", "2.4", "2.5", "2.6", "2.7", "2.8", "2.9",
    "2.10", "2.11", "2.12", "2.13", "2.14", "2.15", "2.16", "2.17", "2.18",
    "2.19", "2.20", "2.21", "2.22", "2.23", "2.24", "2.25", "2.26",
]

CALL_LOG = []
_INJECTED_FAILURES = []
_LOCK = threading.Lock()

_RESOURCES = ("realms", "servers", "nfs-export-policies", "file-systems", "policies")
_STORE = {r: {} for r in _RESOURCES}

# Only the fields the modules actually read. The SDK tolerates missing
# attributes by leaving them None, so this stays deliberately minimal.
_DEFAULTS = {
    "realms": {"destroyed": False},
    "servers": {"created": 1700000000000, "dns": [], "directory_services": []},
    "nfs-export-policies": {"is_local": True, "enabled": True, "rules": []},
    "policies": {"enabled": True, "policy_type": "snapshot", "rules": []},
    "file-systems": {
        "destroyed": False,
        "provisioned": 0,
        "writable": True,
        "requested_promotion_state": "promoted",
        "promotion_status": "promoted",
        "hard_limit_enabled": False,
        "fast_remove_directory_enabled": False,
        "snapshot_directory_enabled": False,
        "nfs": {
            "v3_enabled": False,
            "v4_1_enabled": False,
            "rules": "",
            "export_policy": {"name": None},
        },
        "smb": {"enabled": False, "client_policy": {"name": None}},
        "http": {"enabled": False},
        "multi_protocol": {"access_control_style": "shared", "safeguard_acls": True},
    },
}


def _log(entry):
    with _LOCK:
        CALL_LOG.append(entry)


def _strip_sensitive(headers):
    """Drop anything token-shaped so the call log can be asserted on safely."""
    return {
        k: ("<redacted>" if k.lower() in ("api-token", "x-auth-token", "authorization") else v)
        for k, v in headers.items()
    }


def _merge(base, overlay):
    """Recursive dict merge, so a PATCH of nfs.export_policy keeps nfs.v4_1_enabled."""
    out = dict(base)
    for key, value in overlay.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = _merge(out[key], value)
        else:
            out[key] = value
    return out


def _requested_names(query):
    """Names addressed by a request, from either ?names= or ?filter=name='x'."""
    params = parse_qs(query)
    names = []
    for raw in params.get("names", []):
        names.extend(n.strip().strip("'\"") for n in raw.split(",") if n.strip())
    for raw in params.get("filter", []):
        names.extend(re.findall(r"name=['\"]([^'\"]+)['\"]", raw))
    return names


class MockFlashBladeHandler(BaseHTTPRequestHandler):
    """Handler for the mock FlashBlade REST API."""

    protocol_version = "HTTP/1.1"

    def log_message(self, format, *args):
        pass

    # ── plumbing ──

    def _respond(self, status, data, headers=None):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def _collection(self, items):
        self._respond(200, {"total_item_count": len(items), "items": items,
                            "continuation_token": None})

    def _read_body(self):
        length = int(self.headers.get("Content-Length", 0))
        if not length:
            return {}
        try:
            return json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, ValueError):
            return None

    def _resource(self):
        """Resource name from /api/<version>/<resource>, or None."""
        parts = [p for p in urlparse(self.path).path.strip("/").split("/") if p]
        if len(parts) >= 3 and parts[0] == "api":
            return parts[2]
        return None

    def _check_injected_failure(self, resource, method):
        """Pop and return the first matching injected failure, if any."""
        with _LOCK:
            for i, failure in enumerate(_INJECTED_FAILURES):
                if failure["resource"] == resource and failure["method"] == method:
                    _INJECTED_FAILURES.pop(i)
                    return failure.get("status", 500), failure.get(
                        "body", {"errors": [{"message": "injected failure"}]}
                    )
        return None

    def _control(self, path):
        """Handle the test-harness control endpoints. Returns True if handled."""
        if path == "/_calls":
            with _LOCK:
                snapshot = list(CALL_LOG)
            self._respond(200, snapshot)
            return True
        if path == "/_reset":
            with _LOCK:
                CALL_LOG.clear()
                _INJECTED_FAILURES.clear()
                for resource in _STORE:
                    _STORE[resource].clear()
            self._respond(200, {"status": "reset"})
            return True
        return False

    # ── verbs ──

    def do_GET(self):
        path = urlparse(self.path).path
        if self._control(path):
            return

        # Version negotiation happens before authentication and is not part of
        # the versioned API surface.
        if path.rstrip("/") == "/api/api_version":
            self._respond(200, {"versions": API_VERSIONS})
            return

        _log({"method": "GET", "path": self.path,
              "headers": _strip_sensitive(dict(self.headers))})

        resource = self._resource()
        if resource == "versions":
            self._collection(API_VERSIONS)
            return

        if resource not in _RESOURCES:
            # purefb_info gathers many subsets this mock does not model; an
            # empty collection keeps the module on its success path.
            self._collection([])
            return

        failure = self._check_injected_failure(resource, "GET")
        if failure:
            self._respond(failure[0], failure[1])
            return

        names = _requested_names(urlparse(self.path).query)
        with _LOCK:
            items = list(_STORE[resource].values())
        if names:
            items = [i for i in items if i["name"] in names]

        params = parse_qs(urlparse(self.path).query)
        if params.get("destroyed", [None])[0] == "false":
            items = [i for i in items if not i.get("destroyed")]
        self._collection(items)

    def do_POST(self):
        path = urlparse(self.path).path
        if self._control(path):
            return

        body = self._read_body()

        # The SDK replays the x-auth-token from this response on every later
        # call; without the header it raises before reaching any endpoint.
        if path.rstrip("/") == "/api/login":
            _log({"method": "POST", "path": path,
                  "headers": _strip_sensitive(dict(self.headers))})
            self._respond(200, {"username": "mock-realm-admin"},
                          {"x-auth-token": "mock-session-token"})
            return

        if path == "/_inject_failure":
            if not body or "resource" not in body or "method" not in body:
                self._respond(400, {"error": "resource and method required"})
                return
            with _LOCK:
                _INJECTED_FAILURES.append(body)
            self._respond(200, {"status": "failure injected",
                                "pending": len(_INJECTED_FAILURES)})
            return

        if path == "/_seed":
            # Pre-create Realm-owned resources that the tenant role only ever
            # reads: the Realm itself, its NFS server and its export policy.
            if not body or "resource" not in body or "name" not in body:
                self._respond(400, {"error": "resource and name required"})
                return
            resource = body["resource"]
            if resource not in _RESOURCES:
                self._respond(400, {"error": f"unknown resource {resource}"})
                return
            with _LOCK:
                _STORE[resource][body["name"]] = _merge(
                    {"name": body["name"], "id": body["name"], **_DEFAULTS[resource]},
                    body.get("attributes", {}),
                )
            self._respond(200, {"status": "seeded"})
            return

        _log({"method": "POST", "path": self.path,
              "headers": _strip_sensitive(dict(self.headers)), "body": body})

        resource = self._resource()
        if resource not in _RESOURCES:
            self._collection([])
            return

        failure = self._check_injected_failure(resource, "POST")
        if failure:
            self._respond(failure[0], failure[1])
            return

        names = _requested_names(urlparse(self.path).query)
        if not names:
            self._collection([])
            return

        created = []
        with _LOCK:
            for name in names:
                if name in _STORE[resource]:
                    self._respond(400, {"errors": [
                        {"message": f"{resource} {name} already exists"}]})
                    return
                item = _merge({"name": name, "id": name, **_DEFAULTS[resource]},
                              body or {})
                _STORE[resource][name] = item
                created.append(item)
        self._collection(created)

    def do_PATCH(self):
        body = self._read_body()
        _log({"method": "PATCH", "path": self.path,
              "headers": _strip_sensitive(dict(self.headers)), "body": body})

        resource = self._resource()
        if resource not in _RESOURCES:
            self._collection([])
            return

        failure = self._check_injected_failure(resource, "PATCH")
        if failure:
            self._respond(failure[0], failure[1])
            return

        names = _requested_names(urlparse(self.path).query)
        updated = []
        with _LOCK:
            for name in names:
                if name not in _STORE[resource]:
                    self._respond(400, {"errors": [
                        {"message": f"{resource} {name} does not exist"}]})
                    return
                _STORE[resource][name] = _merge(_STORE[resource][name], body or {})
                updated.append(_STORE[resource][name])
        self._collection(updated)

    def do_DELETE(self):
        _log({"method": "DELETE", "path": self.path,
              "headers": _strip_sensitive(dict(self.headers))})

        resource = self._resource()
        if resource not in _RESOURCES:
            self._collection([])
            return

        failure = self._check_injected_failure(resource, "DELETE")
        if failure:
            self._respond(failure[0], failure[1])
            return

        names = _requested_names(urlparse(self.path).query)
        with _LOCK:
            for name in names:
                _STORE[resource].pop(name, None)
        self._collection([])


class ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
    daemon_threads = True


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("port", type=int)
    parser.add_argument("--tls", action="store_true")
    parser.add_argument("--cert", default=None)
    parser.add_argument("--key", default=None)
    args = parser.parse_args()

    ThreadingHTTPServer.allow_reuse_address = True
    server = ThreadingHTTPServer(("127.0.0.1", args.port), MockFlashBladeHandler)

    if args.tls:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(certfile=args.cert, keyfile=args.key)
        server.socket = ctx.wrap_socket(server.socket, server_side=True)

    print(f"Mock FlashBlade server running on port {args.port} (tls={args.tls})", flush=True)
    server.serve_forever()
