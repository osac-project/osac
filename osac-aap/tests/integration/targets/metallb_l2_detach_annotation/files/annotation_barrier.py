#!/usr/bin/env python3
"""Coordinate stale ComputeInstance reads in the detach annotation test."""

from __future__ import annotations

import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

arrivals = 0
condition = threading.Condition()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        global arrivals
        if self.path == "/arrive":
            with condition:
                arrivals += 1
                condition.notify_all()
                ready = condition.wait_for(lambda: arrivals >= 2, timeout=60)
            self.send_response(204 if ready else 504)
            self.end_headers()
            return

        if self.path == "/shutdown":
            self.send_response(204)
            self.end_headers()
            threading.Thread(target=self.server.shutdown, daemon=True).start()
            return

        self.send_response(404)
        self.end_headers()

    def log_message(self, _format: str, *_args: object) -> None:
        return


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_port), encoding="utf-8")
server.serve_forever()
