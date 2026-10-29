"""Contract tests for the Netris cluster ingress reservation."""
from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path

import pytest

COLLECTIONS_PATH = Path(__file__).resolve().parents[2] / "collections"
MODULE_PATH = (
    COLLECTIONS_PATH
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "modules"
    / "netris_cluster_ingress.py"
)
sys.path.insert(0, str(COLLECTIONS_PATH))
sys.path.insert(
    0,
    str(COLLECTIONS_PATH / "ansible_collections" / "osac" / "templates" / "plugins" / "module_utils"),
)

import ansible.module_utils.basic as ansible_basic  # noqa: E402

from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
    netris_cluster_ingress as netris_ci,
    netris_external_ip as netris_eip,
)

CLUSTER_ID = "9f3d4c21-7a6b-4c58-8d2e-1b0a9c8d7e6f"
TEST_COOKIE = "s%3Asecret-cookie-value"

SUBNET = {"spec": {"ipv4Cidr": "10.0.1.0/24"}}
IPAM = {
    "gateway_ipv4": "10.0.1.1",
    "overlay_ipv4_cidrs": ["10.0.1.0/25"],
    "physical_ipv4_cidrs": ["10.0.1.128/26"],
    "lb_vip_ipv4_cidrs": ["10.0.1.192/26"],
}


def request(**overrides):
    values = {
        "operation": "cluster_ingress.reserve",
        "cluster_id": CLUSTER_ID,
        "subnet": SUBNET,
        "ipam": IPAM,
    }
    values.update(overrides)
    return netris_ci.validate_request(
        values["operation"], values["cluster_id"], values["subnet"], values["ipam"]
    )


class FakeClient:
    """In-memory stand-in for the Netris IPAM subnet client."""

    def __init__(self, records=(), create_errors=(), delete_error=None):
        self.records = [dict(record) for record in records]
        self.create_errors = list(create_errors)
        self.delete_error = delete_error
        self.created = []
        self.deleted = []

    def list_subnets(self):
        return [dict(record) for record in self.records]

    def create_subnet(self, name, prefix, site_id, tenant_id, purpose="nat"):
        self.created.append((name, prefix, purpose))
        if self.create_errors:
            raise self.create_errors.pop(0)
        self.records.append(
            {
                "id": len(self.records) + 1,
                "name": name,
                "prefix": prefix,
                "purpose": purpose,
                "site_id": site_id,
                "tenant_id": tenant_id,
            }
        )

    def delete_subnet(self, subnet_id):
        self.deleted.append(subnet_id)
        if self.delete_error is not None:
            raise self.delete_error
        self.records = [record for record in self.records if record["id"] != subnet_id]


def record(prefix="10.0.1.193/32", name=None, purpose="load-balancer", site_id=1, tenant_id=7):
    return {
        "id": 101,
        "name": name or f"osac-ingress-{CLUSTER_ID}",
        "prefix": prefix,
        "purpose": purpose,
        "site_id": site_id,
        "tenant_id": tenant_id,
    }


@pytest.mark.parametrize(
    "overrides, message",
    [
        ({"operation": "cluster_ingress.drop"}, "unsupported cluster ingress operation"),
        ({"cluster_id": ""}, "cluster_id must be a nonempty string"),
        ({"cluster_id": " padded "}, "cluster_id must be a nonempty string"),
        ({"subnet": {}}, "Subnet snapshot omits spec"),
        ({"subnet": {"spec": {"ipv4Cidr": "10.0.1.5/24"}}}, "must be a canonical IPv4 CIDR"),
        ({"subnet": {"spec": {"ipv4Cidr": "2001:db8::/64"}}}, "must be an IPv4 CIDR"),
        (
            {"ipam": {**IPAM, "lb_vip_ipv4_cidrs": []}},
            "ipam.lb_vip_ipv4_cidrs must be a non-empty list",
        ),
        (
            {"ipam": {**IPAM, "lb_vip_ipv4_cidrs": ["10.0.2.0/26"]}},
            "is outside the Subnet CIDR",
        ),
        (
            {"ipam": {**IPAM, "gateway_ipv4": "10.0.1.1/24"}},
            "ipam.gateway_ipv4 must be a bare IPv4 address",
        ),
    ],
)
def test_invalid_input_is_rejected_before_provider_io(overrides, message):
    with pytest.raises(ValueError, match=message):
        request(**overrides)


def test_request_is_keyed_by_cluster_id_not_resource_id():
    result = request()

    assert result["reservation_name"] == f"osac-ingress-{CLUSTER_ID}"
    assert result["ranges"] == ["10.0.1.192/26"]
    assert result["subnet_cidr"] == "10.0.1.0/24"
    assert result["gateway_ipv4"] == "10.0.1.1"


