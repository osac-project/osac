#!/usr/bin/python
"""Manage generation-guarded CaaS endpoint DNS on an RFC 2136 primary."""

from __future__ import annotations

import base64
import ipaddress
import re
from typing import NamedTuple

from ansible.module_utils.basic import AnsibleModule

try:
    import dns.exception
    import dns.flags
    import dns.message
    import dns.name
    import dns.query
    import dns.rcode
    import dns.rdataclass
    import dns.rdatatype
    import dns.tsig
    import dns.update
except ImportError:
    HAS_DNSPYTHON = False
else:
    HAS_DNSPYTHON = True


DOCUMENTATION = r"""
---
module: endpoint_dns
short_description: Manage generation-guarded CaaS endpoint DNS
version_added: "1.0.0"
author:
  - OSAC project
description:
  - Initializes or advances endpoint TXT markers and replaces endpoint A RRsets using signed RFC 2136 transactions.
  - The caller must own the persisted endpoint identity and generation.
requirements:
  - dnspython
options:
  operation:
    description: Lifecycle operation to perform.
    type: str
    choices: [initialize, activate_replacement, publish]
    required: true
  endpoint_kind:
    description: Selects the managed marker and A owners.
    type: str
    choices: [api, ingress]
    required: true
  cluster_name:
    description: HostedCluster name as one DNS label.
    type: raw
    required: true
  base_domain:
    description: HostedCluster DNS base domain.
    type: raw
    required: true
  generation:
    description: Persisted positive decimal generation.
    type: raw
    required: true
  predecessor_generation:
    description: Authorized predecessor, required only for replacement activation.
    type: raw
  zone:
    description: Authoritative update zone. If unset, use O(base_domain).
    type: raw
  address:
    description: IPv4 address, required only for publication.
    type: str
  ttl:
    description: TTL in seconds for publication only. Defaults to 1800 seconds.
    type: raw
  server:
    description: Literal IPv4 or IPv6 address of the writable primary.
    type: str
    required: true
  port:
    description: DNS port on the writable primary.
    type: raw
    default: 53
  protocol:
    description: Transport for updates and verification.
    type: str
    choices: [tcp, udp]
    default: tcp
  key_name:
    description: TSIG key name in DNS name form.
    type: str
    required: true
  key_algorithm:
    description: SHA-2 TSIG algorithm.
    type: str
    choices: [hmac-sha224, hmac-sha256, hmac-sha384, hmac-sha512]
    default: hmac-sha256
  key_secret:
    description: Base64-encoded TSIG key material.
    type: str
    required: true
attributes:
  check_mode:
    description: Validates input without contacting DNS.
    support: full
"""

EXAMPLES = r"""
- name: Publish the persisted API endpoint
  dns.rfc2136.endpoint_dns:
    operation: publish
    endpoint_kind: api
    cluster_name: example-cluster
    base_domain: clusters.example.test
    generation: 1
    address: 192.0.2.10
    server: 192.0.2.53
    key_name: osac-update
    key_secret: "{{ lookup('ansible.builtin.env', 'DNS_RFC2136_KEY_SECRET') }}"
  no_log: true
"""

RETURN = r"""
failure:
  description: Fixed diagnostic category without DNS data.
  type: str
  returned: on failure
"""

LABEL = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?")
DECIMAL = re.compile(r"0|[1-9][0-9]*")
# The longer "revoked:" prefix must also fit in a 255-byte TXT string.
MAX_GENERATION = 10**247 - 1
TIMEOUT = 5

ARGUMENT_SPEC = {
    "operation": {"type": "str", "choices": ["initialize", "activate_replacement", "publish"], "required": True},
    "endpoint_kind": {"type": "str", "choices": ["api", "ingress"], "required": True},
    "cluster_name": {"type": "raw", "required": True},
    "base_domain": {"type": "raw", "required": True},
    "generation": {"type": "raw", "required": True},
    "predecessor_generation": {"type": "raw"},
    "zone": {"type": "raw"},
    "address": {"type": "str"},
    "ttl": {"type": "raw"},
    "server": {"type": "str", "required": True},
    "port": {"type": "raw", "default": 53},
    "protocol": {"type": "str", "choices": ["tcp", "udp"], "default": "tcp"},
    "key_name": {"type": "str", "required": True},
    "key_algorithm": {
        "type": "str",
        "choices": ["hmac-sha224", "hmac-sha256", "hmac-sha384", "hmac-sha512"],
        "default": "hmac-sha256",
    },
    "key_secret": {"type": "str", "required": True, "no_log": True},
}


