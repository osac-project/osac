"""In-memory RFC 2136 DNS server for testing the dns.rfc2136.dns driver.

The server verifies TSIG signatures, applies RFC 2136 prerequisites and
updates to one zone, answers queries from the same records, and signs its
responses, so the real ``community.general.nsupdate`` module talks to it over
TCP or UDP as it would to BIND. It is not a general DNS server: there is no
wildcard expansion in answers, no zone transfer, and one zone only.
"""

from __future__ import annotations

import base64
import os
import socket
import socketserver
import struct
import threading
from dataclasses import dataclass, field

import dns.flags
import dns.message
import dns.name
import dns.opcode
import dns.rcode
import dns.rdata
import dns.rdataclass
import dns.rdatatype
import dns.rrset
import dns.tsig
import dns.tsigkeyring
import dns.update

SOA_TEXT = "ns1.{zone} hostmaster.{zone} 1 3600 600 86400 60"


@dataclass
class ServerState:
    zone: dns.name.Name
    keyring: dict
    # Refuse record changes but answer prerequisite-only checks, like a BIND
    # update-policy that lets the key update the zone but not these records.
    refuse_updates: bool = False
    # Refuse every UPDATE, including prerequisite-only checks, like BIND when
    # the key may not update the zone at all.
    refuse_all_updates: bool = False
    # Answer queries with this response code (for example REFUSED, like a BIND
    # allow-query that does not cover the key, or SERVFAIL) but accept updates.
    query_rcode: int | None = None
    # (owner name, rdtype) -> (ttl, set of rdata text)
    records: dict = field(default_factory=dict)
    # Every UPDATE that reached the update section, after TSIG and zone checks.
    applied_updates: list = field(default_factory=list)
    # Every request received, including rejected ones.
    requests: int = 0
    lock: threading.Lock = field(default_factory=threading.Lock)


def _key_error_response(wire: bytes, state: ServerState, tsig_error: int) -> bytes:
    """Build a NOTAUTH reply carrying a TSIG error, as BIND does."""
    query = dns.message.from_wire(wire, keyring=False)
    # make_response() would sign with the unknown key, so build it by hand.
    response = query.__class__(id=query.id)
    response.flags = dns.flags.QR | (query.flags & dns.flags.RD)
    response.set_opcode(query.opcode())
    response.sections[0] = list(query.sections[0])
    response.set_rcode(dns.rcode.NOTAUTH)
    # The client checks the TSIG error before the MAC, so any key works here.
    key = dns.tsig.Key(query.keyname, b"\0" * 32, query.keyalgorithm)
    response.use_tsig(key, tsig_error=tsig_error)
    return response.to_wire()


