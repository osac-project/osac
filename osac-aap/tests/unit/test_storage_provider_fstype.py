"""Emit actual Ansible StorageClasses across discovered providers and dispatch routes."""

import copy
import importlib.util
import json
import ssl
import subprocess
import threading

import pytest
import yaml

from .storage_provider_cases import (
    BLOCK_CREATORS, CASES, CONTRACTS, CREATORS, LOCAL, ProviderCase, assert_success,
    created_classes, discover_cases, run_provider,
)
from .storage_runner import OSAC_AAP

FSTYPE = "csi.storage.k8s.io/fstype"
OPAQUE = " Opaque:FS/9 "


@pytest.mark.parametrize("case", CASES, ids=lambda case: case.id)
def test_every_discovered_dispatch_emits_declared_protocols(tmp_path, case):
    result, calls = run_provider(tmp_path, case)
    assert_success(result)
    classes = created_classes(calls)
    assert len(classes) == len(case.protocols)
    contract = CONTRACTS[case.role]
    for protocol in case.protocols:
        emitted = next(entry for entry in classes if entry["metadata"]["name"].endswith(f"-{protocol}"))
        assert emitted["provisioner"] == contract.get(f"{protocol}_provisioner", contract["provisioner"])
        if protocol == "block":
            assert emitted["parameters"][FSTYPE] == "ext4"
            if case.mode == "shared":
                assert emitted["parameters"]["storagePools"] == "fixture:.*"
            elif case.mode == "dedicated":
                assert emitted["parameters"]["selector"].endswith(";tier=block")
                assert "storagePools" not in emitted["parameters"]
        else:
            assert not any(key == FSTYPE or key.lower() == "fstype" for key in emitted["parameters"])
            assert contract["nfs_parameters"].items() <= emitted["parameters"].items()
            if case.role == "vast_storage":
                assert emitted["volumeBindingMode"] == "Immediate"


def test_unknown_provider_fails_coverage(tmp_path):
    metadata = tmp_path / "uncovered_storage/meta"
    metadata.mkdir(parents=True)
    (metadata / "osac.yaml").write_text(yaml.safe_dump({"template_type": "storage_provider"}))
    with pytest.raises(AssertionError, match="Uncovered storage provider: uncovered_storage"):
        discover_cases(tmp_path)


def test_nfs_only_discovery_does_not_require_block(tmp_path, monkeypatch):
    from . import storage_provider_cases

    monkeypatch.setitem(storage_provider_cases.DEFAULTS, "storage_provider_role_overrides", {})
    metadata = tmp_path / "nfs_only_storage/meta"
    metadata.mkdir(parents=True)
    (metadata / "osac.yaml").write_text(yaml.safe_dump({
        "template_type": "storage_provider", "capabilities": {"supported_protocols": ["nfs"]},
    }))
    cases = discover_cases(tmp_path, {"nfs_only_storage": {
        "modes": ["default"], "nfs_parameters": {},
    }})
    assert cases
    assert all(case.protocols == ("nfs",) for case in cases)
    assert not [case for case in cases if "block" in case.protocols]


def test_production_dispatch_consumes_mapping(tmp_path):
    case = ProviderCase("contract-route", "lvms_storage")
    result, calls = run_provider(tmp_path, case, overrides={
        "storage_provider_role_overrides": {"contract_route": "lvms_storage"},
    })
    assert_success(result)
    assert created_classes(calls)[0]["provisioner"] == CONTRACTS[case.role]["provisioner"]


@pytest.fixture(scope="module", params=[*BLOCK_CREATORS, LOCAL], ids=lambda case: case.id)
def snapshot(request, tmp_path_factory):
    case = request.param
    directory = tmp_path_factory.mktemp(case.id)
    result, calls = run_provider(directory, case, protocols=("block",), names=("existing",))
    assert_success(result)
    classes = created_classes(calls)
    assert len(classes) == 1
    assert classes[0]["parameters"][FSTYPE] == "ext4"
    return case, json.loads((directory / "objects.json").read_text()), classes[0]


def test_partial_creates_only_new_filesystem_selection(tmp_path, snapshot):
    case, objects, existing = snapshot
    result, calls = run_provider(tmp_path, case, protocols=("block", "block"), names=("existing", "new"),
                                 existing=objects, environment={"OSAC_STORAGE_BLOCK_FSTYPE": "xfs"})
    assert_success(result)
    classes = created_classes(calls)
    new = [entry for entry in classes if entry["metadata"]["name"] != existing["metadata"]["name"]]
    assert len(new) == 1
    assert new[0]["parameters"][FSTYPE] == "xfs"
    for entry in classes:
        if entry["metadata"]["name"] == existing["metadata"]["name"]:
            assert entry["parameters"] == existing["parameters"]
    assert not [call for call in calls if call.get("state") == "absent"]


