from pathlib import Path

import yaml


PLAYBOOK = Path(__file__).resolve().parents[2] / "playbook_osac_create_compute_instance.yml"


def test_compute_jit_resolves_workload_kubeconfig_before_storage_dispatch():
    """Resolve the optional workload target before dispatch without weakening TLS."""
    play = yaml.safe_load(PLAYBOOK.read_text())[0]
    tasks = play["pre_tasks"]
    resolution = next(
        index
        for index, task in enumerate(tasks)
        if task.get("ansible.builtin.include_role")
        == {
            "name": "osac.service.common",
            "tasks_from": "get_remote_cluster_kubeconfig.yaml",
        }
    )
    dispatch = next(
        index
        for index, task in enumerate(tasks)
        if task.get("name") == "Ensure storage is provisioned for tenant (JIT)"
    )

    assert resolution < dispatch
    assert play["hosts"] == "localhost"
    assert tasks[dispatch]["vars"]["storage_provider_target_kubeconfig"] == (
        "{{ remote_cluster_kubeconfig | default(omit) }}"
    )
    assert "storage_provider_target_kubeconfig_insecure" not in tasks[dispatch]["vars"]


def test_compute_jit_preserves_tier_gate_and_vmaas_target():
    """Keep JIT storage gated by requested tiers and scoped to VM provisioning."""
    play = yaml.safe_load(PLAYBOOK.read_text())[0]
    dispatch = next(
        task
        for task in play["pre_tasks"]
        if task.get("name") == "Ensure storage is provisioned for tenant (JIT)"
    )

    assert dispatch["when"] == "_jit_storage_tiers is defined and _jit_storage_tiers | length > 0"
    assert dispatch["vars"]["storage_provider_provisioning_target"] == "vmaas"
