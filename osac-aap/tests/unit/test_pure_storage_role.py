"""Unit and contract tests for the pure_storage provider role."""

import base64
import json
import secrets
from pathlib import Path

import yaml  # type: ignore[import-untyped]
from ansible.parsing.dataloader import DataLoader  # type: ignore[import-not-found]
from ansible.template import Templar  # type: ignore[import-not-found]

REPO_ROOT = Path(__file__).resolve().parents[2]


class _Redacted(str):
    """A string that compares, hashes, and encodes like its value but never
    prints it, so password-bearing test inputs stay out of assertion output."""

    def __repr__(self) -> str:
        return "'<redacted>'"


def _redacted_b64(value: str) -> str:
    """Return the base64 encoding of value, redacted in test output."""
    return _Redacted(base64.b64encode(str.encode(value)).decode("ascii"))


# Non-secret test credentials, generated per run so no credential-shaped
# literal lives in the test source.
FIXTURE_TOKEN = _Redacted(secrets.token_hex(16))
PURE_ROLE_DIR = (
    REPO_ROOT
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "pure_storage"
)


def _load_yaml(path: Path):
    """Parse and return the YAML document at the given path."""
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _find_task(tasks: list[dict], name: str) -> dict:
    """Return the first task whose name matches exactly, descending into
    block/rescue/always so callers need not know a task's nesting."""
    for task in tasks:
        if not isinstance(task, dict):
            continue
        if task.get("name") == name:
            return task
        for section in ("block", "rescue", "always"):
            nested = task.get(section)
            if not isinstance(nested, list):
                continue
            try:
                return _find_task(nested, name)
            except AssertionError:
                pass
    raise AssertionError(f"Task {name!r} not found")


def _render(expression, variables: dict):
    """Render a Jinja expression or data structure using Ansible's Templar."""
    templar = Templar(loader=DataLoader(), variables=variables)
    return templar.template(expression)


def _str_value_preserved(value: str) -> bool:
    """Report whether str() keeps the value intact, without returning it."""
    return str(value).strip() == value and len(str(value)) == len(value)


def _b64_round_trips(value: str) -> bool:
    """Report whether the redacted base64 decodes back to value, without returning it."""
    return base64.b64decode(_redacted_b64(value)).decode() == value


def test_fixture_token_is_redacted_in_output():
    """The fixture token behaves like its value but never prints it.

    Comparisons that would produce an unredacted string run inside helpers that
    return only a bool, so pytest's assertion output never shows the value.
    """
    assert repr(FIXTURE_TOKEN) == "'<redacted>'"
    assert repr({"api_token": FIXTURE_TOKEN}) == "{'api_token': '<redacted>'}"
    assert len(FIXTURE_TOKEN) == 32
    assert _str_value_preserved(FIXTURE_TOKEN)
    assert _render("{{ t | string | trim | length }}", {"t": FIXTURE_TOKEN}) == 32
    assert repr(_redacted_b64(FIXTURE_TOKEN)) == "'<redacted>'"
    assert _b64_round_trips(FIXTURE_TOKEN)


def test_pure_meta_declares_nfs_provider():
    """Verify meta/osac.yaml declares storage_provider, pure strategy, and NFS capabilities."""
    meta = _load_yaml(PURE_ROLE_DIR / "meta" / "osac.yaml")
    assert meta["template_type"] == "storage_provider"
    assert meta["implementation_strategy"] == "pure"
    assert meta["capabilities"]["supported_protocols"] == ["nfs"]
    assert "vmaas" in meta["capabilities"]["provisioning_targets"]


def test_pure_defaults_use_portworx_provisioner():
    """Verify defaults/main.yaml uses pxd.portworx.com provisioner."""
    defaults = _load_yaml(PURE_ROLE_DIR / "defaults" / "main.yaml")
    assert defaults["pure_storage_csi_provisioner"] == "pxd.portworx.com"
    assert defaults["pure_storage_nfs_server_suffix"] == "nfs-server"
    assert defaults["pure_storage_export_policy_suffix"] == "export-policy"
    assert defaults["pure_storage_nfs_version"] == "nfsvers=4.1"


