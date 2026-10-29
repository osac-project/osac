import os
import shutil
import subprocess
from pathlib import Path

import yaml
from jinja2 import Environment


def _role_path() -> Path:
    return (
        Path(__file__).resolve().parents[2]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "templates"
        / "roles"
        / "cudn_evpn"
    )


def _raw_delete_tasks() -> list[dict]:
    path = _role_path() / "tasks" / "delete_subnet.yaml"
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    flattened = []
    for task in tasks:
        flattened.append(task)
        flattened.extend(_flatten_tasks(task.get("block", [])))
        flattened.extend(_flatten_tasks(task.get("always", [])))
    return flattened


def _delete_tasks() -> list[dict]:
    return _flatten_tasks(_raw_delete_tasks())


def _flatten_values(values):
    for value in values:
        if isinstance(value, (list, tuple)):
            yield from _flatten_values(value)
        else:
            yield value


def _compile_expression(expression: str):
    environment = Environment()
    environment.filters["flatten"] = lambda values: list(_flatten_values(values))
    return environment.compile_expression(expression.removeprefix("{{").removesuffix("}}").strip())


def test_cudn_is_deleted_and_fully_absent_before_namespace_cleanup() -> None:
    tasks = _delete_tasks()
    task_names = [task.get("name") for task in tasks]
    find_siblings_index = task_names.index("Find sibling EVPN Subnets")
    delete_cudn_index = task_names.index("Delete ClusterUserDefinedNetwork")
    wait_for_cudn_index = task_names.index("Wait for CUDN deletion")
    delete_namespace_index = task_names.index("Delete Subnet namespace")

    assert find_siblings_index < delete_cudn_index < wait_for_cudn_index < delete_namespace_index

    sibling_task = tasks[find_siblings_index]
    assert sibling_task["ansible.builtin.set_fact"]["cudn_evpn_sibling_subnets"]
    lookup_index = task_names.index("List Subnets in parent VirtualNetwork")
    lookup = tasks[lookup_index]["kubernetes.core.k8s_info"]
    assert lookup["api_version"] == "osac.openshift.io/v1alpha1"
    assert lookup["kind"] == "Subnet"
    assert lookup["namespace"] == "{{ subnet.metadata.namespace }}"

    delete_cudn = tasks[delete_cudn_index]["kubernetes.core.k8s"]
    assert delete_cudn["state"] == "absent"
    assert delete_cudn["kind"] == "ClusterUserDefinedNetwork"
    assert delete_cudn["name"] == "{{ vnet_name }}"
    assert tasks[delete_cudn_index]["when"] == "_cudn_owner_subnet_name == _deleting_subnet_name"

    wait_task = tasks[wait_for_cudn_index]
    assert wait_task["kubernetes.core.k8s_info"]["name"] == "{{ vnet_name }}"
    assert wait_task["retries"] == 30
    assert wait_task["delay"] == 10
    assert "cudn_status.resources" in wait_task["until"]
    assert "length == 0" in wait_task["until"]
    assert wait_task["when"] == "_cudn_owner_subnet_name == _deleting_subnet_name"

    delete_namespace = tasks[delete_namespace_index]["kubernetes.core.k8s"]
    assert delete_namespace["state"] == "absent"
    assert delete_namespace["kind"] == "Namespace"
    assert delete_namespace["name"] == "{{ _deleting_subnet_name }}"
    assert delete_namespace["wait"] is True

    clear_owner = next(
        task for task in tasks if task.get("name") == "Record completed owner network teardown without removing the deleting CR fence"
    )
    assert task_names.index("Delete Subnet namespace") < task_names.index(
        "Record completed owner network teardown without removing the deleting CR fence"
    )
    clear_definition = clear_owner["kubernetes.core.k8s"]
    assert clear_definition["state"] == "patched"
    metadata = _compile_expression(clear_definition["definition"]["metadata"])(
        vnet_lock_name="shared", vnet_lock_namespace="management", _cudn_owner_subnet_name="owner",
        _cudn_owner_lease={"resources": [{"metadata": {"resourceVersion": "7"}}]},
    )
    assert metadata["resourceVersion"] == "7"
    assert metadata["annotations"] == {
        "osac.openshift.io/cudn-owner-subnet": "owner",
        "osac.openshift.io/cudn-owner-cleaned": "true",
    }
    assert clear_owner["when"] == "_cudn_owner_subnet_name == _deleting_subnet_name"


