"""Regression tests for the cudn_net Subnet CUDN manifest."""

from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/cudn_net"


def test_subnet_sets_persistent_ipam_lifecycle_at_the_cudn_schema_path() -> None:
    tasks = yaml.safe_load((ROLE / "tasks/create_subnet.yaml").read_text(encoding="utf-8"))
    create_cudn = next(
        task for task in tasks if task.get("name") == "Create ClusterUserDefinedNetwork"
    )
    layer2 = create_cudn["kubernetes.core.k8s"]["definition"]["spec"]["network"]["layer2"]
    defaults = yaml.safe_load((ROLE / "defaults/main.yaml").read_text(encoding="utf-8"))

    assert layer2["ipam"] == {"lifecycle": "{{ default_ipam_lifecycle }}"}
    assert defaults["default_ipam_lifecycle"] == "Persistent"
    assert "ipamLifecycle" not in layer2
