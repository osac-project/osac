"""Execute provider tasks with recording Kubernetes and VMS module boundaries."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import ssl
import threading

import pytest


OSAC_AAP = Path(__file__).resolve().parents[2]
FSTYPE = "csi.storage.k8s.io/fstype"
OPAQUE = " Opaque:FS/9 "
LITERAL_STRINGS = ["123", "True", "False", "None", "[]", "{}"]
KUBERNETES_MODULE = """
import base64
import json
import os
from ansible.module_utils.basic import AnsibleModule

module = AnsibleModule(argument_spec={
    "kubeconfig": {"type": "str"},
    "validate_certs": {"type": "bool"},
    "api_version": {"type": "str"},
    "kind": {"type": "str"},
    "name": {"type": "str"},
    "namespace": {"type": "str"},
    "label_selectors": {"type": "list", "elements": "str"},
    "state": {"type": "str"},
    "definition": {"type": "dict"},
})
params = module.params
resources = []
if params["kind"] == "StorageClass":
    resources = [entry if isinstance(entry, dict) else
                 {"metadata": {"name": entry}, "parameters": {"legacy-option": "retained"}} for entry in
                 json.loads(os.environ["OSAC_FSTYPE_EXISTING"])]
elif params["kind"] == "CSIDriver":
    resources = [{"metadata": {"name": params["name"]}}]
elif params["kind"] == "Secret" and params["state"] is None:
    values = {
        "vast_tenant_id": "1", "tenant_manager_username": "fixture-manager",
        "tenant_manager_password": "fixture-only", "vip_pool_name": "fixture-pool",
        "vast_endpoint": "fixture.invalid", "tenant_uid_hash": "12345678",
    }
    resources = [{"data": {key: base64.b64encode(value.encode()).decode()
                           for key, value in values.items()}}]
with open(os.environ["OSAC_FSTYPE_CAPTURE"], "a") as capture:
    capture.write(json.dumps({"module": "__MODULE_NAME__", "resources": resources, **params}) + "\\n")
module.exit_json(changed=False, resources=resources)
"""
VMS_MODULE = """
from ansible.module_utils.basic import AnsibleModule

