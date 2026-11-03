from __future__ import annotations

from typing import Any

import pytest

from tests.e2e.core.runner import CommandFailed
from tests.e2e.networking.bmaas import test_bmaas_networking as test_module


class _PrivateGRPC:
    def __init__(self, external_ips: list[dict[str, Any]]) -> None:
        self.external_ips = external_ips
        self.pool_ids = ["pool-1"]
        self.deleted: list[str] = []
        self.pool_id = "pool-1"
        self.default_ip_on_delete = False

    def list_private_external_ips(self) -> list[dict[str, Any]]:
        return self.external_ips

    def get_external_ip_pool(self, *, pool_id: str) -> dict[str, Any]:
        allocated = sum(item.get("spec", {}).get("pool", {}).get("id") == pool_id for item in self.external_ips)
        return {"object": {"status": {"allocated": allocated}}}

    def delete_external_ip_pool(self, *, pool_id: str) -> None:
        self.deleted.append(pool_id)
        if self.default_ip_on_delete:
            self.external_ips.append(_external_ip(name="default-nat-race", default=True))
            raise CommandFailed(1, ["grpcurl"], stderr="Code: FailedPrecondition, pool still has allocated addresses")
        self.pool_ids.remove(pool_id)

    def list_external_ip_pool_ids(self) -> list[str]:
        return self.pool_ids


def _external_ip(*, name: str, default: bool) -> dict[str, Any]:
    labels = {"osac.openshift.io/default": "true"} if default else {}
    return {
        "id": name,
        "metadata": {"name": name, "tenant": "tenant-1", "labels": labels},
        "spec": {"pool": {"id": "pool-1"}},
    }


def _set_pool_state(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(test_module.TestBmaasNetworking, "state", {"pool_id": "pool-1", "pool_cr": "pool-cr"})
    monkeypatch.setattr(test_module, "wait_for_external_ip_pool_deletion", lambda **_kwargs: None)


def test_bmaas_pool_cleanup_leaves_pool_held_by_default_external_ips(monkeypatch: pytest.MonkeyPatch) -> None:
    _set_pool_state(monkeypatch)
    grpc = _PrivateGRPC([_external_ip(name="default-nat", default=True)])

    test_module.TestBmaasNetworking().test_19_delete_external_ip_pool(grpc, object())

    assert grpc.deleted == []
    assert grpc.pool_ids == ["pool-1"]


def test_bmaas_pool_cleanup_fails_for_an_unexpected_holder(monkeypatch: pytest.MonkeyPatch) -> None:
    _set_pool_state(monkeypatch)
    grpc = _PrivateGRPC([_external_ip(name="unexpected", default=False)])

    with pytest.raises(AssertionError, match="unexpected"):
        test_module.TestBmaasNetworking().test_19_delete_external_ip_pool(grpc, object())

    assert grpc.deleted == []


def test_bmaas_pool_cleanup_deletes_an_unheld_pool(monkeypatch: pytest.MonkeyPatch) -> None:
    _set_pool_state(monkeypatch)
    grpc = _PrivateGRPC([])

    test_module.TestBmaasNetworking().test_19_delete_external_ip_pool(grpc, object())

    assert grpc.deleted == ["pool-1"]
    assert grpc.pool_ids == []


def test_bmaas_pool_cleanup_keeps_default_holder_race(monkeypatch: pytest.MonkeyPatch) -> None:
    _set_pool_state(monkeypatch)
    grpc = _PrivateGRPC([])
    grpc.default_ip_on_delete = True

    test_module.TestBmaasNetworking().test_19_delete_external_ip_pool(grpc, object())

    assert grpc.deleted == ["pool-1"]
    assert grpc.pool_ids == ["pool-1"]