def test_vast_detects_existing_block_deleted_during_creation(tmp_path):
    case = next(case for case in BLOCK_CREATORS if case.role == "vast_storage")
    result, calls = run_provider(tmp_path / "seed", case, protocols=("block",), names=("gold",))
    assert_success(result)
    gold = created_classes(calls)[0]
    gold["parameters"].pop(FSTYPE)
    gold_name = gold["metadata"]["name"]
    silver_name = gold_name.removesuffix("gold") + "silver"
    result, calls = run_provider(
        tmp_path / "disappear", case, protocols=("block", "block"), names=("gold", "silver"), existing=[gold],
        environment={"OSAC_STORAGE_DELETE_ON_CREATE": json.dumps({silver_name: gold_name})},
    )
    assert [entry["metadata"]["name"] for entry in created_classes(calls)] == [silver_name]
    write = next(call for call in calls if (call.get("definition") or {}).get("kind") == "StorageClass")
    names = [entry["metadata"]["name"] for entry in write["objects"] if entry.get("kind") == "StorageClass"]
    assert gold_name not in names
    assert silver_name in names
    assert result.returncode != 0
    assert "VAST block StorageClasses missing at completion" in result.stdout
    assert gold_name in result.stdout
    assert len([call for call in calls if call["module"] == "k8s_info" and call["kind"] == "StorageClass"]) == 2
    assert not [call for call in calls if call.get("state") == "absent"]


def test_each_new_consumer_rejects_invalid_before_writes(tmp_path, snapshot):
    case, _, _ = snapshot
    result, calls = run_provider(tmp_path, case, protocols=("block",),
                                 overrides={"storage_provider_block_fstype": {"bad": "type"}})
    assert result.returncode != 0
    assert "must be a nonempty string" in result.stdout
    assert all(call["module"] == "k8s_info" for call in calls)


@pytest.mark.parametrize("original", [None, "ext4"])
def test_existing_filesystem_and_absence_survive_unused_bad_override(tmp_path, snapshot, original):
    case, original_objects, existing = snapshot
    objects = copy.deepcopy(original_objects)
    for entry in objects:
        if entry.get("kind") == "StorageClass":
            entry["parameters"].pop(FSTYPE, None)
            if original is not None:
                entry["parameters"][FSTYPE] = original
    expected = None if case == LOCAL else [existing["metadata"]["name"]]
    result, calls = run_provider(tmp_path, case, protocols=("block",), names=("existing",), existing=objects,
                                 overrides={"storage_provider_block_fstype": {"bad": "unused"}}, expected=expected)
    assert_success(result)
    assert not [call for call in calls if call.get("state") == "absent"]
    for entry in created_classes(calls):
        assert entry["parameters"].get(FSTYPE) == original
        assert (FSTYPE in entry["parameters"]) == (original is not None)
    if case.role == "vast_storage":
        assert not created_classes(calls)
        assert len(calls) == 1


@pytest.mark.parametrize("value", ["", [], ["xfs"], {"not": "a string"}, 123, True, 1.5, None])
def test_invalid_consumed_override_fails_before_writes(tmp_path, value):
    case = next(case for case in BLOCK_CREATORS if case.role == "lvms_storage")
    result, calls = run_provider(tmp_path, case, overrides={"storage_provider_block_fstype": value})
    assert result.returncode != 0
    assert "must be a nonempty string" in result.stdout
    assert all(call["module"] == "k8s_info" for call in calls)


@pytest.mark.parametrize("value", [OPAQUE, "   ", "123", "False", "None", "[]", "{}"])
def test_opaque_string_override_precedes_environment(tmp_path, value):
    case = next(case for case in BLOCK_CREATORS if case.role == "lvms_storage")
    result, calls = run_provider(tmp_path, case, overrides={"storage_provider_block_fstype": value},
                                 environment={"OSAC_STORAGE_BLOCK_FSTYPE": "environment-value"})
    assert_success(result)
    assert type(created_classes(calls)[0]["parameters"][FSTYPE]) is str
    assert created_classes(calls)[0]["parameters"][FSTYPE] == value