def test_pure_credential_extraction_from_hub_secret():
    """Render credential extraction from hub Secret using Templar."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the credential extraction task
    extract_task = _find_task(
        tasks, "Extract Realm credentials and metadata from hub Secret"
    )

    # Create mock hub Secret with TEST-NET addresses
    hub_secret_data = {
        "api_token": _redacted_b64("test-realm-token"),
        "mgmt_endpoint": _redacted_b64("192.0.2.10"),
        "nfs_endpoint": _redacted_b64("192.0.2.20"),
        "nfs_server": _redacted_b64("test-realm::nfs-server"),
        "nfs_policy": _redacted_b64("test-realm::export-policy"),
    }

    variables = {
        "_pure_hub_secret": {
            "resources": [{"data": hub_secret_data}]
        }
    }

    # Render each extraction
    facts = extract_task["ansible.builtin.set_fact"]
    api_token = _render(facts["_pure_api_token"], variables)
    mgmt_endpoint = _render(facts["_pure_mgmt_endpoint"], variables)
    nfs_endpoint = _render(facts["_pure_nfs_endpoint"], variables)
    nfs_server = _render(facts["_pure_nfs_server"], variables)
    nfs_policy = _render(facts["_pure_nfs_policy"], variables)

    # Note: render() returns _Redacted objects, compare without decoding
    assert str(api_token) == "test-realm-token"
    assert mgmt_endpoint == "192.0.2.10"
    assert nfs_endpoint == "192.0.2.20"
    assert nfs_server == "test-realm::nfs-server"
    assert nfs_policy == "test-realm::export-policy"


def test_pure_realm_scoped_resource_names():
    """Verify Realm-scoped resource name formatting (<realm>::<resource>)."""
    # Test that nfs_server and nfs_policy use the realm::resource pattern
    realm_name = "test-realm-1"
    nfs_server_suffix = "nfs-server"
    export_policy_suffix = "export-policy"

    # Expected format from setup.yaml
    expected_nfs_server = f"{realm_name}::{nfs_server_suffix}"
    expected_nfs_policy = f"{realm_name}::{export_policy_suffix}"

    assert expected_nfs_server == "test-realm-1::nfs-server"
    assert expected_nfs_policy == "test-realm-1::export-policy"

    # Verify the pattern works with the template
    variables = {
        "realm_name": realm_name,
        "pure_storage_nfs_server_suffix": nfs_server_suffix,
        "pure_storage_export_policy_suffix": export_policy_suffix,
    }

    nfs_server = _render(
        "{{ realm_name }}::{{ pure_storage_nfs_server_suffix }}",
        variables
    )
    nfs_policy = _render(
        "{{ realm_name }}::{{ pure_storage_export_policy_suffix }}",
        variables
    )

    assert nfs_server == expected_nfs_server
    assert nfs_policy == expected_nfs_policy


def test_pure_storageclass_parameters():
    """Render StorageClass parameters with Realm-scoped names and correct provisioner."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the StorageClass creation task
    sc_task = _find_task(tasks, "Create StorageClass per tier")

    # Test variables
    variables = {
        "pure_storage_csi_provisioner": "pxd.portworx.com",
        "pure_storage_nfs_version": "nfsvers=4.1",
        "_pure_nfs_server": "test-realm::nfs-server",
        "_pure_nfs_policy": "test-realm::export-policy",
        "_pure_csi_secret_name": "pure-csi-secret-be-test",
        "tenant_namespace": "test-tenant-ns",
        "tenant_name": "test-tenant",
        "_pure_backend_id": "be-test",
        "item": {"name": "default", "protocol": "nfs"},
    }

    # Render provisioner
    provisioner = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["provisioner"],
        variables
    )
    assert provisioner == "pxd.portworx.com"

    # Render parameters
    params = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        variables
    )

    assert params["backend"] == "pure_file"
    assert params["pure_nfs_server"] == "test-realm::nfs-server"
    assert params["pure_nfs_policy"] == "test-realm::export-policy"
    assert params["pure_fb_hard_limit_enabled"] == "true"
    assert params["csi.storage.k8s.io/provisioner-secret-name"] == "pure-csi-secret-be-test"
    assert params["csi.storage.k8s.io/provisioner-secret-namespace"] == "test-tenant-ns"

    # Render mount options
    mount_opts = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["mountOptions"],
        variables
    )
    assert mount_opts == ["nfsvers=4.1", "tcp"]


