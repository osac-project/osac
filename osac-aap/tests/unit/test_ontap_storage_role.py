"""Unit and contract tests for the ontap_storage provider role."""

import base64
import json
import os
import subprocess
from pathlib import Path

import yaml  # type: ignore[import-untyped]
from ansible.parsing.dataloader import DataLoader  # type: ignore[import-not-found]
from ansible.template import Templar  # type: ignore[import-not-found]

REPO_ROOT = Path(__file__).resolve().parents[2]
PREPARE_ONTAP_SCRIPT = (
    REPO_ROOT.parent / "osac-installer" / "scripts" / "dev-full" / "prepare-ontap-svm.sh"
)
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


def _find_task(tasks: list[dict], name: str) -> dict:
    """Return the first task in tasks whose name matches exactly."""
    for task in tasks:
        if isinstance(task, dict) and task.get("name") == name:
            return task
    raise AssertionError(f"Task {name!r} not found")


def _render(expression, variables: dict):
    """Render a Jinja expression or data structure using Ansible's Templar."""
    templar = Templar(loader=DataLoader(), variables=variables)
    return templar.template(expression)


def _render_when(when_expression: str, variables: dict) -> bool:
    """Evaluate an Ansible when: expression to a boolean using Templar."""
    assert "{{" not in when_expression and "{%" not in when_expression
    result = _render("{{ (" + when_expression + ") | bool }}", variables)
    if isinstance(result, bool):
        return result
    return str(result).strip().lower() == "true"


def test_ontap_meta_declares_block_vmaas_only():
    """Verify meta/osac.yaml declares storage_provider, ontap strategy, and block/vmaas capabilities."""
    meta = _load_yaml(ONTAP_ROLE_DIR / "meta" / "osac.yaml")
    assert meta["template_type"] == "storage_provider"
    assert meta["implementation_strategy"] == "ontap"
    assert meta["capabilities"]["supported_protocols"] == ["block"]
    assert meta["capabilities"]["provisioning_targets"] == ["vmaas"]