def test_delete_validates_cudn_owner_before_mutating_shared_state() -> None:
    tasks = _delete_tasks()
    task_names = [task.get("name") for task in tasks]
    read_index = task_names.index("Read shared CUDN before owner teardown")
    validate_index = task_names.index("Validate existing CUDN belongs to the parent VirtualNetwork")
    resolve_index = task_names.index("Resolve persisted CUDN owner before deletion")
    delete_index = task_names.index("Delete ClusterUserDefinedNetwork")
    assert read_index < validate_index < resolve_index < delete_index

    expression = tasks[validate_index]["ansible.builtin.assert"]["that"][0]
    validate_owner = Environment().compile_expression(expression)
    args = {
        "vnet_name": "tenant-vnet",
        "tenant_id": "tenant-a",
        "vnet_uuid": "vnet-uuid-a",
        "subnet": {"metadata": {"namespace": "tenant-a-ns"}},
    }
    identity = {
        "osac.openshift.io/owner-reference": "VirtualNetwork/tenant-vnet",
        "osac.openshift.io/tenant": "tenant-a",
        "osac.openshift.io/virtualnetwork-uuid": "vnet-uuid-a",
        "osac.openshift.io/virtualnetwork-namespace": "tenant-a-ns",
    }
    assert validate_owner(_existing_cudn={"resources": []}, **args) is True
    assert validate_owner(_existing_cudn={"resources": [{"metadata": {"annotations": identity}}]}, **args) is True
    legacy_identity = {
        "osac.openshift.io/owner-reference": "VirtualNetwork/tenant-vnet",
        "osac.openshift.io/tenant": "tenant-a",
        "osac.openshift.io/cudn-owner-subnet": "old-owner",
    }
    assert (
        validate_owner(_existing_cudn={"resources": [{"metadata": {"annotations": legacy_identity}}]}, **args) is True
    )
    for key, value in (
        ("osac.openshift.io/owner-reference", "VirtualNetwork/other-vnet"),
        ("osac.openshift.io/tenant", "tenant-b"),
        ("osac.openshift.io/virtualnetwork-uuid", "vnet-uuid-b"),
        ("osac.openshift.io/virtualnetwork-namespace", "tenant-b-ns"),
    ):
        conflicting = {**identity, key: value}
        assert (
            validate_owner(_existing_cudn={"resources": [{"metadata": {"annotations": conflicting}}]}, **args) is False
        )
    assert validate_owner(_existing_cudn={"resources": [{"metadata": {"annotations": {}}}]}, **args) is False
    assert (
        validate_owner(
            _existing_cudn={
                "resources": [
                    {"metadata": {"annotations": {**legacy_identity, "osac.openshift.io/tenant": "tenant-b"}}}
                ]
            },
            **args,
        )
        is False
    )


def test_shared_cudn_decision_is_serialized_and_releases_the_lock() -> None:
    tasks = _raw_delete_tasks()
    task_names = [task.get("name") for task in tasks]
    assert task_names.index("Ensure shared CUDN lock Lease exists") < task_names.index(
        "Acquire shared CUDN teardown Lease"
    )
    assert task_names.index("Acquire shared CUDN teardown Lease") < task_names.index(
        "Clean up Subnet resources while holding the VirtualNetwork lock"
    )

    cleanup = tasks[task_names.index("Clean up Subnet resources while holding the VirtualNetwork lock")]
    inner_names = [task.get("name") for task in _flatten_tasks(cleanup.get("block", []))]
    assert inner_names.index("List Subnets in parent VirtualNetwork") < inner_names.index(
        "Delete ClusterUserDefinedNetwork"
    )
    assert inner_names.index("Delete ClusterUserDefinedNetwork") < inner_names.index("Delete Subnet namespace")
    assert cleanup["always"][0]["ansible.builtin.include_role"]["tasks_from"] == "release"


