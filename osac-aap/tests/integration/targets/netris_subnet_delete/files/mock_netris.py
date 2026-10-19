"""Mock Netris controller API for Subnet deletion integration tests.

Simulates the Netris v2 endpoints exercised by the Subnet delete path:
``GET /api/v2/vnet``, ``DELETE /api/v2/vnet/<id>``,
``GET /api/v2/ipam/subnets``, and ``DELETE /api/v2/ipam/subnet/<id>``.
Other paths return 404 so unexercised provider calls fail visibly.

The call log records method and path only; authorization headers,
cookies, request bodies, and credentials are never stored.

Test control endpoints:
    POST /_config  -- replace scenario state and clear the call log
    POST /_reset   -- restore empty state and clear the call log
    GET  /_calls   -- return the recorded method/path order
    GET  /_health  -- liveness probe

Usage:
    python3 mock_netris.py <port>
"""

import argparse
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_LOCK = threading.Lock()
_CALL_LOG = []


def _empty_config():
    """Return the default scenario configuration with empty stores."""
    return {
        "vnets": [],
        "vnet_delete_status": 204,
        "ipam_subnets": [],
        "ipam_delete_status": 204,
    }


_CONFIG = _empty_config()


class MockNetrisHandler(BaseHTTPRequestHandler):

    def _respond(self, status, payload=None):
        """Write a JSON response with the given status code and payload."""
        body = json.dumps(payload if payload is not None else {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _record(self, method):
        """Append the request method and query-free path to the call log."""
        with _LOCK:
            _CALL_LOG.append({"method": method, "path": self.path.split("?")[0]})

    def _config(self):
        """Return a snapshot copy of the current scenario configuration."""
        with _LOCK:
            return dict(_CONFIG)

    def do_GET(self):
        """Serve the vnet/ipam list endpoints and the test control probes."""
        path = self.path.split("?")[0]
        config = self._config()
        if path == "/_calls":
            with _LOCK:
                self._respond(200, {"calls": list(_CALL_LOG)})
        elif path == "/_health":
            self._respond(200)
        elif path == "/api/v2/vnet":
            self._record("GET")
            self._respond(200, {"data": config["vnets"]})
        elif path == "/api/v2/ipam/subnets":
            self._record("GET")
            self._respond(200, {"data": config["ipam_subnets"]})
        else:
            self._respond(404, {"message": "unexpected GET %s" % path})

    def do_POST(self):
        """Serve the /_config and /_reset scenario control endpoints."""
        path = self.path.split("?")[0]
        if path == "/_config":
            length = int(self.headers.get("Content-Length", 0))
            try:
                scenario = json.loads(self.rfile.read(length)) if length else {}
            except (json.JSONDecodeError, ValueError):
                self._respond(400, {"message": "invalid config body"})
                return
            with _LOCK:
                _CONFIG.clear()
                _CONFIG.update({**_empty_config(), **scenario})
                _CALL_LOG.clear()
            self._respond(200)
        elif path == "/_reset":
            with _LOCK:
                _CONFIG.clear()
                _CONFIG.update(_empty_config())
                _CALL_LOG.clear()
            self._respond(200)
        else:
            self._respond(404, {"message": "unexpected POST %s" % path})

    def do_DELETE(self):
        """Serve the vnet and ipam subnet delete endpoints."""
        path = self.path.split("?")[0]
        config = self._config()
        if path.startswith("/api/v2/vnet/"):
            self._record("DELETE")
            self._respond(int(config["vnet_delete_status"]), {"message": "vnet delete"})
        elif path.startswith("/api/v2/ipam/subnet/"):
            self._record("DELETE")
            self._respond(int(config["ipam_delete_status"]), {"message": "ipam subnet delete"})
        else:
            self._respond(404, {"message": "unexpected DELETE %s" % path})

    def log_message(self, format, *args):
        """Suppress per-request console logging; the call log is the test record."""
        pass


class MockNetrisServer(ThreadingHTTPServer):
    daemon_threads = True


def main():
    """Parse the port argument and serve the fixture until stopped."""
    parser = argparse.ArgumentParser(description="Mock Netris controller API")
    parser.add_argument("port", type=int)
    args = parser.parse_args()
    server = MockNetrisServer(("127.0.0.1", args.port), MockNetrisHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