def test_ontap_virtual_pools_use_labels_and_selector_not_storage_pools(
    tmp_path: Path, monkeypatch
):
    """Render virtual pool definitions, Dedicated-SVM StorageClass parameters,
    Shared-TBC StorageClass parameters (including sorted multi-key pool_selector
    and dot-escaped storagePools), and CA bundle resolution via Ansible's Templar.
    """
    ensure_backend_path = ONTAP_ROLE_DIR / "tasks" / "_ensure_storage_class_backend.yaml"
    tasks = _load_yaml(ensure_backend_path)
    provision_block = _find_task(
        tasks,
        "Provision CSI Secret, TridentBackendConfig, and StorageClasses for dispatched backend",
    )
    block_tasks = provision_block["block"]

    # 1. Render _virtual_pools for Dedicated-SVM tiers (with and without QoS / encryption).
    pool_task = _find_task(block_tasks, "Build the TBC virtual pools for dispatched backend")
    vpool_expr = pool_task["ansible.builtin.set_fact"]["_virtual_pools"]
    rendered_pools = _render(
        vpool_expr,
        {
            "_tbc_name": "ontap-0000000000000001",
            "_assignment_key": "0000000000000001",
            "_backend_tiers": [
                {
                    "name": "gold",
                    "encryption_enabled": True,
                    "qos_limits": {"provider_config": {"max_iops": 5000}},
                },
                {
                    "name": "silver",
                    "encryption_enabled": False,
                },
            ],
        },
    )
    assert len(rendered_pools) == 2
    assert "name" not in rendered_pools[0]
    assert rendered_pools[0]["labels"] == {
        "osac_backend": "ontap-0000000000000001",
        "tier": "gold",
    }
    assert rendered_pools[0]["defaults"]["encryption"] == "true"
    assert rendered_pools[0]["defaults"]["qosPolicy"].startswith("osac-0000000000000001-")
    assert rendered_pools[0]["defaults"]["qosPolicy"].endswith("-qos")
    assert rendered_pools[1] == {
        "labels": {"osac_backend": "ontap-0000000000000001", "tier": "silver"},
        "defaults": {"encryption": "false"},
    }

    # 2. Render Dedicated-SVM StorageClass parameters.
    sc_task = _find_task(block_tasks, "Create the per-tier StorageClasses")
    dedicated_sc_params = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"_tbc_name": "ontap-0000000000000001", "item": {"name": "gold"}},
    )
    assert dedicated_sc_params == {
        "backendType": "ontap-san",
        "selector": "osac_backend=ontap-0000000000000001;tier=gold",
    }
    assert "storagePools" not in dedicated_sc_params

    # 3. Render Shared-TBC StorageClass parameters: multi-key pool_selector sorted by key,
    #    dot-escaped backendName in storagePools when resolved, and omitted when empty.
    desired_params_task = _find_task(
        block_tasks, "Build desired Shared-TBC StorageClass parameters per tier"
    )
    desired_params_expr = desired_params_task["ansible.builtin.set_fact"][
        "_ontap_shared_sc_desired_params"
    ]
    shared_sc_task = _find_task(
        block_tasks, "Create the per-tier StorageClasses for shared Trident backend"
    )
    assert shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"] == (
        "{{ _ontap_shared_sc_desired_params[item.name] }}"
    )

    shared_tier = {
        "name": "gold",
        "provider_config": {
            "profile": " gold-prof ",
            "pool_selector": {"z_tier": "fast", "a_env": "prod"},
        },
    }
    rendered_desired_with_bname = _render(
        desired_params_expr,
        {
            "_assignment_key": "0000000000000001",
            "_backend_tiers": [shared_tier],
            "_ontap_profile_backend_names": {"gold-prof": "trident.shared-san.01"},
        },
    )
    rendered_shared_with_bname = _render(
        shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"item": shared_tier, "_ontap_shared_sc_desired_params": rendered_desired_with_bname},
    )
    assert rendered_shared_with_bname == {
        "backendType": "ontap-san",
        "selector": "a_env=prod;z_tier=fast",
        "storagePools": r"trident\.shared-san\.01:.*",
    }

    rendered_desired_without_bname = _render(
        desired_params_expr,
        {
            "_assignment_key": "0000000000000001",
            "_backend_tiers": [shared_tier],
            "_ontap_profile_backend_names": {"gold-prof": ""},
        },
    )
    rendered_shared_without_bname = _render(
        shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"item": shared_tier, "_ontap_shared_sc_desired_params": rendered_desired_without_bname},
    )
    assert rendered_shared_without_bname == {
        "backendType": "ontap-san",
        "selector": "a_env=prod;z_tier=fast",
    }

    # Verify stale Shared-TBC StorageClass detection agrees with rendered parameters.
    stale_task = _find_task(
        block_tasks, "Identify existing Shared-TBC StorageClasses with changed immutable parameters"
    )
    stale_expr = stale_task["ansible.builtin.set_fact"]["_ontap_stale_shared_sc_names"]
    sc_name = _render(
        shared_sc_task["kubernetes.core.k8s"]["definition"]["metadata"]["name"],
        {"_assignment_key": "0000000000000001", "item": shared_tier},
    )
    assert (
        _render(
            stale_expr,
            {
                "_assignment_key": "0000000000000001",
                "_backend_tiers": [shared_tier],
                "_ontap_shared_sc_desired_params": rendered_desired_with_bname,
                "_ontap_sc_checks": {
                    "resources": [
                        {"metadata": {"name": sc_name}, "parameters": rendered_shared_with_bname}
                    ]
                },
            },
        )
        == []
    )
    assert _render(
        stale_expr,
        {
            "_assignment_key": "0000000000000001",
            "_backend_tiers": [shared_tier],
            "_ontap_shared_sc_desired_params": rendered_desired_with_bname,
            "_ontap_sc_checks": {
                "resources": [
                    {"metadata": {"name": sc_name}, "parameters": rendered_shared_without_bname}
                ]
            },
        },
    ) == [sc_name]

    # 4. Evaluate ontap_storage_validate_certs default and CA bundle fail-fast conditions.
    defaults = _load_yaml(ONTAP_ROLE_DIR / "defaults" / "main.yaml")
    monkeypatch.delenv("ONTAP_VALIDATE_CERTS", raising=False)
    assert _render(defaults["ontap_storage_validate_certs"], {}) is True
    monkeypatch.setenv("ONTAP_VALIDATE_CERTS", "false")
    assert _render(defaults["ontap_storage_validate_certs"], {}) is False
    monkeypatch.setenv("ONTAP_VALIDATE_CERTS", "unexpected")
    assert _render(defaults["ontap_storage_validate_certs"], {}) is True

    ca_task = _find_task(
        block_tasks,
        "Resolve trustedCACertificate from cluster CA bundle when cert validation is enabled",
    )
    ca_expr = ca_task["ansible.builtin.set_fact"]["_trusted_ca_b64"]
    ca_fail_when = _find_task(
        block_tasks, "Fail when certificate validation is enabled without a CA bundle"
    )["when"]

    ca_file = tmp_path / "bundle.pem"
    ca_pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
    ca_file.write_text(ca_pem, encoding="utf-8")

    rendered_ca_b64 = _render(
        ca_expr,
        {"ontap_storage_validate_certs": True, "ontap_storage_ca_bundle_path": str(ca_file)},
    )
    assert rendered_ca_b64 == base64.b64encode(ca_pem.encode("utf-8")).decode("ascii")
    assert (
        _render_when(
            ca_fail_when,
            {
                "_ontap_is_shared_tbc": False,
                "ontap_storage_validate_certs": True,
                "_trusted_ca_b64": rendered_ca_b64,
            },
        )
        is False
    )

    empty_ca = tmp_path / "empty.pem"
    empty_ca.write_text("", encoding="utf-8")
    for missing_or_empty in (tmp_path / "missing.pem", empty_ca):
        missing_ca_b64 = _render(
            ca_expr,
            {
                "ontap_storage_validate_certs": True,
                "ontap_storage_ca_bundle_path": str(missing_or_empty),
            },
        )
        assert missing_ca_b64 == ""
        assert (
            _render_when(
                ca_fail_when,
                {
                    "_ontap_is_shared_tbc": False,
                    "ontap_storage_validate_certs": True,
                    "_trusted_ca_b64": missing_ca_b64,
                },
            )
            is True
        )
    assert (
        _render_when(
            ca_fail_when,
            {
                "_ontap_is_shared_tbc": False,
                "ontap_storage_validate_certs": False,
                "_trusted_ca_b64": "",
            },
        )
        is False
    )


