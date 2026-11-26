"""Single-zone RFC 2136 protocol double adapted from PR #2526."""

import base64
import secrets
import socketserver
import struct
import threading
from dataclasses import dataclass, field

import dns.flags
import dns.message
import dns.name
import dns.opcode
import dns.rcode
import dns.rdataclass
import dns.rdatatype
import dns.rrset
import dns.tsig


@dataclass
class ServerState:
    zone: dns.name.Name
    key: dns.tsig.Key
    records: dict = field(default_factory=dict)
    updates: list = field(default_factory=list)
    applied_updates: list = field(default_factory=list)
    queries: list = field(default_factory=list)
    transports: list = field(default_factory=list)
    requests: int = 0
    drop_ack: bool = False
    unsigned_update: bool = False
    unsigned_query: bool = False
    authoritative: bool = True
    refuse_updates: bool = False
    answer: list | None = None
    lock: threading.Lock = field(default_factory=threading.Lock)

    def handle(self, wire, transport):
        with self.lock:
            self.requests += 1
            self.transports.append(transport)
            try:
                request = dns.message.from_wire(wire, keyring={self.key.name: self.key})
            except (dns.message.UnknownTSIGKey, dns.tsig.BadSignature, dns.tsig.BadAlgorithm, dns.tsig.BadKey):
                request = dns.message.from_wire(wire, keyring=False)
                request.tsig = None
                response = dns.message.make_response(request)
                response.set_rcode(dns.rcode.NOTAUTH)
                return response.to_wire()
            if not request.had_tsig:
                response = dns.message.make_response(request)
                response.set_rcode(dns.rcode.REFUSED)
                return response.to_wire()
            response = dns.message.make_response(request)
            if request.opcode() == dns.opcode.UPDATE:
                self.updates.append(request)
                rcode = self.update(request)
                response.set_rcode(rcode)
                if self.drop_ack and rcode == dns.rcode.NOERROR and request.update:
                    self.drop_ack = False
                    return None
                unsigned = self.unsigned_update
            else:
                self.queries.append(request)
                self.query(request, response)
                unsigned = self.unsigned_query
            if unsigned:
                response.tsig = None
            return response.to_wire()

    def query(self, request, response):
        if self.authoritative:
            response.flags |= dns.flags.AA
        if self.answer is not None:
            response.answer.extend(self.answer)
            return
        question = request.question[0]
        entry = self.records.get((question.name, question.rdtype))
        if entry:
            ttl, values = entry
            response.answer.append(
                dns.rrset.from_text_list(question.name, ttl, "IN", question.rdtype, sorted(values))
            )

    def update(self, request):
        if len(request.zone) != 1 or request.zone[0].name != self.zone:
            return dns.rcode.NOTAUTH
        if self.refuse_updates:
            return dns.rcode.REFUSED
        for rrset in (*request.prerequisite, *request.update):
            if not rrset.name.is_subdomain(self.zone):
                return dns.rcode.NOTZONE

        for rrset in request.prerequisite:
            entry = self.records.get((rrset.name, rrset.rdtype))
            if rrset.ttl or (rrset.deleting is not None and rrset):
                return dns.rcode.FORMERR
            if rrset.deleting == dns.rdataclass.NONE:
                if rrset.rdtype == dns.rdatatype.ANY:
                    if any(owner == rrset.name for owner, _ in self.records):
                        return dns.rcode.YXDOMAIN
                elif entry is not None:
                    return dns.rcode.YXRRSET
            elif rrset.deleting is None:
                wanted = {rdata.to_text() for rdata in rrset}
                if entry is None or entry[1] != wanted:
                    return dns.rcode.NXRRSET
            else:
                return dns.rcode.FORMERR

        for rrset in request.update:
            owner, rdtype = key = (rrset.name, rrset.rdtype)
            if rrset.deleting is None:
                if rdtype != dns.rdatatype.CNAME and (owner, dns.rdatatype.CNAME) in self.records:
                    continue
                _, values = self.records.get(key, (0, set()))
                self.records[key] = (rrset.ttl, values | {item.to_text() for item in rrset})
            elif rrset.deleting == dns.rdataclass.ANY:
                if rdtype == dns.rdatatype.ANY:
                    for existing in [item for item in self.records if item[0] == owner]:
                        del self.records[existing]
                else:
                    self.records.pop(key, None)
        if request.update:
            self.applied_updates.append(request)
        return dns.rcode.NOERROR

    def seed(self, name, rdtype, *values, ttl=1800):
        self.records[(dns.name.from_text(name), dns.rdatatype.from_text(rdtype))] = (ttl, set(values))


class TCPHandler(socketserver.StreamRequestHandler):
    def handle(self):
        header = self.rfile.read(2)
        if len(header) != 2:
            return
        (length,) = struct.unpack("!H", header)
        reply = self.server.state.handle(self.rfile.read(length), "tcp")
        if reply is not None:
            self.wfile.write(struct.pack("!H", len(reply)) + reply)


class UDPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        wire, sock = self.request
        reply = self.server.state.handle(wire, "udp")
        if reply is not None:
            sock.sendto(reply, self.client_address)


class FakeRFC2136Server:
    def __init__(self, zone, key_name, algorithm="hmac-sha256"):
        secret = secrets.token_bytes(64)
        self.secret = base64.b64encode(secret).decode()
        self.state = ServerState(dns.name.from_text(zone), dns.tsig.Key(key_name, secret, algorithm))

    def __enter__(self):
        tcp = socketserver.TCPServer(("127.0.0.1", 0), TCPHandler)
        try:
            udp = socketserver.UDPServer(("127.0.0.1", 0), UDPHandler)
        except OSError:
            tcp.server_close()
            raise
        self.ports = {"tcp": tcp.server_address[1], "udp": udp.server_address[1]}
        self.servers = [tcp, udp]
        self.threads = []
        for server in self.servers:
            server.state = self.state
            thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
            thread.start()
            self.threads.append(thread)
        return self

    def __exit__(self, *_):
        for server, thread in zip(self.servers, self.threads, strict=True):
            server.shutdown()
            server.server_close()
            thread.join()
