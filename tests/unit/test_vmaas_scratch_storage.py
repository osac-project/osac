from __future__ import annotations

import copy
import json
from collections.abc import Generator
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.vmaas.regression import test_vmaas_vm_lifecycle_demo as lifecycle


class ScratchCluster:
    """In-memory Kubernetes JSON Patch and CDI projection; no cluster calls."""

    namespace = "test-namespace"
    kubeconfig = None
    as_system_admin = True

    def __init__(self, version: str, spec: dict[str, Any]) -> None:
        self.version = version
        self.hco = {
            "metadata": {"name": "test-hco", "namespace": self.namespace, "uid": "original-uid"},
            "spec": copy.deepcopy(spec),
        }
        self.versions = [self.schema(version)]
        self.patches: list[list[dict[str, Any]]] = []
        self.resources: list[str] = []
        self.prune_scratch = False

    @staticmethod
    def schema(version: str) -> dict[str, Any]:
        fields = {"scratchSpaceStorageClass": {"type": "string"}}
        if version == "v1":
            fields = {"storage": {"properties": fields}}
        return {
            "name": version,
            "served": True,
            "schema": {"openAPIV3Schema": {"properties": {"spec": {"properties": fields}}}},
        }

    def storage(self) -> dict[str, Any]:
        spec = self.hco["spec"]
        return spec.get("storage", {}) if self.version == "v1" else spec

    def get_json(self, *, resource: str, name: str) -> dict[str, Any]:
        if resource.startswith("customresourcedefinitions."):
            return {"spec": {"versions": self.versions}}
        if resource.startswith("storageclasses."):
            return {}
        if resource.startswith("cdiconfigs."):
            return {"status": {"scratchSpaceStorageClass": self.storage().get("scratchSpaceStorageClass")}}
        assert resource == f"hyperconvergeds.{self.version}.hco.kubevirt.io"
        self.resources.append(resource)
        return copy.deepcopy(self.hco)

    def _get(self, *args: str) -> tuple[str, int]:
        self.resources.append(args[1])
        assert args[1] == f"hyperconvergeds.{self.version}.hco.kubevirt.io"
        if args[0] == "get":
            return json.dumps({"items": [self.hco]}), 0
        assert args[0] == "patch"
        operations = json.loads(args[-1])
        self.patches.append(operations)
        updated = copy.deepcopy(self.hco)
        for operation in operations:
            parts = [part.replace("~1", "/").replace("~0", "~") for part in operation["path"].split("/")[1:]]
            parent = updated
            for part in parts[:-1]:
                parent = parent[part]
            key = parts[-1]
            if operation["op"] == "test":
                assert parent[key] == operation["value"]
            elif operation["op"] == "remove":
                del parent[key]
            else:
                assert operation["op"] in {"add", "replace"}
                parent[key] = operation["value"]
        self.hco = updated
        if self.prune_scratch:
            self.storage().pop("scratchSpaceStorageClass", None)
        return "{}", 0


@pytest.fixture
def scratch_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_E2E_SCRATCH_STORAGE_CLASS", "scratch-class")
    monkeypatch.delenv("PYTEST_XDIST_WORKER", raising=False)