def test_cudn_absence_poll_succeeds_only_after_resource_disappears() -> None:
    wait_task = next(task for task in _delete_tasks() if task.get("name") == "Wait for CUDN deletion")
    condition = Environment().compile_expression(wait_task["until"])

    assert condition(cudn_status={"resources": []}) is True
    assert condition(cudn_status={"resources": [{"metadata": {"name": "vnet-7"}}]}) is False


def test_sibling_lookup_excludes_current_subnet_and_keeps_same_parent_only() -> None:
    tasks = _delete_tasks()
    sibling_task = next(task for task in tasks if task.get("name") == "Find sibling EVPN Subnets")
    template = sibling_task["ansible.builtin.set_fact"]["cudn_evpn_sibling_subnets"]
    expression = template.removeprefix("{{").removesuffix("}}").strip()
    select_siblings = Environment().compile_expression(expression)

    resources = [
        {"metadata": {"name": "current"}, "spec": {"virtualNetwork": "vnet-uuid"}},
        {"metadata": {"name": "sibling"}, "spec": {"virtualNetwork": "vnet-uuid"}},
        {"metadata": {"name": "other-vnet"}, "spec": {"virtualNetwork": "other-uuid"}},
    ]
    result = select_siblings(_subnet_lookup={"resources": resources}, vnet_uuid="vnet-uuid", subnet_name="current")

    assert [item["metadata"]["name"] for item in result] == ["sibling"]


def test_bare_fabric_only_sibling_is_not_a_live_cudn_consumer() -> None:
    tasks = _delete_tasks()
    names = [task.get("name") for task in tasks]
    initialize = next(task for task in tasks if task.get("name") == "Initialize sibling CUDN consumers")
    initialize_expression = initialize["ansible.builtin.set_fact"]["cudn_evpn_live_siblings"]
    assert initialize_expression == []

    live_siblings = list(initialize_expression)
    assert live_siblings == []

    extract_task = next(
        task for task in tasks if task.get("name") == "Extract Subnet references from ComputeInstances"
    )
    extract_expression = _compile_expression(
        extract_task["ansible.builtin.set_fact"]["_compute_instance_subnet_refs"]
    )
    subnet_refs = extract_expression(_compute_instance_lookup={"resources": []})
    preserve_task = next(
        task for task in tasks if task.get("name") == "Preserve sibling Subnets referenced by ComputeInstances"
    )
    append_expression = _compile_expression(
        preserve_task["ansible.builtin.set_fact"]["cudn_evpn_live_siblings"]
    )
    bare_fabric_only = {"metadata": {"name": "fabric-only-successor"}}
    live_siblings = append_expression(
        cudn_evpn_live_siblings=live_siblings,
        cudn_evpn_sibling_subnets=[bare_fabric_only],
        _compute_instance_subnet_refs=subnet_refs,
    )
    assert live_siblings == []

    preserve_namespace = next(
        task for task in tasks if task.get("name") == "Preserve CUDN for Subnets with workload namespaces"
    )
    assert preserve_namespace["when"] == "item.resources | default([]) | length > 0"
    assert "Add active siblings that allow Kubernetes provisioning as CUDN consumers" not in {
        task.get("name") for task in tasks
    }

    refresh_task = next(
        task for task in tasks if task.get("name") == "Determine whether the shared CUDN has live Subnet consumers"
    )
    is_live = Environment().compile_expression(
        refresh_task["ansible.builtin.set_fact"]["cudn_evpn_has_live_siblings"]
        .removeprefix("{{")
        .removesuffix("}}")
        .strip()
    )
    assert is_live(cudn_evpn_live_sibling_names=live_siblings) is False
    assert is_live(cudn_evpn_live_sibling_names=[bare_fabric_only]) is True

    namespace_lookup = next(
        task for task in tasks if task.get("name") == "Check for workload namespaces belonging to sibling Subnets"
    )
    lookup = namespace_lookup["kubernetes.core.k8s_info"]
    assert lookup["kind"] == "Namespace"
    assert lookup["name"] == "{{ item.metadata.name }}"
    preserve_task = next(
        task for task in tasks if task.get("name") == "Preserve sibling Subnets referenced by ComputeInstances"
    )
    expression = preserve_task["ansible.builtin.set_fact"]["cudn_evpn_live_siblings"]
    select_referenced = Environment().compile_expression(expression.removeprefix("{{").removesuffix("}}").strip())
    siblings = [
        {"metadata": {"name": "active"}},
        {"metadata": {"name": "deleting", "deletionTimestamp": "2026-10-07T00:00:00Z"}},
        {"metadata": {"name": "unused"}},
    ]
    referenced = select_referenced(
        cudn_evpn_live_siblings=[siblings[0], siblings[2]],
        cudn_evpn_sibling_subnets=siblings,
        _compute_instance_subnet_refs=["deleting"],
    )
    assert [item["metadata"]["name"] for item in referenced] == ["active", "unused", "deleting"]
    assert names.index("Check for workload namespaces belonging to sibling Subnets") < names.index(
        "Delete ClusterUserDefinedNetwork"
    )


