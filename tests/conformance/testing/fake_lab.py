"""Fake lab facts for conformance unit and integration tests."""

from __future__ import annotations

import json
import threading
from collections.abc import Mapping
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


@dataclass(frozen=True)
class FakeLab:
    enabled_services: tuple[str, ...] = ("vmaas",)
    registrations: Mapping[str, Mapping[str, Any]] = field(
        default_factory=lambda: {"networking": {"name": "mock", "ansibleRole": "mock.fabric"}}
    )
    installed_release: str = "1.4.0"


class FakeLabClient:
    def __init__(self, lab: FakeLab) -> None:
        self.lab = lab

    def capabilities(self, *, url: str, token: str, ca_file: Path | None) -> Mapping[str, Any]:
        return {"enabled_services": list(self.lab.enabled_services)}

    def registrations(self, *, kubeconfig: Path) -> Mapping[str, Mapping[str, Any]]:
        return self.lab.registrations

    def installed_release(self, *, kubeconfig: Path) -> str:
        return self.lab.installed_release


class FakeLabServer:
    """Serve the fake lab over HTTP for component-integration tests."""

    def __init__(self, lab: FakeLab) -> None:
        self.lab = lab
        self._server: ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.url = ""

    def __enter__(self) -> FakeLabServer:
        lab = self.lab

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                path = urlparse(self.path).path
                if path == "/api/fulfillment/v1/capabilities":
                    body = json.dumps({"enabled_services": list(lab.enabled_services)}).encode()
                elif path == "/registrations":
                    body = json.dumps(lab.registrations).encode()
                elif path == "/release":
                    body = json.dumps({"release": lab.installed_release}).encode()
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_args: object) -> None:
                return

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()
        host, port = self._server.server_address
        self.url = f"http://{host}:{port}"
        return self

    def __exit__(self, *_exc: object) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
        if self._thread is not None:
            self._thread.join(timeout=5)
