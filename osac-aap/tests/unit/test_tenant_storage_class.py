"""Exercise the tier-selector role without Kubernetes or provider dependencies."""

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

OSAC_AAP = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize(
    ("mapping", "tier", "selected", "error"),
    [
        (
            [
                {"name": "slow-class", "tier": "slow"},
                {"name": "fast-class", "tier": "fast"},
            ],
            "fast",
            "fast-class",
            None,
        ),
        (
            [
                {"name": "first-class", "tier": "fast"},
                {"name": "second-class", "tier": "fast"},
                {"name": "slow-class", "tier": "slow"},
            ],
            "slow",
            "slow-class",
            None,
        ),
        ([], "fast", None, "No resolved storage tiers"),
        ([{"name": "slow-class", "tier": "slow"}], "fast", None, "is not available"),
        (
            [
                {"name": "first-class", "tier": "fast"},
                {"name": "second-class", "tier": "fast"},
            ],
            "fast",
            None,
            "multiple resolved StorageClasses",
        ),
        (
            [
                {"name": "same-class", "tier": "fast"},
                {"name": "same-class", "tier": "fast"},
            ],
            "fast",
            "same-class",
            None,
        ),
    ],
)
def test_tier_selection_contract(
    tmp_path: Path,
    mapping: list[dict[str, str]],
    tier: str,
    selected: str | None,
    error: str | None,
) -> None:
    tasks = [
        {
            "name": "Select requested tier",
            "ansible.builtin.include_role": {
                "name": "osac.service.tenant_storage_class",
            },
        }
    ]
    if selected:
        tasks.append(
            {
                "name": "Verify selected class",
                "ansible.builtin.assert": {
                    "that": [f"tenant_storage_class_name == '{selected}'"],
                },
            }
        )
    playbook = [
        {
            "name": "Exercise supplied tier mapping",
            "hosts": "localhost",
            "gather_facts": False,
            "vars": {
                "tenant_storage_class_storage_classes": mapping,
                "tenant_storage_class_storage_tier": tier,
                "tenant_storage_class_tenant_name": "fixture-tenant",
                "tenant_storage_class_disk_label": "boot disk",
                "ansible_python_interpreter": sys.executable,
                "ansible_connection": "local",
            },
            "tasks": tasks,
        }
    ]
    path = tmp_path / "select-tier.json"
    path.write_text(json.dumps(playbook))
    environment = os.environ.copy()
    environment.update(
        {
            "ANSIBLE_CONFIG": str(OSAC_AAP / "ansible.cfg"),
            "ANSIBLE_COLLECTIONS_PATH": f"{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
            "ANSIBLE_FORCE_COLOR": "false",
            "ANSIBLE_STDOUT_CALLBACK": "default",
        }
    )
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(path)],
        cwd=OSAC_AAP,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert (result.returncode == 0) == (error is None), result.stdout + result.stderr
    if error:
        assert error in result.stdout, result.stdout + result.stderr