def test_lowest_free_address_in_the_load_balancer_range_is_selected():
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.192/26"], "10.0.1.0/24", "10.0.1.1", []
        )
        == "10.0.1.193"
    )


def test_subnet_network_broadcast_and_gateway_are_never_offered():
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.240/28"], "10.0.1.0/24", "10.0.1.1", []
        )
        == "10.0.1.241"
    )
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.192/26"], "10.0.1.0/24", "10.0.1.193", []
        )
        == "10.0.1.194"
    )


def test_ranges_are_searched_in_order():
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.192/27", "10.0.1.224/27"], "10.0.1.0/24", "10.0.1.1", []
        )
        == "10.0.1.193"
    )


def test_existing_reservations_block_their_addresses():
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.192/26"], "10.0.1.0/24", "10.0.1.1", ["10.0.1.192/28"]
        )
        == "10.0.1.208"
    )


def test_a_parent_prefix_does_not_block_the_whole_range():
    assert (
        netris_ci.select_ingress_address(
            ["10.0.1.192/26"], "10.0.1.0/24", "10.0.1.1", ["10.0.1.0/24"]
        )
        == "10.0.1.193"
    )


def test_a_full_range_fails_without_inventing_an_address():
    with pytest.raises(netris_ci.NetrisClusterIngressError) as caught:
        netris_ci.select_ingress_address(
            ["10.0.1.196/30"],
            "10.0.1.0/24",
            "10.0.1.1",
            ["10.0.1.196/31", "10.0.1.198/31"],
        )

    assert caught.value.category == "exhausted"
    assert "is full" in str(caught.value)
    assert netris_ci.is_retryable(caught.value.category) is False


def test_reserve_creates_one_address_and_reuses_it_on_retry():
    client = FakeClient()

    first = netris_ci.reconcile(request(), client, 1, 7, delay_seconds=0)
    second = netris_ci.reconcile(request(), client, 1, 7, delay_seconds=0)

    assert first == {"changed": True, "address": "10.0.1.193"}
    assert second == {"changed": False, "address": "10.0.1.193"}
    assert client.created == [
        (f"osac-ingress-{CLUSTER_ID}", "10.0.1.193/32", "load-balancer")
    ]


def test_reserve_retries_a_lost_race_and_keeps_the_observed_address():
    conflict = netris_ci.NetrisClusterIngressError(
        "Netris API POST request failed", category="provider_error", http_status=400
    )
    client = FakeClient(create_errors=[conflict])

    result = netris_ci.reconcile(request(), client, 1, 7, delay_seconds=0)

    assert result == {"changed": True, "address": "10.0.1.193"}
    assert len(client.created) == 2


def test_reserve_never_writes_outside_the_load_balancer_range():
    client = FakeClient(
        records=[
            {
                "id": 1,
                "name": "subnet-a",
                "prefix": "10.0.1.0/24",
                "purpose": "common",
                "site_id": 1,
                "tenant_id": 7,
            }
        ]
    )

    assert netris_ci.reconcile(request(), client, 1, 7, delay_seconds=0)["address"] == "10.0.1.193"


def test_foreign_or_malformed_reservations_are_not_reused():
    with pytest.raises(netris_ci.NetrisClusterIngressError) as caught:
        netris_ci.reconcile(
            request(), FakeClient(records=[record(purpose="nat")]), 1, 7, delay_seconds=0
        )

    assert caught.value.category == "malformed_response"


def test_the_reservation_is_bound_to_the_configured_site_and_tenant():
    with pytest.raises(netris_ci.NetrisClusterIngressError):
        netris_ci.reconcile(
            request(), FakeClient(records=[record(tenant_id=8)]), 1, 7, delay_seconds=0
        )


def test_release_removes_the_reservation_and_succeeds_when_absent():
    client = FakeClient(records=[record()])

    first = netris_ci.reconcile(
        request(operation="cluster_ingress.release"), client, 1, 7, delay_seconds=0
    )
    second = netris_ci.reconcile(
        request(operation="cluster_ingress.release"), client, 1, 7, delay_seconds=0
    )

    assert first == {"changed": True, "address": ""}
    assert second == {"changed": False, "address": ""}
    assert client.deleted == [101]


def test_release_treats_a_missing_provider_object_as_gone():
    class MissingClient(FakeClient):
        def delete_subnet(self, subnet_id):
            self.deleted.append(subnet_id)
            self.records = []

    client = MissingClient(records=[record()])

    result = netris_ci.reconcile(
        request(operation="cluster_ingress.release"), client, 1, 7, delay_seconds=0
    )

    assert result == {"changed": True, "address": ""}


