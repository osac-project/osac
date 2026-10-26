"""Unit tests for netris.controller idempotency filters (OSAC-4923)."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

_FILTER_PATH = (
    Path(__file__).resolve().parents[4]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "plugins"
    / "filter"
    / "idempotency.py"
)


def _load_idempotency():
    spec = importlib.util.spec_from_file_location("netris_idempotency", _FILTER_PATH)
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    sys.modules["netris_idempotency"] = mod
    spec.loader.exec_module(mod)
    return mod


idempotency = _load_idempotency()


class TestNetrisImmutableMismatch:
    def test_empty_when_no_existing(self):
        assert idempotency.netris_immutable_mismatch(None, {"prefix": "10.0.0.0/24"}) == []

    def test_match_prefix(self):
        existing = {"name": "vn1", "prefix": "10.0.0.0/24"}
        assert (
            idempotency.netris_immutable_mismatch(existing, {"prefix": "10.0.0.0/24"})
            == []
        )

    def test_mismatch_prefix(self):
        existing = {"name": "vn1", "prefix": "10.0.0.0/24"}
        mismatches = idempotency.netris_immutable_mismatch(
            existing, {"prefix": "10.1.0.0/24"}
        )
        assert len(mismatches) == 1
        assert mismatches[0]["field"] == "prefix"
        assert mismatches[0]["expected"] == "10.1.0.0/24"
        assert mismatches[0]["actual"] == "10.0.0.0/24"

    @pytest.mark.parametrize("actual", ["198.51.100.25", "198.51.100.25/32"])
    def test_snat_ip_matches_bare_or_host_prefix(self, actual):
        assert idempotency.netris_immutable_mismatch(
            {"snatToIP": actual},
            {"snatToIP": "198.51.100.25/32"},
        ) == []

    @pytest.mark.parametrize(
        "actual",
        ["198.51.100.26/32", "198.51.100.25/24", "not-an-ip"],
    )
    def test_snat_ip_rejects_different_or_non_host_value(self, actual):
        mismatches = idempotency.netris_immutable_mismatch(
            {"snatToIP": actual},
            {"snatToIP": "198.51.100.25/32"},
        )
        assert len(mismatches) == 1
        assert mismatches[0]["field"] == "snatToIP"

    @pytest.mark.parametrize("actual", ["198.51.100.25", "198.51.100.25/32"])
    def test_dnat_destination_address_matches_bare_or_host_prefix(self, actual):
        assert idempotency.netris_immutable_mismatch(
            {"destinationAddress": actual},
            {"destinationAddress": "198.51.100.25/32"},
        ) == []

    def test_dnat_destination_address_preserves_network_prefix_comparison(self):
        assert idempotency.netris_immutable_mismatch(
            {"destinationAddress": "198.51.100.0/24"},
            {"destinationAddress": "198.51.100.0/24"},
        ) == []
        mismatches = idempotency.netris_immutable_mismatch(
            {"destinationAddress": "198.51.100.25"},
            {"destinationAddress": "198.51.100.25/24"},
        )
        assert len(mismatches) == 1
        assert mismatches[0]["field"] == "destinationAddress"

    @pytest.mark.parametrize(
        "actual", ["198.51.100.26/32", "198.51.100.25/24"]
    )
    def test_dnat_destination_address_rejects_different_host_or_prefix(self, actual):
        mismatches = idempotency.netris_immutable_mismatch(
            {"destinationAddress": actual},
            {"destinationAddress": "198.51.100.25/32"},
        )
        assert len(mismatches) == 1
        assert mismatches[0]["field"] == "destinationAddress"

    @pytest.mark.parametrize("actual", [0, "0"])
    def test_dnat_empty_port_matches_provider_zero(self, actual):
        assert idempotency.netris_immutable_mismatch(
            {"dnatToPort": actual}, {"dnatToPort": ""}
        ) == []

    @pytest.mark.parametrize(
        "expected, actual", [("8080", 0), ("", 8080), ("8080", "8081")]
    )
    def test_dnat_port_rejects_explicit_mismatch(self, expected, actual):
        mismatches = idempotency.netris_immutable_mismatch(
            {"dnatToPort": actual}, {"dnatToPort": expected}
        )
        assert len(mismatches) == 1
        assert mismatches[0]["field"] == "dnatToPort"

    def test_nested_vpc_id(self):
        existing = {"name": "vnet1", "vpc": {"id": 7}}
        assert (
            idempotency.netris_immutable_mismatch(existing, {"vpc.id": "7"}) == []
        )
        mismatches = idempotency.netris_immutable_mismatch(
            existing, {"vpc.id": "9"}
        )
        assert mismatches[0]["actual"] == "7"
        assert mismatches[0]["expected"] == "9"

    def test_scalar_vpc_id(self):
        existing = {"name": "vnet1", "vpc": 7}
        assert idempotency.netris_immutable_mismatch(existing, {"vpc.id": "7"}) == []

    def test_scalar_tenant_site_and_site_list_ids(self):
        existing = {"tenant": 77, "site": 3, "sites": [3, 7]}
        assert idempotency.netris_immutable_mismatch(
            existing,
            {"tenant.id": "77", "site.id": 3, "sites.ids": [7, 3]},
        ) == []

    def test_scalar_admin_tenant_id(self):
        assert idempotency.netris_immutable_mismatch(
            {"adminTenant": 77}, {"adminTenant.id": "77"}
        ) == []

    def test_missing_value_is_unverifiable(self):
        mismatches = idempotency.netris_immutable_mismatch(
            {"name": "vpc1"}, {"adminTenant.id": 3}
        )
        assert mismatches == [
            {
                "field": "adminTenant.id",
                "expected": "3",
                "actual": "<missing>",
                "unverifiable": True,
            }
        ]

    def test_missing_fields_are_reported_for_detail_lookup(self):
        assert idempotency.netris_immutable_missing_fields(
            {"name": "vnet1", "vpc": 7}, {"vpc.id": 7, "tenant.id": 9}
        ) == ["tenant.id"]

    def test_nested_list_paths_and_site_ids_are_normalized(self):
        existing = {
            "gateways": [{"prefix": "10.0.0.1/24"}],
            "sites": [{"id": 3}, {"id": 7}],
        }
        assert idempotency.netris_immutable_mismatch(
            existing,
            {"gateways.0.prefix": "10.0.0.1/24", "sites.ids": [7, 3]},
        ) == []

    def test_action_value_object_is_normalized(self):
        assert idempotency.netris_immutable_mismatch(
            {"action": {"label": "SNAT", "value": "snat"}},
            {"action": "SNAT"},
        ) == []

    def test_admin_tenant_nested(self):
        existing = {"name": "vpc1", "adminTenant": {"id": 3}}
        assert (
            idempotency.netris_immutable_mismatch(existing, {"adminTenant.id": 3})
            == []
        )

    def test_action_case_insensitive(self):
        existing = {"name": "n1", "action": "snat", "vpc": {"id": 1}}
        assert (
            idempotency.netris_immutable_mismatch(
                existing, {"action": "SNAT", "vpc.id": "1"}
            )
            == []
        )


class TestOsacIsDefaultNetworkingResource:
    def test_default_true(self):
        resource = {
            "metadata": {
                "name": "default-vn",
                "labels": {"osac.openshift.io/default": "true"},
            }
        }
        assert idempotency.osac_is_default_networking_resource(resource) is True

    def test_default_false(self):
        resource = {
            "metadata": {
                "name": "tenant-vn",
                "labels": {"osac.openshift.io/default": "false"},
            }
        }
        assert idempotency.osac_is_default_networking_resource(resource) is False

    def test_missing_label(self):
        resource = {"metadata": {"name": "x", "labels": {}}}
        assert idempotency.osac_is_default_networking_resource(resource) is False

    def test_none(self):
        assert idempotency.osac_is_default_networking_resource(None) is False


class TestNetrisPortOnVnet:
    def test_present(self):
        detail = {"ports": [{"id": 10}, {"id": 20}]}
        assert idempotency.netris_port_on_vnet(detail, 20) is True

    def test_present_string_vs_int(self):
        detail = {"ports": [{"id": 10}, {"id": 20}]}
        assert idempotency.netris_port_on_vnet(detail, "20") is True

    def test_absent(self):
        detail = {"ports": [{"id": 10}]}
        assert idempotency.netris_port_on_vnet(detail, 99) is False

    def test_empty(self):
        assert idempotency.netris_port_on_vnet({"ports": []}, 1) is False
        assert idempotency.netris_port_on_vnet(None, 1) is False


class TestNetrisPortsExcluding:
    def test_excludes_matching_int(self):
        ports = [{"id": 10}, {"id": 20}, {"id": 30}]
        assert idempotency.netris_ports_excluding(ports, 20) == [
            {"id": 10},
            {"id": 30},
        ]

    def test_excludes_string_vs_int(self):
        ports = [{"id": 10}, {"id": 20}]
        assert idempotency.netris_ports_excluding(ports, "20") == [{"id": 10}]

    def test_excludes_int_vs_string_id(self):
        ports = [{"id": "10"}, {"id": "20"}]
        assert idempotency.netris_ports_excluding(ports, 20) == [{"id": "10"}]

    def test_empty_and_none(self):
        assert idempotency.netris_ports_excluding(None, 1) == []
        assert idempotency.netris_ports_excluding([{"id": 1}], None) == [{"id": 1}]


class TestFormatImmutableMismatchMessage:
    def test_message_shape(self):
        msg = idempotency.format_immutable_mismatch_message(
            "IPAM subnet",
            "subnet-a",
            [{"field": "prefix", "expected": "10.0.0.0/24", "actual": "10.1.0.0/24"}],
        )
        assert "IPAM subnet 'subnet-a'" in msg
        assert "field=prefix" in msg
        assert "expected=10.0.0.0/24" in msg
        assert "actual=10.1.0.0/24" in msg
        assert "Refusing delete-and-recreate" in msg

    def test_unverifiable_fields_are_not_described_as_actual_drift(self):
        msg = idempotency.format_immutable_mismatch_message(
            "VPC",
            "vpc-a",
            [
                {
                    "field": "adminTenant.id",
                    "expected": "3",
                    "actual": "<missing>",
                    "unverifiable": True,
                }
            ],
        )
        assert "could not be verified from the response" in msg
        assert "actual=<missing> (unverifiable)" in msg
        assert "did not treat the resource as a match" in msg
        assert "immutable field mismatch" not in msg


class TestNetrisSafeErrorDetail:
    def test_includes_http_status_and_redacts_credentials(self):
        detail = idempotency.netris_safe_error_detail(
            {
                "status": 503,
                "json": {
                    "message": "connect.sid=abc Cookie: secret Authorization: Bearer token"
                },
            }
        )
        assert detail.startswith("HTTP 503;")
        assert "connect.sid=<redacted>" in detail
        assert "Cookie=<redacted>" in detail
        assert "Authorization=<redacted>" in detail
        assert "abc" not in detail
        assert "secret" not in detail
        assert "token" not in detail

    def test_reports_transport_failure_without_http_status(self):
        detail = idempotency.netris_safe_error_detail(
            {"msg": "TLS certificate verification failed"}
        )
        assert detail == "no HTTP status was received; TLS certificate verification failed"

    def test_truncates_long_error(self):
        detail = idempotency.netris_safe_error_detail({"msg": "x" * 400})
        assert len(detail) == len("no HTTP status was received; ") + 240


class TestNetrisIPInCIDRs:
    def test_host_address_and_host_prefix_are_in_pool(self):
        assert idempotency.netris_ip_in_cidrs(
            "198.51.100.3/32", ["198.51.100.0/29"]
        )

    def test_address_outside_pool_is_rejected(self):
        assert not idempotency.netris_ip_in_cidrs(
            "198.51.101.3/32", ["198.51.100.0/29"]
        )

    def test_invalid_address_and_cidr_are_rejected(self):
        assert not idempotency.netris_ip_in_cidrs("not-an-ip", ["198.51.100.0/29"])
        assert not idempotency.netris_ip_in_cidrs("198.51.100.3", ["invalid"])


class TestFilterModuleRegistration:
    def test_filters_registered(self):
        filters = idempotency.FilterModule().filters()
        assert "netris_immutable_mismatch" in filters
        assert "osac_is_default_networking_resource" in filters
        assert "netris_port_on_vnet" in filters
        assert "netris_ports_excluding" in filters
        assert "format_immutable_mismatch_message" in filters
        assert "netris_immutable_missing_fields" in filters
        assert "netris_safe_error_detail" in filters
        assert "netris_ip_in_cidrs" in filters