class _Handler:
    def __init__(self, state: ServerState):
        self.state = state

    def handle(self, wire: bytes) -> bytes:
        state = self.state
        with state.lock:
            state.requests += 1
            try:
                message = dns.message.from_wire(wire, keyring=state.keyring)
            except dns.message.UnknownTSIGKey:
                return _key_error_response(wire, state, dns.rcode.BADKEY)
            except (dns.tsig.BadSignature, dns.tsig.BadAlgorithm, dns.tsig.BadKey):
                return _key_error_response(wire, state, dns.rcode.BADSIG)

            response = dns.message.make_response(message)
            if message.opcode() == dns.opcode.UPDATE:
                response.set_rcode(self._update(message))
            else:
                self._query(message, response)
            return response.to_wire()

    def _soa(self) -> dns.rrset.RRset:
        zone = self.state.zone.to_text()
        return dns.rrset.from_text(self.state.zone, 60, "IN", "SOA", SOA_TEXT.format(zone=zone))

    def _query(self, message, response) -> None:
        question = message.question[0]
        name, rdtype = question.name, question.rdtype
        if self.state.query_rcode is not None:
            response.set_rcode(self.state.query_rcode)
            return
        if not name.is_subdomain(self.state.zone):
            response.set_rcode(dns.rcode.REFUSED)
            return
        if name == self.state.zone and rdtype == dns.rdatatype.SOA:
            response.answer.append(self._soa())
            return
        entry = self.state.records.get((name, rdtype))
        if entry:
            ttl, values = entry
            response.answer.append(dns.rrset.from_text_list(name, ttl, "IN", rdtype, sorted(values)))
            return
        if not any(owner == name for owner, _ in self.state.records):
            response.set_rcode(dns.rcode.NXDOMAIN)
        response.authority.append(self._soa())

    def _update(self, message) -> int:
        state = self.state
        if len(message.zone) != 1 or message.zone[0].name != state.zone:
            return dns.rcode.NOTAUTH
        if state.refuse_all_updates:
            return dns.rcode.REFUSED
        for rrset in list(message.prerequisite) + list(message.update):
            if not rrset.name.is_subdomain(state.zone):
                return dns.rcode.NOTZONE

        for rrset in message.prerequisite:
            entry = state.records.get((rrset.name, rrset.rdtype))
            if rrset.deleting == dns.rdataclass.ANY:
                # "RRset exists (value independent)"
                if entry is None:
                    return dns.rcode.NXRRSET
            elif rrset.deleting is None:
                # "RRset exists (value dependent)": the whole RRset must match.
                wanted = {rdata.to_text() for rdata in rrset}
                if entry is None or entry[1] != wanted:
                    return dns.rcode.NXRRSET
            else:
                return dns.rcode.FORMERR

        if not message.update:
            return dns.rcode.NOERROR
        if state.refuse_updates:
            return dns.rcode.REFUSED

        for rrset in message.update:
            key = (rrset.name, rrset.rdtype)
            if rrset.deleting is None:
                ttl, values = state.records.get(key, (rrset.ttl, set()))
                state.records[key] = (rrset.ttl, values | {rdata.to_text() for rdata in rrset})
            elif rrset.deleting == dns.rdataclass.ANY:
                if rrset.rdtype == dns.rdatatype.ANY:
                    for owner, rdtype in list(state.records):
                        if owner == rrset.name:
                            del state.records[(owner, rdtype)]
                else:
                    state.records.pop(key, None)
            elif rrset.deleting == dns.rdataclass.NONE:
                if key in state.records:
                    ttl, values = state.records[key]
                    values = values - {rdata.to_text() for rdata in rrset}
                    if values:
                        state.records[key] = (ttl, values)
                    else:
                        del state.records[key]
        state.applied_updates.append(message.to_text())
        return dns.rcode.NOERROR


class _TCPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            header = self._recv_exact(2)
            if not header:
                return
            (length,) = struct.unpack("!H", header)
            wire = self._recv_exact(length)
            if not wire:
                return
            reply = self.server.dns_handler.handle(wire)
            self.request.sendall(struct.pack("!H", len(reply)) + reply)

    def _recv_exact(self, length):
        data = b""
        while len(data) < length:
            chunk = self.request.recv(length - len(data))
            if not chunk:
                return b""
            data += chunk
        return data


class _UDPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        wire, sock = self.request
        sock.sendto(self.server.dns_handler.handle(wire), self.client_address)


class _TCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class _UDPServer(socketserver.ThreadingUDPServer):
    daemon_threads = True


class FakeRFC2136Server:
    """TCP and UDP RFC 2136 server on 127.0.0.1, sharing one port."""

    def __init__(self, zone: str, key_name: str, secret: str, algorithm: str = "hmac-sha256"):
        keyring = {dns.name.from_text(key_name): dns.tsig.Key(key_name, secret, algorithm)}
        self.state = ServerState(zone=dns.name.from_text(zone), keyring=keyring)
        handler = _Handler(self.state)
        self._tcp = _TCPServer(("127.0.0.1", 0), _TCPHandler)
        self.port = self._tcp.server_address[1]
        self._udp = _UDPServer(("127.0.0.1", self.port), _UDPHandler)
        self._tcp.dns_handler = handler
        self._udp.dns_handler = handler
        self._threads = [
            threading.Thread(target=server.serve_forever, daemon=True) for server in (self._tcp, self._udp)
        ]

    def __enter__(self):
        for thread in self._threads:
            thread.start()
        return self

    def __exit__(self, *exc):
        for server in (self._tcp, self._udp):
            server.shutdown()
            server.server_close()

    def records(self) -> dict:
        """Return {(fqdn, type): (ttl, sorted values)} with text keys."""
        with self.state.lock:
            return {
                (name.to_text(), dns.rdatatype.to_text(rdtype)): (ttl, sorted(values))
                for (name, rdtype), (ttl, values) in self.state.records.items()
            }

    def add_record(self, fqdn: str, rdtype: str, ttl: int, value: str) -> None:
        with self.state.lock:
            self.state.records[(dns.name.from_text(fqdn), dns.rdatatype.from_text(rdtype))] = (ttl, {value})


def free_port() -> int:
    """Return a TCP port on 127.0.0.1 that nothing listens on."""
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def random_secret() -> str:
    return base64.b64encode(os.urandom(32)).decode()