def test_release_reports_a_persistent_reservation_as_retryable():
    class StuckClient(FakeClient):
        def delete_subnet(self, subnet_id):
            self.deleted.append(subnet_id)

    client = StuckClient(records=[record()])

    with pytest.raises(netris_ci.NetrisClusterIngressError) as caught:
        netris_ci.reconcile(
            request(operation="cluster_ingress.release"), client, 1, 7, delay_seconds=0
        )

    assert caught.value.category == "exhausted_attempts"
    assert netris_ci.is_retryable(caught.value.category) is True


def load_module():
    spec = importlib.util.spec_from_file_location("netris_cluster_ingress_module", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def module_args(**overrides):
    args = {
        "operation": "cluster_ingress.reserve",
        "cluster_id": CLUSTER_ID,
        "subnet": SUBNET,
        "ipam": IPAM,
        "controller_url": "https://netris.example",
        "session_cookie": TEST_COOKIE,
        "site_id": 1,
        "tenant_id": 7,
    }
    args.update(overrides)
    return args


def run_main(monkeypatch, capsys, args, client):
    loaded = load_module()
    # The retry pause is shared with the ExternalIP module.
    monkeypatch.setattr(netris_eip.time, "sleep", lambda *_: None)
    monkeypatch.setattr(
        ansible_basic,
        "_ANSIBLE_ARGS",
        json.dumps(
            {"ANSIBLE_MODULE_ARGS": args, "ANSIBLE_MODULE_CONSTRAINTS": {}}
        ).encode(),
    )
    monkeypatch.setattr(netris_ci, "NetrisSubnetClient", lambda *a, **k: client)
    with pytest.raises(SystemExit):
        loaded.main()
    out = capsys.readouterr().out
    return json.loads(out.strip().splitlines()[-1])


def test_argument_spec_marks_only_string_secrets_no_log():
    loaded = load_module()

    assert loaded.ARGUMENT_SPEC["session_cookie"].get("no_log") is True
    assert loaded.ARGUMENT_SPEC["controller_url"].get("no_log") is True
    for name in ("subnet", "ipam"):
        assert loaded.ARGUMENT_SPEC[name].get("no_log") is None, name
    assert loaded.ARGUMENT_SPEC["timeout"]["default"] == 30


@pytest.mark.parametrize("status, retryable", [
    (400, False), (401, False), (403, False), (404, False), (409, False),
    (422, False), (408, True), (425, True), (429, True), (500, True), (503, True),
    (-1, True), (None, True),
])
def test_module_provider_failure_retry_policy(monkeypatch, capsys, status, retryable):
    class FailedClient(FakeClient):
        def list_subnets(self):
            raise netris_eip.NetrisExternalIPError(
                "provider read failed",
                category="transport_error" if status == -1 else "provider_error",
                http_status=status,
            )

    result = run_main(monkeypatch, capsys, module_args(), FailedClient())
    assert result["failed"] is True
    assert result["retryable"] is retryable


def test_check_mode_validates_inputs_without_any_provider_write(monkeypatch, capsys):
    client = FakeClient()

    result = run_main(
        monkeypatch, capsys, module_args(_ansible_check_mode=True), client
    )

    assert result["changed"] is False
    assert result["address"] == ""
    assert client.created == []
    assert client.deleted == []


def test_module_reserves_and_returns_the_typed_address(monkeypatch, capsys):
    result = run_main(monkeypatch, capsys, module_args(), FakeClient())

    assert result["changed"] is True
    assert result["address"] == "10.0.1.193"
    assert result["invocation"]["module_args"]["subnet"] == (
        "VALUE_SPECIFIED_IN_NO_LOG_PARAMETER"
    )
    assert result["invocation"]["module_args"]["cluster_id"] == CLUSTER_ID
    assert TEST_COOKIE not in capsys.readouterr().out


def test_module_reports_a_full_range_as_final(monkeypatch, capsys):
    ipam = {
        **IPAM,
        "lb_vip_ipv4_cidrs": ["10.0.1.196/30"],
    }
    client = FakeClient(
        records=[
            record(prefix="10.0.1.196/31", name="other-1"),
            record(prefix="10.0.1.198/31", name="other-2"),
        ]
    )

    result = run_main(monkeypatch, capsys, module_args(ipam=ipam), client)

    assert result["failed"] is True
    assert result["category"] == "exhausted"
    assert result["retryable"] is False
    assert "is full" in result["msg"]


def test_module_reports_a_transport_failure_as_retryable(monkeypatch, capsys):
    transport_error = netris_ci.NetrisClusterIngressError(
        "Netris API POST request failed", category="transport_error", http_status=-1
    )
    client = FakeClient(create_errors=[transport_error] * netris_ci.MAX_RESERVE_ATTEMPTS)

    result = run_main(monkeypatch, capsys, module_args(), client)

    assert result["failed"] is True
    assert result["retryable"] is True
    assert result["category"] == "transport_error"
