"""Mock VAST VMS API server for storage provider integration tests.

Simulates VAST VMS REST endpoints for tenants, vippools, views,
viewpolicies, quotas, roles, managers, and apitokens.
Follows the mock_api_server.py pattern.

Auth: vast-ansible sends either ``Authorization: Api-Token <token>`` header,
``Authorization: Bearer <token>`` header, or HTTP Basic Auth on every
request.  ``POST /api/token/`` returns a mock bearer token.

Usage:
    python3 mock_vms_server.py <port> [--tls --cert <path> --key <path>]
"""

import argparse
import copy
import json
import ssl
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn
from urllib.parse import parse_qs, urlparse

CALL_LOG = []
_INJECTED_FAILURES = []

_NEXT_ID = {r: 1 for r in (
    "tenants", "vippools", "views", "viewpolicies", "quotas", "qospolicies",
    "users", "roles", "managers", "apitokens", "localproviders",
)}
_STORE = {r: {} for r in _NEXT_ID}
_LOCK = threading.Lock()

CANNED_DEFAULTS = {
    "tenants": {"name": "", "client_ip_ranges": [], "encryption": False, "local_provider_id": 1},
    "vippools": {"name": "", "ip_ranges": [], "tenant_id": 1},
    "views": {"name": "", "path": "/", "policy_id": 1, "tenant_id": 1},
    "viewpolicies": {"name": "", "flavor": "NFS", "protocols": ["NFS"], "tenant_id": 1},
    "quotas": {"name": "", "hard_limit": 0, "soft_limit": 0, "tenant_id": 1},
    "qospolicies": {"name": "", "tenant_id": 1, "mode": "STATIC"},
    "users": {"name": "", "local_provider_id": 1},
    "roles": {"name": "", "tenant_id": 1, "tenant_ids": [], "permissions_list": []},
    "managers": {"username": "", "user_type": "TENANT_ADMIN", "tenant_id": 1, "roles": []},
    "apitokens": {"owner": "", "name": "", "token": ""},
    "localproviders": {"name": "", "managed_by": [], "description": ""},
}

# Query parameters supported for filtering list responses.
# Each resource maps to a list of field names that can be used as query params.
_LIST_FILTER_FIELDS = {
    "tenants": ["name"],
    "vippools": ["name"],
    "views": ["path", "tenant_id"],
    "quotas": ["path", "tenant_id"],
    "roles": ["name", "tenant_id"],
    "managers": ["username"],
    "apitokens": ["owner"],
    "viewpolicies": ["name", "tenant_id"],
    "qospolicies": ["name", "tenant_id"],
    "localproviders": ["name"],
}

_RESOURCES = set(CANNED_DEFAULTS)


def _strip_sensitive(headers):
    """Return header dict with Authorization removed."""
    return {k: v for k, v in headers.items() if k.lower() != "authorization"}


def _log(entry):
    if "body" in entry and isinstance(entry["body"], dict):
        entry = dict(entry)
        body = dict(entry["body"])
        for field in ("password", "secret", "token"):
            body.pop(field, None)
        entry["body"] = body
    with _LOCK:
        CALL_LOG.append(entry)