class DriverError(Exception):
    """Carries only a static diagnostic category."""


class EndpointRequest(NamedTuple):
    operation: str
    generation: int
    predecessor: int | None
    zone: dns.name.Name
    marker: dns.name.Name
    owners: tuple[dns.name.Name, ...]
    address: str | None
    ttl: int | None
    server: str
    port: int
    protocol: str
    key: dns.tsig.Key

    @property
    def active(self) -> str:
        return f'"active:{self.generation}"'


def parse_decimal(value, low, high, failure="invalid_input") -> int:
    if isinstance(value, str) and len(value) <= len(str(high)) and DECIMAL.fullmatch(value):
        value = int(value)
    if type(value) is not int or not low <= value <= high:
        raise DriverError(failure)
    return value


def dns_name(value, failure="invalid_name") -> dns.name.Name:
    if not isinstance(value, str):
        raise DriverError(failure)
    labels = value.removesuffix(".").split(".")
    if len(value) > 254 or any(not LABEL.fullmatch(label) for label in labels):
        raise DriverError(failure)
    try:
        return dns.name.from_text(value.lower())
    except dns.exception.DNSException:
        raise DriverError(failure) from None


def parse_request(params: dict) -> EndpointRequest:
    operation = params["operation"]
    generation = parse_decimal(params["generation"], 1, MAX_GENERATION)
    predecessor = params["predecessor_generation"]
    address, ttl = params["address"], params["ttl"]
    if operation == "activate_replacement":
        predecessor = parse_decimal(predecessor, 1, MAX_GENERATION)
        if generation != predecessor + 1:
            raise DriverError("invalid_input")
    elif predecessor is not None or (operation == "initialize" and generation != 1):
        raise DriverError("invalid_input")
    if operation == "publish":
        try:
            address = str(ipaddress.IPv4Address(address))
        except (ValueError, TypeError):
            raise DriverError("invalid_address") from None
        ttl = 1800 if ttl is None else parse_decimal(ttl, 0, 2**31 - 1, "invalid_ttl")
    elif address is not None or ttl is not None:
        raise DriverError("invalid_input")

    cluster = params["cluster_name"]
    if not isinstance(cluster, str) or not LABEL.fullmatch(cluster):
        raise DriverError("invalid_name")
    base = dns_name(params["base_domain"])
    zone = base if params["zone"] is None else dns_name(params["zone"])
    kind = params["endpoint_kind"]
    prefixes = ((b"api",), (b"api-int",)) if kind == "api" else ((b"*", b"apps"),)
    suffix = (cluster.lower().encode("ascii"), *base.labels)
    try:
        marker = dns.name.Name((f"_osac-publish-{kind}".encode("ascii"), *suffix))
        owners = tuple(dns.name.Name((*prefix, *suffix)) for prefix in prefixes)
    except dns.exception.DNSException:
        raise DriverError("invalid_name") from None
    if not all(name.is_subdomain(zone) for name in (marker, *owners)):
        raise DriverError("out_of_zone")

    try:
        server = str(ipaddress.ip_address(params["server"]))
        if "%" in server:
            raise ValueError
    except ValueError:
        raise DriverError("invalid_server") from None
    port = parse_decimal(params["port"], 1, 65535, "invalid_port")
    key_name = dns_name(params["key_name"], "invalid_key")
    try:
        secret = base64.b64decode(params["key_secret"], validate=True)
    except ValueError:
        raise DriverError("invalid_key") from None
    if not secret:
        raise DriverError("invalid_key")
    key = dns.tsig.Key(key_name, secret, params["key_algorithm"])
    return EndpointRequest(
        operation, generation, predecessor, zone, marker, owners, address, ttl, server, port, params["protocol"], key
    )


