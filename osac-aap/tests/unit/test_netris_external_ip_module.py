import importlib.util
import io
import json
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest

COLLECTIONS_PATH = Path(__file__).resolve().parents[2] / "collections"
MODULE_PATH = (
    COLLECTIONS_PATH
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "modules"
    / "netris_external_ip.py"
)
sys.path.insert(0, str(COLLECTIONS_PATH))
sys.path.insert(
    0,
    str(
        COLLECTIONS_PATH
        / "ansible_collections"
        / "osac"
        / "templates"
        / "plugins"
        / "module_utils"
    ),
)

import ansible.module_utils.basic as ansible_basic  # noqa: E402

from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
    netris_external_ip as netris_eip,
)

RESOURCE_UID = "11111111-1111-4111-8111-111111111111"
COOKIE = "s%3Asecret-cookie-value"


def load_module():
    spec = importlib.util.spec_from_file_location("netris_external_ip_module", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def make_resource():
    return {
        "metadata": {
            "name": "eip-test",
            "namespace": "osac",
            "uid": RESOURCE_UID,
            "generation": 3,
            "labels": {"osac.openshift.io/externalip-uuid": "44444444-4444-4444-8444-444444444444"},
            "annotations": {
                "osac.openshift.io/tenant": "tenant-a",
                "osac.openshift.io/externalip-contract-version": "v1",
            },
        },
        "spec": {"pool": "22222222-2222-4222-8222-222222222222"},
    }


def make_pool():
    return {
        "fulfillment_id": "22222222-2222-4222-8222-222222222222",
        "kubernetes_uid": "33333333-3333-4333-8333-333333333333",
        "tenant_id": "tenant-a",
        "cidr": "10.20.0.0/24",
        "phase": "Ready",
    }


def module_args(**overrides):
    args = {
        "operation": "external_ip.allocate",
        "resource": make_resource(),
        "pool": make_pool(),
        "controller_url": "https://netris.example",
        "session_cookie": COOKIE,
        "site_id": 1,
        "tenant_id": 7,
    }
    args.update(overrides)
    return args


class StubNetrisClient:
    def __init__(self, records=(), create_error=None, delete_error=None):
        self.records = list(records)
        self.create_error = create_error
        self.delete_error = delete_error
        self.created = []
        self.deleted = []

    def list_subnets(self):
        return self.records

    def create_subnet(self, name, prefix, site_id, tenant_id):
        self.created.append((name, prefix, site_id, tenant_id))
        if self.create_error is not None:
            raise self.create_error

    def delete_subnet(self, subnet_id):
        self.deleted.append(subnet_id)
        if self.delete_error is not None:
            raise self.delete_error


def run_main(monkeypatch, capsys, args, client):
    loaded = load_module()
    monkeypatch.setattr(netris_eip.time, "sleep", lambda *_: None)
    monkeypatch.setattr(
        ansible_basic,
        "_ANSIBLE_ARGS",
        json.dumps(
            {"ANSIBLE_MODULE_ARGS": args, "ANSIBLE_MODULE_CONSTRAINTS": {}}
        ).encode(),
    )
    monkeypatch.setattr(netris_eip, "NetrisSubnetClient", lambda *a, **k: client)
    with pytest.raises(SystemExit):
        loaded.main()
    out = capsys.readouterr().out
    return json.loads(out.strip().splitlines()[-1])


def test_argument_spec_marks_session_cookie_no_log():
    loaded = load_module()

    assert loaded.ARGUMENT_SPEC["session_cookie"].get("no_log") is True
    assert loaded.ARGUMENT_SPEC["timeout"]["default"] == 30
    assert loaded.ARGUMENT_SPEC["validate_certs"]["default"] is True


def test_check_mode_validates_inputs_without_any_provider_write(monkeypatch, capsys):
    client = StubNetrisClient(records=[])
    args = module_args(_ansible_check_mode=True)

    result = run_main(monkeypatch, capsys, args, client)

    assert result["changed"] is False
    assert result["address"] == ""
    assert client.created == []
    assert client.deleted == []


def test_successful_allocation_returns_changed_and_address(monkeypatch, capsys):
    record = {
        "id": 101,
        "name": f"osac-eip-{RESOURCE_UID}",
        "prefix": "10.20.0.5/32",
        "purpose": "nat",
        "site_id": 1,
        "tenant_id": 7,
    }
    client = StubNetrisClient(records=[])

    class VerifyingClient(StubNetrisClient):
        def list_subnets(self):
            if self.created:
                return [{**record, "prefix": f"{self.created[-1][1].split('/')[0]}/32"}]
            return []

    result = run_main(monkeypatch, capsys, module_args(), VerifyingClient())

    assert result["changed"] is True
    assert result["address"] == "10.20.0.1"


def test_provider_failure_is_sanitized(monkeypatch, capsys):
    client = StubNetrisClient(
        records=[],
        create_error=netris_eip.NetrisExternalIPError(
            "Netris IPAM subnet create failed", category="provider_error", http_status=400
        ),
    )

    result = run_main(monkeypatch, capsys, module_args(), client)

    assert result["failed"] is True
    assert result["msg"] == "Netris ExternalIP external_ip.allocate failed (provider_error, HTTP 400)"
    assert COOKIE not in capsys.readouterr().out


def test_unknown_response_shape_fails_the_module(monkeypatch, capsys):
    client = StubNetrisClient(records=["garbage"])

    result = run_main(monkeypatch, capsys, module_args(), client)

    assert result["failed"] is True
    assert result.get("address", "") == ""


def test_release_module_returns_empty_address(monkeypatch, capsys):
    resource = make_resource()
    resource["metadata"]["deletionTimestamp"] = "2026-10-19T00:00:00Z"
    args = module_args(
        operation="external_ip.release",
        resource=resource,
        _ansible_check_mode=True,
    )

    result = run_main(monkeypatch, capsys, args, StubNetrisClient(records=[]))

    assert result["changed"] is False
    assert result["address"] == ""


def test_transport_configuration_is_passed_to_fetch_url(monkeypatch):
    captured = {}

    def fake_fetch_url(module, url, **kwargs):
        captured["url"] = url
        captured["module"] = module
        captured.update(kwargs)
        return (
            io.BytesIO(json.dumps({"data": []}).encode()),
            {"status": 200},
        )

    monkeypatch.setattr(netris_eip, "fetch_url", fake_fetch_url)
    module_stub = SimpleNamespace(params={})
    client = netris_eip.NetrisSubnetClient(
        module_stub,
        "https://netris.example",
        COOKIE,
        timeout=45,
        validate_certs=False,
        ca_path="/tmp/netris-ca.pem",
    )

    records = client.list_subnets()

    assert records == []
    assert captured["url"] == "https://netris.example/api/v2/ipam/subnets"
    assert captured["unredirected_headers"]["Cookie"] == f"connect.sid={COOKIE}"
    assert captured["timeout"] == 45
    assert captured["ca_path"] == "/tmp/netris-ca.pem"
    assert module_stub.params["validate_certs"] is False


def test_session_cookie_never_follows_redirects(monkeypatch):
    captured = {}

    def fake_fetch_url(module, url, **kwargs):
        captured.update(kwargs)
        return (io.BytesIO(json.dumps({"data": []}).encode()), {"status": 200})

    monkeypatch.setattr(netris_eip, "fetch_url", fake_fetch_url)
    client = netris_eip.NetrisSubnetClient(
        SimpleNamespace(params={}), "https://netris.example", COOKIE
    )

    client.list_subnets()

    assert captured["unredirected_headers"]["Cookie"] == f"connect.sid={COOKIE}"
