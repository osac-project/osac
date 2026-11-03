#!/usr/bin/env python3
"""A local proxy between kubernetes.core and the Kind API server that injects faults.

A real API server cannot be told to answer one request with a 503, a 409 or a
dropped connection. This proxy forwards every request unchanged to the API
server of a kubeconfig, with that kubeconfig's credentials, except the requests
a test named in a fault. It holds no objects and never changes a forwarded
request or response, so everything else stays the real API server.

Admin endpoints, in the style of fixtures/netris/fake_netris.py:

  GET  /__admin/calls          the request log [method, path]
  POST /__admin/calls/clear    empty the request log
  POST /__admin/faults         add a fault: {"key": "METHOD path", "status",
                               "reason", "message", "sticky", "mode"}; mode is
                               "respond" (default: answer a Kubernetes Status
                               with `status`, nothing reaches the API server)
                               or "drop" (reset the connection without an
                               answer). A key may use * as a wildcard. Without
                               "sticky" a fault fires once.
  POST /__admin/faults/clear   drop all faults
  POST /__admin/shutdown       stop the proxy

Command line:

  python fault_proxy.py --kubeconfig <file> --port-file <file> --out-kubeconfig <file>

writes the chosen port to the port file once it listens, and a kubeconfig that
points at the proxy to the output file.
"""

from __future__ import annotations

import argparse
import base64
import fnmatch
import http.client
import json
import socket
import ssl
import struct
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

import yaml

HOP_HEADERS = {"connection", "keep-alive", "transfer-encoding", "te", "upgrade", "proxy-connection", "host", "content-length"}


def upstream_context(kubeconfig: dict, workdir: Path) -> tuple[str, int, ssl.SSLContext]:
    """Host, port and TLS context of the current context of a kubeconfig."""
    current = next(c["context"] for c in kubeconfig["contexts"] if c["name"] == kubeconfig["current-context"])
    cluster = next(c["cluster"] for c in kubeconfig["clusters"] if c["name"] == current["cluster"])
    user = next(u["user"] for u in kubeconfig["users"] if u["name"] == current["user"])
    context = ssl.create_default_context(cadata=base64.b64decode(cluster["certificate-authority-data"]).decode())
    context.check_hostname = False
    cert, key = workdir / "client.crt", workdir / "client.key"
    cert.write_bytes(base64.b64decode(user["client-certificate-data"]))
    key.touch(mode=0o600)
    key.write_bytes(base64.b64decode(user["client-key-data"]))
    context.load_cert_chain(cert, key)
    server = urlsplit(cluster["server"])
    return server.hostname, server.port or 443, context


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args) -> None:
        pass

    def _reply(self, status: int, payload: object) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _drop(self) -> None:
        self.close_connection = True
        try:
            self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
            self.connection.close()
        except OSError:
            pass

    def _admin(self, action: str, body: dict) -> None:
        srv = self.server
        with srv.lock:
            if action == "calls":
                return self._reply(200, srv.calls)
            if action == "calls/clear":
                srv.calls.clear()
            elif action == "faults":
                srv.faults.append(body)
            elif action == "faults/clear":
                srv.faults.clear()
            elif action == "shutdown":
                threading.Thread(target=srv.shutdown, daemon=True).start()
            else:
                return self._reply(404, {"message": "unknown admin action"})
        self._reply(200, {"ok": True})

    def _take_fault(self, path: str) -> dict | None:
        srv = self.server
        key = f"{self.command} {path}"
        with srv.lock:
            srv.calls.append([self.command, path])
            for fault in srv.faults:
                if fnmatch.fnmatchcase(key, fault["key"]):
                    if not fault.get("sticky"):
                        srv.faults.remove(fault)
                    return fault
        return None

    def _handle(self) -> None:
        size = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(size) if size else b""
        path = urlsplit(self.path).path
        if path.startswith("/__admin/"):
            return self._admin(path[len("/__admin/"):], json.loads(body or b"{}"))
        fault = self._take_fault(path)
        if fault:
            if fault.get("mode") == "drop":
                return self._drop()
            status = int(fault.get("status", 500))
            return self._reply(status, {
                "kind": "Status", "apiVersion": "v1", "metadata": {}, "status": "Failure",
                "message": fault.get("message", "injected by the fault proxy"),
                "reason": fault.get("reason", "InternalError"), "code": status,
            })
        srv = self.server
        connection = http.client.HTTPSConnection(srv.upstream_host, srv.upstream_port, context=srv.upstream_tls, timeout=60)
        try:
            headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP_HEADERS}
            connection.request(self.command, self.path, body=body or None, headers=headers)
            response = connection.getresponse()
            payload = response.read()
        finally:
            connection.close()
        self.send_response(response.status, response.reason)
        for key, value in response.getheaders():
            if key.lower() not in HOP_HEADERS:
                self.send_header(key, value)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _handle  # noqa: N815


class Proxy(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, host: str, port: int, context: ssl.SSLContext) -> None:
        super().__init__(("127.0.0.1", 0), Handler)
        self.upstream_host, self.upstream_port, self.upstream_tls = host, port, context
        self.lock = threading.Lock()
        self.calls: list[list[str]] = []
        self.faults: list[dict] = []


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument("--kubeconfig", required=True, help="kubeconfig of the real API server")
    parser.add_argument("--port-file", required=True, help="file that receives the listening port")
    parser.add_argument("--out-kubeconfig", required=True, help="file that receives a kubeconfig of the proxy")
    args = parser.parse_args()
    kubeconfig = yaml.safe_load(Path(args.kubeconfig).read_text())
    with tempfile.TemporaryDirectory(prefix="kube-fault-proxy-") as workdir:
        server = Proxy(*upstream_context(kubeconfig, Path(workdir)))
        out = {
            "apiVersion": "v1", "kind": "Config", "current-context": "proxy",
            "clusters": [{"name": "proxy", "cluster": {"server": f"http://127.0.0.1:{server.server_port}"}}],
            "contexts": [{"name": "proxy", "context": {"cluster": "proxy", "user": "proxy"}}],
            "users": [{"name": "proxy", "user": {}}],
        }
        Path(args.out_kubeconfig).write_text(json.dumps(out))
        Path(args.port_file).write_text(str(server.server_port))
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        finally:
            server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
