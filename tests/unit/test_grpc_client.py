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
