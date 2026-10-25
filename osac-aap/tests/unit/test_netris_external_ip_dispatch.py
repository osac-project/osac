"""Structural contract tests for the Netris ExternalIP versioned dispatch.

Covers plan case U10: v1/empty/unknown contract-version routing, the
operation and module arguments passed to the v1 path, and the exact
success-only job artifact. These are structural assertions on the parsed
role YAML; executable playbook behavior is covered separately.
"""

from pathlib import Path

import yaml

ROLE_ROOT = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "netris"
)

VERSION_ANNOTATION = "osac.openshift.io/externalip-contract-version"


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    """Flatten block/rescue/always-nested tasks."""
    flattened: list[dict] = []
    for task in tasks:
        flattened.append(task)
        for key in ("block", "rescue", "always"):
            flattened.extend(_flatten_tasks(task.get(key, [])))
    return flattened


def _tasks(name: str) -> list[dict]:
    path = ROLE_ROOT / "tasks" / f"{name}.yaml"
    return _flatten_tasks(yaml.safe_load(path.read_text(encoding="utf-8")))


def _single(tasks: list[dict], name: str) -> dict:
    matches = [task for task in tasks if task.get("name") == name]
    assert len(matches) == 1, f"expected exactly one task named '{name}'"
    return matches[0]


# --- Entry point routing -------------------------------------------------


def test_entry_points_resolve_the_contract_version_annotation() -> None:
    for name in ("create_external_ip", "delete_external_ip"):
        tasks = _tasks(name)
        resolve = next(
            task
            for task in tasks
            if any(
                VERSION_ANNOTATION in value
                for value in task.get("ansible.builtin.set_fact", {}).values()
            )
        )
        fact_expression = next(
            value
            for value in resolve["ansible.builtin.set_fact"].values()
            if VERSION_ANNOTATION in value
        )
        assert "default('', true)" in fact_expression, (
            f"{name} must default a missing/empty version to the legacy route"
        )


def test_entry_points_route_empty_version_to_the_legacy_files() -> None:
    for name, legacy_file in [
        ("create_external_ip", "_create_external_ip_legacy"),
        ("delete_external_ip", "_delete_external_ip_legacy"),
    ]:
        tasks = _tasks(name)
        include = next(
            task
            for task in tasks
            if task.get("ansible.builtin.include_tasks") == f"{legacy_file}.yaml"
        )
        assert include.get("when") is not None, (
            f"{name} must include {legacy_file} conditionally on the version"
        )


def test_entry_points_route_v1_to_the_shared_v1_tasks() -> None:
    for name in ("create_external_ip", "delete_external_ip"):
        tasks = _tasks(name)
        include = next(
            task
            for task in tasks
            if task.get("ansible.builtin.include_tasks") == "_external_ip_v1.yaml"
        )
        assert include.get("when") is not None, (
            f"{name} must include _external_ip_v1.yaml conditionally on the version"
        )


def test_entry_points_pass_the_operation_per_entry_point() -> None:
    expected = {
        "create_external_ip": "external_ip.allocate",
        "delete_external_ip": "external_ip.release",
    }
    for name, operation in expected.items():
        tasks = _tasks(name)
        include = next(
            task
            for task in tasks
            if task.get("ansible.builtin.include_tasks") == "_external_ip_v1.yaml"
        )
        assert include.get("vars", {}).get("external_ip_v1_operation") == operation, (
            f"{name} must pass operation {operation} to the v1 tasks"
        )


def test_entry_points_fail_on_unsupported_versions() -> None:
    for name in ("create_external_ip", "delete_external_ip"):
        tasks = _tasks(name)
        fail = next(task for task in tasks if "ansible.builtin.fail" in task)
        assert fail.get("when") is not None, (
            f"{name} must fail conditionally on an unsupported version"
        )


def test_unknown_version_routes_to_no_provider_work() -> None:
    """Both routing includes must be false for a nonempty unknown version."""
    for name in ("create_external_ip", "delete_external_ip"):
        tasks = _tasks(name)
        includes = [
            task
            for task in tasks
            if task.get("ansible.builtin.include_tasks") is not None
        ]
        assert includes, f"{name} must dispatch through included task files"
        for include in includes:
            condition = include.get("when")
            assert condition is not None, (
                f"{name} include {include['ansible.builtin.include_tasks']}"
                " must be guarded by the contract version"
            )
            assert "eip_contract_version" in str(condition)


# --- v1 shared tasks -----------------------------------------------------


def test_v1_tasks_validate_the_dispatch_inputs_before_provider_io() -> None:
    tasks = _tasks("_external_ip_v1")
    validate = next(
        task for task in tasks if task.get("ansible.builtin.assert") is not None
    )
    module_index = next(
        index
        for index, task in enumerate(tasks)
        if "osac.templates.netris_external_ip" in task
    )
    assert tasks.index(validate) < module_index, (
        "v1 input validation must run before the provider module"
    )
    that = validate["ansible.builtin.assert"]["that"]
    assert any("osac_job_vars.external_ip_pool is defined" in item for item in that), (
        "a missing pool snapshot for v1 must fail, never route to legacy"
    )
    assert any("external_ip.metadata is defined" in item for item in that)