@pytest.mark.parametrize("case", [case for case in BLOCK_CREATORS if "nfs" in case.protocols],
                         ids=lambda case: case.id)
def test_nfs_and_existing_block_ignore_invalid_setting(tmp_path, case):
    result, calls = run_provider(tmp_path / "seed", case, protocols=("block",), names=("existing",))
    assert_success(result)
    existing = created_classes(calls)[0]
    result, calls = run_provider(tmp_path / "nfs", case, protocols=("block", "nfs"), names=("existing", "new"),
                                 existing=[existing], overrides={"storage_provider_block_fstype": ["xfs"]})
    assert_success(result)
    classes = created_classes(calls)
    assert len(classes) == 1
    assert FSTYPE not in classes[0]["parameters"]
    if case.role == "vast_storage":
        assert classes[0]["parameters"]["root_export"].endswith("/new")
        assert classes[0]["parameters"]["view_policy"] == "osac-fstype-test-nfs"
        assert classes[0]["volumeBindingMode"] == "Immediate"
        assert len([call for call in calls if call["module"] == "k8s_info" and call["kind"] == "StorageClass"]) == 1


@pytest.mark.parametrize("case", [case for case in CREATORS if "nfs" in case.protocols], ids=lambda case: case.id)
def test_nfs_only_ignores_invalid_setting(tmp_path, case):
    result, calls = run_provider(tmp_path, case, protocols=("nfs",),
                                 overrides={"storage_provider_block_fstype": ["xfs"]})
    assert_success(result)
    assert len(created_classes(calls)) == 1
    assert FSTYPE not in created_classes(calls)[0]["parameters"]


def test_empty_environment_fails_at_consumer(tmp_path):
    result, calls = run_provider(tmp_path, BLOCK_CREATORS[0], protocols=("block",),
                                 environment={"OSAC_STORAGE_BLOCK_FSTYPE": ""})
    assert result.returncode != 0
    assert "must be a nonempty string" in result.stdout
    assert not created_classes(calls)


def test_local_immutable_nonfilesystem_fields_still_checked(tmp_path, snapshot):
    case, original_objects, existing = snapshot
    if case != LOCAL:
        return
    objects = copy.deepcopy(original_objects)
    next(entry for entry in objects if entry.get("kind") == "StorageClass")["parameters"]["osac.tier"] = "wrong"
    result, calls = run_provider(tmp_path, case, names=("existing",), existing=objects)
    assert result.returncode != 0
    assert "incompatible" in result.stdout
    assert not created_classes(calls)


@pytest.fixture
def vms_server(tmp_path):
    spec = importlib.util.spec_from_file_location("fstype_mock_vms", OSAC_AAP / "tests/integration/mock_vms_server.py")
    mock = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mock)
    certificate = tmp_path / "fixture.crt"
    key = tmp_path / "fixture.key"
    subprocess.run(
        ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
         "-subj", "/CN=localhost", "-keyout", str(key), "-out", str(certificate)],
        check=True, capture_output=True,
    )
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    context.load_cert_chain(certificate, key)
    server = mock.ThreadingHTTPServer(("127.0.0.1", 0), mock.MockVmsHandler)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"127.0.0.1:{server.server_port}", mock.CALL_LOG
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@pytest.mark.parametrize("case", CREATORS, ids=lambda case: case.id)
@pytest.mark.parametrize("action", ["teardown_backend", "teardown_cluster_storage"])
def test_teardown_does_not_consume_invalid_filesystem(tmp_path, case, action, vms_server):
    endpoint, requests = vms_server
    result, calls = run_provider(tmp_path, case, action=action, overrides={
        "storage_provider_block_fstype": ["xfs"],
        "storage_provider_backend_connections": {"fixture": {
            "endpoint": endpoint, "username": "fixture", "password": "fixture-only",
        }},
    })
    assert_success(result)
    assert not created_classes(calls)
    if case.role in ("lvms_storage", "vast_storage"):
        if action == "teardown_backend":
            assert any(call.get("state") == "absent" and call.get("kind") == "Secret" for call in calls)
            if case.role == "vast_storage":
                assert any(request["path"].startswith("/api/views/") for request in requests)
                secret_lists = [call for call in calls if call.get("kind") == "Secret" and call.get("name") is None]
                assert any(call["resources"][0]["metadata"]["name"] for call in secret_lists if call.get("resources"))
        else:
            assert any(call.get("kind") == "StorageClass" for call in calls)
