from __future__ import annotations

from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient


def test_update_compute_instance_instance_type_updates_only_instance_type(monkeypatch: pytest.MonkeyPatch) -> None:
    client = GRPCClient(address="fulfillment-api.example.test:443", token="test-token")
    calls: list[dict[str, Any]] = []
    response = {"object": {"id": "compute-instance-id"}}

    def capture_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        calls.append({"service": service, "data": data})
        return response

    monkeypatch.setattr(client, "call", capture_call)

    result = client.update_compute_instance_instance_type(ci_id="compute-instance-id", instance_type="medium")

    assert result is response
    assert calls == [
        {
            "service": "osac.public.v1.ComputeInstances/Update",
            "data": {
                "object": {"id": "compute-instance-id", "spec": {"instance_type": {"id": "medium"}}},
                "updateMask": {"paths": ["spec.instance_type"]},
            },
        }
    ]


def test_create_compute_instance_with_disk_image_includes_security_group(monkeypatch: pytest.MonkeyPatch) -> None:
    client = GRPCClient(address="fulfillment-api.example.test:443", token="test-token")
    calls: list[dict[str, Any]] = []
    response = {"object": {"id": "compute-instance-id"}}

    def capture_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        calls.append({"service": service, "data": data})
        return response

    monkeypatch.setattr(client, "call", capture_call)

    result = client.create_compute_instance_with_disk_image(
        template="vm-template",
        disk_image_name="fedora",
        subnet_ids=["subnet-id"],
        security_group_ids=["security-group-id"],
    )

    assert result == response
    assert calls == [
        {
            "service": "osac.public.v1.ComputeInstances/Create",
            "data": {
                "object": {
                    "spec": {
                        "template": {"name": "vm-template", "shared": True},
                        "disk_image": {"name": "fedora"},
                        "network_attachments": [
                            {"subnet": {"id": "subnet-id"}, "security_groups": [{"id": "security-group-id"}]}
                        ],
                    }
                }
            },
        }
    ]


def test_create_and_delete_shared_baremetal_instance_type(monkeypatch: pytest.MonkeyPatch) -> None:
    client = GRPCClient(address="fulfillment-api.example.test:443", token="test-token")
    calls: list[dict[str, Any]] = []

    def capture_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        calls.append({"service": service, "data": data})
        return {"object": {"id": "bmit-123"}} if service.endswith("/Create") else {}

    monkeypatch.setattr(client, "call", capture_call)

    type_id = client.create_baremetal_instance_type(
        name="e2e-bmi-type", host_type_label="default", fabric_port="data-0"
    )
    client.delete_baremetal_instance_type(type_id=type_id)

    assert type_id == "bmit-123"
    assert calls == [
        {
            "service": f"{PRIVATE_API}.BareMetalInstanceTypes/Create",
            "data": {
                "object": {
                    "metadata": {"name": "e2e-bmi-type", "tenant": "shared"},
                    "spec": {
                        "hardware": {
                            "cpu": {"cores": 4, "architecture": "x86_64", "threads_per_core": 2},
                            "memory": {"total_gb": 8},
                            "network_ports": [
                                {"name": "data-0", "role": "fabric", "type": "Ethernet", "speed": "10Gbps"}
                            ],
                        },
                        "host_label_selector": {"match_labels": {"osac.openshift.io/host-type": "default"}},
                    },
                }
            },
        },
        {"service": f"{PRIVATE_API}.BareMetalInstanceTypes/Delete", "data": {"id": "bmit-123"}},
    ]


def test_update_security_group_rules_rejects_omitted_ingress_and_egress(monkeypatch: pytest.MonkeyPatch) -> None:
    """Do not GET or Update a SecurityGroup when neither rule list is provided."""
    client = GRPCClient(address="fulfillment-api.example.test:443", token="test-token")
    calls: list[dict[str, Any]] = []

    def capture_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        calls.append({"service": service, "data": data})
        return {}

    monkeypatch.setattr(client, "call", capture_call)

    with pytest.raises(ValueError, match="ingress and/or egress"):
        client.update_security_group_rules(sg_id="security-group-id")

    assert calls == []


def test_update_security_group_rules_updates_provided_ingress(monkeypatch: pytest.MonkeyPatch) -> None:
    """An ingress-only call still GET-merges and Updates spec.ingress."""
    client = GRPCClient(address="fulfillment-api.example.test:443", token="test-token")
    calls: list[dict[str, Any]] = []

    def capture_call(*, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        calls.append({"service": service, "data": data})
        return {
            "object": {
                "id": "security-group-id",
                "spec": {"virtual_network": {"id": "vn-id"}},
                "metadata": {"name": "sg"},
            }
        }

    monkeypatch.setattr(client, "call", capture_call)

    client.update_security_group_rules(sg_id="security-group-id", ingress=[])

    assert calls[0]["service"] == "osac.private.v1.SecurityGroups/Get"
    assert calls[1]["service"] == "osac.private.v1.SecurityGroups/Update"
    assert calls[1]["data"]["updateMask"]["paths"] == ["spec.ingress"]
