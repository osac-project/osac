from __future__ import annotations

from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API
from tests.e2e.core.helpers import bmi_instance_type_for_tests, get_bmi_instance_type_from_template


class FakeGRPCClient:
    def __init__(self, responses: dict[str, dict[str, Any]]) -> None:
        self.responses = responses
        self.calls: list[tuple[str, dict[str, Any] | None]] = []
        self.created: list[tuple[str, str, str]] = []
        self.deleted: list[str] = []

    def call(self, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        self.calls.append((service, data))
        return self.responses[service]

    def create_baremetal_instance_type(self, *, name: str, host_type_label: str, fabric_port: str) -> str:
        self.created.append((name, host_type_label, fabric_port))
        return "bmit-123"

    def delete_baremetal_instance_type(self, *, type_id: str) -> None:
        self.deleted.append(type_id)


def test_resolves_instance_type_name_from_template_reference() -> None:
    grpc = FakeGRPCClient(
        {
            f"{PRIVATE_API}.BareMetalInstanceTemplates/List": {
                "items": [
                    {
                        "metadata": {"name": "bm-host-provisioning"},
                        "instance_type": {"name": "metal.default", "shared": True},
                    }
                ]
            }
        }
    )

    assert get_bmi_instance_type_from_template(grpc, template_name="bm-host-provisioning") == "metal.default"
    assert grpc.calls == [
        (f"{PRIVATE_API}.BareMetalInstanceTemplates/List", {"filter": 'this.metadata.name == "bm-host-provisioning"'})
    ]


def test_resolves_instance_type_name_by_id_when_template_reference_has_no_name() -> None:
    grpc = FakeGRPCClient(
        {
            f"{PRIVATE_API}.BareMetalInstanceTemplates/List": {
                "items": [
                    {"metadata": {"name": "bm-host-provisioning"}, "instanceType": {"id": "bmit-123", "shared": True}}
                ]
            },
            f"{PRIVATE_API}.BareMetalInstanceTypes/Get": {"object": {"metadata": {"name": "metal.default"}}},
        }
    )

    assert get_bmi_instance_type_from_template(grpc, template_name="bm-host-provisioning") == "metal.default"
    assert grpc.calls[-1] == (f"{PRIVATE_API}.BareMetalInstanceTypes/Get", {"id": "bmit-123"})


def test_reports_missing_template_type_default_for_fixture_fallback() -> None:
    grpc = FakeGRPCClient(
        {f"{PRIVATE_API}.BareMetalInstanceTemplates/List": {"items": [{"metadata": {"name": "bm-host-provisioning"}}]}}
    )

    assert get_bmi_instance_type_from_template(grpc, template_name="bm-host-provisioning") is None


def test_uses_template_type_without_creating_another() -> None:
    grpc = FakeGRPCClient(
        {
            f"{PRIVATE_API}.BareMetalInstanceTemplates/List": {
                "items": [{"metadata": {"name": "bm-host-provisioning"}, "instance_type": {"name": "metal.default"}}]
            }
        }
    )

    fixture = bmi_instance_type_for_tests(grpc, template_name="bm-host-provisioning", configured="")
    assert next(fixture) == "metal.default"
    with pytest.raises(StopIteration):
        next(fixture)
    assert grpc.created == []
    assert grpc.deleted == []


def test_creates_type_matching_virtual_hosts_and_cleans_it_up() -> None:
    grpc = FakeGRPCClient(
        {f"{PRIVATE_API}.BareMetalInstanceTemplates/List": {"items": [{"metadata": {"name": "bm-host-provisioning"}}]}}
    )

    fixture = bmi_instance_type_for_tests(grpc, template_name="bm-host-provisioning", configured="")
    name = next(fixture)
    assert name.startswith("e2e-bmi-type-")
    assert grpc.created == [(name, "default", "data-0")]
    assert grpc.deleted == []
    with pytest.raises(StopIteration):
        next(fixture)
    assert grpc.deleted == ["bmit-123"]


def test_configured_type_needs_no_template_lookup_or_cleanup() -> None:
    grpc = FakeGRPCClient({})

    fixture = bmi_instance_type_for_tests(grpc, template_name="bm-host-provisioning", configured="existing-bmit")
    assert next(fixture) == "existing-bmit"
    with pytest.raises(StopIteration):
        next(fixture)
    assert grpc.calls == []
    assert grpc.created == []
    assert grpc.deleted == []