def test_compute_instance_references_are_considered_for_any_sibling_state() -> None:
    tasks = _delete_tasks()
    lookup = next(task for task in tasks if task.get("name") == "List ComputeInstances in the management namespace")
    assert lookup["kubernetes.core.k8s_info"]["api_version"] == "osac.openshift.io/v1alpha1"
    assert lookup["kubernetes.core.k8s_info"]["kind"] == "ComputeInstance"

    extract = next(
        task for task in tasks if task.get("name") == "Extract Subnet references from ComputeInstances"
    )
    preserve = next(
        task for task in tasks if task.get("name") == "Preserve sibling Subnets referenced by ComputeInstances"
    )
    names = [task.get("name") for task in tasks]
    assert names.index("List ComputeInstances in the management namespace") < names.index(
        "Extract Subnet references from ComputeInstances"
    ) < names.index("Preserve sibling Subnets referenced by ComputeInstances")

    extract_expression = _compile_expression(
        extract["ansible.builtin.set_fact"]["_compute_instance_subnet_refs"]
    )
    refs = extract_expression(
        _compute_instance_lookup={
            "resources": [
                {"spec": {"networkAttachments": [{"subnetRef": "active"}, {"subnetRef": "deleting"}]}},
                {"spec": {"networkAttachments": [{"subnetRef": "active"}]}},
            ]
        }
    )
    assert refs == ["active", "deleting", "active"]

    preserve_expression = _compile_expression(
        preserve["ansible.builtin.set_fact"]["cudn_evpn_live_siblings"]
    )
    siblings = [{"metadata": {"name": "active"}}, {"metadata": {"name": "deleting"}}]
    live_siblings = preserve_expression(
        cudn_evpn_live_siblings=[], cudn_evpn_sibling_subnets=siblings, _compute_instance_subnet_refs=refs
    )
    assert live_siblings == siblings


def test_resource_deletes_are_idempotent() -> None:
    tasks = _delete_tasks()
    delete_tasks = [
        task for task in tasks if task.get("name") in {"Delete ClusterUserDefinedNetwork", "Delete Subnet namespace"}
    ]

    assert len(delete_tasks) == 2
    assert all(task["kubernetes.core.k8s"]["state"] == "absent" for task in delete_tasks)


def test_delete_role_leaves_vm_lifecycle_to_subnet_controller() -> None:
    tasks = _delete_tasks()

    assert not any(
        task.get("kubernetes.core.k8s", {}).get("kind")
        in {"VirtualMachine", "ComputeInstance", "VirtualMachineInstance"}
        for task in tasks
    )
    assert not any(task.get("name") == "List VMs in deleting sibling Subnet namespaces" for task in tasks)










def test_delete_entrypoint_is_declared_for_manager_dispatch() -> None:
    argspec_path = _role_path() / "meta" / "argument_specs.yaml"
    argspec = yaml.safe_load(argspec_path.read_text(encoding="utf-8"))["argument_specs"]["delete_subnet"]["options"]

    assert argspec["subnet"]["required"] is True
    assert argspec["subnet_name"]["required"] is True
