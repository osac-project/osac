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

# {resource: (create playbook, delete playbook)}
LIFECYCLE_PLAYBOOKS = {
    "VirtualNetwork": ("create_virtual_network", "delete_virtual_network"),
    "Subnet": ("create_subnet", "delete_subnet"),
    "SecurityGroup": ("create_security_group", "delete_security_group"),
    "ExternalIP": ("create_external_ip", "delete_external_ip"),
    "ExternalIPAttachment": ("attach_external_ip", "detach_external_ip"),
    "NATGateway": ("create_nat_gateway", "delete_nat_gateway"),
}


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    """Flatten block-nested tasks so guardrails cover tasks inside blocks."""
    flattened: list[dict] = []
    for task in tasks:
        flattened.append(task)
        flattened.extend(_flatten_tasks(task.get("block", [])))
    return flattened


def _lifecycle_tasks(name: str) -> list[dict]:
    return _flatten_tasks(
        yaml.safe_load((ROLE_ROOT / "tasks" / f"{name}.yaml").read_text(encoding="utf-8"))
    )


def _included_write_tasks(name: str) -> list[tuple[str, str]]:
    """Return (target role, tasks_from) pairs for every provider task inclusion."""
    inclusions: list[tuple[str, str]] = []
    for task in _lifecycle_tasks(name):
        role = task.get("ansible.builtin.include_role")
        if role is None:
            continue
        inclusions.append((role.get("name", ""), role.get("tasks_from", "")))
    return inclusions


def test_every_shared_resource_has_create_and_delete_playbooks() -> None:
    for resource, (create_file, delete_file) in LIFECYCLE_PLAYBOOKS.items():
        create_tasks = _lifecycle_tasks(create_file)
        delete_tasks = _lifecycle_tasks(delete_file)
        assert create_tasks, f"{resource} create playbook must not be empty"
        assert delete_tasks, f"{resource} delete playbook must not be empty"
        for tasks, stage in [(create_tasks, "create"), (delete_tasks, "delete")]:
            for task in tasks:
                assert task.get("ansible.builtin.uri") is None, (
                    f"{resource} {stage} playbook must dispatch through roles, not raw HTTP"
                )


def test_lifecycle_playbooks_issue_create_delete_only_provider_writes() -> None:
    """Provider writes are create/delete (or allocate/release) task inclusions.

    The one documented exception is create_subnet's EVPN gateway contract
    reconciliation: an internal idempotency path that repairs an existing
    V-Net gateway when it does not match the subnet's fabric pool.
    """
    documented_internal_update = ("netris.controller.vnet", "update")
    for create_file, delete_file in LIFECYCLE_PLAYBOOKS.values():
        for name in (create_file, delete_file):
            for target, tasks_from in _included_write_tasks(name):
                if (target, tasks_from) == documented_internal_update:
                    assert name == "create_subnet", (
                        "the EVPN gateway reconciliation belongs only in create_subnet"
                    )
                    continue
                assert "update" not in tasks_from, (
                    f"{name} must not issue update-like provider writes (found {target}/{tasks_from})"
                )


def test_attachment_and_natgateway_playbooks_resolve_allocated_address_from_the_external_ip_cr() -> None:
    for create_file in ["attach_external_ip", "create_nat_gateway"]:
        tasks = _lifecycle_tasks(create_file)
        lookup = next(
            task
            for task in tasks
            if task.get("name") == "Look up ExternalIP CR to get allocated address"
        )
        k8s_info = lookup["kubernetes.core.k8s_info"]
        assert k8s_info["kind"] == "ExternalIP"
        assert k8s_info["api_version"] == "osac.openshift.io/v1alpha1"
        assert k8s_info["name"] == (
            "{{ attachment_externalip_name }}"
            if create_file == "attach_external_ip"
            else "{{ natgw_externalip_name }}"
        )
        assert k8s_info["namespace"] == (
            "{{ attachment_namespace }}"
            if create_file == "attach_external_ip"
            else "{{ natgw_namespace }}"
        )


def test_delete_playbooks_release_through_the_expected_provider_operations() -> None:
    # delete_external_ip's provider write now lives behind the versioned
    # dispatch wrapper in _delete_external_ip_legacy.yaml.
    for delete_file, expected_inclusions in [
        (
            "delete_virtual_network",
            [("netris.controller.ipam", "delete_allocation"), ("netris.controller.vpc", "delete")],
        ),
        ("delete_subnet", [("netris.controller.vnet", "delete"), ("netris.controller.ipam", "delete_subnet")]),
        ("delete_security_group", [("netris.controller.acl", "delete")]),
        ("_delete_external_ip_legacy", [("netris.controller.ipam", "delete_subnet")]),
        ("detach_external_ip", [("netris.controller.nat", "delete")]),
        ("delete_nat_gateway", [("netris.controller.nat", "delete")]),
    ]:
        inclusions = _included_write_tasks(delete_file)
        for expected in expected_inclusions:
            assert expected in inclusions, f"{delete_file} must include {expected}"
