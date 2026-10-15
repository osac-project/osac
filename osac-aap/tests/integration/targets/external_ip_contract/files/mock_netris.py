"""Small Netris IPAM API double for ExternalIP allocation tests."""

import argparse
import json
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_LOCK = threading.Lock()
_CONFIG = {}
_SUBNETS = []
_CALLS = []
_NEXT_ID = 1
_CONFLICT_USED = False
_STATUS_UPDATE_APPLIED = False


class Handler(BaseHTTPRequestHandler):
    def _send(self, status, payload=None):
        body = b"" if status == 204 else json.dumps(payload or {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _path(self):
        return self.path.split("?", 1)[0]

    def _record(self, method):
        with _LOCK:
            _CALLS.append({"method": method, "path": self._path()})

    def do_GET(self):
        path = self._path()
        if path == "/_state":
            with _LOCK:
                self._send(200, {"subnets": list(_SUBNETS), "calls": list(_CALLS)})
            return
        self._record("GET")
        if path == "/api/v2/ipam":
            self._send(200, {"data": _CONFIG.get("allocations", [])})
        elif path == "/api/v2/ipam/subnets":
            with _LOCK:
                self._send(200, {"data": list(_SUBNETS)})
        else:
            self._send(404, {"message": "unexpected GET " + path})

    def do_POST(self):
        global _NEXT_ID, _CONFLICT_USED, _STATUS_UPDATE_APPLIED
        path = self._path()
        length = int(self.headers.get("Content-Length", 0))
        try:
            payload = json.loads(self.rfile.read(length)) if length else {}
        except (json.JSONDecodeError, ValueError):
            self._send(400, {"message": "invalid JSON"})
            return
        if path == "/_config":
            global _CONFIG
            with _LOCK:
                _CONFIG = payload
                _SUBNETS.clear()
                _SUBNETS.extend(payload.get("subnets", []))
                _CALLS.clear()
                _NEXT_ID = 1
                _CONFLICT_USED = False
                _STATUS_UPDATE_APPLIED = False
            self._send(200)
            return
        self._record("POST")
        if path != "/api/v2/ipam/subnet":
            self._send(404, {"message": "unexpected POST " + path})
            return
        with _LOCK:
            if _CONFIG.get("conflict_once") and not _CONFLICT_USED:
                _CONFLICT_USED = True
                _SUBNETS.append({
                    "id": 900,
                    "name": "concurrent-reservation",
                    "prefix": payload["prefix"],
                })
                self._send(409, {"message": "address was claimed concurrently"})
                return
            if any(item.get("prefix") == payload.get("prefix") for item in _SUBNETS):
                self._send(409, {"message": "prefix is already reserved"})
                return
            subnet = {
                "id": _NEXT_ID,
                "name": payload.get("name"),
                "prefix": payload.get("prefix"),
            }
            _NEXT_ID += 1
            _SUBNETS.append(subnet)
            status_update = _CONFIG.get("status_update_once")
            if status_update and not _STATUS_UPDATE_APPLIED:
                subprocess.run(
                    [
                        "kubectl",
                        "patch",
                        "externalip",
                        status_update["name"],
                        "--namespace",
                        status_update["namespace"],
                        "--subresource=status",
                        "--type=merge",
                        "--patch",
                        json.dumps({"status": status_update["status"]}),
                    ],
                    check=True,
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
                _STATUS_UPDATE_APPLIED = True
            self._send(201, {"data": subnet})

    def do_DELETE(self):
        self._record("DELETE")
        prefix = "/api/v2/ipam/subnet/"
        if not self._path().startswith(prefix):
            self._send(404, {"message": "unexpected DELETE " + self._path()})
            return
        subnet_id = self._path()[len(prefix):]
        with _LOCK:
            before = len(_SUBNETS)
            _SUBNETS[:] = [item for item in _SUBNETS if str(item.get("id")) != subnet_id]
            self._send(204 if len(_SUBNETS) != before else 404)

    def log_message(self, _format, *_args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("port", type=int)
    server = ThreadingHTTPServer(("127.0.0.1", parser.parse_args().port), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()