def test_ontap_dual_mode_validation_and_shared_tbc_contracts():
    """Evaluate dual-mode validation (_validate_backend_mode.yaml) and Shared-TBC
    lookup/fail-fast expressions (_ensure_storage_class_backend.yaml and
    _setup_backend.yaml) via Ansible's Templar.
    """
    validate_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "_validate_backend_mode.yaml")

    # Mode discrimination (_ontap_is_shared_tbc) and _backend_tiers filtering
    mode_task = _find_task(validate_tasks, "Select backend tiers and determine ONTAP backend tenancy mode")
    is_shared_expr = mode_task["ansible.builtin.set_fact"]["_ontap_is_shared_tbc"]
    assert (
        _render(
            is_shared_expr,
            {
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"provider_config": {"profiles": []}}},
            },
        )
        is True
    )
    assert (
        _render(
            is_shared_expr,
            {
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"tenant_preparation": {"provider_config": {}}}},
            },
        )
        is False
    )

    # Reject backend defining both provider_config.profiles and tenant_preparation
    both_modes_when = _find_task(
        validate_tasks,
        "Fail if backend connection defines both provider_config.profiles and tenant_preparation",
    )["when"]
    assert (
        _render_when(
            both_modes_when,
            {
                "_ontap_is_shared_tbc": True,
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": []}, "tenant_preparation": {}}
                },
            },
        )
        is True
    )

    # Duplicate profile detection (including whitespace-padded duplicates)
    dup_profile_when = _find_task(
        validate_tasks, "Fail if Shared-TBC profile names are not unique"
    )["when"]
    assert (
        _render_when(
            dup_profile_when,
            {
                "_ontap_is_shared_tbc": True,
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": [{"name": "gold"}, {"name": " gold "}]}}
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            dup_profile_when,
            {
                "_ontap_is_shared_tbc": True,
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": [{"name": "gold"}, {"name": "silver"}]}}
                },
            },
        )
        is False
    )

    # Shared-TBC pool_selector key/value validation and max_iops rejection
    invalid_selector_when = _find_task(
        validate_tasks, "Fail if any Shared-TBC tier pool_selector key or value is invalid"
    )["when"]
    assert (
        _render_when(
            invalid_selector_when,
            {
                "_ontap_is_shared_tbc": True,
                "_backend_tiers": [{"provider_config": {"pool_selector": {"tier": "gold;evil=1"}}}],
            },
        )
        is True
    )
    assert (
        _render_when(
            invalid_selector_when,
            {
                "_ontap_is_shared_tbc": True,
                "_backend_tiers": [
                    {"provider_config": {"pool_selector": {"storage.tier/name": "gold_01"}}}
                ],
            },
        )
        is False
    )

    shared_qos_when = _find_task(
        validate_tasks, "Fail if a Shared-TBC tier specifies qos_limits.provider_config.max_iops"
    )["when"]
    assert (
        _render_when(
            shared_qos_when,
            {
                "_ontap_is_shared_tbc": True,
                "_backend_tiers": [{"qos_limits": {"provider_config": {"max_iops": 1000}}}],
            },
        )
        is True
    )
    assert (
        _render_when(
            shared_qos_when,
            {
                "_ontap_is_shared_tbc": True,
                "_backend_tiers": [{"qos_limits": {"provider_config": {"max_iops": 0}}}],
            },
        )
        is False
    )

    # Dedicated-SVM tier rejecting profile and pool_selector
    ded_reject_shared_when = _find_task(
        validate_tasks, "Fail if a Dedicated-SVM tier specifies Shared-TBC provider_config fields"
    )["when"]
    assert (
        _render_when(
            ded_reject_shared_when,
            {
                "_ontap_is_shared_tbc": False,
                "_backend_tiers": [{"name": "gold", "provider_config": {"profile": "p1"}}],
            },
        )
        is True
    )
    assert (
        _render_when(
            ded_reject_shared_when,
            {
                "_ontap_is_shared_tbc": False,
                "_backend_tiers": [
                    {"name": "gold", "provider_config": {"pool_selector": {"tier": "gold"}}}
                ],
            },
        )
        is True
    )
    assert (
        _render_when(
            ded_reject_shared_when,
            {"_ontap_is_shared_tbc": False, "_backend_tiers": [{"name": "gold"}]},
        )
        is False
    )

    # Shared-TBC backendName resolution and fail-fast conditions in _ensure_storage_class_backend.yaml
    ensure_backend_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "_ensure_storage_class_backend.yaml")
    provision_block = _find_task(
        ensure_backend_tasks,
        "Provision CSI Secret, TridentBackendConfig, and StorageClasses for dispatched backend",
    )
    assert "when" not in provision_block
    block_tasks = provision_block["block"]
    always_tasks = provision_block.get("always", [])

    lookup_task = _find_task(
        block_tasks, "Inspect referenced customer TridentBackendConfigs (best-effort)"
    )
    assert lookup_task.get("failed_when") is False
    assert lookup_task.get("no_log") is True

    resolve_bnames_expr = _find_task(
        block_tasks, "Resolve bound Trident backend names per Shared-TBC profile"
    )["ansible.builtin.set_fact"]["_ontap_profile_backend_names"]
    resolved_bnames = _render(
        resolve_bnames_expr,
        {
            "_ontap_referenced_profiles": [
                "prof-status",
                "prof-spec-fallback",
                "prof-unbound",
                "prof-rbac-denied",
            ],
            "_shared_tbc_lookups": {
                "results": [
                    {
                        "item": "prof-status",
                        "resources": [
                            {
                                "status": {
                                    "phase": "Bound",
                                    "backendInfo": {"backendName": "ontap.status.bname"},
                                },
                                "spec": {"backendName": "ontap.spec.ignored"},
                            }
                        ],
                    },
                    {
                        "item": "prof-spec-fallback",
                        "resources": [
                            {
                                "status": {"phase": "Bound", "backendInfo": {}},
                                "spec": {"backendName": "ontap.spec.fallback"},
                            }
                        ],
                    },
                    {
                        "item": "prof-unbound",
                        "resources": [
                            {
                                "status": {"phase": "Deleting"},
                                "spec": {"backendName": "ontap.ignored"},
                            }
                        ],
                    },
                    {"item": "prof-rbac-denied", "resources": []},
                ]
            },
        },
    )
    assert resolved_bnames == {
        "prof-status": "ontap.status.bname",
        "prof-spec-fallback": "ontap.spec.fallback",
        "prof-unbound": "",
        "prof-rbac-denied": "",
    }

    fail_state_when = _find_task(
        block_tasks, "Fail when a visible customer TridentBackendConfig is in Failed state"
    )["when"]
    assert (
        _render_when(
            fail_state_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {"resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Failed"}}]}
                    ]
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_state_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {"results": [{"resources": [{"status": {"phase": "Bound"}}]}]},
            },
        )
        is False
    )

    fail_not_bound_when = _find_task(
        block_tasks, "Fail when a visible customer TridentBackendConfig is not Bound"
    )["when"]
    assert (
        _render_when(
            fail_not_bound_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {
                            "item": "prof-unbound",
                            "resources": [
                                {
                                    "status": {"phase": "Deleting"},
                                    "spec": {"backendName": "ontap.ignored"},
                                }
                            ],
                        }
                    ]
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_not_bound_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {"item": "prof-status", "resources": [{"status": {"phase": "Bound"}}]},
                        {"item": "prof-rbac-denied", "resources": []},
                    ]
                },
            },
        )
        is False
    )

    fail_spec_when = _find_task(
        block_tasks,
        "Fail when a visible customer TridentBackendConfig uses an unsupported driver or sanType",
    )["when"]
    assert (
        _render_when(
            fail_spec_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {"resources": [{"spec": {"storageDriverName": "ontap-nas", "sanType": "iscsi"}}]}
                    ]
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_spec_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {"resources": [{"spec": {"storageDriverName": "ontap-san", "sanType": "bad"}}]}
                    ]
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_spec_when,
            {
                "_ontap_is_shared_tbc": True,
                "_shared_tbc_lookups": {
                    "results": [
                        {"resources": [{"spec": {"storageDriverName": "ontap-san", "sanType": "nvme"}}]}
                    ]
                },
            },
        )
        is False
    )

    fail_bname_when = _find_task(
        block_tasks, "Validate resolved bound Trident backend names against regex injection"
    )["when"]
    assert (
        _render_when(
            fail_bname_when,
            {
                "_ontap_is_shared_tbc": True,
                "_ontap_profile_backend_names": {"p1": "invalid:name.*"},
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_bname_when,
            {
                "_ontap_is_shared_tbc": True,
                "_ontap_profile_backend_names": {"p1": "trident.prod_backend-01"},
            },
        )
        is False
    )

    for dedicated_task_name in (
        "Create the CSI Secret in the Trident namespace",
        "Create the TridentBackendConfig for dispatched backend",
    ):
        dedicated_when = _find_task(block_tasks, dedicated_task_name)["when"]
        assert _render_when(dedicated_when, {"_ontap_is_shared_tbc": False}) is True
        assert _render_when(dedicated_when, {"_ontap_is_shared_tbc": True}) is False

    # Wait for TBC to become Bound must set failed_when: false so timeout reaches explicit fail task
    wait_tbc_task = _find_task(block_tasks, "Wait for the TridentBackendConfig to become Bound")
    assert wait_tbc_task.get("failed_when") is False
    assert wait_tbc_task.get("no_log") is True
    assert (
        _render_when(
            wait_tbc_task["until"],
            {
                "_tbc_status": {
                    "resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Failed"}}]
                }
            },
        )
        is True
    )
    ded_fail_tbc_when = _find_task(block_tasks, "Fail when TridentBackendConfig phase is Failed")[
        "when"
    ]
    assert (
        _render_when(
            ded_fail_tbc_when,
            {
                "_ontap_is_shared_tbc": False,
                "_tbc_status": {
                    "resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Failed"}}]
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            ded_fail_tbc_when,
            {
                "_ontap_is_shared_tbc": False,
                "_tbc_status": {
                    "resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Success"}}]
                },
            },
        )
        is False
    )

    # Dedicated-SVM endpoint validation and _management_lif whitespace/scheme stripping
    endpoint_fail_when = _find_task(
        validate_tasks, "Fail if Dedicated-SVM backend connection is missing endpoint"
    )["when"]
    assert (
        _render_when(
            endpoint_fail_when,
            {
                "_ontap_is_shared_tbc": False,
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"endpoint": "https://"}},
            },
        )
        is True
    )
    assert (
        _render_when(
            endpoint_fail_when,
            {
                "_ontap_is_shared_tbc": False,
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"endpoint": " https://10.46.246.90/api "}},
            },
        )
        is False
    )
    lif_expr = _find_task(
        block_tasks, "Derive the management LIF from the backend connection endpoint"
    )["ansible.builtin.set_fact"]["_management_lif"]
    assert (
        _render(
            lif_expr,
            {
                "_ontap_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"endpoint": " https://10.46.246.90/api "}},
            },
        )
        == "10.46.246.90"
    )

    clear_task = _find_task(always_tasks, "Clear credential-bearing facts")
    assert clear_task.get("no_log") is True
    cleared_facts = clear_task["ansible.builtin.set_fact"]
    for fact_name in (
        "_runtime_password",
        "_svm_name",
        "_assignment_key",
        "_tbc_name",
        "_virtual_pools",
        "_shared_tbc_lookups",
        "_ontap_shared_sc_desired_params",
        "_ontap_stale_dedicated_sc_names",
    ):
        assert fact_name in cleared_facts

    # _setup_backend.yaml rendered tenant config in Shared-TBC and Dedicated-SVM modes
    setup_backend_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "_setup_backend.yaml")
    setup_block = _find_task(
        setup_backend_tasks, "Validate ONTAP backend mode and record per-backend tenant config"
    )
    shared_record_expr = _find_task(
        setup_block["block"], "Record the per-backend tenant config for Shared-TBC mode"
    )["ansible.builtin.set_fact"]["_ontap_tenant_configs"]
    shared_cfg = _render(
        shared_record_expr,
        {
            "_ontap_tenant_configs": {},
            "_ontap_backend_id": "shared-1",
            "_assignment_key": "0000000000000001",
            "_ontap_shared_profile_map": {"p1": {"tbc_namespace": "trident", "tbc_name": "tbc1"}},
        },
    )
    assert shared_cfg == {
        "shared-1": {
            "assignment_key": "0000000000000001",
            "mode": "shared",
            "profiles": {"p1": {"tbc_namespace": "trident", "tbc_name": "tbc1"}},
            "status": "ready",
        }
    }

    dedicated_record_expr = _find_task(
        setup_block["block"], "Record the per-backend tenant config for Dedicated-SVM mode"
    )["ansible.builtin.set_fact"]["_ontap_tenant_configs"]
    combined_cfg = _render(
        dedicated_record_expr,
        {
            "_ontap_tenant_configs": shared_cfg,
            "_ontap_backend_id": "ded-1",
            "_assignment_key": "0000000000000002",
            "_expected_svm_name": "svm-acme",
        },
    )
    assert combined_cfg["ded-1"] == {
        "assignment_key": "0000000000000002",
        "mode": "dedicated",
        "svm_name": "svm-acme",
        "status": "ready",
    }

    setup_clear_task = _find_task(setup_block["always"], "Clear credential-bearing facts")
    assert setup_clear_task.get("no_log") is True
    for fact_name in (
        "_expected_svm_name",
        "_assignment_key",
        "_ontap_shared_profile_map",
        "_ontap_is_shared_tbc",
        "_backend_tiers",
    ):
        assert fact_name in setup_clear_task["ansible.builtin.set_fact"]


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


