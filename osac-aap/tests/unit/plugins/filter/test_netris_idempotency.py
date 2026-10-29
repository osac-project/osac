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

    @pytest.mark.parametrize(
        "key", ["token", "password", "secret", "Cookie", "connect.sid", "Authorization"]
    )
    @pytest.mark.parametrize("quote", ['"', "'"])
    def test_redacts_quoted_keys_and_complete_quoted_values(self, key, quote):
        marker = (
            "SYNTHETIC_VALUE with spaces, separators; and escaped \\" + quote + "quote"
        )
        message = (
            f"backend unavailable: {{{quote}{key}{quote}: {quote}{marker}{quote}}}"
        )
        detail = idempotency.netris_safe_error_detail(
            {"status": 503, "json": {"message": message}}
        )
        assert "backend unavailable" in detail
        assert "<redacted>" in detail
        assert "SYNTHETIC_VALUE" not in detail
        assert "with spaces" not in detail
        assert "quote" not in detail

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

    def test_no_response_sentinel_is_not_rendered_as_an_http_status(self):
        detail = idempotency.netris_safe_error_detail(
            {
                "status": -1,
                "msg": "Connection failure: Remote end closed connection without response",
            }
        )
        assert detail.startswith("no HTTP status was received;")
        assert "HTTP -1" not in detail


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
        assert "netris_retryable_error" in filters
        assert "netris_active_dhcp_addresses" in filters
        assert "netris_ip_in_cidrs" in filters


class TestNetrisRetryableError:
    def test_explicit_retryable_wins(self):
        assert idempotency.netris_retryable_error(
            {"retryable": False, "status": 503, "msg": "provider unavailable"}
        ) is False
        assert idempotency.netris_retryable_error(
            {"retryable": True, "status": 400, "msg": "bad request"}
        ) is True

    def test_server_and_throttling_statuses_are_retryable(self):
        for status in (500, 502, 503, 504, 408, 425, 429):
            assert idempotency.netris_retryable_error({"status": status}) is True

    def test_client_statuses_are_final(self):
        for status in (400, 401, 403, 404, 409, 422):
            assert idempotency.netris_retryable_error({"status": status}) is False

    def test_transport_failure_without_status_is_retryable(self):
        assert idempotency.netris_retryable_error(
            {"msg": "Status code was 503 and not [200]: HTTP Error 503: Service Unavailable"}
        ) is True
        assert idempotency.netris_retryable_error(
            {"msg": "Connection failure: [Errno 111] Connection refused"}
        ) is True

    def test_assert_and_tls_failures_are_final(self):
        assert idempotency.netris_retryable_error(
            {"failed": True, "msg": "Netris VPC 'demo' was not found"}
        ) is False
        assert idempotency.netris_retryable_error(
            {"msg": "TLS certificate verification failed"}
        ) is False

    def test_non_mapping_result_is_final(self):
        assert idempotency.netris_retryable_error("boom") is False

    def test_no_response_sentinel_falls_back_to_transport_classification(self):
        assert idempotency.netris_retryable_error(
            {
                "status": -1,
                "msg": "Status code was -1 and not [200]: Connection failure: Remote end closed connection without response",
            }
        ) is True
        assert idempotency.netris_retryable_error(
            {"status": -1, "msg": "Invalid JSON returned"}
        ) is False

    def test_missing_setting_failure_is_retryable(self):
        assert idempotency.netris_retryable_error(
            {
                "failed": True,
                "msg": "Netris setting error: NETRIS_BM_PROVISIONING_VNET must be set",
            }
        ) is True


class TestNetrisActiveDhcpAddresses:
    MAC = "aa:bb:cc:dd:ee:ff"

    def test_active_dhcp_binding_is_a_lease(self):
        host = {
            "address": "10.0.1.130",
            "mac": [
                {"address": self.MAC.upper(), "source": "dhcp", "state": "active"},
                {"address": "11:22:33:44:55:66", "source": "dhcp", "state": "active"},
            ]
        }
        assert idempotency.netris_active_dhcp_addresses(host, self.MAC) == [
            "10.0.1.130"
        ]

    def test_inactive_and_reservation_only_records_are_not_leases(self):
        host = {
            "address": "10.0.1.130",
            "mac": [
                {"address": self.MAC, "source": "dhcp", "state": "inactive"},
                {"address": self.MAC, "source": "reservation", "state": "active"},
            ]
        }
        assert idempotency.netris_active_dhcp_addresses(host, self.MAC) == []

    def test_mixed_records_count_only_active_dhcp(self):
        host = {
            "address": "10.0.1.130",
            "mac": [
                {"address": self.MAC, "source": "reservation", "state": "active"},
                {"address": self.MAC, "source": "dhcp", "state": "inactive"},
                {"address": self.MAC, "source": "dhcp", "state": "active"},
            ]
        }
        assert idempotency.netris_active_dhcp_addresses(host, self.MAC) == ["10.0.1.130"]

    def test_repeated_mac_history_counts_the_host_address_once(self):
        host = {
            "address": "10.0.1.130",
            "mac": [{"address": self.MAC, "source": "dhcp", "state": "active"}] * 2,
        }
        assert idempotency.netris_active_dhcp_addresses(host, self.MAC) == ["10.0.1.130"]

    @pytest.mark.parametrize("address", [None, "invalid", "aa:bb:cc:dd:ee:ff", "2001:db8::1"])
    def test_invalid_or_non_ipv4_host_address_is_not_a_lease(self, address):
        host = {
            "address": address,
            "mac": [{"address": self.MAC, "source": "dhcp", "state": "active"}],
        }
        assert idempotency.netris_active_dhcp_addresses(host, self.MAC) == []

    def test_malformed_input_is_ignored(self):
        assert idempotency.netris_active_dhcp_addresses(None, self.MAC) == []
        assert idempotency.netris_active_dhcp_addresses({"mac": []}, "") == []
        assert idempotency.netris_active_dhcp_addresses(
            {"mac": ["not-a-dict", {"address": self.MAC}]}, self.MAC
        ) == []


