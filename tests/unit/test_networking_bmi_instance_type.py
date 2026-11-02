from __future__ import annotations

from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.networking import conftest as networking_fixtures


@pytest.mark.parametrize("configured", [None, "", "  "])
def test_networking_instance_type_falls_back_to_legacy_host_selector(
    monkeypatch: pytest.MonkeyPatch, configured: str | None
) -> None:
    monkeypatch.setenv("OSAC_BM_HOST_TYPE", "metal.default")
    if configured is None:
        monkeypatch.delenv("OSAC_BMI_INSTANCE_TYPE", raising=False)
    else:
        monkeypatch.setenv("OSAC_BMI_INSTANCE_TYPE", configured)
    grpc = Mock(spec=GRPCClient)
    grpc.call.return_value = {"items": [{"metadata": {"name": "metal.default"}}]}

    assert networking_fixtures.bmi_instance_type.__wrapped__(grpc) == "metal.default"
    grpc.call.assert_called_once_with(service="osac.private.v1.BareMetalInstanceTypes/List")


@pytest.mark.parametrize(
    ("response", "expected"),
    [
        ({"items": [{"metadata": {"name": "metal.default"}}]}, "metal.default"),
        ({"items": [{"metadata": {"name": "other-type"}}]}, ""),
        ({"items": []}, ""),
        ({"items": None}, ""),
        ({}, ""),
        ({"items": [{}, {"metadata": None}]}, ""),
    ],
)
def test_networking_instance_type_trims_explicit_name_and_checks_catalog(
    monkeypatch: pytest.MonkeyPatch, response: dict[str, Any], expected: str
) -> None:
    monkeypatch.setenv("OSAC_BMI_INSTANCE_TYPE", "  metal.default  ")
    monkeypatch.setenv("OSAC_BM_HOST_TYPE", "other-type")
    grpc = Mock(spec=GRPCClient)
    grpc.call.return_value = response

    assert networking_fixtures.bmi_instance_type.__wrapped__(grpc) == expected
    grpc.call.assert_called_once_with(service="osac.private.v1.BareMetalInstanceTypes/List")