class MockVmsHandler(BaseHTTPRequestHandler):
    def _check_injected_failure(self, method):
        """Check if an injected failure matches this request. Returns (status, body) or None."""
        resource, _ = self._parse_path()
        with _LOCK:
            for i, f in enumerate(_INJECTED_FAILURES):
                if f["resource"] == resource and f["method"] == method:
                    status = f.get("status", 500)
                    body = f.get("body", {"error": "injected failure"})
                    _INJECTED_FAILURES.pop(i)
                    return status, body
        return None

    def _parse_path(self):
        """Return (resource, resource_id) or (None, None) for non-resource paths."""
        path = self.path.split("?")[0].rstrip("/")
        parts = path.strip("/").split("/")
        if len(parts) >= 2 and parts[0] == "api":
            if len(parts) >= 3 and parts[1] not in _RESOURCES:
                parts = parts[2:]
            else:
                parts = parts[1:]
        if not parts or parts[0] not in _RESOURCES:
            return None, None
        resource = parts[0]
        resource_id = int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else None
        return resource, resource_id

    def _read_body(self):
        length = int(self.headers.get("Content-Length", 0))
        if not length:
            return {}
        try:
            return json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, ValueError):
            return None

    def _respond(self, status, data):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = self.path.split("?")[0]

        if path == "/_calls":
            with _LOCK:
                snapshot = list(CALL_LOG)
            self._respond(200, snapshot)
            return

        if path == "/_reset":
            with _LOCK:
                CALL_LOG.clear()
                for r in _STORE:
                    _STORE[r].clear()
                for r in _NEXT_ID:
                    _NEXT_ID[r] = 1
            self._respond(200, {"status": "reset"})
            return

        if path.rstrip("/") == "/api":
            _log({"method": "GET", "path": path, "headers": _strip_sensitive(dict(self.headers))})
            self._respond(200, {"status": "ok"})
            return

        if path.rstrip("/").endswith("/clusters"):
            _log({"method": "GET", "path": path, "headers": _strip_sensitive(dict(self.headers))})
            self._respond(200, [{"id": 1, "name": "mock-cluster", "sw_version": "5.4.0"}])
            return

        resource, resource_id = self._parse_path()
        _log({"method": "GET", "path": self.path, "headers": _strip_sensitive(dict(self.headers))})

        if resource is None:
            self._respond(404, {"error": "not found"})
            return

        with _LOCK:
            if resource_id is not None:
                obj = _STORE[resource].get(resource_id)
            else:
                obj = list(_STORE[resource].values())

                # Apply query-parameter filtering (supports multiple fields)
                filter_fields = _LIST_FILTER_FIELDS.get(resource, [])
                if filter_fields:
                    qs = parse_qs(urlparse(self.path).query)
                    for field in filter_fields:
                        filter_value = qs.get(field, [None])[0]
                        if filter_value is not None:
                            obj = [o for o in obj if str(o.get(field, "")) == filter_value]

        if resource_id is not None:
            if obj is None:
                self._respond(404, {"error": f"{resource} {resource_id} not found"})
            else:
                self._respond(200, obj)
        else:
            self._respond(200, obj)

    def do_POST(self):
        path = self.path.split("?")[0]

        if path == "/_reset":
            with _LOCK:
                CALL_LOG.clear()
                _INJECTED_FAILURES.clear()
                for r in _STORE:
                    _STORE[r].clear()
                for r in _NEXT_ID:
                    _NEXT_ID[r] = 1
            self._respond(200, {"status": "reset"})
            return

        if path == "/_inject_failure":
            body = self._read_body()
            if not body or "resource" not in body or "method" not in body:
                self._respond(400, {"error": "resource and method required"})
                return
            with _LOCK:
                _INJECTED_FAILURES.append(body)
            self._respond(200, {"status": "failure injected", "pending": len(_INJECTED_FAILURES)})
            return

        body = self._read_body()
        if body is None:
            self._respond(400, {"error": "invalid JSON body"})
            return
        _log({"method": "POST", "path": path, "headers": _strip_sensitive(dict(self.headers)), "body": body})

        # Special endpoint: /api/token/ — authentication (no resource CRUD)
        if path.rstrip("/") == "/api/token":
            self._respond(200, {"access": "mock-bearer-token-XXXXXX"})
            return

        failure = self._check_injected_failure("POST")
        if failure:
            self._respond(failure[0], failure[1])
            return

        resource, _ = self._parse_path()
        if resource is None:
            self._respond(404, {"error": "not found"})
            return

        # QoS STATIC mode requires at least one limit field
        if resource == "qospolicies":
            mode = body.get("mode", "")
            if mode == "STATIC" and not body.get("static_limits") and not body.get("static_total_limits"):
                self._respond(400, {
                    "error": "At least one Qos Static or Static Total Limit should be set"
                })
                return

        with _LOCK:
            # Cross-tenant view policy validation
            if resource == "views":
                policy_id = body.get("policy_id")
                view_tenant = body.get("tenant_id", 1)
                if policy_id is not None:
                    policy = _STORE["viewpolicies"].get(int(policy_id))
                    if policy and int(policy.get("tenant_id", 1)) != int(view_tenant):
                        self._respond(400, {
                            "error": "You cannot attach a view policy that belongs to a different tenant."
                        })
                        return

            obj_id = _NEXT_ID[resource]
            _NEXT_ID[resource] += 1
            obj = {**copy.deepcopy(CANNED_DEFAULTS[resource]), **body, "id": obj_id}

            # Resource-specific post-processing
            if resource == "managers":
                obj.pop("password", None)
            elif resource == "apitokens":
                obj["token"] = f"mock-api-token-{obj_id}"
            elif resource == "tenants":
                lp_id = _NEXT_ID["localproviders"]
                _NEXT_ID["localproviders"] += 1
                lp = {
                    "id": lp_id,
                    "name": f"provider-{obj.get('name', '')}",
                    "tenant_id": obj_id,
                    "managed_by": [],
                    "description": "",
                }
                _STORE["localproviders"][lp_id] = lp
                obj["local_provider_id"] = lp_id

            _STORE[resource][obj_id] = obj
        self._respond(201, obj)

    def do_PATCH(self):
        path = self.path.split("?")[0]
        body = self._read_body()
        if body is None:
            self._respond(400, {"error": "invalid JSON body"})
            return
        _log({"method": "PATCH", "path": path, "headers": _strip_sensitive(dict(self.headers)), "body": body})

        resource, resource_id = self._parse_path()
        if resource is None or resource_id is None:
            self._respond(404, {"error": "not found"})
            return

        result = None
        with _LOCK:
            obj = _STORE[resource].get(resource_id)
            if obj is not None:
                obj.update(body)
                result = dict(obj)
        if result is None:
            self._respond(404, {"error": f"{resource} {resource_id} not found"})
        else:
            self._respond(200, result)

    def do_DELETE(self):
        path = self.path.split("?")[0]
        _log({"method": "DELETE", "path": path, "headers": _strip_sensitive(dict(self.headers))})

        resource, resource_id = self._parse_path()
        if resource is None or resource_id is None:
            self._respond(404, {"error": "not found"})
            return

        with _LOCK:
            if resource_id not in _STORE[resource]:
                found = False
            else:
                found = True
                del _STORE[resource][resource_id]
        if not found:
            self._respond(404, {"error": f"{resource} {resource_id} not found"})
        else:
            self.send_response(204)
            self.end_headers()

    def log_message(self, format, *args):
        pass


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
    server = ThreadingHTTPServer(("127.0.0.1", args.port), MockVmsHandler)

    if args.tls:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(certfile=args.cert, keyfile=args.key)
        server.socket = ctx.wrap_socket(server.socket, server_side=True)

    print(f"Mock VMS server running on port {args.port} (tls={args.tls})", flush=True)
    server.serve_forever()