def test_ontap_ensure_short_circuit_checks_tbc_and_csi_secret():
    """Render ensure_storage_class.yaml dedicated/shared partitioning, expected
    selectors, and _ontap_all_sc_exist short-circuit evaluation via Templar, and
    verify no task file emits set_stats.
    """
    ensure_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "ensure_storage_class.yaml")
    subset_task = _find_task(
        ensure_tasks, "Compute the dispatched backend ids and dedicated-SVM backend subset"
    )
    tiers = [
        {"name": "ded-gold", "backend_id": "ded-1"},
        {
            "name": "shared-fast",
            "backend_id": "shared-1",
            "provider_config": {"profile": "p1", "pool_selector": {"z": "2", "a": "1"}},
        },
    ]
    conns = {
        "ded-1": {
            "endpoint": "10.0.0.1",
            "tenant_preparation": {"provider_config": {"svm": "svm1", "username": "u", "password": "p"}},
        },
        "shared-1": {
            "provider_config": {
                "profiles": [{"name": "p1", "shared": {"tbc_namespace": "trident", "tbc_name": "tbc1"}}]
            }
        },
    }
    backend_ids = _render(
        subset_task["ansible.builtin.set_fact"]["_ontap_backend_ids"],
        {"_provider_tiers": tiers},
    )
    dedicated_ids = _render(
        subset_task["ansible.builtin.set_fact"]["_ontap_dedicated_backend_ids"],
        {"_provider_tiers": tiers, "_provider_backend_connections": conns},
    )
    assert backend_ids == ["ded-1", "shared-1"]
    assert dedicated_ids == ["ded-1"]

    entries_task = _find_task(
        ensure_tasks, "Build expected StorageClass entries and dedicated backend keys"
    )
    entries_vars = {
        "_provider_tiers": tiers,
        "_ontap_dedicated_backend_ids": dedicated_ids,
        "tenant_name": "acme",
    }
    sc_entries = _render(
        entries_task["ansible.builtin.set_fact"]["_ontap_expected_sc_entries"], entries_vars
    )
    ded_keys = _render(
        entries_task["ansible.builtin.set_fact"]["_ontap_dedicated_assignment_keys"], entries_vars
    )

    expected_task = _find_task(
        ensure_tasks,
        "Build expected StorageClass names, selectors, TBC names, and CSI Secret names",
    )
    expected_vars = {
        **entries_vars,
        "_ontap_expected_sc_entries": sc_entries,
        "_ontap_dedicated_assignment_keys": ded_keys,
    }
    sc_names = _render(expected_task["ansible.builtin.set_fact"]["_ontap_expected_sc_names"], expected_vars)
    sc_selectors = _render(
        expected_task["ansible.builtin.set_fact"]["_ontap_expected_sc_selectors"], expected_vars
    )
    tbc_names = _render(
        expected_task["ansible.builtin.set_fact"]["_ontap_expected_tbc_names"], expected_vars
    )
    csi_names = _render(
        expected_task["ansible.builtin.set_fact"]["_ontap_expected_csi_secret_names"], expected_vars
    )
    assert len(sc_names) == 2
    assert len(tbc_names) == 1
    assert len(csi_names) == 1
    assert sc_selectors[sc_names[1]] == ""

    all_exist_expr = _find_task(
        ensure_tasks,
        "Determine if all StorageClasses, Bound TridentBackendConfigs, and CSI Secrets already exist",
    )["ansible.builtin.set_fact"]["_ontap_all_sc_exist"]

    # When Shared-TBC backends are present, _ontap_all_sc_exist must be False so
    # _ensure_storage_class_backend.yaml runs customer TBC validation and storagePools checks.
    expected_tbc_spec = {
        "storageDriverName": "ontap-san",
        "backendName": tbc_names[0],
        "sanType": "iscsi",
        "managementLIF": "10.0.0.1",
        "svm": "svm1",
        "credentials": {"name": csi_names[0]},
        "storage": [
            {
                "labels": {"osac_backend": tbc_names[0], "tier": "ded-gold"},
                "defaults": {"encryption": "false"},
            }
        ],
    }
    mixed_exist_vars = {
        "tenant_name": "acme",
        "_provider_tiers": tiers,
        "_provider_backend_connections": conns,
        "_ontap_backend_ids": backend_ids,
        "_ontap_dedicated_backend_ids": dedicated_ids,
        "_ontap_expected_sc_names": sc_names,
        "_ontap_expected_sc_selectors": sc_selectors,
        "_ontap_expected_tbc_names": tbc_names,
        "_ontap_expected_csi_secret_names": csi_names,
        "_ontap_sc_checks": {
            "resources": [
                {
                    "metadata": {"name": sc_names[0]},
                    "parameters": {"backendType": "ontap-san", "selector": sc_selectors[sc_names[0]]},
                },
                {
                    "metadata": {"name": sc_names[1]},
                    "parameters": {"backendType": "ontap-san", "selector": "a=1;z=2"},
                },
            ]
        },
        "_ontap_tbc_checks": {
            "resources": [
                {
                    "metadata": {"name": tbc_names[0]},
                    "spec": expected_tbc_spec,
                    "status": {"phase": "Bound"},
                }
            ]
        },
        "_ontap_csi_checks": {
            "resources": [
                {
                    "metadata": {"name": csi_names[0]},
                    "data": {
                        "username": base64.b64encode(b"u").decode("ascii"),
                        "password": base64.b64encode(b"p").decode("ascii"),
                    },
                }
            ]
        },
    }
    assert _render(all_exist_expr, mixed_exist_vars) is False

    # When only Dedicated-SVM backends are present and all resources exist & TBC is Bound, short-circuit is True.
    dedicated_only_vars = {
        **mixed_exist_vars,
        "_ontap_backend_ids": ["ded-1"],
        "_ontap_expected_sc_names": [sc_names[0]],
    }
    assert _render(all_exist_expr, dedicated_only_vars) is True
    # When existing Bound TBC spec drifts (e.g., tier encryption_enabled or svm changes), do not short-circuit.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_ontap_tbc_checks": {
                    "resources": [
                        {
                            "metadata": {"name": tbc_names[0]},
                            "spec": {**expected_tbc_spec, "svm": "stale-svm"},
                            "status": {"phase": "Bound"},
                        }
                    ]
                },
            },
        )
        is False
    )
    # When CSI Secret username differs or password is empty, do not short-circuit.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_ontap_csi_checks": {
                    "resources": [
                        {
                            "metadata": {"name": csi_names[0]},
                            "data": {
                                "username": base64.b64encode(b"stale-user").decode("ascii"),
                                "password": base64.b64encode(b"p").decode("ascii"),
                            },
                        }
                    ]
                },
            },
        )
        is False
    )
    # When ontap_storage_validate_certs is True and CA bundle path is missing, do not short-circuit
    # even if existing TBC has no trustedCACertificate.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "ontap_storage_validate_certs": True,
                "ontap_storage_ca_bundle_path": "/nonexistent/ca-bundle.pem",
            },
        )
        is False
    )
    ensure_block = _find_task(
        ensure_tasks, "Provision TridentBackendConfig and StorageClasses on target cluster"
    )["block"]
    reconcile_csi_task = _find_task(
        ensure_block, "Reconcile Dedicated-SVM CSI Secrets before short-circuiting"
    )
    assert reconcile_csi_task.get("no_log") is True
    assert (
        _render_when(
            reconcile_csi_task["when"],
            {"_ontap_all_sc_exist": True, "_ontap_dedicated_backend_ids": ["ded-1"]},
        )
        is True
    )
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_ontap_tbc_checks": {
                    "resources": [{"metadata": {"name": tbc_names[0]}, "status": {"phase": "Failed"}}]
                },
            },
        )
        is False
    )
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_ontap_tbc_checks": {
                    "resources": [
                        {
                            "metadata": {"name": tbc_names[0]},
                            "spec": expected_tbc_spec,
                            "status": {"phase": "Bound", "lastOperationStatus": "Failed"},
                        }
                    ]
                },
            },
        )
        is False
    )

    for task_file in (ONTAP_ROLE_DIR / "tasks").glob("*.yaml"):
        parsed_tasks = _load_yaml(task_file)
        for task in parsed_tasks:
            assert "ansible.builtin.set_stats" not in task
            assert "set_stats" not in task
            for section in ("block", "rescue", "always"):
                for sub_task in task.get(section, []):
                    assert "ansible.builtin.set_stats" not in sub_task
                    assert "set_stats" not in sub_task

    # Verify _validate_tiers.yaml and teardown_backend.yaml share strict string backend_id checks
    # and teardown_backend.yaml does not leave _ontap_backend_ids in play scope.
    validate_tiers_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "_validate_tiers.yaml")
    tier_bid_when = _find_task(
        validate_tiers_tasks, "Fail if any dispatched tier is missing backend_id"
    )["when"]
    teardown_backend_tasks = _load_yaml(ONTAP_ROLE_DIR / "tasks" / "teardown_backend.yaml")
    teardown_bid_when = _find_task(
        teardown_backend_tasks, "Fail if any dispatched tier has an invalid backend_id"
    )["when"]
    for when_expr in (tier_bid_when, teardown_bid_when):
        assert _render_when(when_expr, {"_provider_tiers": [{"name": "gold"}]}) is True
        assert (
            _render_when(when_expr, {"_provider_tiers": [{"name": "gold", "backend_id": "   "}]})
            is True
        )
        assert (
            _render_when(when_expr, {"_provider_tiers": [{"name": "gold", "backend_id": 123}]})
            is True
        )
        assert (
            _render_when(
                when_expr, {"_provider_tiers": [{"name": "gold", "backend_id": "ontap-be-1"}]}
            )
            is False
        )
    assert all(
        "_ontap_backend_ids" not in task.get("ansible.builtin.set_fact", {})
        for task in teardown_backend_tasks
    )