module = AnsibleModule(argument_spec={
    "vms": {"type": "dict"}, "name": {"type": "str"},
    "flavor": {"type": "str"}, "tenant_id": {"type": "str"},
    "state": {"type": "str"}, "path": {"type": "str"},
    "create_dir": {"type": "bool"}, "policy_id": {"type": "str"},
    "protocols": {"type": "list", "elements": "str"},
    "force_remove": {"type": "bool"},
})
module.exit_json(changed=False, viewpolicies={"id": 1})
"""


def run_provider(
    tmp_path,
    provider,
    fstype=None,
    action="ensure_storage_class",
    protocols=("block",),
    existing=(),
    tier_names=None,
    endpoint="fixture.invalid",
    explicit_fstype=None,
    expected_classes=None,
):
    for collection, names, source in (
        ("kubernetes/core", ("k8s", "k8s_info"), KUBERNETES_MODULE),
        ("vastdata/vms", ("viewpolicies", "views", "tenants"), VMS_MODULE),
    ):
        modules = tmp_path / "ansible_collections" / collection / "plugins/modules"
        modules.mkdir(parents=True)
        for name in names:
            (modules / f"{name}.py").write_text(source.replace("__MODULE_NAME__", name))
    tiers = [
        {"name": name, "protocol": protocol, "provider": provider, "backend_id": "fixture"}
        for name, protocol in zip(tier_names or protocols, protocols, strict=True)
    ]
    overrides = {} if explicit_fstype is None else {"storage_provider_block_fstype": explicit_fstype}
    playbook = tmp_path / "provider.json"
    playbook.write_text(
        json.dumps(
            [
                {
                    "name": "Exercise storage provider filesystem configuration",
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": {
                        **overrides,
                        "ansible_connection": "local",
                        "ansible_python_interpreter": sys.executable,
                        "tenant_name": "fstype-test",
                        "tenant_namespace": "osac-tenants",
                        "vast_storage_config_namespace": "osac-system",
                        "vast_storage_validate_certs": False,
                        "lvms_storage_config_namespace": "osac-system",
                        "_cluster_name": "fixture-cluster",
                        "storage_provider_tiers": tiers,
                        "storage_provider_action": action,
                        "storage_provider_backend_connections": {
                            "fixture": {"endpoint": endpoint, "username": "fixture", "password": "fixture-only"}
                        },
                    },
                    "tasks": [
                        {
                            "name": "Dispatch real provider tasks",
                            "ansible.builtin.include_role": {"name": "osac.service.storage_provider"},
                        }
                    ]
                    + (
                        [
                            {
                                "name": "Verify complete returned StorageClass names",
                                "ansible.builtin.assert": {
                                    "that": "storage_provider_storage_class_names == expected_classes"
                                },
                                "vars": {"expected_classes": expected_classes},
                            }
                        ]
                        if expected_classes is not None
                        else []
                    ),
                }
            ]
        )
    )
    capture = tmp_path / "calls.jsonl"
    environment = {
        **os.environ,
        "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
        "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path}:{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
        "ANSIBLE_FORCE_COLOR": "false",
        "ANSIBLE_STDOUT_CALLBACK": "default",
        "NO_PROXY": "127.0.0.1,localhost",
        "no_proxy": "127.0.0.1,localhost",
        "OSAC_FSTYPE_CAPTURE": str(capture),
        "OSAC_FSTYPE_EXISTING": json.dumps(existing),
    }
    environment.pop("OSAC_STORAGE_BLOCK_FSTYPE", None)
    if fstype is not None:
        environment["OSAC_STORAGE_BLOCK_FSTYPE"] = fstype
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(playbook)],
        cwd=OSAC_AAP,
        env=environment,
        capture_output=True,
        text=True,
        timeout=90,
        check=False,
    )
    calls = [json.loads(line) for line in capture.read_text().splitlines()] if capture.exists() else []
    return result, calls


def created_classes(calls):
    return [
        call["definition"]
        for call in calls
        if call["state"] == "present" and (call["definition"] or {}).get("kind") == "StorageClass"
    ]


def existing_block_class(provider, name, fstype):
    parameters = (
        {"subsystem": "view-fstype-test-12345678-block", "tenant_name": "fstype-test"}
        if provider == "vast"
        else {"topolvm.io/device-class": "vg1"}
    )
    if fstype is not None:
        parameters[FSTYPE] = fstype
    return {
        "apiVersion": "storage.k8s.io/v1",
        "kind": "StorageClass",
        "metadata": {
            "name": name,
            "labels": {
                "osac.openshift.io/tenant": "fstype-test",
                "app.kubernetes.io/managed-by": "osac-aap",
            },
        },
        "provisioner": "csi.vastdata.com" if provider == "vast" else "topolvm.io",
        "parameters": parameters,
        "reclaimPolicy": "Delete",
        "volumeBindingMode": "WaitForFirstConsumer",
    }


def assert_existing_classes_untouched(calls, existing):
    lookups = [call for call in calls if call["module"] == "k8s_info" and call["kind"] == "StorageClass"]
    assert len(lookups) == 1
    assert lookups[0]["resources"] == existing
    existing_names = {resource["metadata"]["name"] for resource in existing}
    for call in calls:
        if call["module"] != "k8s":
            continue
        assert call["state"] == "present"
        definition = call["definition"] or {}
        kind = definition.get("kind", call["kind"])
        assert kind not in ("PersistentVolume", "PersistentVolumeClaim")
        if kind == "StorageClass":
            assert definition.get("metadata", {}).get("name", call["name"]) not in existing_names


@pytest.fixture
def vms_server(tmp_path):
    spec = importlib.util.spec_from_file_location("fstype_mock_vms", OSAC_AAP / "tests/integration/mock_vms_server.py")
    mock = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mock)
    certificate = tmp_path / "fixture.crt"
    key = tmp_path / "fixture.key"
    subprocess.run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=localhost",
            "-keyout",
            str(key),
            "-out",
            str(certificate),
        ],
        check=True,
        capture_output=True,
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


@pytest.mark.parametrize("provider", ["vast", "lvms"])
@pytest.mark.parametrize("fstype", [None, "xfs", OPAQUE, "   ", *LITERAL_STRINGS])
def test_generated_block_fstype(tmp_path, provider, fstype):
    result, calls = run_provider(tmp_path, provider, fstype)
    assert result.returncode == 0, result.stdout + result.stderr
    classes = created_classes(calls)
    assert len(classes) == 1
    assert type(classes[0]["parameters"][FSTYPE]) is str
    assert classes[0]["parameters"][FSTYPE] == ("ext4" if fstype is None else fstype)
    assert classes[0]["volumeBindingMode"] == "WaitForFirstConsumer"


@pytest.mark.parametrize("provider", ["vast", "lvms"])
def test_empty_fstype_rejected_at_block_consumer(tmp_path, provider):
    result, calls = run_provider(tmp_path, provider, "")
    assert result.returncode != 0
    assert "must be defined and nonempty for block StorageClasses" in result.stdout
    assert created_classes(calls) == []


@pytest.mark.parametrize("provider", ["vast", "lvms"])
@pytest.mark.parametrize("explicit_fstype", ["xfs", OPAQUE, "   ", "", *LITERAL_STRINGS])
def test_ansible_override_precedes_environment(tmp_path, provider, explicit_fstype):
    result, calls = run_provider(tmp_path, provider, "environment-value", explicit_fstype=explicit_fstype)
    if explicit_fstype:
        assert result.returncode == 0, result.stdout + result.stderr
        classes = created_classes(calls)
        assert len(classes) == 1
        assert type(classes[0]["parameters"][FSTYPE]) is str
        assert classes[0]["parameters"][FSTYPE] == explicit_fstype
    else:
        assert result.returncode != 0
        assert "must be defined and nonempty for block StorageClasses" in result.stdout
        assert created_classes(calls) == []


@pytest.mark.parametrize("fstype", [None, OPAQUE, ""])
def test_nfs_unchanged(tmp_path, fstype):
    result, calls = run_provider(tmp_path, "vast", fstype, protocols=("nfs",))
    assert result.returncode == 0, result.stdout + result.stderr
    classes = created_classes(calls)
    assert len(classes) == 1
    parameters = classes[0]["parameters"]
    assert FSTYPE not in parameters
    assert parameters["root_export"] == "/osac-fstype-test-12345678/nfs"
    assert parameters["view_policy"] == "osac-fstype-test-nfs"
    assert classes[0]["provisioner"] == "csi.vastdata.com"
    assert classes[0]["volumeBindingMode"] == "Immediate"


@pytest.mark.parametrize("provider", ["vast", "lvms"])
@pytest.mark.parametrize("action", ["teardown_cluster_storage", "teardown_backend"])
def test_teardown_ignores_empty_block_setting(tmp_path, vms_server, provider, action):
    endpoint, requests = vms_server
    result, calls = run_provider(tmp_path, provider, "", action=action, endpoint=endpoint)
    assert result.returncode == 0, result.stdout + result.stderr
    assert created_classes(calls) == []
    if action == "teardown_backend":
        assert any(call["state"] == "absent" and call["kind"] == "Secret" for call in calls)
        if provider == "vast":
            assert any(request["path"].startswith("/api/views/") for request in requests)
    else:
        assert any(call["kind"] == "StorageClass" for call in calls)


@pytest.mark.parametrize("provider", ["vast", "lvms"])
@pytest.mark.parametrize("fstype", [OPAQUE, ""])
def test_existing_class_short_circuit(tmp_path, provider, fstype):
    name = "vast-block-fstype-test-block" if provider == "vast" else "osac-fstype-test-block"
    result, calls = run_provider(tmp_path, provider, fstype, existing=[name])
    assert result.returncode == 0, result.stdout + result.stderr
    assert created_classes(calls) == []
    assert len(calls) == 1


def test_lvms_creates_only_missing_class(tmp_path):
    result, calls = run_provider(
        tmp_path,
        "lvms",
        OPAQUE,
        protocols=("block", "block"),
        tier_names=("existing", "block"),
        existing=["osac-fstype-test-existing"],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert [entry["metadata"]["name"] for entry in created_classes(calls)] == ["osac-fstype-test-block"]


@pytest.mark.parametrize("fstype", [None, OPAQUE])
def test_vast_creates_only_missing_block_class(tmp_path, fstype):
    existing_name = "vast-block-fstype-test-existing"
    missing_name = "vast-block-fstype-test-new"
    result, calls = run_provider(
        tmp_path,
        "vast",
        fstype,
        protocols=("block", "block"),
        tier_names=("existing", "new"),
        existing=[existing_name],
        expected_classes=[{"name": existing_name, "tier": "existing"}, {"name": missing_name, "tier": "new"}],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    classes = created_classes(calls)
    assert [entry["metadata"]["name"] for entry in classes] == [missing_name]
    assert classes[0]["parameters"][FSTYPE] == ("ext4" if fstype is None else fstype)


@pytest.mark.parametrize("explicit_fstype", [None, ""])
def test_vast_existing_block_creates_nfs_with_empty_fstype(tmp_path, explicit_fstype):
    block_name = "vast-block-fstype-test-block"
    nfs_name = "vast-nfs-fstype-test-nfs"
    result, calls = run_provider(
        tmp_path,
        "vast",
        "",
        protocols=("block", "nfs"),
        existing=[block_name],
        explicit_fstype=explicit_fstype,
        expected_classes=[{"name": block_name, "tier": "block"}, {"name": nfs_name, "tier": "nfs"}],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    classes = created_classes(calls)
    assert [entry["metadata"]["name"] for entry in classes] == [nfs_name]
    parameters = classes[0]["parameters"]
    assert FSTYPE not in parameters
    assert parameters["root_export"] == "/osac-fstype-test-12345678/nfs"
    assert parameters["view_policy"] == "osac-fstype-test-nfs"
    assert classes[0]["provisioner"] == "csi.vastdata.com"
    assert classes[0]["volumeBindingMode"] == "Immediate"


@pytest.mark.parametrize("explicit_fstype", [None, ""])
def test_vast_missing_block_rejects_empty_fstype_before_writes(tmp_path, explicit_fstype):
    result, calls = run_provider(
        tmp_path,
        "vast",
        "",
        protocols=("nfs", "block"),
        existing=["vast-nfs-fstype-test-nfs"],
        explicit_fstype=explicit_fstype,
    )
    assert result.returncode != 0
    assert "must be defined and nonempty for block StorageClasses" in result.stdout
    assert created_classes(calls) == []
    assert len(calls) == 1


def test_vast_all_existing_returns_complete_class_names(tmp_path):
    names = ["vast-block-fstype-test-block", "vast-nfs-fstype-test-nfs"]
    result, calls = run_provider(
        tmp_path,
        "vast",
        "",
        protocols=("block", "nfs"),
        existing=names,
        expected_classes=[{"name": name, "tier": tier} for name, tier in zip(names, ("block", "nfs"), strict=True)],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert created_classes(calls) == []
    assert len(calls) == 1


@pytest.mark.parametrize("provider", ["vast", "lvms"])
@pytest.mark.parametrize("original_fstype", ["ext4", None], ids=["ext4", "legacy-missing"])
@pytest.mark.parametrize("source", ["environment", "explicit"])
def test_changed_override_preserves_existing_classes(tmp_path, provider, original_fstype, source):
    name = "vast-block-fstype-test-block" if provider == "vast" else "osac-fstype-test-block"
    existing = [existing_block_class(provider, name, original_fstype)]
    result, calls = run_provider(
        tmp_path,
        provider,
        "xfs" if source == "environment" else "environment-value",
        explicit_fstype="xfs" if source == "explicit" else None,
        existing=existing,
        expected_classes=[{"name": name, "tier": "block"}],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert_existing_classes_untouched(calls, existing)
    assert len(calls) == 1
    assert calls[0]["module"] == "k8s_info"


@pytest.mark.parametrize("original_fstype", ["ext4", None], ids=["ext4", "legacy-missing"])
@pytest.mark.parametrize("missing_protocol", ["block", "nfs"])
def test_vast_changed_override_creates_only_missing_class(tmp_path, original_fstype, missing_protocol):
    existing_name = "vast-block-fstype-test-existing"
    missing_name = f"vast-{missing_protocol}-fstype-test-new"
    existing = [existing_block_class("vast", existing_name, original_fstype)]
    result, calls = run_provider(
        tmp_path,
        "vast",
        "xfs",
        protocols=("block", missing_protocol),
        tier_names=("existing", "new"),
        existing=existing,
        expected_classes=[{"name": existing_name, "tier": "existing"}, {"name": missing_name, "tier": "new"}],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert_existing_classes_untouched(calls, existing)
    classes = created_classes(calls)
    assert [entry["metadata"]["name"] for entry in classes] == [missing_name]
    if missing_protocol == "block":
        assert type(classes[0]["parameters"][FSTYPE]) is str
        assert classes[0]["parameters"][FSTYPE] == "xfs"
    else:
        assert FSTYPE not in classes[0]["parameters"]