def test_pure_csi_secret_format():
    """Verify CSI Secret format matches Pure driver requirements."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the CSI Secret creation task
    secret_task = _find_task(tasks, "Create pure.json Secret for CSI driver")

    # Test variables with TEST-NET addresses
    variables = {
        "_pure_mgmt_endpoint": "192.0.2.10",
        "_pure_nfs_endpoint": "192.0.2.20",
        "_pure_api_token": FIXTURE_TOKEN,
        "_pure_csi_secret_name": "pure-csi-secret-be-test",
        "tenant_namespace": "test-tenant-ns",
        "tenant_name": "test-tenant",
        "_pure_backend_id": "be-test",
    }

    # Render the stringData pure.json field
    rendered = _render(
        secret_task["kubernetes.core.k8s"]["definition"]["stringData"]["pure.json"],
        variables
    )

    # Templar native types turn JSON-shaped output into a dict on some
    # ansible-core versions and leave it a string on others.
    pure_json = rendered if isinstance(rendered, dict) else json.loads(rendered)

    # Verify structure
    assert "FlashBlades" in pure_json
    assert len(pure_json["FlashBlades"]) == 1

    fb = pure_json["FlashBlades"][0]
    assert fb["MgmtEndPoint"] == "192.0.2.10"
    assert fb["NFSEndPoint"] == "192.0.2.20"
    # Token should be redacted in output but present in structure
    assert "APIToken" in fb


def test_pure_storageclass_naming_convention():
    """Verify StorageClass naming follows pure-<protocol>-<tenant>-<tier> pattern."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the StorageClass creation task
    sc_task = _find_task(tasks, "Create StorageClass per tier")

    # Test with different tiers
    test_cases = [
        {"tenant": "tenant-a", "tier": "default", "protocol": "nfs", "expected": "pure-nfs-tenant-a-default"},
        {"tenant": "tenant-b", "tier": "fast", "protocol": "nfs", "expected": "pure-nfs-tenant-b-fast"},
    ]

    for case in test_cases:
        variables = {
            "tenant_name": case["tenant"],
            "item": {"name": case["tier"], "protocol": case["protocol"]},
        }

        sc_name = _render(
            sc_task["kubernetes.core.k8s"]["definition"]["metadata"]["name"],
            variables
        )

        assert sc_name == case["expected"]


def test_pure_expected_storageclass_names():
    """Verify expected StorageClass name list generation."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the task that builds expected SC names
    sc_names_task = _find_task(tasks, "Build expected StorageClass entries per tier")

    # Test variables
    variables = {
        "tenant_name": "test-tenant",
        "_provider_tiers": [
            {"name": "default", "protocol": "nfs"},
            {"name": "fast", "protocol": "nfs"},
        ],
    }

    # Render the expected SC names list
    expected_names = _render(
        sc_names_task["ansible.builtin.set_fact"]["_pure_expected_sc_names"],
        variables
    )

    assert expected_names == [
        "pure-nfs-test-tenant-default",
        "pure-nfs-test-tenant-fast",
    ]


def test_pure_csi_secret_name_format():
    """Verify CSI Secret naming convention."""
    ensure_sc_path = PURE_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    tasks = _load_yaml(ensure_sc_path)

    # Find the task that sets CSI Secret name
    secret_name_task = _find_task(tasks, "Set the backend-scoped CSI Secret name")

    # Test variables
    variables = {
        "_pure_backend_id": "be-pure-test",
    }

    # Render the secret name
    secret_name = _render(
        secret_name_task["ansible.builtin.set_fact"]["_pure_csi_secret_name"],
        variables
    )

    assert secret_name == "pure-csi-secret-be-pure-test"


def test_pure_export_policy_suffix():
    """Verify export policy suffix is 'export-policy' not 'nfs-export'."""
    defaults = _load_yaml(PURE_ROLE_DIR / "defaults" / "main.yaml")

    # This was a bug fix - ensure it stays correct
    assert defaults["pure_storage_export_policy_suffix"] == "export-policy"
    assert defaults["pure_storage_export_policy_suffix"] != "nfs-export"
