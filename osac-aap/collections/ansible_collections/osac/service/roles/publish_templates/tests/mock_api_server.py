"""Minimal mock HTTP server for publish_templates role tests.

Simulates the fulfillment-service private API list/create/update endpoints
for cluster_templates, compute_instance_templates, and network_classes.

Usage:
    python mock_api_server.py [port] [scenario]

Scenarios:
    empty    - All endpoints return {"items": []} with no size field (proto3 omit)
    populated - Endpoints return items with size field present
    no_items_key - Response is {} (edge case: no items key at all)
"""

import json
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler

SCENARIO = "empty"
# Track API calls for test verification
CALL_LOG = []

POPULATED_RESPONSES = {
    "/api/private/v1/cluster_templates": {
        "size": 1,
        "total": 1,
        "items": [{"id": "existing-cluster-template", "title": "Test Cluster"}],
    },
    "/api/private/v1/compute_instance_templates": {
        "size": 1,
        "total": 1,
        "items": [{"id": "existing-ci-template", "title": "Test CI"}],
    },
    "/api/private/v1/network_classes": {
        "size": 1,
        "total": 1,
        "items": [{"id": "existing-network-class", "implementation_strategy": "cudn_net", "title": "Test NC"}],
    },
}


class MockHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = self.path.split("?")[0]

        if path == "/_calls":
            self._respond(200, CALL_LOG)
            return

        if path == "/_reset":
            CALL_LOG.clear()
            self._respond(200, {"status": "reset"})
            return

        CALL_LOG.append({"method": "GET", "path": path})

        if SCENARIO == "empty":
            self._respond(200, {"items": []})
        elif SCENARIO == "no_items_key":
            self._respond(200, {})
        elif SCENARIO == "populated":
            base_path = path.rstrip("/")
            # If path has an ID suffix (e.g. /api/.../templates/some-id), use base
            for endpoint, data in POPULATED_RESPONSES.items():
                if base_path == endpoint:
                    self._respond(200, data)
                    return
            self._respond(200, {"items": []})
        else:
            self._respond(200, {"items": []})

    def do_POST(self):
        path = self.path.split("?")[0]
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b""
        CALL_LOG.append({
            "method": "POST",
            "path": path,
            "body": json.loads(body) if body else None,
        })
        self._respond(200, {"id": "new-item", "status": "created"})

    def do_PATCH(self):
        path = self.path.split("?")[0]
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b""
        CALL_LOG.append({
            "method": "PATCH",
            "path": path,
            "body": json.loads(body) if body else None,
        })
        self._respond(200, {"id": "updated-item", "status": "updated"})

    def _respond(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        pass  # Suppress request logging


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    SCENARIO = sys.argv[2] if len(sys.argv) > 2 else "empty"
    HTTPServer.allow_reuse_address = True
    server = HTTPServer(("127.0.0.1", port), MockHandler)
    print(f"Mock API server running on port {port} (scenario: {SCENARIO})")
    sys.stdout.flush()
    server.serve_forever()
