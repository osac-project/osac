"""Run the real Ansible DNS role against a loopback DNS protocol double."""

import json
import os
import subprocess
import sys
from pathlib import Path

import dns.flags
import dns.name
import dns.rdataclass
import dns.rdatatype
import dns.rrset
import pytest

from .rfc2136_fake_server import FakeRFC2136Server

ROOT = Path(__file__).resolve().parents[2]
ZONE = "clusters.example.test."
CLUSTER = "test-cluster"
KEY = "test-update."
MARKER = f"_osac-publish-api.{CLUSTER}.{ZONE}"
ADDRESS = "192.0.2.21"
PLAYBOOK = """\
- name: Exercise the RFC 2136 role
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Call the driver
      ansible.builtin.include_role:
        name: "{{ test_role }}"
        tasks_from: "{{ test_operation }}"
"""


@pytest.fixture
def server():
    with FakeRFC2136Server(ZONE, KEY) as fixture:
        yield fixture


def invoke(
    tmp_path, server, operation, *, success=True, protocol="tcp", algorithm=None, check=False, settings=None, **overrides
):
    playbook = tmp_path / "dns.yml"
    playbook.write_text(PLAYBOOK)
    variables = {
        "test_role": "dns.rfc2136.dns",
        "test_operation": operation,
        "dns_cluster_name": CLUSTER,
        "dns_base_domain": ZONE,
        "dns_endpoint_kind": "api",
        "dns_generation": 1,
        **overrides,
    }
    env = {key: value for key, value in os.environ.items() if not key.startswith("DNS_RFC2136_")}
    env.update(
        ANSIBLE_COLLECTIONS_PATH=str(ROOT / "collections"),
        ANSIBLE_NOCOLOR="1",
        DNS_RFC2136_SERVER="127.0.0.1",
        DNS_RFC2136_PORT=str(server.ports[protocol]),
        DNS_RFC2136_PROTOCOL=protocol,
        DNS_RFC2136_KEY_NAME=KEY,
        DNS_RFC2136_KEY_ALGORITHM=algorithm or server.state.key.algorithm.to_text().removesuffix("."),
        DNS_RFC2136_KEY_SECRET=server.secret,
    )
    env.update(settings or {})
    result = subprocess.run(
        [
            str(Path(sys.executable).with_name("ansible-playbook")),
            "-i", "localhost,", "-c", "local",
            "-e", json.dumps({"ansible_python_interpreter": sys.executable, **variables}),
            *(["--check"] if check else []), str(playbook),
        ],
        cwd=tmp_path,
        env=env,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    output = result.stdout + result.stderr
    for hidden in (server.secret, CLUSTER, MARKER, ADDRESS):
        assert hidden not in output, "DNS role leaked sensitive input"
    assert (result.returncode == 0) is success, output
    if not success:
        assert "RFC 2136" in output, output
    return output


def shape(rrset):
    return (
        rrset.name.to_text(), rrset.rdtype, rrset.deleting, rrset.ttl,
        {item.to_text() for item in rrset},
    )


def test_initialize_requires_owner_absence_and_exact_readback(tmp_path, server):
    invoke(tmp_path, server, "initialize", dns_base_domain=ZONE.upper(), dns_cluster_name=CLUSTER.upper())
    update, = server.state.applied_updates
    prereq, = update.prerequisite
    assert shape(prereq) == (MARKER, dns.rdatatype.ANY, dns.rdataclass.NONE, 0, set())
    assert len(update.update) == 1
    assert shape(update.update[0])[:3] == (MARKER, dns.rdatatype.TXT, None)
    assert shape(update.update[0])[-1] == {'"active:1"'}
    query, = server.state.queries
    assert query.had_tsig and not query.flags & dns.flags.RD
    invoke(tmp_path, server, "initialize")
    assert len(server.state.applied_updates) == 1
    assert not server.state.updates[-1].update


@pytest.mark.parametrize("rdtype,values", [
    ("A", (ADDRESS,)),
    ("TXT", ('"revoked:1"',)),
    ("TXT", ('"active:1"', '"extra"')),
    ("TXT", ('"active:1" "extra"',)),
])
def test_initialize_cannot_overwrite_an_existing_owner(tmp_path, server, rdtype, values):
    server.state.seed(MARKER, rdtype, *values)
    previous = server.state.records.copy()
    invoke(tmp_path, server, "initialize", success=False)
    assert server.state.records == previous
    assert not server.state.applied_updates


@pytest.mark.parametrize("operation,old,new,params", [
    ("initialize", None, 1, {}),
    ("activate_replacement", '"revoked:1"', 2, {"dns_generation": 2, "dns_predecessor_generation": 1}),
])
def test_retry_after_lost_acknowledgment(tmp_path, server, operation, old, new, params):
    if old:
        server.state.seed(MARKER, "TXT", old)
    server.state.drop_ack = True
    invoke(tmp_path, server, operation, protocol="udp", success=False, **params)
    invoke(tmp_path, server, operation, protocol="udp", **params)
    assert server.state.records[(dns.name.from_text(MARKER), dns.rdatatype.TXT)][1] == {f'"active:{new}"'}
    assert len(server.state.applied_updates) == 1
    assert set(server.state.transports) == {"udp"}


def test_activate_requires_the_exact_revoked_predecessor(tmp_path, server):
    params = {"dns_generation": 2, "dns_predecessor_generation": 1}
    for values in [('"active:1"',), ('"revoked:2"',), ('"revoked:1"', '"extra"')]:
        server.state.seed(MARKER, "TXT", *values)
        invoke(tmp_path, server, "activate_replacement", success=False, **params)
    server.state.seed(MARKER, "TXT", '"revoked:1"')
    invoke(tmp_path, server, "activate_replacement", **params)
    update, = server.state.applied_updates
    assert [shape(rr) for rr in update.prerequisite] == [
        (MARKER, dns.rdatatype.TXT, None, 0, {'"revoked:1"'}),
    ]
    assert [shape(rr)[-1] for rr in update.update] == [set(), {'"active:2"'}]


@pytest.mark.parametrize("fault", ["unsigned_update", "unsigned_query", "nonauthoritative", "alias", "synthesized", "refused"])
def test_retry_requires_authenticated_literal_marker_evidence(tmp_path, server, fault):
    server.state.seed(MARKER, "A", ADDRESS)
    if fault == "synthesized":
        server.state.answer = [dns.rrset.from_text(MARKER, 1800, "IN", "TXT", '"active:1"')]
    elif fault == "alias":
        server.state.answer = [dns.rrset.from_text(MARKER, 1800, "IN", "CNAME", ZONE)]
    else:
        server.state.seed(MARKER, "TXT", '"active:1"')
        if fault == "nonauthoritative":
            server.state.authoritative = False
        else:
            setattr(server.state, "refuse_updates" if fault == "refused" else fault, True)
    invoke(tmp_path, server, "initialize", success=False)
    assert not server.state.applied_updates
    if fault in {"unsigned_update", "refused"}:
        assert not server.state.queries


@pytest.mark.parametrize("operation,overrides", [
    ("main", {}), ("create", {}), ("delete", {}),
    ("initialize", {"dns_generation": None}),
    ("initialize", {"dns_generation": True}),
    ("initialize", {"dns_generation": 1.5}),
    ("initialize", {"dns_generation": 2}),
    ("initialize", {"dns_predecessor_generation": 1}),
    ("activate_replacement", {"dns_generation": 3, "dns_predecessor_generation": 1}),
    ("initialize", {"dns_base_domain": "notclusters.example.test.", "dns_zone": ZONE}),
    ("initialize", {"dns_base_domain": r"evil\.clusters.example.test."}),
    ("initialize", {"dns_base_domain": ".".join(["a" * 62] * 4)}),
    ("initialize", {"dns_cluster_name": "bad_name"}),
    ("initialize", {"dns_cluster_name": True}),
    ("initialize", {"dns_cluster_name": 123}),
    ("initialize", {"dns_base_domain": False}),
    ("initialize", {"dns_zone": False}),
    ("initialize", {"dns_zone": ""}),
    ("initialize", {"dns_endpoint_kind": "*"}),
    ("initialize", {"algorithm": "hmac-sha1"}),
    ("initialize", {"algorithm": "hmac-md5"}),
])
def test_invalid_and_markerless_requests_never_reach_dns(tmp_path, server, operation, overrides):
    invoke(tmp_path, server, operation, success=False, **overrides)
    assert server.state.requests == 0


def test_check_mode_validates_without_dns(tmp_path, server):
    invoke(tmp_path, server, "initialize", check=True)
    invoke(tmp_path, server, "initialize", check=True, success=False, dns_generation=0)
    assert server.state.requests == 0


@pytest.mark.parametrize("settings", [
    {"DNS_RFC2136_SERVER": "dns.example.test"},
    {"DNS_RFC2136_SERVER": "fe80::1%eth0"},
    {"DNS_RFC2136_PORT": "65536"},
    {"DNS_RFC2136_PORT": "5.3"},
    {"DNS_RFC2136_PORT": ""},
    {"DNS_RFC2136_PROTOCOL": "http"},
    {"DNS_RFC2136_PROTOCOL": ""},
    {"DNS_RFC2136_KEY_ALGORITHM": ""},
    {"DNS_RFC2136_KEY_NAME": ""},
    {"DNS_RFC2136_KEY_SECRET": ""},
    {"DNS_RFC2136_KEY_SECRET": "not*base64"},
])
def test_invalid_connection_settings_do_not_contact_dns(tmp_path, server, settings):
    invoke(tmp_path, server, "initialize", success=False, settings=settings)
    assert server.state.requests == 0


@pytest.mark.parametrize("credential", ["DNS_RFC2136_KEY_NAME", "DNS_RFC2136_KEY_ALGORITHM", "DNS_RFC2136_KEY_SECRET"])
def test_bad_tsig_never_falls_back(tmp_path, server, credential):
    bad_settings = {
        "DNS_RFC2136_KEY_NAME": "different-key",
        "DNS_RFC2136_KEY_ALGORITHM": "hmac-sha384",
        "DNS_RFC2136_KEY_SECRET": "ZGlmZmVyZW50",
    }
    output = invoke(tmp_path, server, "initialize", success=False, settings={credential: bad_settings[credential]})
    assert "Check DNS_RFC2136_KEY_NAME, DNS_RFC2136_KEY_ALGORITHM" in output
    assert server.state.requests == 1
    assert not server.state.updates and not server.state.queries and not server.state.records


def test_explicit_parent_zone_owns_nested_hosted_cluster_domain(tmp_path, server):
    base = f"hosted.{ZONE}"
    invoke(tmp_path, server, "initialize", dns_base_domain=base, dns_zone=ZONE)
    update, = server.state.applied_updates
    assert update.zone[0].name.to_text() == ZONE
    assert update.update[0].name.to_text() == f"_osac-publish-api.{CLUSTER}.{base}"


@pytest.mark.parametrize("protocol,algorithm", [("tcp", "hmac-sha256"), ("udp", "hmac-sha512")])
def test_publish_exact_rrsets_in_one_signed_update_per_endpoint(tmp_path, protocol, algorithm):
    api = f"api.{CLUSTER}.{ZONE}"
    internal = f"api-int.{CLUSTER}.{ZONE}"
    wildcard = f"*.apps.{CLUSTER}.{ZONE}"
    unrelated = f"other.{CLUSTER}.{ZONE}"
    ingress_marker = f"_osac-publish-ingress.{CLUSTER}.{ZONE}"
    with FakeRFC2136Server(ZONE, KEY, algorithm) as server:
        state = server.state
        state.seed(api, "TXT", '"keep"')
        state.seed(unrelated, "A", "192.0.2.90")
        state.seed(api, "A", "192.0.2.98", "192.0.2.99")
        preserved = {key: value for key, value in state.records.items() if value[1] in ({'"keep"'}, {"192.0.2.90"})}
        invoke(tmp_path, server, "initialize", protocol=protocol)
        invoke(tmp_path, server, "initialize", protocol=protocol, dns_endpoint_kind="ingress")
        for address, ttl in [(ADDRESS, None), (ADDRESS, None), ("192.0.2.22", None), ("192.0.2.22", 42)]:
            start = len(state.applied_updates)
            invoke(tmp_path, server, "publish", protocol=protocol, dns_address=address, dns_ttl=ttl)
            update, = state.applied_updates[start:]
            assert update.had_tsig and update.keyalgorithm.to_text() == algorithm + "."
            assert shape(update.prerequisite[0]) == (MARKER, dns.rdatatype.TXT, None, 0, {'"active:1"'})
            assert {shape(rr)[0] for rr in update.prerequisite[1:]} == {api, internal}
            expected = [
                (owner, dns.rdatatype.A, deletion, record_ttl, values)
                for owner in (api, internal)
                for deletion, record_ttl, values in (
                    (dns.rdataclass.ANY, 0, set()), (None, ttl or 1800, {address})
                )
            ]
            assert [shape(rr) for rr in update.update] == expected
            for owner in (api, internal):
                assert state.records[(dns.name.from_text(owner), dns.rdatatype.A)] == (ttl or 1800, {address})
        start = len(state.applied_updates)
        invoke(tmp_path, server, "publish", protocol=protocol, dns_endpoint_kind="ingress", dns_address=ADDRESS)
        update, = state.applied_updates[start:]
        assert shape(update.prerequisite[0]) == (ingress_marker, dns.rdatatype.TXT, None, 0, {'"active:1"'})
        assert [shape(rr)[0] for rr in update.update] == [wildcard, wildcard]
        assert state.records[(dns.name.from_text(wildcard), dns.rdatatype.A)] == (1800, {ADDRESS})
        assert all(state.records[key] == value for key, value in preserved.items())
        assert len(state.queries) == 2
        assert set(state.transports) == {protocol}


@pytest.mark.parametrize("values", [(), ('"revoked:1"',), ('"active:2"',), ('"active:1"', '"other"')])
def test_stale_or_inexact_marker_cannot_publish(tmp_path, server, values):
    if values:
        server.state.seed(MARKER, "TXT", *values)
    server.state.seed(f"api.{CLUSTER}.{ZONE}", "A", "192.0.2.91")
    previous = server.state.records.copy()
    invoke(tmp_path, server, "publish", dns_address=ADDRESS, success=False)
    assert server.state.records == previous
    update, = server.state.updates
    assert shape(update.prerequisite[0]) == (MARKER, dns.rdatatype.TXT, None, 0, {'"active:1"'})
    assert not server.state.queries


def test_cname_conflict_rejects_the_entire_publication(tmp_path, server):
    server.state.seed(MARKER, "TXT", '"active:1"')
    server.state.seed(f"api-int.{CLUSTER}.{ZONE}", "CNAME", ZONE)
    previous = server.state.records.copy()
    invoke(tmp_path, server, "publish", dns_address=ADDRESS, success=False)
    assert server.state.records == previous
    assert not server.state.applied_updates


def test_unsigned_publication_response_cannot_confirm_success(tmp_path, server):
    server.state.seed(MARKER, "TXT", '"active:1"')
    server.state.unsigned_update = True
    invoke(tmp_path, server, "publish", dns_address=ADDRESS, success=False)
    assert len(server.state.applied_updates) == 1
    assert server.state.requests == 1


@pytest.mark.parametrize("overrides", [
    {"dns_address": None},
    {"dns_address": "2001:db8::1"},
    {"dns_address": "192.0.2.22/32"},
    {"dns_ttl": -1},
    {"dns_ttl": 2**31},
    {"dns_predecessor_generation": 1},
])
def test_invalid_publication_does_not_contact_dns(tmp_path, server, overrides):
    invoke(tmp_path, server, "publish", success=False, **({"dns_address": ADDRESS} | overrides))
    assert server.state.requests == 0