def _setup_prepare_ontap_mocks(tmp_path: Path) -> dict[str, Path]:
    """Create stub kubectl, curl, and sleep binaries in tmp_path/bin for prepare-ontap-svm.sh."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    applied_file = tmp_path / "applied_secrets.jsonl"
    pf_pids_file = tmp_path / "pf_pids.txt"
    header_paths_file = tmp_path / "header_paths.txt"
    applied_file.write_text("", encoding="utf-8")
    pf_pids_file.write_text("", encoding="utf-8")
    header_paths_file.write_text("", encoding="utf-8")

    kubectl_stub = bin_dir / "kubectl"
    kubectl_stub.write_text(
        """#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *"get configmap ca-bundle"*)
    printf '%s' "${MOCK_CA_BUNDLE:-mock-ca-pem}"
    ;;
  *"get configmap -l osac.openshift.io/fulfillment-auth-config=true -o name"*)
    printf '%s\\n' 'configmap/fulfillment-auth'
    ;;
  *"get configmap/fulfillment-auth -o jsonpath="*)
    printf 'https://keycloak.example.test/realms/osac\\tclient-sec\\tcid\\tclient-sec\\tcsec'
    ;;
  *"get secret client-sec"*)
    if [[ "$*" == *"cid"* ]]; then
      printf '%s' "${MOCK_CLIENT_ID_B64}"
    else
      printf '%s' "${MOCK_CLIENT_SECRET_B64}"
    fi
    ;;
  *"port-forward"*)
    printf '%s\\n' "$$" >> "${MOCK_PF_PIDS_FILE}"
    exec /bin/sleep 300
    ;;
  *"get tenant.osac.openshift.io "*)
    if [[ "$*" == *"tenant-missing"* ]]; then
      exit 1
    elif [[ "$*" == *"tenant-unlabeled"* ]]; then
      printf ''
    else
      printf 'tid-from-tenant-cr'
    fi
    ;;
  *"get namespace "*)
    exit 0
    ;;
  *"apply --server-side --field-manager=prepare-ontap-svm -f -"*)
    jq -c . >> "${MOCK_APPLIED_FILE}"
    ;;
