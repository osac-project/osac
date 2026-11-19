"""Mock Pure Storage FlashBlade API server for integration tests.

Simulates FlashBlade REST API v2.26 endpoints for realms, file-systems,
servers, nfs-export-policies, and other resources.

Usage:
    python3 mock_flashblade_server.py --port 18444 --tls --cert certs/mock.pem --key certs/mock.key
"""

import argparse
import copy
import json
import ssl
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn
from urllib.parse import parse_qs, urlparse, unquote

CALL_LOG = []
_INJECTED_FAILURES = []

_NEXT_ID = {r: 1 for r in (
    "realms", "file-systems", "servers", "nfs-export-policies",
    "network-interfaces", "dns", "resource-accesses"
)}
_STORE = {r: {} for r in _NEXT_ID}
_LOCK = threading.Lock()

CANNED_DEFAULTS = {
    "realms": {"name": "", "destroyed": False, "created": 0},
    "file-systems": {"name": "", "provisioned": 0, "destroyed": False},
    "servers": {"name": "", "enabled": True},
    "nfs-export-policies": {"name": "", "enabled": True},
    "network-interfaces": {"name": "", "address": ""},
    "dns": {"name": "", "domain": ""},
    "resource-accesses": {"resource": {}, "scope": {}},
}

_LIST_FILTER_FIELDS = {
    "realms": ["name", "names"],
    "file-systems": ["name", "names"],
    "servers": ["name", "names"],
    "nfs-export-policies": ["name", "names"],
}


class ThreadedHTTPServer(ThreadingMixIn, HTTPServer):
    """Multi-threaded HTTP server."""
    daemon_threads = True


