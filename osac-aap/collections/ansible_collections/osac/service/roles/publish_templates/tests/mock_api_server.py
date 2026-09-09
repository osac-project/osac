"""Minimal mock HTTP server for publish_templates role tests.

Simulates the fulfillment-service private API list/create/update endpoints
for cluster_templates, compute_instance_templates, and baremetal_instance_templates.
Unknown routes return 404. Known collection routes use the selected scenario, while
known member routes are accepted only for PATCH requests.

Usage:
    python mock_api_server.py [port] [scenario]

Scenarios:
    empty    - All endpoints return {"items": []} with no size field (proto3 omit)
    populated - Endpoints return items with size field present
    no_items_key - Response is {} (edge case: no items key at all)
    disabled - All known endpoints return 404
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
    "/api/private/v1/baremetal_instance_templates": {
        "size": 1,
        "total": 1,
        "items": [{"id": "existing-bm-template", "title": "Test BM"}],
    },
    "/api/private/v1/add_on_operators": {
        "size": 1,
        "total": 1,
        "items": [{"id": "existing-addon-operator", "title": "Test AddOnOperator"}],
    },
}

KNOWN_MEMBER_PATHS = {
    f"{endpoint}/{item['id']}"
    for endpoint, response in POPULATED_RESPONSES.items()
    for item in response["items"]
}


def _is_member_path(path):
    return path in KNOWN_MEMBER_PATHS


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

        CALL_LOG.append(
            {
                "method": "GET",
                "path": path,
                "authorization": self.headers.get("Authorization"),
            }
        )

        known_collection = path in POPULATED_RESPONSES
        known_member = _is_member_path(path)
        if not known_collection and not known_member:
            CALL_LOG[-1]["status"] = 404
            self._respond(404, {"error": "not found"})
        elif known_member:
            CALL_LOG[-1]["status"] = 404
            self._respond(404, {"error": "not found"})
        elif SCENARIO == "disabled":
            CALL_LOG[-1]["status"] = 404
            self._respond(404, {"error": "service disabled"})
        elif SCENARIO == "empty":
            self._respond(200, {"items": []})
        elif SCENARIO == "no_items_key":
            self._respond(200, {})
        elif SCENARIO == "populated":
            for endpoint, data in POPULATED_RESPONSES.items():
                if path == endpoint:
                    self._respond(200, data)
                    return
            self._respond(200, {"items": []})
        else:
            self._respond(200, {"items": []})

    def do_POST(self):
        path = self.path.split("?")[0]
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b""
        if SCENARIO == "disabled" or path not in POPULATED_RESPONSES:
            self._respond(404, {"error": "not found"})
            return
        CALL_LOG.append({
            "method": "POST",
            "path": path,
            "authorization": self.headers.get("Authorization"),
            "body": json.loads(body) if body else None,
        })
        self._respond(200, {"id": "new-item", "status": "created"})

    def do_PATCH(self):
        path = self.path.split("?")[0]
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b""
        if SCENARIO == "disabled" or not _is_member_path(path):
            self._respond(404, {"error": "not found"})
            return
        CALL_LOG.append({
            "method": "PATCH",
            "path": path,
            "request_uri": self.path,
            "authorization": self.headers.get("Authorization"),
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
