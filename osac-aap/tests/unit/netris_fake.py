"""A small stateful Netris controller double for the unit tests of the role.

Serves auth, VPC, IPAM allocation/subnet, V-Net, NAT rule, server cluster,
server (hw), server port and IPAM host endpoints in memory. The
VPC gets a ``vni`` and the V-Net a ``vxlanID`` at create time unless the test
turns that off. ``responses`` maps "METHOD /path" to (status, body) overrides.
"""

from __future__ import annotations

import copy
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit


def empty_state() -> dict:
    return {
        "vpcs": [], "allocations": [], "ipam_subnets": [], "vnets": [], "nats": [],
        "clusters": [], "servers": [], "server_ports": {}, "ipam_hosts": {},
        "responses": {}, "list_omit": {}, "get_304": {}, "vanish_after_list": None, "assign_vni": True, "next_vni": 10001, "next_vxlan": 10101,
        "next_id": {"vpc": 101, "alloc": 201, "ipam": 301, "vnet": 401, "nat": 501, "sc": 601},
    }


class Handler(BaseHTTPRequestHandler):
    def _send(self, status: int, payload: object, cookie: bool = False) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        if cookie:
            self.send_header("Set-Cookie", "connect.sid=fake-session; Path=/")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self) -> dict:
        size = int(self.headers.get("Content-Length", "0"))
        try:
            value = json.loads(self.rfile.read(size) or b"{}")
        except ValueError:
            return {}
        return value if isinstance(value, dict) else {}

    def _handle(self) -> None:
        path = urlsplit(self.path).path
        srv = self.server
        body = self._body() if self.command in ("POST", "PUT") else {}
        with srv.lock:
            srv.calls.append((self.command, path, body))
            st = srv.state
            if path == "/api/auth":
                return self._send(200, {"ok": True}, cookie=True)
            override = st["responses"].get(f"{self.command} {path}")
            if override:
                return self._send(*override)
            routes = {
                "/api/v2/vpc": ("vpcs", "vpc"),
                "/api/v2/ipam": ("allocations", "alloc"),
                "/api/v2/ipam/allocation": ("allocations", "alloc"),
                "/api/v2/ipam/subnets": ("ipam_subnets", "ipam"),
                "/api/v2/ipam/subnet": ("ipam_subnets", "ipam"),
                "/api/v2/vnet": ("vnets", "vnet"),
                "/api/v2/nat": ("nats", "nat"),
                "/api/v2/server-cluster": ("clusters", "sc"),
            }
            if self.command == "GET" and path == "/api/v2/hw":
                return self._send(200, {"data": st["servers"]})
            if self.command == "GET" and path == "/api/v2/ports":
                server = parse_qs(urlsplit(self.path).query).get("switchID", [""])[0]
                return self._send(200, {"data": st["server_ports"].get(server, [])})
            if self.command == "GET" and path.startswith("/api/v2/ipam/hosts/"):
                return self._send(200, {"data": st["ipam_hosts"].get(path.rsplit("/", 1)[1], [])})
            if self.command == "GET" and path in routes:
                rows = st[routes[path][0]]
                omit = st["list_omit"]  # {object id: [keys the list response leaves out]}
                rows = [{k: v for k, v in o.items() if k not in omit.get(str(o["id"]), [])} for o in rows]
                gone = st["vanish_after_list"]  # id removed right after the V-Net list is served
                if gone is not None and path == "/api/v2/vnet":
                    st["vnets"] = [o for o in st["vnets"] if o["id"] != gone]
                    st["vanish_after_list"] = None
                return self._send(200, {"data": rows})
            if self.command == "POST" and path in routes:
                coll, key = routes[path]
                new_id = st["next_id"][key]
                st["next_id"][key] += 1
                obj = {**copy.deepcopy(body), "id": new_id}
                if coll == "vpcs" and st["assign_vni"]:
                    obj["vni"] = st["next_vni"]
                    st["next_vni"] += 1
                if coll == "clusters":
                    obj["status"] = {"label": "Active"}
                if coll == "vnets":
                    obj["state"] = "active"
                    if st["assign_vni"]:
                        obj["vxlanID"] = st["next_vxlan"]
                        st["next_vxlan"] += 1
                st[coll].append(obj)
                return self._send(201, {"data": {"id": new_id}})
            parts = path.rsplit("/", 1)
            prefixes = {
                "/api/v2/vpc": "vpcs", "/api/v2/ipam/allocation": "allocations",
                "/api/v2/ipam/subnet": "ipam_subnets", "/api/v2/vnet": "vnets",
                "/api/v2/nat": "nats", "/api/v2/server-cluster": "clusters",
            }
            if len(parts) == 2 and parts[0] in prefixes:
                coll = st[prefixes[parts[0]]]
                found = next((o for o in coll if str(o["id"]) == parts[1]), None)
                if self.command == "GET" and st["get_304"].get(path, 0) > 0:
                    st["get_304"][path] -= 1  # {path: number of GETs answered 304 first}
                    return self._send(304, {})
                if self.command == "GET":
                    return self._send(200 if found else 404, {"data": found} if found else {"message": "no"})
                if self.command == "DELETE":
                    if found:
                        coll.remove(found)
                    return self._send(200 if found else 404, {"data": {}})
                if self.command == "PUT" and found:
                    found.update(copy.deepcopy(body))
                    return self._send(200, {"data": found})
            self._send(404, {"message": "unexpected fixture request"})

    do_GET = do_POST = do_PUT = do_DELETE = _handle  # noqa: N815

    def log_message(self, *args) -> None:
        pass


class Netris(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self) -> None:
        super().__init__(("127.0.0.1", 0), Handler)
        self.lock = threading.RLock()
        self.state = empty_state()
        self.calls: list[tuple[str, str, dict]] = []
        threading.Thread(target=self.serve_forever, daemon=True).start()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.server_port}"

    def writes(self) -> list[tuple[str, str]]:
        return [(m, p) for m, p, _ in self.calls if m in ("POST", "PUT", "DELETE") and p != "/api/auth"]

    def bodies(self, method: str, path: str) -> list[dict]:
        return [b for m, p, b in self.calls if (m, p) == (method, path)]
