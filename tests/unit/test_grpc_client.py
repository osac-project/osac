from __future__ import annotations

from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient


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
