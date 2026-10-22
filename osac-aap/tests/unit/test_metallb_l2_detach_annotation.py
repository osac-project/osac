from pathlib import Path

import yaml

ROLE_ROOT = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "metallb_l2"
)


def test_detach_removes_compute_instance_annotation_with_idempotent_merge_patch() -> None:
    patch_tasks = yaml.safe_load(
        (ROLE_ROOT / "tasks" / "patch_external_ip_annotation.yaml").read_text(encoding="utf-8")
    )

    removal = next(task for task in patch_tasks if task.get("name") == "Remove ExternalIP address annotation")
    patch = removal["kubernetes.core.k8s"]

    assert patch["state"] == "patched"
    assert patch["merge_type"] == ["merge"]
    assert patch["definition"]["apiVersion"] == "osac.openshift.io/v1alpha1"
    assert patch["definition"]["kind"] == "ComputeInstance"
    assert patch["definition"]["metadata"]["name"] == "{{ detach_compute_instance_name }}"
    assert patch["definition"]["metadata"]["annotations"]["osac.openshift.io/external-ip-address"] is None
    assert removal["when"] == "ci_info.resources | length > 0"
    assert not any("kubernetes.core.k8s_json_patch" in task for task in patch_tasks)

    tasks = yaml.safe_load(
        (ROLE_ROOT / "tasks" / "remove_external_ip_annotation.yaml").read_text(encoding="utf-8")
    )
    assert any(
        task.get("ansible.builtin.include_tasks") == "patch_external_ip_annotation.yaml"
        for task in tasks
    )

    detach_tasks = yaml.safe_load(
        (ROLE_ROOT / "tasks" / "detach_external_ip.yaml").read_text(encoding="utf-8")
    )
    assert any(
        task.get("ansible.builtin.include_tasks") == "remove_external_ip_annotation.yaml"
        for task in detach_tasks
    )

    integration_tasks_path = (
        Path(__file__).resolve().parents[1]
        / "integration"
        / "targets"
        / "metallb_l2_detach_annotation"
        / "tasks"
        / "run_annotation_cleanup.yml"
    )
    integration_tasks = yaml.safe_load(integration_tasks_path.read_text(encoding="utf-8"))[0]["tasks"]
    cleanup = next(
        task
        for task in integration_tasks
        if task.get("name") == "Run the same idempotent annotation cleanup task as detach"
    )
    include = cleanup["ansible.builtin.include_tasks"]
    assert include["file"].endswith("patch_external_ip_annotation.yaml")
    assert cleanup["vars"]["ci_info"] == "{{ stale_compute_instance }}"
