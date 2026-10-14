"""Mock NetApp ONTAP REST API server for ONTAP storage provider integration tests.

Simulates the subset of ONTAP REST endpoints the ontap_storage role uses:
SVM discovery (``GET /api/svm/svms?name=<svm>``). The role's contract is
read-only against ONTAP at claim time, so the mock's job is to answer SVM
lookups with the prepared SVM record and record every call for assertions.

Follows the mock_vms_server.py pattern: CALL_LOG, _INJECTED_FAILURES,
control endpoints ``GET /_calls`` and ``POST /_reset``.

Auth: the role uses HTTP Basic Auth on every request. Any username/password
is accepted except the literal ``unauthorized`` username, which returns 401
so tests can exercise the authentication-failure path.

Usage:
    python3 mock_ontap_server.py <port> [--tls --cert <path> --key <path>]
"""

import argparse
import base64
import copy
import json
import ssl
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn
from urllib.parse import parse_qs, urlparse

CALL_LOG = []
_INJECTED_FAILURES = []
_LOCK = threading.Lock()

# Seeded SVM and QoS policy records, keyed by record ID. Tests register SVMs
# via POST /_svm and QoS policies via POST /_qos_policy.
_STORE = {
    "svms": {},
    "qos_policies": {},
}

_NEXT_ID = {"svms": 1, "qos_policies": 1}

# Arbitrary UUID stable across runs — tests assert against it via lookup, not
# by hardcoding.
DEFAULT_SVM_UUID = "svm-uuid-0001"


def _strip_sensitive(headers):
    """Return header dict with Authorization removed."""
    return {k: v for k, v in headers.items() if k.lower() != "authorization"}


def _log(entry):
    """Append an HTTP request entry to the thread-safe call log."""
    with _LOCK:
        CALL_LOG.append(entry)


