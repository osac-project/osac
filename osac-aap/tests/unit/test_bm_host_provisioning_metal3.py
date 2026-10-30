import json
import os
import subprocess
import sys
from pathlib import Path

OSAC_AAP_ROOT = Path(__file__).resolve().parents[2]
TARGET_ROOT = (
    OSAC_AAP_ROOT
    / "tests/integration/targets/bm_host_provisioning_patch"
)
TASK_ROOT = TARGET_ROOT / "tasks"
FAKE_COLLECTIONS = TARGET_ROOT / "test_collections"
REAL_COLLECTIONS = OSAC_AAP_ROOT / "collections"
VENDOR_COLLECTIONS = OSAC_AAP_ROOT / "vendor"

BMH_KEY = "metal3.io/v1alpha1|BareMetalHost|metal3-test|test-bmh"
SOURCE_SECRET_KEY = "v1|Secret|tenant-test|source-user-data"
IMAGE_URL = "oci://registry.example.test/osac/test-image:latest"


def bmh_resource(*, state="available", image_url=None):
    resource = {
        "apiVersion": "metal3.io/v1alpha1",
        "kind": "BareMetalHost",
        "metadata": {"name": "test-bmh", "namespace": "metal3-test"},
        "spec": {"online": False},
        "status": {"provisioning": {"state": state}},
    }
    if image_url is not None:
        image = {"url": image_url}
        resource["spec"]["image"] = image
        resource["status"]["provisioning"]["image"] = image
    return resource


def source_secret():
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {"name": "source-user-data", "namespace": "tenant-test"},
        "data": {"userdata": "I2Nsb3VkLWNvbmZpZwo="},
    }


def bare_metal_instance(external_host_id="metal3-test/test-bmh"):
    return {
        "metadata": {"name": "test-instance", "namespace": "tenant-test"},
        "spec": {"externalHostID": external_host_id},
    }


def template_params(**overrides):
    params = {"imageURL": IMAGE_URL}
    params.update(overrides)
    return params


def run_role(tmp_path, task_file, resources, *, instance=None, params=None):
    state_path = tmp_path / "state.json"
    state_path.write_text(
        json.dumps({"resources": resources, "operations": []}), encoding="utf-8"
    )
    variables = {
        "ansible_python_interpreter": sys.executable,
        "bm_test_state_file": str(state_path),
        "bm_test_bare_metal_instance": instance or bare_metal_instance(),
    }
    if params is not None:
        variables["bm_test_template_params"] = params
    vars_path = tmp_path / "vars.json"
    vars_path.write_text(json.dumps(variables), encoding="utf-8")

    env = os.environ.copy()
    env["ANSIBLE_CONFIG"] = str(OSAC_AAP_ROOT / "tests/integration/ansible.cfg")
    env["ANSIBLE_COLLECTIONS_PATH"] = os.pathsep.join(
        [str(FAKE_COLLECTIONS), str(REAL_COLLECTIONS), str(VENDOR_COLLECTIONS)]
    )
    result = subprocess.run(
        ["ansible-playbook", str(TASK_ROOT / task_file), "-e", f"@{vars_path}"],
        cwd=OSAC_AAP_ROOT / "tests/integration",
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=False,
    )
    return result, json.loads(state_path.read_text(encoding="utf-8"))


def k8s_operations(state):
    return [operation for operation in state["operations"] if operation["module"] == "k8s"]


def secret_operations(state, name=None, operation_state=None):
    operations = [op for op in k8s_operations(state) if op["kind"] == "Secret"]
    if name is not None:
        operations = [op for op in operations if op["name"] == name]
    if operation_state is not None:
        operations = [op for op in operations if op["state"] == operation_state]
    return operations


def test_create_fails_when_baremetalhost_is_missing(tmp_path):
    result, _ = run_role(tmp_path, "unit_create.yml", {}, params=template_params())

    assert result.returncode != 0
    assert "BareMetalHost 'metal3-test/test-bmh' not found" in result.stdout


def test_create_skips_baremetalhost_already_provisioned_with_image(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource(state="provisioned", image_url=IMAGE_URL)},
        params=template_params(),
    )

    assert result.returncode == 0, result.stdout
    assert k8s_operations(state) == []


def test_create_fails_when_userdata_source_secret_is_missing(tmp_path):
    result, _ = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource()},
        params=template_params(userDataSecret="source-user-data"),
    )

    assert result.returncode != 0
    assert "Secret 'source-user-data' not found in namespace 'tenant-test'" in result.stdout


def test_create_copies_userdata_to_bmh_namespace(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource(), SOURCE_SECRET_KEY: source_secret()},
        params=template_params(userDataSecret="source-user-data"),
    )

    assert result.returncode == 0, result.stdout
    operations = secret_operations(state, "test-bmh-user-data", "present")
    assert len(operations) == 1
    assert operations[0]["definition"]["data"]["userData"] == "I2Nsb3VkLWNvbmZpZwo="


def test_create_writes_metadata_secret_when_ssh_key_is_present(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource()},
        params=template_params(sshPublicKey="ssh-ed25519 TESTKEY osac-test"),
    )

    assert result.returncode == 0, result.stdout
    operations = secret_operations(state, "test-bmh-meta-data", "present")
    assert len(operations) == 1
    assert "ssh-ed25519 TESTKEY osac-test" in operations[0]["definition"]["stringData"]["metaData"]


def test_create_omits_metadata_secret_without_ssh_key(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource()},
        params=template_params(),
    )

    assert result.returncode == 0, result.stdout
    assert secret_operations(state, "test-bmh-meta-data") == []


def test_create_writes_networkdata_secret_when_data_is_present(tmp_path):
    network_data = "links:\n  - id: test-interface\n    type: phy\n"
    result, state = run_role(
        tmp_path,
        "unit_create.yml",
        {BMH_KEY: bmh_resource()},
        params=template_params(networkData=network_data),
    )

    assert result.returncode == 0, result.stdout
    operations = secret_operations(state, "test-bmh-network-data", "present")
    assert len(operations) == 1
    assert operations[0]["definition"]["stringData"]["networkData"] == network_data


def test_delete_skips_without_external_host_id(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_delete.yml",
        {},
        instance=bare_metal_instance(external_host_id=""),
    )

    assert result.returncode == 0, result.stdout
    assert k8s_operations(state) == []


def test_delete_is_idempotent_when_baremetalhost_is_missing(tmp_path):
    result, state = run_role(tmp_path, "unit_delete.yml", {})

    assert result.returncode == 0, result.stdout
    assert k8s_operations(state) == []


def test_delete_skips_deprovisioning_when_baremetalhost_has_no_image(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_delete.yml",
        {BMH_KEY: bmh_resource()},
    )

    assert result.returncode == 0, result.stdout
    assert all(operation["kind"] == "Secret" for operation in k8s_operations(state))


def test_delete_tolerates_already_absent_generated_secrets(tmp_path):
    result, state = run_role(
        tmp_path,
        "unit_delete.yml",
        {BMH_KEY: bmh_resource(state="provisioned", image_url=IMAGE_URL)},
    )

    assert result.returncode == 0, result.stdout
    deleted_names = {
        operation["name"]
        for operation in secret_operations(state, operation_state="absent")
    }
    assert deleted_names == {
        "test-bmh-network-data",
        "test-bmh-user-data",
        "test-bmh-meta-data",
    }
