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

    def test_absent(self):
        detail = {"ports": [{"id": 10}]}
        assert idempotency.netris_port_on_vnet(detail, 99) is False

    def test_empty(self):
        assert idempotency.netris_port_on_vnet({"ports": []}, 1) is False
        assert idempotency.netris_port_on_vnet(None, 1) is False


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


class TestFilterModuleRegistration:
    def test_filters_registered(self):
        filters = idempotency.FilterModule().filters()
        assert "netris_immutable_mismatch" in filters
        assert "osac_is_default_networking_resource" in filters
        assert "netris_port_on_vnet" in filters
        assert "format_immutable_mismatch_message" in filters