def prepare(cluster: ScratchCluster, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Generator[None, None, None]:
    monkeypatch.setattr(lifecycle, "K8sClient", lambda **kwargs: cluster)
    return lifecycle.temporary_scratch_storage.__wrapped__(cluster, tmp_path)


@pytest.mark.usefixtures("scratch_env")
@pytest.mark.parametrize("version", ["v1", "v1beta1"])
@pytest.mark.parametrize("previous", [None, "", "original-class", "scratch-class"])
def test_prepare_and_restore(
    version: str, previous: str | None, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    fields = {} if previous is None else {"scratchSpaceStorageClass": previous}
    spec = {"storage": fields, "unrelated": "preserved"} if version == "v1" else {**fields, "unrelated": "preserved"}
    cluster = ScratchCluster(version, spec)
    cluster.hco["metadata"]["annotations"] = {"existing": "preserved"}
    original = copy.deepcopy(cluster.hco)
    fixture = prepare(cluster, monkeypatch, tmp_path)

    next(fixture)
    assert cluster.storage()["scratchSpaceStorageClass"] == "scratch-class"
    record = json.loads((tmp_path / "scratch-storage-original.json").read_text())
    assert record["resource"] == f"hyperconvergeds.{version}.hco.kubevirt.io"
    with pytest.raises(StopIteration):
        next(fixture)

    assert cluster.hco == original
    assert len(cluster.patches) == (0 if previous == "scratch-class" else 2)


@pytest.mark.usefixtures("scratch_env")
@pytest.mark.parametrize("add_other_field", [False, True])
def test_absent_storage_parent_restored_without_losing_concurrent_fields(
    add_other_field: bool, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    cluster = ScratchCluster("v1", {})
    fixture = prepare(cluster, monkeypatch, tmp_path)
    next(fixture)
    if add_other_field:
        cluster.storage()["vmStateStorageClass"] = "other-class"
    with pytest.raises(StopIteration):
        next(fixture)
    assert cluster.hco["spec"] == ({"storage": {"vmStateStorageClass": "other-class"}} if add_other_field else {})
    assert lifecycle._SCRATCH_OWNER not in cluster.hco["metadata"]["annotations"]


@pytest.mark.usefixtures("scratch_env")
@pytest.mark.parametrize("change", ["uid", "owner", "value"])
def test_restore_refuses_external_changes(change: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    cluster = ScratchCluster("v1", {"storage": {}})
    fixture = prepare(cluster, monkeypatch, tmp_path)
    next(fixture)
    if change == "uid":
        cluster.hco["metadata"]["uid"] = "replacement-uid"
    elif change == "owner":
        cluster.hco["metadata"]["annotations"][lifecycle._SCRATCH_OWNER] = "other-owner"
    else:
        cluster.storage()["scratchSpaceStorageClass"] = "external-class"
    external = copy.deepcopy(cluster.hco)
    with pytest.raises(RuntimeError, match="restore scratch storage"):
        next(fixture)
    assert cluster.hco == external
    assert len(cluster.patches) == 1


def test_schema_selection_prefers_v1_and_checks_served_fields() -> None:
    cluster = ScratchCluster("v1", {})
    cluster.versions = [cluster.schema("v1beta1"), cluster.schema("v1")]
    assert lifecycle._scratch_api(cluster) == ("hyperconvergeds.v1.hco.kubevirt.io", True)
    cluster.versions[1]["served"] = False
    assert lifecycle._scratch_api(cluster) == ("hyperconvergeds.v1beta1.hco.kubevirt.io", False)
    cluster.versions[1]["served"] = True
    cluster.versions[1]["schema"] = {}
    assert lifecycle._scratch_api(cluster) == ("hyperconvergeds.v1beta1.hco.kubevirt.io", False)


@pytest.mark.usefixtures("scratch_env")
def test_unknown_schema_fails_before_writes(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    cluster = ScratchCluster("v2", {})
    fixture = prepare(cluster, monkeypatch, tmp_path)
    with pytest.raises(RuntimeError, match="No supported HyperConverged"):
        next(fixture)
    assert cluster.patches == []


@pytest.mark.usefixtures("scratch_env")
@pytest.mark.parametrize("version", ["v1", "v1beta1"])
def test_test_failure_still_restores_setting(version: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    fields = {"scratchSpaceStorageClass": "original-class"}
    cluster = ScratchCluster(version, {"storage": fields} if version == "v1" else fields)
    fixture = prepare(cluster, monkeypatch, tmp_path)
    next(fixture)
    with pytest.raises(AssertionError, match="scenario failed"):
        fixture.throw(AssertionError("scenario failed"))
    assert cluster.storage()["scratchSpaceStorageClass"] == "original-class"
    assert lifecycle._SCRATCH_OWNER not in cluster.hco["metadata"]["annotations"]


@pytest.mark.usefixtures("scratch_env")
def test_parallel_preparation_refused_before_cluster_calls(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw0")
    cluster = ScratchCluster("v1", {})
    fixture = prepare(cluster, monkeypatch, tmp_path)
    with pytest.raises(RuntimeError, match="serial execution"):
        next(fixture)
    assert cluster.patches == []
    assert cluster.resources == []


@pytest.mark.usefixtures("scratch_env")
def test_existing_owner_refused_before_writes(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    cluster = ScratchCluster("v1", {})
    cluster.hco["metadata"]["annotations"] = {lifecycle._SCRATCH_OWNER: "other-owner"}
    fixture = prepare(cluster, monkeypatch, tmp_path)
    with pytest.raises(RuntimeError, match="Another scratch preparation"):
        next(fixture)
    assert cluster.patches == []


@pytest.mark.usefixtures("scratch_env")
@pytest.mark.parametrize("version", ["v1", "v1beta1"])
def test_pruned_setting_fails_fast_and_removes_ownership(
    version: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    cluster = ScratchCluster(version, {})
    cluster.prune_scratch = True
    fixture = prepare(cluster, monkeypatch, tmp_path)
    with pytest.raises(RuntimeError, match="did not retain"):
        next(fixture)
    assert lifecycle._SCRATCH_OWNER not in cluster.hco["metadata"]["annotations"]
    assert "scratchSpaceStorageClass" not in cluster.storage()
    assert cluster.hco["spec"] == {}


def test_disabled_preparation_has_no_cluster_calls(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.delenv("OSAC_E2E_SCRATCH_STORAGE_CLASS", raising=False)
    cluster = ScratchCluster("v1", {})
    fixture = prepare(cluster, monkeypatch, tmp_path)
    next(fixture)
    with pytest.raises(StopIteration):
        next(fixture)
    assert cluster.resources == []
    assert cluster.patches == []