def new_update(request: EndpointRequest) -> dns.update.Update:
    return dns.update.Update(request.zone, keyring=request.key, keyalgorithm=request.key.algorithm)


def build_update(request: EndpointRequest) -> dns.update.Update:
    message = new_update(request)
    if request.operation == "initialize":
        message.absent(request.marker)
        message.add(request.marker, 1800, "TXT", request.active)
    elif request.operation == "activate_replacement":
        message.present(request.marker, "TXT", f'"revoked:{request.predecessor}"')
        message.replace(request.marker, 1800, "TXT", request.active)
    else:
        message.present(request.marker, "TXT", request.active)
        for owner in request.owners:
            # An existing CNAME can silently suppress an A addition.
            message.absent(owner, "CNAME")
            message.replace(owner, request.ttl, "A", request.address)
    return message


def exchange(message: dns.message.Message, request: EndpointRequest) -> dns.message.Message:
    transport = dns.query.tcp if request.protocol == "tcp" else dns.query.udp
    try:
        response = transport(message, request.server, port=request.port, timeout=TIMEOUT)
    except (
        dns.message.UnknownTSIGKey,
        dns.tsig.BadAlgorithm,
        dns.tsig.BadKey,
        dns.tsig.BadSignature,
        dns.tsig.BadTime,
        dns.tsig.PeerError,
    ):
        raise DriverError("authentication") from None
    except (OSError, EOFError, dns.exception.Timeout):
        raise DriverError("transport") from None
    except (dns.exception.DNSException, ValueError):
        raise DriverError("invalid_response") from None
    if not response.had_tsig or response.keyname != request.key.name or response.keyalgorithm != request.key.algorithm:
        raise DriverError("authentication")
    if response.flags & dns.flags.TC:
        raise DriverError("invalid_response")
    return response


def require_success(response: dns.message.Message) -> None:
    rcode = response.rcode()
    if rcode != dns.rcode.NOERROR:
        failure = {
            dns.rcode.REFUSED: "refused",
            dns.rcode.NOTAUTH: "zone",
            dns.rcode.NOTZONE: "zone",
            dns.rcode.YXDOMAIN: "prerequisite",
            dns.rcode.YXRRSET: "prerequisite",
            dns.rcode.NXRRSET: "prerequisite",
        }.get(rcode, "invalid_response")
        raise DriverError(failure)


def confirm_active(request: EndpointRequest, *, require_literal: bool) -> None:
    query = dns.message.make_query(request.marker, "TXT", flags=0)
    query.use_tsig(request.key, algorithm=request.key.algorithm)
    response = exchange(query, request)
    require_success(response)
    if not response.flags & dns.flags.AA or len(response.answer) != 1:
        raise DriverError("verification")
    rrset = response.answer[0]
    if (
        rrset.name != request.marker
        or rrset.rdtype != dns.rdatatype.TXT
        or rrset.rdclass != dns.rdataclass.IN
        or len(rrset) != 1
        or rrset[0].strings != (f"active:{request.generation}".encode("ascii"),)
    ):
        raise DriverError("verification")
    if require_literal:
        # Unlike a query answer, a prerequisite cannot match a synthesized wildcard.
        proof = new_update(request)
        proof.present(request.marker, "TXT", request.active)
        require_success(exchange(proof, request))


def apply_request(request: EndpointRequest) -> bool:
    response = exchange(build_update(request), request)
    retry_code = {
        "initialize": dns.rcode.YXDOMAIN,
        "activate_replacement": dns.rcode.NXRRSET,
    }.get(request.operation)
    if response.rcode() == retry_code:
        confirm_active(request, require_literal=True)
        return False
    require_success(response)
    if request.operation != "publish":
        confirm_active(request, require_literal=False)
    return True


def main() -> None:
    module = AnsibleModule(argument_spec=ARGUMENT_SPEC, supports_check_mode=True)
    try:
        if not HAS_DNSPYTHON:
            raise DriverError("missing_dependency")
        request = parse_request(module.params)
        changed = False if module.check_mode else apply_request(request)
    except DriverError as error:
        module.fail_json(msg="RFC 2136 endpoint operation failed.", failure=error.args[0])
    module.exit_json(changed=changed)


if __name__ == "__main__":
    main()
