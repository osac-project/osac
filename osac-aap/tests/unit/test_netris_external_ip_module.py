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
TEST_COOKIE = "s%3Asecret-cookie-value"


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
        "session_cookie": TEST_COOKIE,
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


def test_argument_spec_marks_only_string_secrets_no_log():
    loaded = load_module()

    assert loaded.ARGUMENT_SPEC["controller_url"].get("no_log") is True
    assert loaded.ARGUMENT_SPEC["session_cookie"].get("no_log") is True
    for name in ("resource", "pool"):
        assert loaded.ARGUMENT_SPEC[name].get("no_log") is None, name


def test_module_construction_disables_invocation_logging(monkeypatch, capsys):
    """Regression: construction-time invocation logging exposed the snapshots.

    ansible-core's AnsibleModule logs non-no_log arguments via _log_invocation
    at construction, before _redacted_invocation() runs. Module-level no_log
    gates _log_invocation and log() while leaving the no_log_values censor
    set — and therefore the returned address — untouched.
    """
    loaded = load_module()
    captured = {}
    real_ansible_module = loaded.AnsibleModule

    def capturing_ansible_module(*args, **kwargs):
        captured.update(kwargs)
        return real_ansible_module(*args, **kwargs)

    monkeypatch.setattr(loaded, "AnsibleModule", capturing_ansible_module)
    monkeypatch.setattr(
        ansible_basic,
        "_ANSIBLE_ARGS",
        json.dumps(
            {
                "ANSIBLE_MODULE_ARGS": module_args(_ansible_check_mode=True),
                "ANSIBLE_MODULE_CONSTRAINTS": {},
            }
        ).encode(),
    )

    with pytest.raises(SystemExit):
        loaded.main()

    assert captured.get("no_log") is True


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


def test_allocation_returns_address_intact_when_resource_leaf_values_collide(
    monkeypatch, capsys
):
    """Regression: argspec no_log on dict args censored the returned address.

    ansible-core adds every leaf value of a no_log'd dict to the module
    censor set. A resource snapshot with generation 1 contributed the
    censor string "1", which mangled the returned 192.0.2.1 address into
    "********92.0.2.********".
    """
    resource = make_resource()
    resource["metadata"]["generation"] = 1
    pool = make_pool()
    pool["cidr"] = "192.0.2.0/30"
    record = {
        "id": 101,
        "name": f"osac-eip-{RESOURCE_UID}",
        "prefix": "192.0.2.1/32",
        "purpose": "nat",
        "site_id": 1,
        "tenant_id": 7,
    }

    class VerifyingClient(StubNetrisClient):
        def list_subnets(self):
            if self.created:
                return [{**record, "prefix": f"{self.created[-1][1].split('/')[0]}/32"}]
            return []

    result = run_main(
        monkeypatch, capsys, module_args(resource=resource, pool=pool), VerifyingClient()
    )

    assert result["changed"] is True
    assert result["address"] == "192.0.2.1"


def test_invocation_redacts_provider_inputs(monkeypatch, capsys):
    record = {
        "id": 101,
        "name": f"osac-eip-{RESOURCE_UID}",
        "prefix": "10.20.0.5/32",
        "purpose": "nat",
        "site_id": 1,
        "tenant_id": 7,
    }

    class VerifyingClient(StubNetrisClient):
        def list_subnets(self):
            if self.created:
                return [{**record, "prefix": f"{self.created[-1][1].split('/')[0]}/32"}]
            return []

    result = run_main(monkeypatch, capsys, module_args(), VerifyingClient())
    module_args_result = result["invocation"]["module_args"]

    for name in ("resource", "pool", "controller_url", "session_cookie"):
        assert module_args_result[name] == "VALUE_SPECIFIED_IN_NO_LOG_PARAMETER", name
    assert module_args_result["operation"] == "external_ip.allocate"
    assert module_args_result["site_id"] == 1
    assert module_args_result["tenant_id"] == 7


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
    assert TEST_COOKIE not in capsys.readouterr().out


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
        TEST_COOKIE,
        timeout=45,
        validate_certs=False,
        ca_path="/tmp/netris-ca.pem",
    )

    records = client.list_subnets()

    assert records == []
    assert captured["url"] == "https://netris.example/api/v2/ipam/subnets"
    assert captured["unredirected_headers"]["Cookie"] == f"connect.sid={TEST_COOKIE}"
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
        SimpleNamespace(params={}), "https://netris.example", TEST_COOKIE
    )

    client.list_subnets()

    assert captured["unredirected_headers"]["Cookie"] == f"connect.sid={TEST_COOKIE}"


def _client_with_response(monkeypatch, status, payload=b""):
    captured = {}

    def fake_fetch_url(module, url, **kwargs):
        captured["url"] = url
        captured["method"] = kwargs.get("method")
        captured["data"] = kwargs.get("data")
        return (io.BytesIO(payload), {"status": status})

    monkeypatch.setattr(netris_eip, "fetch_url", fake_fetch_url)
    return (
        netris_eip.NetrisSubnetClient(SimpleNamespace(params={}), "https://n", TEST_COOKIE),
        captured,
    )


def test_create_subnet_sends_the_nat_reservation_body(monkeypatch):
    client, captured = _client_with_response(
        monkeypatch, 201, json.dumps({"data": {"id": 507}}).encode()
    )

    subnet_id = client.create_subnet("osac-eip-uid", "192.0.2.5/32", 1, 2)

    assert subnet_id == 507
    assert captured["method"] == "POST"
    assert captured["url"] == "https://n/api/v2/ipam/subnet"
    body = json.loads(captured["data"])
    assert body == {
        "name": "osac-eip-uid",
        "prefix": "192.0.2.5/32",
        "purpose": "nat",
        "sites": [{"id": 1}],
        "tenant": {"id": 2},
    }


def test_create_subnet_accepts_the_flat_data_id_form(monkeypatch):
    client, _ = _client_with_response(
        monkeypatch, 200, json.dumps({"data": "508"}).encode()
    )

    assert client.create_subnet("osac-eip-uid", "192.0.2.5/32", 1, 2) == "508"


def test_create_subnet_fails_closed_without_an_id(monkeypatch):
    client, _ = _client_with_response(
        monkeypatch, 201, json.dumps({"data": {"name": "x"}}).encode()
    )

    with pytest.raises(netris_eip.NetrisExternalIPError):
        client.create_subnet("osac-eip-uid", "192.0.2.5/32", 1, 2)


def test_delete_subnet_targets_the_subnet_id(monkeypatch):
    client, captured = _client_with_response(
        monkeypatch, 204, json.dumps({"message": "provider delete result"}).encode()
    )

    client.delete_subnet(507)

    assert captured["method"] == "DELETE"
    assert captured["url"] == "https://n/api/v2/ipam/subnet/507"


def test_request_fails_closed_on_provider_and_transport_errors(monkeypatch):
    for status in (400, 500, -1):
        client, _ = _client_with_response(monkeypatch, status, b"{}")
        with pytest.raises(netris_eip.NetrisExternalIPError):
            client.list_subnets()


def test_request_fails_closed_on_invalid_and_non_object_json(monkeypatch):
    for payload in (b"not-json", json.dumps([1, 2]).encode()):
        client, _ = _client_with_response(monkeypatch, 200, payload)
        with pytest.raises(netris_eip.NetrisExternalIPError):
            client.list_subnets()
