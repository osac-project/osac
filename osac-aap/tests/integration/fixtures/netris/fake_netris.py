#!/usr/bin/env python3
"""A shared, stateful fake of the Netris controller API for the Kind/IT targets.

One in-process HTTP server (standard library only) that serves every endpoint
the osac.templates.netris role and the netris.controller roles call:

  POST /api/auth                         login, sets the connect.sid cookie
  /api/v2/vpc[/<id>]                     VPC, `vni` from its own counter
  /api/v2/ipam                           IPAM allocations (list)
  /api/v2/ipam/allocation[/<id>]         IPAM allocation
  /api/v2/ipam/subnets                   IPAM subnets (list)
  /api/v2/ipam/subnet[/<id>]             IPAM subnet
  /api/v2/ipam/hosts/<subnet id>         IPAM hosts (DHCP leases by MAC)
  /api/v2/vnet[/<id>]                    V-Net, `vxlanID` from its own counter,
                                         `gateways` kept as posted
  /api/v2/nat[/<id>]                     NAT rules (SNAT/DNAT)
  /api/v2/server-cluster[/<id>]          server clusters (FabricDomain)
  /api/v2/hw, /api/v2/ports?switchID=    servers and their ports

Every request except the login needs the cookie handed out by the login.

VPC `vni` and V-Net `vxlanID` come from two disjoint counters. A value is freed
when its object is deleted and is the first one handed out again.

Admin endpoints (not part of the Netris API) let a test inspect and steer the
fake while it runs as a background process:

  GET  /__admin/state          all objects, counters and pending faults
  GET  /__admin/calls          the request log [method, path, body]; auth bodies are redacted
  POST /__admin/calls/clear    empty the request log
  POST /__admin/reset          back to the empty state
  POST /__admin/seed           {"collection", "objects": [...]} add objects
                               (an object without "id" gets the next one)
  POST /__admin/update         {"collection", "id", "fields": {...}, "drop": [keys]}
                               change one object
  POST /__admin/remove         {"collection", "id"} drop one without a request
  POST /__admin/set            {"values": {...}} set top-level knobs, e.g.
                               assign_vni, list_omit, vanish_after_list
  POST /__admin/faults         add a fault: {"key": "METHOD path", "status",
                               "body", "sticky", "mode"}; mode is "respond"
                               (default: answer instead of the real handler,
                               nothing is applied) or "commit_then_drop" (apply
                               the request, then reset the connection so the
                               client never sees the answer). A key may end in
                               "*". Without "sticky" a fault fires once.
  POST /__admin/faults/clear   drop all faults
  POST /__admin/shutdown       stop the server

Command line:

  python fake_netris.py --port-file <file> [--state-file <file>] [--cookie-file <file>] [--port N]

writes the chosen port to the port file once it listens, and a JSON snapshot of
the state to the state file after every write request. The snapshot omits fault
response bodies; the in-memory state keeps them for the redaction tests.

The cookie file holds the session cookie clients must present. Without it the
cookie comes from NF_SESSION_COOKIE or is generated per run.
"""

from __future__ import annotations

import argparse
import copy
import fnmatch
import hmac
import json
import os
import secrets
import socket
import struct
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

COOKIE = os.environ.get("NF_SESSION_COOKIE") or secrets.token_urlsafe(32)

# Collection name -> (list paths, item prefix, id counter key)
COLLECTIONS = {
    "vpcs": (["/api/v2/vpc"], "/api/v2/vpc", "vpc"),
    "allocations": (["/api/v2/ipam", "/api/v2/ipam/allocation"], "/api/v2/ipam/allocation", "alloc"),
    "ipam_subnets": (["/api/v2/ipam/subnets", "/api/v2/ipam/subnet"], "/api/v2/ipam/subnet", "ipam"),
    "vnets": (["/api/v2/vnet"], "/api/v2/vnet", "vnet"),
    "nats": (["/api/v2/nat"], "/api/v2/nat", "nat"),
    "clusters": (["/api/v2/server-cluster"], "/api/v2/server-cluster", "sc"),
}
LIST_PATHS = {p: name for name, (paths, _, _) in COLLECTIONS.items() for p in paths}
ITEM_PREFIXES = {prefix: name for name, (_, prefix, _) in COLLECTIONS.items()}
# Collections that hold a VNI-like number freed on delete: collection -> (field, counter)
VNI_FIELDS = {"vpcs": ("vni", "vpc"), "vnets": ("vxlanID", "vnet")}