class MockFlashBladeHandler(BaseHTTPRequestHandler):
    """HTTP request handler for mock FlashBlade API."""

    protocol_version = "HTTP/1.1"

    def _set_headers(self, status=200, content_type="application/json"):
        """Set response headers."""
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Connection", "close")
        self.end_headers()

    def _send_json(self, data, status=200):
        """Send JSON response."""
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)

    def _log_call(self):
        """Log the API call for verification."""
        with _LOCK:
            CALL_LOG.append({
                "method": self.command,
                "path": self.path,
                "headers": dict(self.headers),
            })

    def _check_injected_failure(self, resource, method):
        """Check if a failure should be injected for this call."""
        with _LOCK:
            for idx, failure in enumerate(_INJECTED_FAILURES):
                if (failure.get("resource") == resource and
                    failure.get("method") == method):
                    _INJECTED_FAILURES.pop(idx)
                    return failure
        return None

    def _get_resource_type(self, path):
        """Extract resource type from API path."""
        # /api/2.26/file-systems -> file-systems
        parts = path.split("/")
        if len(parts) >= 4 and parts[1] == "api":
            return parts[3].split("?")[0]
        return None

    def _parse_query_params(self):
        """Parse query parameters from path."""
        parsed = urlparse(self.path)
        return parse_qs(parsed.query)

    def do_POST(self):
        """Handle POST requests."""
        self._log_call()

        if self.path == "/_inject_failure":
            # Test control endpoint
            content_length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}
            with _LOCK:
                _INJECTED_FAILURES.append(body)
            self._send_json({"status": "injected"})
            return

        if self.path == "/_seed":
            # Test control endpoint - pre-populate resources
            content_length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}
            resource = body.get("resource")
            name = body.get("name")

            with _LOCK:
                if resource in _STORE:
                    item_id = str(_NEXT_ID[resource])
                    _NEXT_ID[resource] += 1
                    item = copy.deepcopy(CANNED_DEFAULTS.get(resource, {}))
                    item["id"] = item_id
                    item["name"] = name
                    _STORE[resource][name] = item

            self._send_json({"status": "seeded", "resource": resource, "name": name})
            return

        # Login endpoint
        if self.path == "/api/login":
            # Mock authentication - return session token in header
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("x-auth-token", "mock-session-token")
            self.send_header("Connection", "close")
            self.end_headers()
            return

        # API resource creation
        resource = self._get_resource_type(self.path)
        if not resource:
            self._send_json({"errors": [{"message": "Unknown endpoint"}]}, status=404)
            return

        # Check for injected failure
        failure = self._check_injected_failure(resource, "POST")
        if failure:
            self._send_json(
                failure.get("body", {"errors": [{"message": "Injected failure"}]}),
                status=failure.get("status", 400)
            )
            return

        # Parse request body
        content_length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}

        # Extract name from query params or body
        query_params = self._parse_query_params()
        name = query_params.get("names", [None])[0] or body.get("name", f"test-{resource}-1")

        # Check if already exists (idempotency)
        with _LOCK:
            if name in _STORE[resource]:
                self._send_json(
                    {"errors": [{"message": f"{resource} '{name}' already exists"}]},
                    status=400
                )
                return

            # Create new resource
            item_id = str(_NEXT_ID[resource])
            _NEXT_ID[resource] += 1

            item = copy.deepcopy(CANNED_DEFAULTS.get(resource, {}))
            item.update(body)
            item["id"] = item_id
            item["name"] = name

            _STORE[resource][name] = item

        self._send_json({"items": [item]}, status=200)

    def do_GET(self):
        """Handle GET requests."""
        self._log_call()

        # Test control endpoints
        if self.path == "/_reset":
            with _LOCK:
                CALL_LOG.clear()
                _INJECTED_FAILURES.clear()
                for resource in _STORE:
                    _STORE[resource].clear()
                for resource in _NEXT_ID:
                    _NEXT_ID[resource] = 1
            self._send_json({"status": "reset"})
            return

        if self.path == "/_calls":
            with _LOCK:
                calls = copy.deepcopy(CALL_LOG)
            self._send_json(calls)
            return

        # API resource listing
        resource = self._get_resource_type(self.path)
        if not resource:
            self._send_json({"errors": [{"message": "Unknown endpoint"}]}, status=404)
            return

        query_params = self._parse_query_params()

        with _LOCK:
            items = list(_STORE[resource].values())

        # Filter by names query param
        if "names" in query_params:
            filter_names = [unquote(n) for n in query_params["names"][0].split(",")]
            items = [item for item in items if item["name"] in filter_names]

        self._send_json({
            "items": items,
            "total_item_count": len(items),
            "continuation_token": None
        })

    def do_PATCH(self):
        """Handle PATCH requests (updates/deletes)."""
        self._log_call()

        resource = self._get_resource_type(self.path)
        if not resource:
            self._send_json({"errors": [{"message": "Unknown endpoint"}]}, status=404)
            return

        # Parse request body
        content_length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}

        # Extract name from query params
        query_params = self._parse_query_params()
        names = query_params.get("names", [None])

        if not names[0]:
            self._send_json({"errors": [{"message": "names parameter required"}]}, status=400)
            return

        name = unquote(names[0])

        with _LOCK:
            if name not in _STORE[resource]:
                self._send_json(
                    {"errors": [{"message": f"{resource} '{name}' not found"}]},
                    status=404
                )
                return

            # Update resource
            _STORE[resource][name].update(body)

            # Handle destroy
            if body.get("destroyed") is True:
                item = _STORE[resource][name]
                item["destroyed"] = True

        self._send_json({"items": [_STORE[resource][name]]})

    def do_DELETE(self):
        """Handle DELETE requests."""
        self._log_call()

        resource = self._get_resource_type(self.path)
        query_params = self._parse_query_params()

        if "ids" in query_params:
            # Delete by ID (resource-accesses)
            item_id = query_params["ids"][0]
            with _LOCK:
                for name, item in list(_STORE[resource].items()):
                    if item.get("id") == item_id:
                        del _STORE[resource][name]
                        break
            self._send_json({"items": []})
        else:
            self._send_json({"errors": [{"message": "DELETE not implemented"}]}, status=501)

    def log_message(self, format, *args):
        """Suppress default logging."""
        pass


def main():
    parser = argparse.ArgumentParser(description="Mock FlashBlade API server")
    parser.add_argument("--port", type=int, default=18444, help="Port to listen on")
    parser.add_argument("--tls", action="store_true", help="Enable TLS")
    parser.add_argument("--cert", default="certs/mock.pem", help="TLS certificate path")
    parser.add_argument("--key", default="certs/mock.key", help="TLS key path")
    args = parser.parse_args()

    server_address = ("127.0.0.1", args.port)
    httpd = ThreadedHTTPServer(server_address, MockFlashBladeHandler)

    if args.tls:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(args.cert, args.key)
        httpd.socket = context.wrap_socket(httpd.socket, server_side=True)
        print(f"Mock FlashBlade API server running on https://127.0.0.1:{args.port}")
    else:
        print(f"Mock FlashBlade API server running on http://127.0.0.1:{args.port}")

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down mock server")
        httpd.shutdown()


if __name__ == "__main__":
    main()
