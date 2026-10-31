"""Behavioral tests for Netris task result and error publication."""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
RULE = ROOT / "collections/ansible_collections/osac/templates/roles/netris"


def run_stats(tmp_path: Path, helper: str, variables: dict) -> dict:
    playbook = tmp_path / "play.yml"
    playbook.write_text(
        yaml.safe_dump(
            [
                {
                    "hosts": "localhost",
                    "gather_facts": False,
                    "vars": variables,
                    "tasks": [
                        {
                            "name": "Run helper",
                            "ansible.builtin.include_tasks": str(RULE / "tasks" / helper),
                        }
                    ],
                }
            ]
        ),
        encoding="utf-8",
    )
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", "-c", "local", str(playbook)],
        cwd=ROOT,
        env={
            **os.environ,
            "ANSIBLE_LOCAL_TEMP": str(tmp_path / "local"),
            "ANSIBLE_STDOUT_CALLBACK": "json",
        },
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    data = json.loads(result.stdout)
    for play in data["plays"]:
        for task in play["tasks"]:
            for host_result in task["hosts"].values():
                stats = host_result.get("ansible_stats")
                if stats is not None:
                    return stats["data"]
    return {}


def test_typed_result_is_published_with_native_types(tmp_path: Path) -> None:
    data = run_stats(
        tmp_path,
        "_emit_result.yaml",
        {"_osac_result_data": {"network_segment": {"type": "evpn", "l2_vni": 10100, "l3_vni": 20001}}},
    )

    assert data == {
        "osac_result": {"network_segment": {"type": "evpn", "l2_vni": 10100, "l3_vni": 20001}}
    }
    assert isinstance(data["osac_result"]["network_segment"]["l2_vni"], int)


def test_a_task_without_typed_output_publishes_nothing(tmp_path: Path) -> None:
    data = run_stats(tmp_path, "_emit_result.yaml", {"_osac_result_data": {}})

    assert data == {}


def test_failure_publishes_a_sanitized_error(tmp_path: Path) -> None:
    data = run_stats(
        tmp_path,
        "_fail_task.yaml",
        {"_osac_error_message": "no free VLAN left", "_osac_error_retryable": True},
    )

    assert data == {"osac_error": {"message": "no free VLAN left", "retryable": True}}
    assert isinstance(data["osac_error"]["retryable"], bool)


def test_failure_defaults_to_not_retryable(tmp_path: Path) -> None:
    data = run_stats(tmp_path, "_fail_task.yaml", {"_osac_error_message": "rule cannot be enforced"})

    assert data == {"osac_error": {"message": "rule cannot be enforced", "retryable": False}}


def test_empty_error_message_publishes_nothing(tmp_path: Path) -> None:
    data = run_stats(tmp_path, "_fail_task.yaml", {"_osac_error_message": ""})

    assert data == {}