def empty_state() -> dict:
    """Create the initial mutable state used by the fake Netris server."""
    return {
        "vpcs": [], "allocations": [], "ipam_subnets": [], "vnets": [], "nats": [], "clusters": [],
        "servers": [], "server_ports": {}, "ipam_hosts": {},
        "faults": [], "list_omit": {}, "vanish_after_list": None, "assign_vni": True,
        # Disjoint VNI counters; a freed value is handed out again first.
        "vni": {"vpc": {"next": 10001, "free": []}, "vnet": {"next": 5000001, "free": []}},
        "next_id": {"vpc": 101, "alloc": 201, "ipam": 301, "vnet": 401, "nat": 501, "sc": 601},
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    # -- plumbing ----------------------------------------------------------

    def _body(self) -> dict:
        """Read and decode a JSON object from the request body."""
        size = int(self.headers.get("Content-Length", "0"))
        try:
            value = json.loads(self.rfile.read(size) or b"{}")
        except ValueError:
            return {}
        return value if isinstance(value, dict) else {}

    def _send(self, status: int, payload: object, cookie: bool = False) -> None:
        """Send a JSON response and optionally issue the session cookie."""
        body = b"" if status == 304 else json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        if cookie:
            self.send_header("Set-Cookie", f"connect.sid={COOKIE}; Path=/")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _drop(self) -> None:
        """Reset the connection without an answer."""
        self.close_connection = True
        try:
            self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
            self.connection.close()
        except OSError:
            pass

    def log_message(self, *args) -> None:
        """Suppress per-request logs from the HTTP test server."""
        pass

    def _logged_in(self) -> bool:
        """True when the Cookie header carries connect.sid with exactly the issued value."""
        pairs = [part.strip() for part in (self.headers.get("Cookie") or "").split(";")]
        expected = f"connect.sid={COOKIE}".encode()
        return any(
            hmac.compare_digest(pair.encode(), expected) for pair in pairs
        )

    # -- request entry -----------------------------------------------------

    def _handle(self) -> None:
        """Authenticate and dispatch one HTTP request under the state lock."""
        parts = urlsplit(self.path)
        path = parts.path
        query = parse_qs(parts.query)
        body = self._body() if self.command in ("POST", "PUT") else {}
        srv = self.server
        with srv.lock:
            if path.startswith("/__admin/"):
                status, payload = self._admin(path, body)
                self._send(status, payload)
                if path == "/__admin/shutdown":
                    threading.Thread(target=srv.shutdown, daemon=True).start()
                return
            srv.calls.append([self.command, path, self._logged_body(path, body)])
            if path != "/api/auth" and not self._logged_in():
                self._send(401, {"message": "not logged in"})
                return
            fault = self._take_fault(path)
            if fault and fault.get("mode", "respond") == "respond":
                self._send(fault.get("status", 500), fault.get("body", {}))
                return
            status, payload, cookie = self._dispatch(path, query, body)
            if self.command in ("POST", "PUT", "DELETE"):
                srv.dump()
            if fault:  # commit_then_drop
                self._drop()
                return
            self._send(status, payload, cookie)

    do_GET = do_POST = do_PUT = do_DELETE = _handle  # noqa: N815

    @staticmethod
    def _logged_body(path: str, body: dict) -> dict:
        """Redact credentials from the authentication request log entry."""
        if path == "/api/auth":
            return {"redacted": True}
        return body

    def _take_fault(self, path: str) -> dict | None:
        """Return the matching configured fault, preserving sticky faults."""
        faults = self.server.state["faults"]
        key = f"{self.command} {path}"
        for fault in faults:
            if fnmatch.fnmatchcase(key, fault["key"]):
                if not fault.get("sticky"):
                    faults.remove(fault)
                return fault
        return None

    # -- the Netris API ----------------------------------------------------

    def _dispatch(self, path: str, query: dict, body: dict) -> tuple[int, object, bool]:
        """Route a request to a fake Netris resource or collection handler."""
        st = self.server.state
        cmd = self.command
        if path == "/api/auth":
            return 200, {"ok": True}, True
        if cmd == "GET" and path == "/api/v2/hw":
            return 200, {"data": st["servers"]}, False
        if cmd == "GET" and path == "/api/v2/ports":
            server = query.get("switchID", [""])[0]
            return 200, {"data": st["server_ports"].get(server, [])}, False
        if cmd == "GET" and path.startswith("/api/v2/ipam/hosts/"):
            return 200, {"data": st["ipam_hosts"].get(path.rsplit("/", 1)[1], [])}, False
        if path in LIST_PATHS:
            coll = LIST_PATHS[path]
            if cmd == "GET":
                return self._list(coll)
            if cmd == "POST":
                return self._create(coll, body)
        head, _, tail = path.rpartition("/")
        if head in ITEM_PREFIXES:
            return self._item(ITEM_PREFIXES[head], tail, body)
        return 404, {"message": "unexpected fake request"}, False

    def _list(self, coll: str) -> tuple[int, object, bool]:
        """Build a collection response with configured omissions and races."""
        st = self.server.state
        omit = st["list_omit"]  # {object id: [keys the list response leaves out]}
        rows = [{k: v for k, v in o.items() if k not in omit.get(str(o["id"]), [])} for o in st[coll]]
        gone = st["vanish_after_list"]  # id removed right after the V-Net list is served
        if gone is not None and coll == "vnets":
            st["vnets"] = [o for o in st["vnets"] if o["id"] != gone]
            st["vanish_after_list"] = None
        return 200, {"data": rows}, False

    def _next_vni(self, kind: str) -> int:
        """Return the next available VNI for the requested resource type."""
        counter = self.server.state["vni"][kind]
        if counter["free"]:
            counter["free"].sort()
            return counter["free"].pop(0)
        counter["next"] += 1
        return counter["next"] - 1

    def _create(self, coll: str, body: dict) -> tuple[int, object, bool]:
        """Create a collection record while modeling provider allocation rules."""
        st = self.server.state
        if coll == "ipam_subnets":
            # Netris documents IPAM subnet prefixes as unique and tracks them
            # per VPC: https://docs.netris.io/en/latest/ipam.html
            # Keep this check inside the request lock to model that invariant
            # under concurrent POSTs.
            vpc_id = (body.get("vpc") or {}).get("id")
            if any(
                record.get("prefix") == body.get("prefix")
                and (record.get("vpc") or {}).get("id") == vpc_id
                for record in st[coll]
            ):
                return 409, {"message": "subnet prefix already exists in VPC"}, False
        key = COLLECTIONS[coll][2]
        new_id = st["next_id"][key]
        st["next_id"][key] += 1
        obj = {**copy.deepcopy(body), "id": new_id}
        if coll == "clusters":
            obj["status"] = {"label": "Active"}
        if coll == "vnets":
            obj["state"] = "active"
        if coll in VNI_FIELDS and st["assign_vni"]:
            field, kind = VNI_FIELDS[coll]
            obj[field] = self._next_vni(kind)
        st[coll].append(obj)
        return 201, {"data": {"id": new_id}}, False

    def _item(self, coll: str, item_id: str, body: dict) -> tuple[int, object, bool]:
        """Read, update, or delete one collection record by its provider ID."""
        st = self.server.state
        rows = st[coll]
        found = next((o for o in rows if str(o["id"]) == item_id), None)
        if self.command == "GET":
            return (200, {"data": found}, False) if found else (404, {"message": "no"}, False)
        if self.command == "DELETE":
            if found:
                rows.remove(found)
                if coll in VNI_FIELDS and found.get(VNI_FIELDS[coll][0]):
                    st["vni"][VNI_FIELDS[coll][1]]["free"].append(found[VNI_FIELDS[coll][0]])
            return (200 if found else 404), {"data": {}}, False
        if self.command == "PUT" and found:
            found.update(copy.deepcopy(body))
            return 200, {"data": found}, False
        return 404, {"message": "unexpected fake request"}, False

    # -- admin -------------------------------------------------------------

    def _admin(self, path: str, body: dict) -> tuple[int, object]:
        """Handle test-only state, seeding, fault, and shutdown requests."""
        srv = self.server
        st = srv.state
        action = path[len("/__admin/"):]
        if action == "state":
            return 200, st
        if action == "calls":
            return 200, srv.calls
        if action == "calls/clear":
            srv.calls.clear()
        elif action == "reset":
            srv.state = empty_state()
            srv.calls.clear()
        elif action == "seed":
            coll = body["collection"]
            for obj in body.get("objects", []):
                obj = copy.deepcopy(obj)
                if coll in COLLECTIONS:
                    if "id" not in obj:
                        key = COLLECTIONS[coll][2]
                        obj["id"] = st["next_id"][key]
                        st["next_id"][key] += 1
                    st[coll].append(obj)
                elif isinstance(st[coll], dict):  # server_ports, ipam_hosts: {"<id>": [rows]}
                    st[coll].setdefault(str(obj["key"]), []).extend(obj["rows"])
                else:  # servers
                    st[coll].append(obj)
        elif action == "update":
            found = next((o for o in st[body["collection"]] if str(o["id"]) == str(body["id"])), None)
            if found is None:
                return 404, {"message": "no such object"}
            found.update(copy.deepcopy(body.get("fields", {})))
            for key in body.get("drop", []):
                found.pop(key, None)
        elif action == "remove":
            st[body["collection"]] = [o for o in st[body["collection"]] if str(o["id"]) != str(body["id"])]
        elif action == "set":
            st.update(copy.deepcopy(body["values"]))
        elif action == "faults":
            st["faults"].append(copy.deepcopy(body))
        elif action == "faults/clear":
            st["faults"].clear()
        elif action == "shutdown":
            pass
        else:
            return 404, {"message": "unknown admin action"}
        srv.dump()
        return 200, {"ok": True}


class Netris(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, port: int = 0, state_file: str | None = None) -> None:
        """Initialize the HTTP server, request lock, and provider state."""
        super().__init__(("127.0.0.1", port), Handler)
        self.lock = threading.RLock()
        self.state = empty_state()
        self.calls: list[list] = []
        self.state_file = state_file

    def dump(self) -> None:
        """Write a snapshot without credential-bearing fault response bodies."""
        if self.state_file:
            snapshot = copy.deepcopy(self.state)
            for fault in snapshot.get("faults", []):
                fault.pop("body", None)
            Path(self.state_file).write_text(json.dumps(snapshot, indent=1, sort_keys=True))


def main() -> int:
    """Run the fake Netris server using the requested port and state files."""
    global COOKIE
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument("--port-file", required=True, help="file that receives the listening port")
    parser.add_argument("--state-file", help="file that receives a JSON snapshot of the state after each write")
    parser.add_argument("--cookie-file", help="file that holds the session cookie clients must present")
    parser.add_argument("--port", type=int, default=0)
    args = parser.parse_args()
    if args.cookie_file:
        COOKIE = Path(args.cookie_file).read_text(encoding="utf-8").strip()
    server = Netris(args.port, args.state_file)
    server.dump()
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
