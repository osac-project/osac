"""Unit and contract tests for the ontap_storage provider role."""

from pathlib import Path

import yaml  # type: ignore[import-untyped]

REPO_ROOT = Path(__file__).resolve().parents[2]
ONTAP_ROLE_DIR = (
    REPO_ROOT
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "ontap_storage"
)


def _load_yaml(path: Path):
    """Parse and return the YAML document at the given path."""
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def test_ontap_meta_declares_block_vmaas_only():
    """Verify meta/osac.yaml declares storage_provider, ontap strategy, and block/vmaas capabilities."""
    meta = _load_yaml(ONTAP_ROLE_DIR / "meta" / "osac.yaml")
    assert meta["template_type"] == "storage_provider"
    assert meta["implementation_strategy"] == "ontap"
    assert meta["capabilities"]["supported_protocols"] == ["block"]
    assert meta["capabilities"]["provisioning_targets"] == ["vmaas"]


def test_ontap_virtual_pools_use_labels_and_selector_not_storage_pools():
    """Regression guard: Trident's OntapStorageDriverPool struct has no 'name'
    field (Trident auto-names virtual pools <backendName>_pool_<index>).
    Each virtual pool in TBC spec.storage must be matched via labels
    (osac_backend + tier) and selected in StorageClasses via parameters.selector,
    never parameters.storagePools.
    """
    ensure_backend_path = ONTAP_ROLE_DIR / "tasks" / "_ensure_storage_class_backend.yaml"
    raw_text = ensure_backend_path.read_text(encoding="utf-8")
    tasks = _load_yaml(ensure_backend_path)

    provision_block = next(
        t for t in tasks if "Provision CSI Secret, TridentBackendConfig, and StorageClasses" in t.get("name", "")
    )
    block_tasks = provision_block["block"]

    # 1. Virtual pool builder must set labels.osac_backend and labels.tier,
    #    nest encryption and qosPolicy under defaults, and NOT set a pool 'name' key.
    pool_task = next(
        t for t in block_tasks if "Build the TBC virtual pools" in t.get("name", "")
    )
    vpool_expr = pool_task["ansible.builtin.set_fact"]["_virtual_pools"]
    assert "'osac_backend': _tbc_name" in vpool_expr
    assert "'tier': tier.name" in vpool_expr
    assert "'defaults': _defaults" in vpool_expr
    assert "'name': tier.name" not in vpool_expr
    assert "tier.qos_policy" not in vpool_expr

    # 2. StorageClass creation task must use parameters.selector and NOT storagePools.
    sc_task = next(
        t for t in block_tasks if t.get("name") == "Create the per-tier StorageClasses"
    )
    sc_params = sc_task["kubernetes.core.k8s"]["definition"]["parameters"]
    assert sc_params["backendType"] == "ontap-san"
    assert sc_params["selector"] == "osac_backend={{ _tbc_name }};tier={{ item.name }}"
    assert "storagePools" not in sc_params
    assert "storagePools:" not in raw_text

    # 3. CA bundle reading must use lookup('file', ...) + b64encode gated on
    #    ontap_storage_validate_certs rather than slurp.
    assert "ontap_storage_validate_certs" in raw_text
    assert "lookup('file', ontap_storage_ca_bundle_path" in raw_text
    assert "b64encode" in raw_text
    assert "slurp" not in raw_text


def test_ontap_consumes_svm_preparation_without_hub_secrets_or_ontap_http():
    """Verify ontap_storage requires only SVM-scoped credentials in
    tenant_preparation.provider_config, makes no direct HTTP calls to ONTAP from
    the hub cluster, and performs no hub-cluster Secret reads or writes.
    """
    setup_path = ONTAP_ROLE_DIR / "tasks" / "setup.yaml"
    setup_text = setup_path.read_text(encoding="utf-8")
    assert "_provider_backend_connections[item].username" not in setup_text
    assert "_provider_backend_connections[item].password" not in setup_text

    setup_backend_path = ONTAP_ROLE_DIR / "tasks" / "_setup_backend.yaml"
    raw_text = setup_backend_path.read_text(encoding="utf-8")
    assert "tenant_preparation" in raw_text
    assert "_prepared_secret" not in raw_text
    assert "storage-assignment-state" not in raw_text

    defaults_text = (ONTAP_ROLE_DIR / "defaults" / "main.yaml").read_text(encoding="utf-8")
    assert "ontap_storage_config_namespace" not in defaults_text
    assert "ontap_storage_validate_certs:" in defaults_text

    for yaml_file in ONTAP_ROLE_DIR.rglob("*.yaml"):
        content = yaml_file.read_text(encoding="utf-8")
        assert "ontap-tenant-" not in content
        assert "ontap_storage_config_namespace" not in content
        assert "kubeconfig: ~" not in content
        assert "ansible.builtin.uri" not in content
        assert "slurp" not in content
        assert "PR#" not in content


def test_ontap_ensure_short_circuit_checks_tbc_and_csi_secret():
    """Verify ensure_storage_class.yaml checks StorageClasses, TridentBackendConfigs,
    and CSI Secrets before setting _ontap_all_sc_exist, and no task file emits set_stats.
    """
    ensure_path = ONTAP_ROLE_DIR / "tasks" / "ensure_storage_class.yaml"
    raw_text = ensure_path.read_text(encoding="utf-8")
    assert "_ontap_expected_sc_names" in raw_text
    assert "_ontap_expected_tbc_names" in raw_text
    assert "_ontap_expected_csi_secret_names" in raw_text
    assert "_ontap_tbc_checks" in raw_text
    assert "_ontap_csi_checks" in raw_text

    for task_file in (ONTAP_ROLE_DIR / "tasks").glob("*.yaml"):
        parsed_tasks = _load_yaml(task_file)
        for task in parsed_tasks:
            assert "ansible.builtin.set_stats" not in task
            assert "set_stats" not in task
            for section in ("block", "rescue", "always"):
                for sub_task in task.get(section, []):
                    assert "ansible.builtin.set_stats" not in sub_task
                    assert "set_stats" not in sub_task
