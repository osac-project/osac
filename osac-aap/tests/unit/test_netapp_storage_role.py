"""Unit and contract tests for the netapp_storage provider role."""

import base64
import secrets
from pathlib import Path

import yaml  # type: ignore[import-untyped]
from ansible.parsing.dataloader import DataLoader  # type: ignore[import-not-found]
from ansible.template import Templar  # type: ignore[import-not-found]

REPO_ROOT = Path(__file__).resolve().parents[2]
# Non-secret test credentials, generated per run so no credential-shaped
# literal lives in the test source.
FIXTURE_PASSWORD = secrets.token_hex(8)
FIXTURE_WHITESPACE_PASSWORD = "   "
NETAPP_ROLE_DIR = (
    REPO_ROOT
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "netapp_storage"
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


def test_netapp_meta_declares_block_vmaas_only():
    """Verify meta/osac.yaml declares storage_provider, netapp strategy, and block/vmaas capabilities."""
    meta = _load_yaml(NETAPP_ROLE_DIR / "meta" / "osac.yaml")
    assert meta["template_type"] == "storage_provider"
    assert meta["implementation_strategy"] == "netapp"
    assert meta["capabilities"]["supported_protocols"] == ["block"]
    assert meta["capabilities"]["provisioning_targets"] == ["vmaas"]


def test_netapp_virtual_pools_use_labels_and_selector_not_storage_pools(
    tmp_path: Path, monkeypatch
):
    """Render virtual pool definitions, Dedicated-SVM StorageClass parameters,
    Shared-TBC StorageClass parameters (including sorted multi-key pool_selector
    and dot-escaped storagePools), and CA bundle resolution via Ansible's Templar.
    """
    ensure_backend_path = NETAPP_ROLE_DIR / "tasks" / "_ensure_storage_class_backend.yaml"
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
            "_tbc_name": "netapp-0000000000000001",
            "_assignment_key": "0000000000000001",
            "_backend_tiers": [
                {
                    "name": "gold",
                    "encryption_enabled": True,
                    "qos_limits": {
                        "static_limits": {"max_reads_bw_mbps": 500, "max_writes_bw_mbps": 250}
                    },
                },
                {
                    "name": "silver",
                    "encryption_enabled": False,
                },
            ],
            "_netapp_qos_policies": {"gold": "acme_gold_qos"},
        },
    )
    assert len(rendered_pools) == 2
    assert "name" not in rendered_pools[0]
    assert rendered_pools[0]["labels"] == {
        "osac_backend": "netapp-0000000000000001",
        "tier": "gold",
    }
    assert rendered_pools[0]["defaults"] == {
        "encryption": "true",
        "qosPolicy": "acme_gold_qos",
    }
    assert rendered_pools[1] == {
        "labels": {"osac_backend": "netapp-0000000000000001", "tier": "silver"},
        "defaults": {"encryption": "false"},
    }

    # 1b. managementLIF prefers the prepared SVM LIF and falls back to the endpoint.
    lif_task = _find_task(
        block_tasks, "Derive the management LIF from the prepared assignment or backend endpoint"
    )
    lif_expr = lif_task["ansible.builtin.set_fact"]["_management_lif"]

    def _lif(conn):
        return _render(
            lif_expr,
            {"_netapp_backend_id": "be-1", "_provider_backend_connections": {"be-1": conn}},
        )

    prep_with_lif = {"provider_config": {"management_lif": "https://svm-mgmt.example:443/api"}}
    assert _lif({"endpoint": "https://cluster-mgmt", "tenant_preparation": prep_with_lif}) == (
        "svm-mgmt.example:443"
    )
    assert _lif({"endpoint": "https://cluster-mgmt/", "tenant_preparation": {"provider_config": {}}}) == (
        "cluster-mgmt"
    )

    # 2. Render Dedicated-SVM StorageClass parameters and stale Dedicated-SVM SC detection.
    sc_task = _find_task(block_tasks, "Create the per-tier StorageClasses")
    dedicated_sc_params = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"_tbc_name": "netapp-0000000000000001", "item": {"name": "gold"}},
    )
    assert dedicated_sc_params == {
        "backendType": "ontap-san",
        "selector": "osac_backend=netapp-0000000000000001;tier=gold",
    }
    assert "storagePools" not in dedicated_sc_params

    stale_ded_task = _find_task(
        block_tasks,
        "Identify existing Dedicated-SVM StorageClasses with changed immutable parameters",
    )
    stale_ded_expr = stale_ded_task["ansible.builtin.set_fact"]["_netapp_stale_dedicated_sc_names"]
    ded_sc_name = _render(
        sc_task["kubernetes.core.k8s"]["definition"]["metadata"]["name"],
        {"_assignment_key": "0000000000000001", "item": {"name": "gold"}},
    )
    assert (
        _render(
            stale_ded_expr,
            {
                "_assignment_key": "0000000000000001",
                "_tbc_name": "netapp-0000000000000001",
                "_backend_tiers": [{"name": "gold"}],
                "_netapp_sc_checks": {
                    "resources": [
                        {"metadata": {"name": ded_sc_name}, "parameters": dedicated_sc_params}
                    ]
                },
            },
        )
        == []
    )
    assert _render(
        stale_ded_expr,
        {
            "_assignment_key": "0000000000000001",
            "_tbc_name": "netapp-0000000000000001",
            "_backend_tiers": [{"name": "gold"}],
            "_netapp_sc_checks": {
                "resources": [
                    {
                        "metadata": {"name": ded_sc_name},
                        "parameters": {
                            "backendType": "ontap-san",
                            "selector": "osac_backend=netapp-old;tier=gold",
                        },
                    }
                ]
            },
        },
    ) == [ded_sc_name]

    # 3. Render Shared-TBC StorageClass parameters: multi-key pool_selector sorted by key,
    #    dot-escaped backendName in storagePools when resolved, and omitted when empty.
    desired_params_task = _find_task(
        block_tasks, "Build desired Shared-TBC StorageClass parameters per tier"
    )
    desired_params_expr = desired_params_task["ansible.builtin.set_fact"][
        "_netapp_shared_sc_desired_params"
    ]
    shared_sc_task = _find_task(
        block_tasks, "Create the per-tier StorageClasses for shared Trident backend"
    )
    assert shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"] == (
        "{{ _netapp_shared_sc_desired_params[item.name] }}"
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
            "_netapp_profile_backend_names": {"gold-prof": "trident.shared-san.01"},
        },
    )
    rendered_shared_with_bname = _render(
        shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"item": shared_tier, "_netapp_shared_sc_desired_params": rendered_desired_with_bname},
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
            "_netapp_profile_backend_names": {"gold-prof": ""},
        },
    )
    rendered_shared_without_bname = _render(
        shared_sc_task["kubernetes.core.k8s"]["definition"]["parameters"],
        {"item": shared_tier, "_netapp_shared_sc_desired_params": rendered_desired_without_bname},
    )
    assert rendered_shared_without_bname == {
        "backendType": "ontap-san",
        "selector": "a_env=prod;z_tier=fast",
    }

    # Verify stale Shared-TBC StorageClass detection agrees with rendered parameters.
    stale_task = _find_task(
        block_tasks, "Identify existing Shared-TBC StorageClasses with changed immutable parameters"
    )
    stale_expr = stale_task["ansible.builtin.set_fact"]["_netapp_stale_shared_sc_names"]
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
                "_netapp_shared_sc_desired_params": rendered_desired_with_bname,
                "_netapp_sc_checks": {
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
            "_netapp_shared_sc_desired_params": rendered_desired_with_bname,
            "_netapp_sc_checks": {
                "resources": [
                    {"metadata": {"name": sc_name}, "parameters": rendered_shared_without_bname}
                ]
            },
        },
    ) == [sc_name]

    # 4. Evaluate netapp_storage_validate_certs default and CA bundle fail-fast conditions.
    defaults = _load_yaml(NETAPP_ROLE_DIR / "defaults" / "main.yaml")
    monkeypatch.delenv("NETAPP_VALIDATE_CERTS", raising=False)
    assert _render(defaults["netapp_storage_validate_certs"], {}) is True
    monkeypatch.setenv("NETAPP_VALIDATE_CERTS", "false")
    assert _render(defaults["netapp_storage_validate_certs"], {}) is False
    monkeypatch.setenv("NETAPP_VALIDATE_CERTS", "unexpected")
    assert _render(defaults["netapp_storage_validate_certs"], {}) is True

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
        {"netapp_storage_validate_certs": True, "netapp_storage_ca_bundle_path": str(ca_file)},
    )
    assert rendered_ca_b64 == base64.b64encode(ca_pem.encode("utf-8")).decode("ascii")
    assert (
        _render_when(
            ca_fail_when,
            {
                "_netapp_is_shared_tbc": False,
                "netapp_storage_validate_certs": True,
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
                "netapp_storage_validate_certs": True,
                "netapp_storage_ca_bundle_path": str(missing_or_empty),
            },
        )
        assert missing_ca_b64 == ""
        assert (
            _render_when(
                ca_fail_when,
                {
                    "_netapp_is_shared_tbc": False,
                    "netapp_storage_validate_certs": True,
                    "_trusted_ca_b64": missing_ca_b64,
                },
            )
            is True
        )
    assert (
        _render_when(
            ca_fail_when,
            {
                "_netapp_is_shared_tbc": False,
                "netapp_storage_validate_certs": False,
                "_trusted_ca_b64": "",
            },
        )
        is False
    )


def test_netapp_dual_mode_validation_and_shared_tbc_contracts():
    """Evaluate dual-mode validation (_validate_backend_mode.yaml) and Shared-TBC
    lookup/fail-fast expressions (_ensure_storage_class_backend.yaml and
    _setup_backend.yaml) via Ansible's Templar.
    """
    validate_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "_validate_backend_mode.yaml")

    # Missing or non-mapping backend connection entry
    missing_conn_when = _find_task(
        validate_tasks,
        "Fail if backend connection details are missing or invalid for dispatched backend",
    )["when"]
    assert _render_when(missing_conn_when, {"_netapp_backend_id": "b1"}) is True
    assert (
        _render_when(
            missing_conn_when,
            {"_netapp_backend_id": "b1", "_provider_backend_connections": {"b2": {}}},
        )
        is True
    )
    assert (
        _render_when(
            missing_conn_when,
            {"_netapp_backend_id": "b1", "_provider_backend_connections": {"b1": "not-a-dict"}},
        )
        is True
    )
    assert (
        _render_when(
            missing_conn_when,
            {"_netapp_backend_id": "b1", "_provider_backend_connections": {"b1": {}}},
        )
        is False
    )

    # Mode discrimination (_netapp_is_shared_tbc) and _backend_tiers filtering
    mode_task = _find_task(validate_tasks, "Select backend tiers and determine ONTAP backend tenancy mode")
    backend_tiers_expr = mode_task["ansible.builtin.set_fact"]["_backend_tiers"]
    assert _render(
        backend_tiers_expr,
        {
            "_netapp_backend_id": "b1",
            "_provider_tiers": [
                {"name": "gold", "backend_id": "b1"},
                {"name": "silver", "backend_id": "b2"},
            ],
        },
    ) == [{"name": "gold", "backend_id": "b1"}]
    is_shared_expr = mode_task["ansible.builtin.set_fact"]["_netapp_is_shared_tbc"]
    assert (
        _render(
            is_shared_expr,
            {
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"provider_config": {"profiles": []}}},
            },
        )
        is True
    )
    assert (
        _render(
            is_shared_expr,
            {
                "_netapp_backend_id": "b1",
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
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": []}, "tenant_preparation": {}}
                },
            },
        )
        is True
    )
    assert (
        _render_when(
            both_modes_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {"b1": {"provider_config": {"profiles": []}}},
            },
        )
        is False
    )

    # Reject empty or non-list provider_config.profiles in Shared-TBC mode
    non_empty_profiles_when = _find_task(
        validate_tasks, "Fail if Shared-TBC provider_config.profiles is not a non-empty list"
    )["when"]
    for bad_profiles in ([], "not-a-list", {"name": "gold"}):
        assert (
            _render_when(
                non_empty_profiles_when,
                {
                    "_netapp_is_shared_tbc": True,
                    "_netapp_backend_id": "b1",
                    "_provider_backend_connections": {
                        "b1": {"provider_config": {"profiles": bad_profiles}}
                    },
                },
            )
            is True
        )
    assert (
        _render_when(
            non_empty_profiles_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": [{"name": "gold"}]}}
                },
            },
        )
        is False
    )

    # Validate Shared-TBC profile entries (DNS label name/tbc_namespace, DNS subdomain tbc_name <= 253)
    profile_entries_when = _find_task(validate_tasks, "Validate Shared-TBC profile entries")["when"]
    for bad_entry in (
        {"name": "gold"},
        {"name": "INVALID_NAME", "shared": {"tbc_namespace": "trident", "tbc_name": "tbc-1"}},
        {"name": "gold", "shared": {"tbc_namespace": "-bad-ns", "tbc_name": "tbc-1"}},
        {"name": "gold", "shared": {"tbc_namespace": "trident", "tbc_name": "bad..subdomain"}},
        {"name": "gold", "shared": {"tbc_namespace": "trident", "tbc_name": "a" * 254}},
    ):
        assert (
            _render_when(
                profile_entries_when,
                {
                    "_netapp_is_shared_tbc": True,
                    "_netapp_backend_id": "b1",
                    "_provider_backend_connections": {
                        "b1": {"provider_config": {"profiles": [bad_entry]}}
                    },
                },
            )
            is True
        )
    assert (
        _render_when(
            profile_entries_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {
                        "provider_config": {
                            "profiles": [
                                {
                                    "name": " gold-01 ",
                                    "shared": {
                                        "tbc_namespace": " trident ",
                                        "tbc_name": " shared.tbc-01 ",
                                    },
                                }
                            ]
                        }
                    }
                },
            },
        )
        is False
    )

    # Duplicate profile detection (including whitespace-padded duplicates)
    dup_profile_when = _find_task(
        validate_tasks, "Fail if Shared-TBC profile names are not unique"
    )["when"]
    assert (
        _render_when(
            dup_profile_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
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
                "_netapp_is_shared_tbc": True,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"provider_config": {"profiles": [{"name": "gold"}, {"name": "silver"}]}}
                },
            },
        )
        is False
    )

    # Build Shared-TBC profile map and validate tier profile reference & non-empty pool_selector
    profile_map_expr = _find_task(
        validate_tasks, "Build Shared-TBC profile map for dispatched backend"
    )["ansible.builtin.set_fact"]["_netapp_shared_profile_map"]
    rendered_profile_map = _render(
        profile_map_expr,
        {
            "_netapp_backend_id": "b1",
            "_provider_backend_connections": {
                "b1": {
                    "provider_config": {
                        "profiles": [
                            {
                                "name": " gold ",
                                "shared": {"tbc_namespace": " trident ", "tbc_name": " tbc-gold "},
                            }
                        ]
                    }
                }
            },
        },
    )
    assert rendered_profile_map == {
        "gold": {"tbc_namespace": "trident", "tbc_name": "tbc-gold"}
    }

    tier_profile_when = _find_task(
        validate_tasks,
        "Fail if a Shared-TBC tier is missing valid provider_config profile or pool_selector",
    )["when"]
    for bad_tier in (
        {"name": "t1"},
        {"name": "t1", "provider_config": {"profile": "unknown", "pool_selector": {"tier": "gold"}}},
        {"name": "t1", "provider_config": {"profile": "gold", "pool_selector": {}}},
    ):
        assert (
            _render_when(
                tier_profile_when,
                {
                    "_netapp_is_shared_tbc": True,
                    "_netapp_shared_profile_map": rendered_profile_map,
                    "_backend_tiers": [bad_tier],
                },
            )
            is True
        )
    assert (
        _render_when(
            tier_profile_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_shared_profile_map": rendered_profile_map,
                "_backend_tiers": [
                    {
                        "name": "t1",
                        "provider_config": {
                            "profile": " gold ",
                            "pool_selector": {"tier": "gold"},
                        },
                    }
                ],
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
                "_netapp_is_shared_tbc": True,
                "_backend_tiers": [{"provider_config": {"pool_selector": {"tier": "gold;evil=1"}}}],
            },
        )
        is True
    )
    assert (
        _render_when(
            invalid_selector_when,
            {
                "_netapp_is_shared_tbc": True,
                "_backend_tiers": [
                    {"provider_config": {"pool_selector": {"storage.tier/name": "gold_01"}}}
                ],
            },
        )
        is False
    )

    # Bandwidth QoS: tiers with non-zero static_limits request QoS.
    qos_names_expr = _find_task(
        validate_tasks, "Determine which dispatched tiers request bandwidth QoS"
    )["ansible.builtin.set_fact"]["_netapp_qos_tier_names"]
    assert _render(
        qos_names_expr,
        {
            "_backend_tiers": [
                {"name": "gold", "qos_limits": {"static_limits": {"max_reads_bw_mbps": 100}}},
                {"name": "silver", "qos_limits": {"static_limits": {"max_writes_bw_mbps": "50"}}},
                {
                    "name": "bronze",
                    "qos_limits": {
                        "static_limits": {"max_reads_bw_mbps": 0, "max_writes_bw_mbps": 0}
                    },
                },
                {"name": "plain"},
                {"name": "nulls", "qos_limits": {"static_limits": None}},
            ]
        },
    ) == ["gold", "silver"]

    shared_qos_when = _find_task(
        validate_tasks, "Fail if a Shared-TBC tier requests bandwidth QoS"
    )["when"]
    assert (
        _render_when(
            shared_qos_when, {"_netapp_is_shared_tbc": True, "_netapp_qos_tier_names": ["gold"]}
        )
        is True
    )
    assert (
        _render_when(shared_qos_when, {"_netapp_is_shared_tbc": True, "_netapp_qos_tier_names": []})
        is False
    )
    assert (
        _render_when(
            shared_qos_when, {"_netapp_is_shared_tbc": False, "_netapp_qos_tier_names": ["gold"]}
        )
        is False
    )

    # Dedicated-SVM QoS policy names come from the prepared assignment.
    qos_select_expr = _find_task(
        validate_tasks, "Select the prepared QoS policy group names for Dedicated-SVM backend"
    )["ansible.builtin.set_fact"]["_netapp_qos_policies"]

    def _qos_conn(pcfg):
        return {"b1": {"tenant_preparation": {"provider_config": pcfg}}}

    assert _render(
        qos_select_expr,
        {
            "_netapp_is_shared_tbc": False,
            "_netapp_backend_id": "b1",
            "_provider_backend_connections": _qos_conn({"qos_policies": {"gold": "pg_gold"}}),
        },
    ) == {"gold": "pg_gold"}
    assert (
        _render(
            qos_select_expr,
            {
                "_netapp_is_shared_tbc": False,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": _qos_conn({}),
            },
        )
        == {}
    )

    qos_shape_when = _find_task(
        validate_tasks,
        "Fail if Dedicated-SVM qos_policies is not a mapping of valid policy group names",
    )["when"]
    for bad_policies in (["pg"], {"gold": ""}, {"gold": "-bad"}, {"gold": "has space"}, {"gold": 7}):
        assert (
            _render_when(
                qos_shape_when,
                {"_netapp_is_shared_tbc": False, "_netapp_qos_policies": bad_policies},
            )
            is True
        ), bad_policies
    assert (
        _render_when(
            qos_shape_when,
            {"_netapp_is_shared_tbc": False, "_netapp_qos_policies": {"gold": "acme_gold.qos-1"}},
        )
        is False
    )

    qos_missing_when = _find_task(
        validate_tasks,
        "Fail if a Dedicated-SVM tier requests bandwidth QoS without a prepared policy group",
    )["when"]
    assert (
        _render_when(
            qos_missing_when,
            {
                "_netapp_is_shared_tbc": False,
                "_netapp_qos_tier_names": ["gold", "silver"],
                "_netapp_qos_policies": {"gold": "pg_gold"},
            },
        )
        is True
    )
    assert (
        _render_when(
            qos_missing_when,
            {
                "_netapp_is_shared_tbc": False,
                "_netapp_qos_tier_names": ["gold"],
                "_netapp_qos_policies": {"gold": "pg_gold", "bronze": "pg_bronze"},
            },
        )
        is False
    )

    # Dedicated-SVM tenant_preparation presence, non-empty required fields, and tier rejection of profile/pool_selector
    missing_prep_when = _find_task(
        validate_tasks, "Fail if tenant_preparation is missing for Dedicated-SVM backend"
    )["when"]
    for bad_conn in ({}, {"tenant_preparation": "bad"}, {"tenant_preparation": {"provider_config": None}}):
        assert (
            _render_when(
                missing_prep_when,
                {
                    "_netapp_is_shared_tbc": False,
                    "_netapp_backend_id": "b1",
                    "_provider_backend_connections": {"b1": bad_conn},
                },
            )
            is True
        )
    assert (
        _render_when(
            missing_prep_when,
            {
                "_netapp_is_shared_tbc": False,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {"tenant_preparation": {"provider_config": {}}}
                },
            },
        )
        is False
    )

    prep_fields_when = _find_task(
        validate_tasks,
        "Validate operator-supplied tenant preparation fields for Dedicated-SVM backend",
    )["when"]
    for bad_pcfg in (
        {"username": "", "password": FIXTURE_PASSWORD, "svm": "svm1"},
        {"username": "u", "password": FIXTURE_WHITESPACE_PASSWORD, "svm": "svm1"},
        {"username": "u", "password": FIXTURE_PASSWORD, "svm": ""},
    ):
        assert (
            _render_when(
                prep_fields_when,
                {
                    "_netapp_is_shared_tbc": False,
                    "_netapp_backend_id": "b1",
                    "_provider_backend_connections": {
                        "b1": {"tenant_preparation": {"provider_config": bad_pcfg}}
                    },
                },
            )
            is True
        )
    assert (
        _render_when(
            prep_fields_when,
            {
                "_netapp_is_shared_tbc": False,
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {
                        "tenant_preparation": {
                            "provider_config": {"username": "u", "password": FIXTURE_PASSWORD, "svm": "svm1"}
                        }
                    }
                },
            },
        )
        is False
    )

    ded_reject_shared_when = _find_task(
        validate_tasks, "Fail if a Dedicated-SVM tier specifies Shared-TBC provider_config fields"
    )["when"]
    assert (
        _render_when(
            ded_reject_shared_when,
            {
                "_netapp_is_shared_tbc": False,
                "_backend_tiers": [{"name": "gold", "provider_config": {"profile": "p1"}}],
            },
        )
        is True
    )
    assert (
        _render_when(
            ded_reject_shared_when,
            {
                "_netapp_is_shared_tbc": False,
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
            {"_netapp_is_shared_tbc": False, "_backend_tiers": [{"name": "gold"}]},
        )
        is False
    )

    # Shared-TBC backendName resolution and fail-fast conditions in _ensure_storage_class_backend.yaml
    ensure_backend_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "_ensure_storage_class_backend.yaml")
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

    # Only NotFound (empty resources) and Forbidden fall back to selector-only;
    # any other lookup error fails instead of dropping the storagePools pin.
    lookup_error_when = _find_task(
        block_tasks,
        "Fail when a customer TridentBackendConfig lookup failed for a reason other than Forbidden",
    )["when"]

    def _lookup_error(results):
        return _render_when(
            lookup_error_when,
            {"_netapp_is_shared_tbc": True, "_shared_tbc_lookups": {"results": results}},
        )

    assert _lookup_error([{"item": "p", "resources": []}]) is False
    assert _lookup_error([{"item": "p", "failed": False, "msg": "403 Forbidden: denied"}]) is False
    assert _lookup_error([{"item": "p", "skipped": True}]) is False
    assert (
        _lookup_error([{"item": "p", "failed": False, "msg": "Failed to get client: timed out"}])
        is True
    )
    assert _lookup_error([{"item": "p", "failed": False}]) is True
    assert (
        _render_when(
            lookup_error_when,
            {
                "_netapp_is_shared_tbc": False,
                "_shared_tbc_lookups": {"results": [{"item": "p", "msg": "boom"}]},
            },
        )
        is False
    )

    resolve_bnames_expr = _find_task(
        block_tasks, "Resolve bound Trident backend names per Shared-TBC profile"
    )["ansible.builtin.set_fact"]["_netapp_profile_backend_names"]
    resolved_bnames = _render(
        resolve_bnames_expr,
        {
            "_netapp_referenced_profiles": [
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
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
                "_netapp_is_shared_tbc": True,
                "_netapp_profile_backend_names": {"p1": "invalid:name.*"},
            },
        )
        is True
    )
    assert (
        _render_when(
            fail_bname_when,
            {
                "_netapp_is_shared_tbc": True,
                "_netapp_profile_backend_names": {"p1": "trident.prod_backend-01"},
            },
        )
        is False
    )

    for dedicated_task_name in (
        "Create the CSI Secret in the Trident namespace",
        "Create the TridentBackendConfig for dispatched backend",
    ):
        dedicated_when = _find_task(block_tasks, dedicated_task_name)["when"]
        assert _render_when(dedicated_when, {"_netapp_is_shared_tbc": False}) is True
        assert _render_when(dedicated_when, {"_netapp_is_shared_tbc": True}) is False

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
                "_netapp_is_shared_tbc": False,
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
                "_netapp_is_shared_tbc": False,
                "_tbc_status": {
                    "resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Success"}}]
                },
            },
        )
        is False
    )
    ded_not_bound_when = _find_task(
        block_tasks, "Fail if the TridentBackendConfig did not become Bound"
    )["when"]
    for unbound_status in (
        {"resources": []},
        {"resources": [{"status": {"phase": "Pending"}}]},
        {"resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Pending"}}]},
    ):
        assert (
            _render_when(
                ded_not_bound_when,
                {"_netapp_is_shared_tbc": False, "_tbc_status": unbound_status},
            )
            is True
        )
    assert (
        _render_when(
            ded_not_bound_when,
            {
                "_netapp_is_shared_tbc": False,
                "_tbc_status": {
                    "resources": [{"status": {"phase": "Bound", "lastOperationStatus": "Success"}}]
                },
            },
        )
        is False
    )

    # Dedicated-SVM management endpoint: prepared management_lif or backend endpoint,
    # and _management_lif whitespace/scheme stripping
    endpoint_fail_when = _find_task(
        validate_tasks, "Fail if Dedicated-SVM backend has neither management_lif nor endpoint"
    )["when"]

    def _ep_vars(conn):
        conn = {"tenant_preparation": {"provider_config": {}}} | conn
        return {
            "_netapp_is_shared_tbc": False,
            "_netapp_backend_id": "b1",
            "_provider_backend_connections": {"b1": conn},
        }

    assert _render_when(endpoint_fail_when, _ep_vars({"endpoint": "https://"})) is True
    assert _render_when(endpoint_fail_when, _ep_vars({})) is True
    assert (
        _render_when(endpoint_fail_when, _ep_vars({"endpoint": " https://10.46.246.90/api "}))
        is False
    )
    assert (
        _render_when(
            endpoint_fail_when,
            _ep_vars(
                {"tenant_preparation": {"provider_config": {"management_lif": "10.46.246.91"}}}
            ),
        )
        is False
    )

    lif_format_when = _find_task(
        validate_tasks, "Fail if Dedicated-SVM management_lif is not a host or host:port"
    )["when"]
    for good_lif in ("10.46.246.91", "svm1-mgmt.example.com:443", "https://[fd00::10]:8443/"):
        assert (
            _render_when(
                lif_format_when,
                _ep_vars({"tenant_preparation": {"provider_config": {"management_lif": good_lif}}}),
            )
            is False
        ), good_lif
    for bad_lif in ("svm mgmt", "host:port", "-lead.example"):
        assert (
            _render_when(
                lif_format_when,
                _ep_vars({"tenant_preparation": {"provider_config": {"management_lif": bad_lif}}}),
            )
            is True
        ), bad_lif

    lif_expr = _find_task(
        block_tasks, "Derive the management LIF from the prepared assignment or backend endpoint"
    )["ansible.builtin.set_fact"]["_management_lif"]
    assert (
        _render(
            lif_expr,
            {
                "_netapp_backend_id": "b1",
                "_provider_backend_connections": {
                    "b1": {
                        "endpoint": " https://10.46.246.90/api ",
                        "tenant_preparation": {"provider_config": {}},
                    }
                },
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
        "_netapp_shared_sc_desired_params",
        "_netapp_stale_dedicated_sc_names",
    ):
        assert fact_name in cleared_facts
    # The runtime password fact is dropped (null), not overwritten with a literal.
    assert cleared_facts["_runtime_password"] is None

    # _setup_backend.yaml rendered tenant config in Shared-TBC and Dedicated-SVM modes
    setup_backend_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "_setup_backend.yaml")
    setup_block = _find_task(
        setup_backend_tasks, "Validate ONTAP backend mode and record per-backend tenant config"
    )
    shared_record_expr = _find_task(
        setup_block["block"], "Record the per-backend tenant config for Shared-TBC mode"
    )["ansible.builtin.set_fact"]["_netapp_tenant_configs"]
    shared_cfg = _render(
        shared_record_expr,
        {
            "_netapp_tenant_configs": {},
            "_netapp_backend_id": "shared-1",
            "_assignment_key": "0000000000000001",
            "_netapp_shared_profile_map": {"p1": {"tbc_namespace": "trident", "tbc_name": "tbc1"}},
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
    )["ansible.builtin.set_fact"]["_netapp_tenant_configs"]
    combined_cfg = _render(
        dedicated_record_expr,
        {
            "_netapp_tenant_configs": shared_cfg,
            "_netapp_backend_id": "ded-1",
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
        "_netapp_shared_profile_map",
        "_netapp_is_shared_tbc",
        "_backend_tiers",
    ):
        assert fact_name in setup_clear_task["ansible.builtin.set_fact"]


def test_netapp_consumes_svm_preparation_without_hub_secrets_or_ontap_http():
    """Verify netapp_storage requires only SVM-scoped credentials in
    tenant_preparation.provider_config, makes no direct HTTP calls to ONTAP from
    the hub cluster, and performs no hub-cluster Secret reads or writes.
    """
    setup_path = NETAPP_ROLE_DIR / "tasks" / "setup.yaml"
    setup_text = setup_path.read_text(encoding="utf-8")
    assert "_provider_backend_connections[item].username" not in setup_text
    assert "_provider_backend_connections[item].password" not in setup_text

    setup_backend_path = NETAPP_ROLE_DIR / "tasks" / "_setup_backend.yaml"
    raw_text = setup_backend_path.read_text(encoding="utf-8")
    assert "tenant_preparation" in raw_text
    assert "_prepared_secret" not in raw_text
    assert "storage-assignment-state" not in raw_text

    defaults_text = (NETAPP_ROLE_DIR / "defaults" / "main.yaml").read_text(encoding="utf-8")
    assert "netapp_storage_config_namespace" not in defaults_text
    assert "netapp_storage_validate_certs:" in defaults_text

    for yaml_file in NETAPP_ROLE_DIR.rglob("*.yaml"):
        content = yaml_file.read_text(encoding="utf-8")
        assert "netapp-tenant-" not in content
        assert "netapp_storage_config_namespace" not in content
        assert "kubeconfig: ~" not in content
        assert "ansible.builtin.uri" not in content
        assert "slurp" not in content


def test_netapp_ensure_short_circuit_checks_tbc_and_csi_secret():
    """Render ensure_storage_class.yaml dedicated/shared partitioning, expected
    selectors, and _netapp_all_sc_exist short-circuit evaluation via Templar, and
    verify no task file emits set_stats.
    """
    ensure_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "ensure_storage_class.yaml")
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
            "tenant_preparation": {"provider_config": {"svm": "svm1", "username": "u", "password": FIXTURE_PASSWORD}},
        },
        "shared-1": {
            "provider_config": {
                "profiles": [{"name": "p1", "shared": {"tbc_namespace": "trident", "tbc_name": "tbc1"}}]
            }
        },
    }
    backend_ids = _render(
        subset_task["ansible.builtin.set_fact"]["_netapp_backend_ids"],
        {"_provider_tiers": tiers},
    )
    dedicated_ids = _render(
        subset_task["ansible.builtin.set_fact"]["_netapp_dedicated_backend_ids"],
        {"_provider_tiers": tiers, "_provider_backend_connections": conns},
    )
    assert backend_ids == ["ded-1", "shared-1"]
    assert dedicated_ids == ["ded-1"]
    # Backend IDs that differ only by case are distinct backends.
    assert _render(
        subset_task["ansible.builtin.set_fact"]["_netapp_backend_ids"],
        {"_provider_tiers": [{"backend_id": "BE-1"}, {"backend_id": "be-1"}]},
    ) == ["BE-1", "be-1"]

    entries_task = _find_task(
        ensure_tasks, "Build expected StorageClass entries and dedicated backend keys"
    )
    entries_vars = {
        "_provider_tiers": tiers,
        "_netapp_dedicated_backend_ids": dedicated_ids,
        "tenant_name": "acme",
    }
    sc_entries = _render(
        entries_task["ansible.builtin.set_fact"]["_netapp_expected_sc_entries"], entries_vars
    )
    ded_keys = _render(
        entries_task["ansible.builtin.set_fact"]["_netapp_dedicated_assignment_keys"], entries_vars
    )

    expected_task = _find_task(
        ensure_tasks,
        "Build expected StorageClass names, selectors, TBC names, and CSI Secret names",
    )
    expected_vars = {
        **entries_vars,
        "_netapp_expected_sc_entries": sc_entries,
        "_netapp_dedicated_assignment_keys": ded_keys,
    }
    sc_names = _render(expected_task["ansible.builtin.set_fact"]["_netapp_expected_sc_names"], expected_vars)
    sc_selectors = _render(
        expected_task["ansible.builtin.set_fact"]["_netapp_expected_sc_selectors"], expected_vars
    )
    tbc_names = _render(
        expected_task["ansible.builtin.set_fact"]["_netapp_expected_tbc_names"], expected_vars
    )
    csi_names = _render(
        expected_task["ansible.builtin.set_fact"]["_netapp_expected_csi_secret_names"], expected_vars
    )
    assert len(sc_names) == 2
    assert len(tbc_names) == 1
    assert len(csi_names) == 1
    assert sc_selectors[sc_names[1]] == ""

    trident_crd_fail_when = _find_task(
        ensure_tasks, "Fail if Trident is not installed on the target cluster"
    )["when"]
    assert (
        _render_when(
            trident_crd_fail_when,
            {"_netapp_dedicated_backend_ids": ["ded-1"], "_trident_crd_check": {"resources": []}},
        )
        is True
    )
    assert (
        _render_when(
            trident_crd_fail_when,
            {
                "_netapp_dedicated_backend_ids": ["ded-1"],
                "_trident_crd_check": {
                    "resources": [{"metadata": {"name": "tridentbackendconfigs.trident.netapp.io"}}]
                },
            },
        )
        is False
    )
    assert (
        _render_when(
            trident_crd_fail_when,
            {"_netapp_dedicated_backend_ids": [], "_trident_crd_check": {"resources": []}},
        )
        is False
    )

    all_exist_expr = _find_task(
        ensure_tasks,
        "Determine if all StorageClasses, Bound TridentBackendConfigs, and CSI Secrets already exist",
    )["ansible.builtin.set_fact"]["_netapp_all_sc_exist"]

    # When Shared-TBC backends are present, _netapp_all_sc_exist must be False so
    # _ensure_storage_class_backend.yaml runs customer TBC validation and storagePools checks.
    expected_tbc_spec = {
        "storageDriverName": "ontap-san",
        "backendName": tbc_names[0],
        "sanType": "fcp",
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
        "_netapp_backend_ids": backend_ids,
        "_netapp_dedicated_backend_ids": dedicated_ids,
        "_netapp_expected_sc_names": sc_names,
        "_netapp_expected_sc_selectors": sc_selectors,
        "_netapp_expected_tbc_names": tbc_names,
        "_netapp_expected_csi_secret_names": csi_names,
        "_netapp_sc_checks": {
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
        "_netapp_tbc_checks": {
            "resources": [
                {
                    "metadata": {"name": tbc_names[0]},
                    "spec": expected_tbc_spec,
                    "status": {"phase": "Bound"},
                }
            ]
        },
        "_netapp_csi_checks": {
            "resources": [
                {
                    "metadata": {"name": csi_names[0]},
                    "data": {
                        "username": base64.b64encode(b"u").decode("ascii"),
                        "password": base64.b64encode(FIXTURE_PASSWORD.encode()).decode("ascii"),
                    },
                }
            ]
        },
    }
    assert _render(all_exist_expr, mixed_exist_vars) is False

    # When only Dedicated-SVM backends are present and all resources exist & TBC is Bound, short-circuit is True.
    dedicated_only_vars = {
        **mixed_exist_vars,
        "_netapp_backend_ids": ["ded-1"],
        "_netapp_expected_sc_names": [sc_names[0]],
    }
    assert _render(all_exist_expr, dedicated_only_vars) is True

    # Prepared management_lif and qos_policies participate in the comparison.
    qos_tiers = [
        {
            "name": "ded-gold",
            "backend_id": "ded-1",
            "qos_limits": {"static_limits": {"max_reads_bw_mbps": 100, "max_writes_bw_mbps": 0}},
        }
    ]
    qos_prep = {
        "svm": "svm1",
        "username": "u",
        "password": FIXTURE_PASSWORD,
        "management_lif": "https://10.0.0.9",
        "qos_policies": {"ded-gold": "pg_gold"},
    }
    qos_tbc_spec = {
        **expected_tbc_spec,
        "managementLIF": "10.0.0.9",
        "storage": [
            {
                "labels": {"osac_backend": tbc_names[0], "tier": "ded-gold"},
                "defaults": {"encryption": "false", "qosPolicy": "pg_gold"},
            }
        ],
    }
    qos_vars = {
        **dedicated_only_vars,
        "_provider_tiers": qos_tiers,
        "_provider_backend_connections": {
            "ded-1": {"endpoint": "10.0.0.1", "tenant_preparation": {"provider_config": qos_prep}}
        },
        "_netapp_tbc_checks": {
            "resources": [
                {"metadata": {"name": tbc_names[0]}, "spec": qos_tbc_spec, "status": {"phase": "Bound"}}
            ]
        },
    }
    assert _render(all_exist_expr, qos_vars) is True
    # The cluster endpoint no longer matches once a prepared SVM LIF is supplied.
    assert (
        _render(
            all_exist_expr,
            {
                **qos_vars,
                "_netapp_tbc_checks": {
                    "resources": [
                        {
                            "metadata": {"name": tbc_names[0]},
                            "spec": {**qos_tbc_spec, "managementLIF": "10.0.0.1"},
                            "status": {"phase": "Bound"},
                        }
                    ]
                },
            },
        )
        is False
    )
    # A QoS-requesting tier without a prepared policy never short-circuits, so
    # the provisioning path fails closed instead of silently dropping the limit.
    assert (
        _render(
            all_exist_expr,
            {
                **qos_vars,
                "_provider_backend_connections": {
                    "ded-1": {
                        "endpoint": "10.0.0.1",
                        "tenant_preparation": {
                            "provider_config": {**qos_prep, "qos_policies": {}}
                        },
                    }
                },
            },
        )
        is False
    )
    # When existing Bound TBC spec drifts (e.g., tier encryption_enabled or svm changes), do not short-circuit.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_netapp_tbc_checks": {
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
    # When CSI Secret username differs or password is empty, or incoming _pcfg.password is empty/whitespace
    # or Dedicated-SVM tier sets Shared-TBC provider_config fields, do not short-circuit.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_netapp_csi_checks": {
                    "resources": [
                        {
                            "metadata": {"name": csi_names[0]},
                            "data": {
                                "username": base64.b64encode(b"stale-user").decode("ascii"),
                                "password": base64.b64encode(FIXTURE_PASSWORD.encode()).decode("ascii"),
                            },
                        }
                    ]
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
                "_provider_backend_connections": {
                    **conns,
                    "ded-1": {
                        "endpoint": "10.0.0.1",
                        "tenant_preparation": {
                            "provider_config": {"svm": "svm1", "username": "u", "password": FIXTURE_WHITESPACE_PASSWORD}
                        },
                    },
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
                "_provider_tiers": [
                    {
                        "name": "ded-gold",
                        "backend_id": "ded-1",
                        "provider_config": {"profile": "p1"},
                    }
                ],
            },
        )
        is False
    )
    # When netapp_storage_validate_certs is True and CA bundle path is missing, do not short-circuit
    # even if existing TBC has no trustedCACertificate.
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "netapp_storage_validate_certs": True,
                "netapp_storage_ca_bundle_path": "/nonexistent/ca-bundle.pem",
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
            {"_netapp_all_sc_exist": True, "_netapp_dedicated_backend_ids": ["ded-1"]},
        )
        is True
    )
    assert (
        _render(
            all_exist_expr,
            {
                **dedicated_only_vars,
                "_netapp_tbc_checks": {
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
                "_netapp_tbc_checks": {
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

    for task_file in (NETAPP_ROLE_DIR / "tasks").glob("*.yaml"):
        parsed_tasks = _load_yaml(task_file)
        for task in parsed_tasks:
            assert "ansible.builtin.set_stats" not in task
            assert "set_stats" not in task
            for section in ("block", "rescue", "always"):
                for sub_task in task.get(section, []):
                    assert "ansible.builtin.set_stats" not in sub_task
                    assert "set_stats" not in sub_task

    # Verify _validate_tiers.yaml and teardown_backend.yaml share strict string backend_id checks
    # and teardown_backend.yaml does not leave _netapp_backend_ids in play scope.
    validate_tiers_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "_validate_tiers.yaml")
    tier_bid_when = _find_task(
        validate_tiers_tasks, "Fail if any dispatched tier is missing backend_id"
    )["when"]
    teardown_backend_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "teardown_backend.yaml")
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
                when_expr, {"_provider_tiers": [{"name": "gold", "backend_id": "netapp-be-1"}]}
            )
            is False
        )
    assert all(
        "_netapp_backend_ids" not in task.get("ansible.builtin.set_fact", {})
        for task in teardown_backend_tasks
    )

    # Verify remaining _validate_tiers.yaml failure guards (tenant_name DNS label, protocol, sanType, CSI namespace).
    tier_tenant_when = _find_task(
        validate_tiers_tasks, "Validate tenant_name is a valid DNS label"
    )["when"]
    for invalid_tenant in ("", "-bad", "bad-", "Bad_Tenant", "a" * 64):
        assert _render_when(tier_tenant_when, {"tenant_name": invalid_tenant}) is True
    for valid_tenant in ("a", "acme-01"):
        assert _render_when(tier_tenant_when, {"tenant_name": valid_tenant}) is False

    tier_proto_when = _find_task(
        validate_tiers_tasks, "Fail if any tier protocol is unsupported by the ONTAP provider"
    )["when"]
    assert (
        _render_when(tier_proto_when, {"_provider_tiers": [{"name": "gold", "protocol": "nfs"}]})
        is True
    )
    assert (
        _render_when(
            tier_proto_when, {"_provider_tiers": [{"name": "gold", "protocol": "block"}]}
        )
        is False
    )

    tier_san_when = _find_task(
        validate_tiers_tasks, "Fail if sanType is not a valid Trident SAN transport"
    )["when"]
    for bad_san in ("", "nfs", "smb"):
        assert _render_when(tier_san_when, {"netapp_storage_san_type": bad_san}) is True
    for valid_san in ("iscsi", "fcp", "nvme"):
        assert _render_when(tier_san_when, {"netapp_storage_san_type": valid_san}) is False

    tier_ns_when = _find_task(
        validate_tiers_tasks, "Fail if the CSI namespace is empty"
    )["when"]
    assert _render_when(tier_ns_when, {"netapp_storage_csi_namespace": ""}) is True
    assert _render_when(tier_ns_when, {}) is True
    assert _render_when(tier_ns_when, {"netapp_storage_csi_namespace": "trident"}) is False

    # Tier names must be complete DNS labels (a trailing newline is rejected).
    tier_name_when = _find_task(
        validate_tiers_tasks, "Fail if any dispatched tier name is not a complete DNS label"
    )["when"]
    for bad_tiers in (
        [{"name": "gold\n"}],
        [{"name": "Gold"}],
        [{"name": "-gold"}],
        [{"name": "a" * 64}],
        [{"name": 7}],
        [{}],
        [{"name": "gold"}, {"name": "silver "}],
    ):
        assert _render_when(tier_name_when, {"_provider_tiers": bad_tiers}) is True, bad_tiers
    assert (
        _render_when(
            tier_name_when, {"_provider_tiers": [{"name": "gold"}, {"name": "tier-2"}]}
        )
        is False
    )

    # Verify teardown_cluster_storage.yaml tenant_name DNS label guard, CRD/TBC/CSI Secret ordering guards, and always cleanup.
    teardown_cluster_tasks = _load_yaml(NETAPP_ROLE_DIR / "tasks" / "teardown_cluster_storage.yaml")
    tenant_dns_when = _find_task(
        teardown_cluster_tasks, "Validate tenant_name is a valid DNS label"
    )["when"]
    for invalid_tenant in ("", "-bad", "bad-", "Bad_Tenant", "a" * 64):
        assert _render_when(tenant_dns_when, {"tenant_name": invalid_tenant}) is True
    assert _render_when(tenant_dns_when, {"tenant_name": "acme-tenant-01"}) is False

    # The dependency guard runs before any deletion and counts PVs/PVCs that
    # belong to the tenant's NetApp storage (and only those).
    guard_names = [t.get("name") for t in teardown_cluster_tasks]
    assert guard_names.index("Verify no volumes still use the tenant's NetApp storage") < (
        guard_names.index("Clean up cluster-side K8s resources")
    )
    guard_block = _find_task(
        teardown_cluster_tasks, "Verify no volumes still use the tenant's NetApp storage"
    )["block"]
    for lookup_name in (
        "Find tenant NetApp StorageClasses for the dependency guard",
        "Find tenant TridentBackendConfigs for the dependency guard",
        "Find PersistentVolumes for the dependency guard",
        "Find PersistentVolumeClaims for the dependency guard",
    ):
        lookup = _find_task(guard_block, lookup_name)
        assert lookup.get("no_log") is True, lookup_name
        # Lookups always run so classes that no longer exist are still guarded.
        assert "when" not in lookup, lookup_name
    dependents_expr = _find_task(
        guard_block, "Count volumes that still use the tenant's NetApp storage"
    )["ansible.builtin.set_fact"]["_netapp_guard_dependents"]
    key = (__import__("hashlib").sha256(b"be-1|acme").hexdigest())[:16]
    removed_tier_sc = f"netapp-{key}-{'0' * 16}"
    guard_vars = {
        "tenant_name": "acme",
        "_provider_tiers": [{"name": "gold", "backend_id": "be-1"}],
        "_netapp_guard_sc_list": {"resources": [{"metadata": {"name": "custom-current-sc"}}]},
        "_netapp_guard_tbc_list": {
            "resources": [{"status": {"backendInfo": {"backendUUID": "uuid-tenant"}}}]
        },
        "_netapp_guard_pv_list": {
            "resources": [
                # Current tenant class.
                {"spec": {"storageClassName": "custom-current-sc"}},
                # Class of a removed tier (no StorageClass object left).
                {"spec": {"storageClassName": removed_tier_sc}},
                # Bound PV on a tenant Trident backend under an unknown class.
                {
                    "spec": {
                        "storageClassName": "gone-sc",
                        "csi": {"volumeAttributes": {"backendUUID": "uuid-tenant"}},
                    }
                },
                # Unrelated volumes.
                {
                    "spec": {
                        "storageClassName": "other-sc",
                        "csi": {"volumeAttributes": {"backendUUID": "uuid-other"}},
                    }
                },
                {"spec": {"storageClassName": "netapp-ffffffffffffffff-0000000000000000"}},
                {"spec": {}},
            ]
        },
        "_netapp_guard_pvc_list": {
            "resources": [
                {"spec": {"storageClassName": "custom-current-sc"}},
                {"spec": {"storageClassName": removed_tier_sc}},
                {"spec": {"storageClassName": "other-sc"}},
                {"spec": {}},
            ]
        },
    }
    assert _render(dependents_expr, guard_vars) == {"pv": 3, "pvc": 2}
    # Case-distinct backend IDs each contribute their own prefix.
    lower_key = (__import__("hashlib").sha256(b"be-1|acme").hexdigest())[:16]
    upper_key = (__import__("hashlib").sha256(b"BE-1|acme").hexdigest())[:16]
    assert _render(
        dependents_expr,
        {
            **guard_vars,
            "_provider_tiers": [{"backend_id": "BE-1"}, {"backend_id": "be-1"}],
            "_netapp_guard_sc_list": {"resources": []},
            "_netapp_guard_tbc_list": {"resources": []},
            "_netapp_guard_pv_list": {"resources": []},
            "_netapp_guard_pvc_list": {
                "resources": [
                    {"spec": {"storageClassName": f"netapp-{upper_key}-{'1' * 16}"}},
                    {"spec": {"storageClassName": f"netapp-{lower_key}-{'2' * 16}"}},
                ]
            },
        },
    ) == {"pv": 0, "pvc": 2}
    # Nothing tenant-owned: no classes, no TBCs, no dispatched tiers.
    assert _render(
        dependents_expr,
        {
            "tenant_name": "acme",
            "_provider_tiers": [],
            "_netapp_guard_sc_list": {"resources": []},
            "_netapp_guard_tbc_list": {"resources": []},
            "_netapp_guard_pv_list": {"resources": [{"spec": {"storageClassName": "x"}}]},
            "_netapp_guard_pvc_list": {"resources": []},
        },
    ) == {"pv": 0, "pvc": 0}
    guard_fail_when = _find_task(
        guard_block, "Fail while volumes still use the tenant's NetApp storage"
    )["when"]
    assert _render_when(guard_fail_when, {"_netapp_guard_dependents": {"pv": 0, "pvc": 1}}) is True
    assert _render_when(guard_fail_when, {"_netapp_guard_dependents": {"pv": 1, "pvc": 0}}) is True
    assert _render_when(guard_fail_when, {"_netapp_guard_dependents": {"pv": 0, "pvc": 0}}) is False

    cleanup_block = _find_task(teardown_cluster_tasks, "Clean up cluster-side K8s resources")
    cleanup_tasks = cleanup_block["block"]
    for tbc_crd_task_name in (
        "Find all tenant ONTAP TridentBackendConfigs by label",
        "Delete tenant ONTAP TridentBackendConfigs",
    ):
        crd_when = _find_task(cleanup_tasks, tbc_crd_task_name)["when"]
        assert _render_when(crd_when, {"_netapp_cleanup_trident_crd": {"resources": []}}) is False
        assert (
            _render_when(
                crd_when,
                {"_netapp_cleanup_trident_crd": {"resources": [{"metadata": {"name": "crd"}}]}},
            )
            is True
        )

    wait_remove_task = _find_task(cleanup_tasks, "Wait for the TridentBackendConfigs to be removed")
    assert (
        _render_when(wait_remove_task["when"], {"_netapp_cleanup_tbc_list": {"resources": []}})
        is False
    )
    assert (
        _render_when(
            wait_remove_task["when"],
            {"_netapp_cleanup_tbc_list": {"resources": [{"metadata": {"name": "netapp-1"}}]}},
        )
        is True
    )
    assert _render_when(wait_remove_task["until"], {"_tbc_check": {"resources": []}}) is True
    assert (
        _render_when(
            wait_remove_task["until"],
            {"_tbc_check": {"resources": [{"metadata": {"name": "netapp-1"}}]}},
        )
        is False
    )

    for csi_cleanup_task_name in (
        "Find all tenant ONTAP CSI Secrets by label",
        "Delete tenant ONTAP CSI Secrets after TBCs are confirmed gone",
    ):
        csi_when = _find_task(cleanup_tasks, csi_cleanup_task_name)["when"]
        assert _render_when(csi_when, {"_netapp_cleanup_tbc_list": {}}) is True
        assert (
            _render_when(
                csi_when,
                {
                    "_netapp_cleanup_tbc_list": {"resources": [{"metadata": {"name": "netapp-1"}}]},
                    "_tbc_check": {"failed": False, "resources": []},
                },
            )
            is True
        )
        assert (
            _render_when(
                csi_when,
                {
                    "_netapp_cleanup_tbc_list": {"resources": [{"metadata": {"name": "netapp-1"}}]},
                    "_tbc_check": {
                        "failed": True,
                        "resources": [{"metadata": {"name": "netapp-1"}}],
                    },
                },
            )
            is False
        )

    cleanup_clear_task = _find_task(cleanup_block["always"], "Clear credential-bearing facts")
    assert cleanup_clear_task.get("no_log") is True
    for fact_name in (
        "_netapp_cleanup_sc_list",
        "_netapp_cleanup_trident_crd",
        "_netapp_cleanup_tbc_list",
        "_netapp_cleanup_csi_list",
        "_tbc_check",
    ):
        assert fact_name in cleanup_clear_task["ansible.builtin.set_fact"]