class TestNetrisAttachmentPortPlan:
    PORTS = [{"id": 101}, {"id": 102}, {"id": 103}]

    def test_captures_only_host_ports_on_provisioning(self):
        vnet = {"ports": [{"id": "101"}, {"id": 102}, {"id": 201}], "tags": ["keep"]}
        plan = idempotency.netris_attachment_port_plan(
            vnet, self.PORTS, "target-id", 101
        )
        assert plan["port_ids"] == [101, 102]
        assert plan["tags"] == ["keep", "osac-na-target:target-id:101,102"]

    def test_fresh_job_recovers_detached_ports_and_preserves_other_snapshots(self):
        vnet = {
            "ports": [],
            "tags": ["osac-na-target:target-id:101,102", "osac-na:other:201"],
        }
        plan = idempotency.netris_attachment_port_plan(
            vnet, self.PORTS, "target-id", 101, True
        )
        assert plan["port_ids"] == [101, 102]
        assert plan["tags"] == ["osac-na:other:201", "osac-na-target:target-id:101,102"]

    @pytest.mark.parametrize(
        "tags",
        [
            ["osac-na-target:target-id:201"],
            ["osac-na-target:target-id:101", "osac-na-target:target-id:102"],
            ["osac-na-target:target-id:102"],
            ["osac-na-target:target-id:"],
        ],
    )
    def test_invalid_or_changed_saved_identity_is_refused(self, tags):
        with pytest.raises(ValueError):
            idempotency.netris_attachment_port_plan(
                {"ports": [], "tags": tags}, self.PORTS, "target-id", 101
            )

    def test_verified_legacy_snapshot_is_migrated(self):
        vnet = {
            "ports": [],
            "tags": ["keep", "osac-na:uid-1:101,102", "osac-na:other:201"],
        }
        plan = idempotency.netris_attachment_port_plan(
            vnet, self.PORTS, "target-id", 101, legacy_uid="uid-1"
        )
        assert plan["port_ids"] == [101, 102]
        assert plan["tags"] == [
            "keep",
            "osac-na:other:201",
            "osac-na-target:target-id:101,102",
        ]
        assert plan["saved_tag"] == "osac-na:uid-1:101,102"

    @pytest.mark.parametrize("deleting", [False, True])
    def test_unknown_legacy_owner_refuses_incomplete_restoration(self, deleting):
        with pytest.raises(ValueError, match="legacy"):
            idempotency.netris_attachment_port_plan(
                {"ports": [], "tags": ["osac-na:old-uid:101,102"]},
                self.PORTS,
                "target-id",
                101,
                deleting,
                legacy_uid="new-uid",
            )

    def test_stable_and_legacy_snapshots_are_ambiguous(self):
        with pytest.raises(ValueError, match="Ambiguous"):
            idempotency.netris_attachment_port_plan(
                {"tags": ["osac-na-target:target-id:101,102", "osac-na:uid-1:101,102"]},
                self.PORTS,
                "target-id",
                101,
                legacy_uid="uid-1",
            )

    def test_apply_refuses_an_interface_outside_provisioning(self):
        with pytest.raises(ValueError, match="not a provisioning fabric port"):
            idempotency.netris_attachment_port_plan(
                {"ports": [{"id": 101}]}, self.PORTS, "uid-1", 103
            )


class TestNetrisControllerURLValid:
    @pytest.mark.parametrize(
        "url",
        [
            "http://localhost:8080",
            "https://controller.example/",
            "https://controller.example/netris",
            "https://[::1]:443",
        ],
    )
    def test_supported_base_url(self, url):
        assert idempotency.netris_controller_url_valid(url)

    @pytest.mark.parametrize(
        "url",
        [
            None,
            "",
            "not-a-url",
            "ftp://controller",
            "https://",
            "http://host:bad",
            "https://host:0",
            "https://host:65536",
            "http://host:",
            "https://bad host",
            "https://host\\path",
            "https://host?query=value",
            "https://host#fragment",
            "https://user:password@host",
            "https://[broken",
            " http://host",
        ],
    )
    def test_malformed_or_unsupported_url(self, url):
        assert not idempotency.netris_controller_url_valid(url)
