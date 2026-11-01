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
  GET  /__admin/calls          the request log [method, path, body]
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

  python fake_netris.py --port-file <file> [--state-file <file>] [--port N]

writes the chosen port to the port file once it listens, and a JSON snapshot of
the state to the state file after every write request.
"""

from __future__ import annotations

import argparse
import copy
import fnmatch
import json
import socket
import struct
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

COOKIE = "fake-session"

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
        size = int(self.headers.get("Content-Length", "0"))
        try:
            value = json.loads(self.rfile.read(size) or b"{}")
        except ValueError:
            return {}
        return value if isinstance(value, dict) else {}

    def _send(self, status: int, payload: object, cookie: bool = False) -> None:
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
        pass

    def _logged_in(self) -> bool:
        """True when the Cookie header carries connect.sid with exactly the issued value."""
        pairs = [part.strip() for part in (self.headers.get("Cookie") or "").split(";")]
        return f"connect.sid={COOKIE}" in pairs

    # -- request entry -----------------------------------------------------

    def _handle(self) -> None:
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
            srv.calls.append([self.command, path, body])
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

    def _take_fault(self, path: str) -> dict | None:
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
        st = self.server.state
        omit = st["list_omit"]  # {object id: [keys the list response leaves out]}
        rows = [{k: v for k, v in o.items() if k not in omit.get(str(o["id"]), [])} for o in st[coll]]
        gone = st["vanish_after_list"]  # id removed right after the V-Net list is served
        if gone is not None and coll == "vnets":
            st["vnets"] = [o for o in st["vnets"] if o["id"] != gone]
            st["vanish_after_list"] = None
        return 200, {"data": rows}, False

    def _next_vni(self, kind: str) -> int:
        counter = self.server.state["vni"][kind]
        if counter["free"]:
            counter["free"].sort()
            return counter["free"].pop(0)
        counter["next"] += 1
        return counter["next"] - 1

    def _create(self, coll: str, body: dict) -> tuple[int, object, bool]:
        st = self.server.state
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
        super().__init__(("127.0.0.1", port), Handler)
        self.lock = threading.RLock()
        self.state = empty_state()
        self.calls: list[list] = []
        self.state_file = state_file

    def dump(self) -> None:
        if self.state_file:
            Path(self.state_file).write_text(json.dumps(self.state, indent=1, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument("--port-file", required=True, help="file that receives the listening port")
    parser.add_argument("--state-file", help="file that receives a JSON snapshot of the state after each write")
    parser.add_argument("--port", type=int, default=0)
    args = parser.parse_args()
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