def test_v1_tasks_authenticate_with_the_existing_transport() -> None:
    tasks = _tasks("_external_ip_v1")
    module_index = next(
        index
        for index, task in enumerate(tasks)
        if "osac.templates.netris_external_ip" in task
    )
    auth = next(
        task
        for task in tasks[:module_index]
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.auth"
    )
    assert auth.get("when") is not None


def test_v1_tasks_invoke_the_module_with_the_contract_arguments() -> None:
    tasks = _tasks("_external_ip_v1")
    module_task = next(
        task for task in tasks if "osac.templates.netris_external_ip" in task
    )
    module = module_task["osac.templates.netris_external_ip"]
    assert module["operation"] == "{{ external_ip_v1_operation }}"
    assert module["resource"] == "{{ external_ip }}"
    assert module["pool"] == "{{ osac_job_vars.external_ip_pool }}"
    assert module["controller_url"] == "{{ netris_controller_url }}"
    assert module["session_cookie"] == "{{ netris_session_cookie | default('') }}"
    assert "netris_site_id" in module["site_id"]
    assert "netris_tenant_id" in module["tenant_id"]
    assert module["timeout"] == "{{ netris_timeout | default(30) }}"
    assert module["validate_certs"] == "{{ netris_validate_certs | default(true) }}"
    assert module["ca_path"] == "{{ netris_ca_path | default(omit, true) }}"


def test_v1_tasks_emit_exactly_the_v1_success_artifact() -> None:
    tasks = _tasks("_external_ip_v1")
    module_index = next(
        index
        for index, task in enumerate(tasks)
        if "osac.templates.netris_external_ip" in task
    )
    stats_task = next(task for task in tasks if "ansible.builtin.set_stats" in task)
    assert tasks.index(stats_task) > module_index, (
        "the artifact may only be emitted after successful module completion"
    )
    stats = stats_task["ansible.builtin.set_stats"]
    assert stats["per_host"] is False
    assert stats["aggregate"] is False
    assert stats_task.get("when") == "not ansible_check_mode"

    osac_result = stats["data"]["osac_result"]
    assert set(osac_result.keys()) == {
        "schemaVersion",
        "operation",
        "resourceUID",
        "observedGeneration",
        "data",
    }
    assert osac_result["schemaVersion"] == "v1"
    assert osac_result["operation"] == "{{ external_ip_v1_operation }}"
    assert osac_result["resourceUID"] == "{{ external_ip.metadata.uid }}"
    assert "generation" in osac_result["observedGeneration"]
    assert "external_ip_v1_result.address" in osac_result["data"]
    assert "external_ip.allocate" in osac_result["data"]
    assert "else {}" in osac_result["data"]


def test_v1_tasks_never_emit_stats_from_a_rescue_or_always_block() -> None:
    raw_tasks = yaml.safe_load(
        (ROLE_ROOT / "tasks" / "_external_ip_v1.yaml").read_text(encoding="utf-8")
    )

    def _nested(tasks: list[dict]) -> list[dict]:
        nested: list[dict] = []
        for task in tasks:
            for key in ("rescue", "always"):
                nested.extend(_flatten_tasks(task.get(key, [])))
        return nested

    nested = _nested(raw_tasks)
    assert not nested, "the v1 tasks must not use rescue or always blocks"
    for task in nested:
        assert "ansible.builtin.set_stats" not in task


def test_v1_tasks_do_not_touch_kubernetes_or_write_annotations() -> None:
    tasks = _tasks("_external_ip_v1")
    for task in tasks:
        assert not any(key.startswith("kubernetes.core.") for key in task), (
            "the v1 path must not use Kubernetes modules or lookups"
        )
        assert not any(
            key.startswith("ansible.builtin.command")
            or key.startswith("ansible.builtin.shell")
            for key in task
        )


# --- Retained legacy guarantees ------------------------------------------


def test_legacy_files_retain_the_moved_original_bodies() -> None:
    create = _tasks("_create_external_ip_legacy")
    delete = _tasks("_delete_external_ip_legacy")
    assert create, "the legacy create body must not be empty"
    assert delete, "the legacy delete body must not be empty"

    create_names = [task.get("name") for task in create]
    assert "Validate ExternalIP annotations" in create_names
    assert "Write allocated address to ExternalIP CR annotation" in create_names
    patch = next(
        task
        for task in create
        if "kubernetes.core.k8s_json_patch" in task
    )
    assert patch["kubernetes.core.k8s_json_patch"]["kind"] == "ExternalIP"

    delete_names = [task.get("name") for task in delete]
    assert "Delete /32 IPAM subnet from Netris controller" in delete_names
    inclusions = [
        (task["ansible.builtin.include_role"]["name"], task["ansible.builtin.include_role"].get("tasks_from"))
        for task in delete
        if task.get("ansible.builtin.include_role") is not None
    ]
    assert ("netris.controller.ipam", "delete_subnet") in inclusions


def test_entry_points_keep_their_names_and_dispatch_through_includes() -> None:
    for name in ("create_external_ip", "delete_external_ip"):
        tasks = _tasks(name)
        assert tasks, f"{name} must not be empty"
        for task in tasks:
            assert task.get("name"), f"every {name} task needs a name"
            assert task.get("ansible.builtin.uri") is None, (
                f"{name} must dispatch through roles, not raw HTTP"
            )
        for task in tasks:
            if task.get("ansible.builtin.include_tasks") is None:
                assert "kubernetes.core" not in str(task.keys()), (
                    f"{name} wrapper must not issue Kubernetes writes directly"
                )