esac
""",
        encoding="utf-8",
    )
    kubectl_stub.chmod(0o755)

    curl_stub = bin_dir / "curl"
    curl_stub.write_text(
        """#!/usr/bin/env bash
set -euo pipefail
args="$*"
while (($#)); do
  if [[ "$1" == "--header" || "$1" == "-H" ]]; then
    shift
    if [[ "${1:-}" == @* ]]; then
      printf '%s\\n' "${1#@}" >> "${MOCK_HEADER_PATHS_FILE}"
    fi
  fi
  shift || true
done
if [[ "${args}" == *"/protocol/openid-connect/token"* ]]; then
  printf '{"access_token":"mock-fulfillment-token","token_type":"Bearer"}\\n'
elif [[ "${args}" == *"/api/private/v1/storage_backends"* ]]; then
  printf '%s' "${MOCK_STORAGE_BACKENDS_JSON:-{\\"items\\":[]}}"
fi
""",
        encoding="utf-8",
    )
    curl_stub.chmod(0o755)

    sleep_stub = bin_dir / "sleep"
    sleep_stub.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
    sleep_stub.chmod(0o755)

    return {
        "bin_dir": bin_dir,
        "applied_file": applied_file,
        "pf_pids_file": pf_pids_file,
        "header_paths_file": header_paths_file,
    }


def _mock_env(paths: dict[str, Path], extra: dict[str, str] | None = None) -> dict[str, str]:
    env = os.environ.copy()
    env.pop("ONTAP_SVM_PASSWORD", None)
    env["PATH"] = f"{paths['bin_dir']}:{env['PATH']}"
    env["MOCK_APPLIED_FILE"] = str(paths["applied_file"])
    env["MOCK_PF_PIDS_FILE"] = str(paths["pf_pids_file"])
    env["MOCK_HEADER_PATHS_FILE"] = str(paths["header_paths_file"])
    env["MOCK_CLIENT_ID_B64"] = base64.b64encode(b"mock-client").decode()
    env["MOCK_CLIENT_SECRET_B64"] = base64.b64encode(b"mock-secret").decode()
    if extra:
        env.update(extra)
    return env


def test_prepare_ontap_svm_batch_map_env_password_safe_backend_and_resolve_id(tmp_path: Path):
    """Exercise --map batch mode, ONTAP_SVM_PASSWORD, safe_backend normalization,
    resolve_backend_id_by_name via the Fulfillment internal API, and cleanup traps.
    """
    paths = _setup_prepare_ontap_mocks(tmp_path)
    backends_json = json.dumps(
        {"items": [{"id": "ONTAP_BE.01", "metadata": {"name": "ontap-primary"}}]}
    )
    env = _mock_env(
        paths,
        {
            "ONTAP_SVM_PASSWORD": "env-supplied-password",
            "MOCK_STORAGE_BACKENDS_JSON": backends_json,
        },
    )

    proc = subprocess.run(
        [
            str(PREPARE_ONTAP_SCRIPT),
            "--backend",
            "ontap-primary",
            "--map",
            "tenant1:svm_t1:user_t1",
            "--map",
            "tenant2:svm_t2:user_t2:explicit-tid-2:custom-sec-t2",
        ],
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr
    assert "Resolved StorageBackend ID." in proc.stdout
    assert "ONTAP_BE.01" not in proc.stdout
    assert "ONTAP_BE.01" not in proc.stderr
    assert "Prepared 2 ONTAP tenant preparation Secret(s)." in proc.stdout

    secrets = [
        json.loads(line)
        for line in paths["applied_file"].read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]
    assert len(secrets) == 2

    # First --map entry uses resolved tenant-id and safe_backend normalization ("ONTAP_BE.01" -> "ontap-be-01")
    sec1 = secrets[0]
    assert sec1["metadata"]["name"] == "ontap-prep-tenant1-ontap-be-01"
    assert sec1["metadata"]["labels"] == {
        "osac.openshift.io/secret-type": "storage-tenant-preparation",
        "osac.openshift.io/storage-backend": "ontap-primary",
        "osac.openshift.io/storage-backend-id": "ONTAP_BE.01",
        "osac.openshift.io/tenant-id": "tid-from-tenant-cr",
        "osac.openshift.io/tenant": "tenant1",
    }
    assert sec1["stringData"] == {
        "username": "user_t1",
        "password": "env-supplied-password",
        "svm": "svm_t1",
    }

    # Second --map entry uses explicit tenant_id and explicit secret_name
    sec2 = secrets[1]
    assert sec2["metadata"]["name"] == "custom-sec-t2"
    assert sec2["metadata"]["labels"]["osac.openshift.io/tenant-id"] == "explicit-tid-2"
    assert sec2["metadata"]["labels"]["osac.openshift.io/tenant"] == "tenant2"
    assert sec2["stringData"] == {
        "username": "user_t2",
        "password": "env-supplied-password",
        "svm": "svm_t2",
    }

    # Verify port-forward process and temporary token header file were cleaned up
    for pid_line in paths["pf_pids_file"].read_text(encoding="utf-8").splitlines():
        if pid_line.strip():
            pid = int(pid_line.strip())
            try:
                os.kill(pid, 0)
                raise AssertionError(f"port-forward pid {pid} was not terminated")
            except ProcessLookupError:
                pass
    for header_path in paths["header_paths_file"].read_text(encoding="utf-8").splitlines():
        if header_path.strip():
            assert not Path(header_path.strip()).exists()


def test_prepare_ontap_svm_password_stdin_secret_name_and_resolve_name_by_id(tmp_path: Path):
    """Exercise --password-stdin, --secret-name, resolve_backend_name_by_id,
    and error/fallback paths when API resolution fails.
    """
    paths = _setup_prepare_ontap_mocks(tmp_path)
    backends_json = json.dumps(
        {"items": [{"id": "be-uuid-99", "metadata": {"name": "ontap-resolved"}}]}
    )
    env = _mock_env(paths, {"MOCK_STORAGE_BACKENDS_JSON": backends_json})

    proc = subprocess.run(
        [
            str(PREPARE_ONTAP_SCRIPT),
            "--tenant",
            "tenant1",
            "--tenant-id",
            "tid-explicit-1",
            "--backend-id",
            "be-uuid-99",
            "--svm",
            "svm_stdin",
            "--username",
            "user_stdin",
            "--secret-name",
            "explicit-secret-name",
            "--password-stdin",
        ],
        input="stdin-secret-password",
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr
    assert "Resolved StorageBackend name." in proc.stdout
    assert "ontap-resolved" not in proc.stdout
    assert "ontap-resolved" not in proc.stderr

    secrets = [
        json.loads(line)
        for line in paths["applied_file"].read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]
    assert len(secrets) == 1
    assert secrets[0]["metadata"]["name"] == "explicit-secret-name"
    assert secrets[0]["metadata"]["labels"]["osac.openshift.io/storage-backend"] == "ontap-resolved"
    assert secrets[0]["metadata"]["labels"]["osac.openshift.io/storage-backend-id"] == "be-uuid-99"
    assert secrets[0]["stringData"]["password"] == "stdin-secret-password"

    # Error when --backend-id is passed without --backend and API cannot resolve name
    unresolvable_env = _mock_env(paths, {"MOCK_STORAGE_BACKENDS_JSON": '{"items":[]}'})
    fail_proc = subprocess.run(
        [
            str(PREPARE_ONTAP_SCRIPT),
            "--tenant",
            "tenant1",
            "--tenant-id",
            "tid-1",
            "--backend-id",
            "unknown-backend-id",
            "--svm",
            "svm1",
            "--username",
            "user1",
            "--generate-password",
        ],
        env=unresolvable_env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert fail_proc.returncode != 0
    assert (
        "Could not resolve StorageBackend name via API; pass --backend <name> explicitly."
        in fail_proc.stderr
    )
    assert "unknown-backend-id" not in fail_proc.stdout
    assert "unknown-backend-id" not in fail_proc.stderr

    # Fallback warnings when --backend is passed without --backend-id (API cannot resolve ID)
    # and when Tenant CR has no osac.openshift.io/tenant-id label (tenant-unlabeled).
    paths["applied_file"].write_text("", encoding="utf-8")
    fallback_proc = subprocess.run(
        [
            str(PREPARE_ONTAP_SCRIPT),
            "--tenant",
            "tenant-unlabeled",
            "--backend",
            "ontap-unregistered",
            "--svm",
            "svm1",
            "--username",
            "user1",
            "--generate-password",
        ],
        env=unresolvable_env,
        capture_output=True,
        text=True,
        check=False,
    )
    assert fallback_proc.returncode == 0, fallback_proc.stderr
    assert (
        "Could not resolve StorageBackend ID via API; falling back to the supplied backend name."
        in fallback_proc.stderr
    )
    assert (
        "Target Tenant CR has no osac.openshift.io/tenant-id label; falling back to the Tenant name."
        in fallback_proc.stderr
    )
    assert "ontap-unregistered" not in fallback_proc.stdout
    assert "ontap-unregistered" not in fallback_proc.stderr
    assert "tenant-unlabeled" not in fallback_proc.stdout
    assert "tenant-unlabeled" not in fallback_proc.stderr
    assert "Resolved StorageBackend ID" not in fallback_proc.stdout