class MockOntapHandler(BaseHTTPRequestHandler):
    """HTTP request handler simulating ONTAP REST endpoints and test control endpoints."""

    def _read_body(self):
        """Read and JSON-decode the HTTP request body, returning {} if empty or None on parse error."""
        length = int(self.headers.get("Content-Length", 0))
        if not length:
            return {}
        try:
            return json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, ValueError):
            return None

    def _respond(self, status, data):
        """Serialize data as JSON and write the HTTP response with the given status code."""
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _check_injected_failure(self, method):
        """Check if an injected failure matches this request. Returns (status, body) or None."""
        with _LOCK:
            for i, f in enumerate(_INJECTED_FAILURES):
                if f.get("path") in self.path and f.get("method") == method:
                    status = f.get("status", 500)
                    body = f.get("body", {"error": "injected failure"})
                    _INJECTED_FAILURES.pop(i)
                    return status, body
        return None

    def _authenticated(self):
        """ONTAP-style basic auth. The literal 'unauthorized' user fails.

        Basic credentials arrive base64-encoded, so the username check happens
        after decoding — matching the raw header against a literal would never
        trigger.
        """
        auth = self.headers.get("Authorization", "")
        if not auth.startswith("Basic "):
            return False
        try:
            decoded = base64.b64decode(auth[len("Basic "):]).decode("utf-8")
        except (ValueError, UnicodeDecodeError):
            return False
        return decoded.split(":", 1)[0] != "unauthorized"

    def do_GET(self):
        """Handle GET requests for ONTAP SVM/QoS discovery and /_calls inspection."""
        path = self.path.split("?")[0]

        if path == "/_calls":
            with _LOCK:
                snapshot = list(CALL_LOG)
            self._respond(200, snapshot)
            return

        _log({"method": "GET", "path": self.path, "headers": _strip_sensitive(dict(self.headers))})

        if not self._authenticated():
            self._respond(401, {"error": "unauthorized"})
            return

        failure = self._check_injected_failure("GET")
        if failure:
            self._respond(failure[0], failure[1])
            return

        if path == "/api/svm/svms":
            qs = parse_qs(urlparse(self.path).query)
            name_filter = qs.get("name", [None])[0]
            with _LOCK:
                records = [
                    copy.deepcopy(svm)
                    for svm in _STORE["svms"].values()
                    if name_filter is None or svm["name"] == name_filter
                ]
            self._respond(200, {"records": records, "num_records": len(records)})
            return

        if path == "/api/storage/qos/policies":
            qs = parse_qs(urlparse(self.path).query)
            name_filter = qs.get("name", [None])[0]
            with _LOCK:
                records = [
                    copy.deepcopy(p)
                    for p in _STORE["qos_policies"].values()
                    if name_filter is None or p["name"] == name_filter
                ]
            self._respond(200, {"records": records, "num_records": len(records)})
            return

        self._respond(404, {"error": "not found"})

    def do_POST(self):
        """Handle POST requests for test control endpoints (/_reset, /_svm, /_qos_policy, /_inject_failure)."""
        path = self.path.split("?")[0]

        if path == "/_reset":
            with _LOCK:
                CALL_LOG.clear()
                _INJECTED_FAILURES.clear()
                _STORE["svms"].clear()
                _STORE["qos_policies"].clear()
                _NEXT_ID["svms"] = 1
                _NEXT_ID["qos_policies"] = 1
            self._respond(200, {"status": "reset"})
            return

        if path == "/_svm":
            body = self._read_body()
            if not body or "name" not in body:
                self._respond(400, {"error": "svm name required"})
            else:
                with _LOCK:
                    svm_id = _NEXT_ID["svms"]
                    _NEXT_ID["svms"] += 1
                    svm = {
                        "id": svm_id,
                        "name": body["name"],
                        "uuid": body.get("uuid", f"svm-uuid-{svm_id:04d}"),
                    }
                    _STORE["svms"][svm_id] = svm
                self._respond(201, svm)
            return

        if path == "/_qos_policy":
            body = self._read_body()
            if not body or "name" not in body:
                self._respond(400, {"error": "qos policy name required"})
                return
            with _LOCK:
                policy_id = _NEXT_ID["qos_policies"]
                _NEXT_ID["qos_policies"] += 1
                policy = {
                    "id": policy_id,
                    "name": body["name"],
                    "uuid": body.get("uuid", f"qos-uuid-{policy_id:04d}"),
                    "svm": body.get("svm", {}),
                    "fixed": body.get(
                        "fixed",
                        {
                            "max_throughput_iops": body.get("max_iops", 0),
                            "capacity_shared": body.get("capacity_shared", False),
                        },
                    ),
                }
                _STORE["qos_policies"][policy_id] = policy
            self._respond(201, policy)
            return

        if path == "/_inject_failure":
            body = self._read_body()
            if not body or "path" not in body or "method" not in body:
                self._respond(400, {"error": "path and method required"})
                return
            with _LOCK:
                _INJECTED_FAILURES.append(body)
            self._respond(200, {"status": "failure injected", "pending": len(_INJECTED_FAILURES)})
            return

        self._respond(404, {"error": "not found"})

    def log_message(self, format, *args):  # noqa: A002
        """Suppress per-request stderr noise."""
        del format, args


class ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
    """Multi-threaded HTTP server for concurrent mock ONTAP requests."""

    daemon_threads = True


def main():
    """Parse CLI arguments and run the mock ONTAP HTTP(S) server."""
    parser = argparse.ArgumentParser()
    parser.add_argument("port", type=int)
    parser.add_argument("--tls", action="store_true")
    parser.add_argument("--cert")
    parser.add_argument("--key")
    args = parser.parse_args()

    server = ThreadingHTTPServer(("127.0.0.1", args.port), MockOntapHandler)

    if args.tls:
        if not args.cert or not args.key:
            parser.error("--tls requires --cert and --key")
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        # PROTOCOL_TLS_SERVER alone permits TLSv1/TLSv1_1 negotiation, which
        # CodeQL flags (py/insecure-protocol). The mock only serves local
        # test traffic; TLSv1.2+ is all any modern client speaks.
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(args.cert, args.key)
        server.socket = context.wrap_socket(server.socket, server_side=True)

    print(f"Mock ONTAP server listening on 127.0.0.1:{args.port} (tls={args.tls})", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
