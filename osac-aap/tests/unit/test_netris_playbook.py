"""Run Netris Fabric Manager helpers through the generic networking playbook.

Each case launches playbook_osac_network_task.yml in a child ansible-playbook
process with `-e @extravars`, as AAP runs a networking job, and reads the
set_stats artifacts. This skeleton covers what the foundation helpers decide
before any task body runs: rejected input and missing settings. Later tasks
add the per-task cases (unreachable Netris URL, no-ops, task file list).
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_netris_role import GOOD_ENV, job  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/netris"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"


def run(tmp_path: Path, job_vars: dict, env: dict | None = None) -> tuple[int, dict]:
    extra = tmp_path / "extravars.json"
    extra.write_text(json.dumps({"osac_job_vars": job_vars}))
    environ = {k: v for k, v in os.environ.items() if not k.startswith("NETRIS_")}
    environ.update({
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
    })
    environ.update(env if env is not None else {})
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", "playbook_osac_network_task.yml", "-e", f"@{extra}"],
        cwd=ROOT, env=environ, capture_output=True, text=True, timeout=300,
    )
    doc = json.loads(proc.stdout[proc.stdout.index("{"):])
    return proc.returncode, doc.get("global_custom_stats") or {}


def assert_error(rc: int, stats: dict, retryable: bool) -> str:
    assert rc != 0
    assert list(stats) == ["osac_error"]
    err = stats["osac_error"]
    assert set(err) == {"message", "retryable"}
    assert err["retryable"] is retryable
    assert "pw-not-a-secret" not in err["message"]
    return err["message"]


def test_extra_vars_other_than_osac_job_vars_are_rejected(tmp_path: Path) -> None:
    extra = tmp_path / "extravars.json"
    extra.write_text(json.dumps({"osac_job_vars": job(), "netris_site_id": 9}))
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", "playbook_osac_network_task.yml", "-e", f"@{extra}"],
        cwd=ROOT, capture_output=True, text=True, timeout=300,
        env={**os.environ, "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json", "ANSIBLE_SHOW_CUSTOM_STATS": "true",
             "ANSIBLE_LOCALHOST_WARNING": "False", "ANSIBLE_PYTHON_INTERPRETER": sys.executable},
    )
    stats = json.loads(proc.stdout[proc.stdout.index("{"):]).get("global_custom_stats") or {}
    assert "netris_site_id" in assert_error(proc.returncode, stats, retryable=False)


def test_helper_files_are_not_launchable_tasks(tmp_path: Path) -> None:
    rc, stats = run(tmp_path, job("_settings"), GOOD_ENV)
    assert "not in the fabric manager task set" in assert_error(rc, stats, retryable=False)


# The cases below run with the production variable context: the playbook loads
# group_vars/all/configuration.yaml, which defines the same netris_* names as
# the role defaults and wins over them.


@pytest.mark.parametrize("task", ["create_virtual_network", "create_subnet"])
@pytest.mark.parametrize("variable", ["NETRIS_SITE_ID", "NETRIS_TENANT_ID", "NETRIS_CONTROLLER_URL", "NETRIS_USERNAME", "NETRIS_PASSWORD"])
def test_a_missing_setting_is_retryable_and_named(tmp_path: Path, task: str, variable: str) -> None:
    env = {k: v for k, v in GOOD_ENV.items() if k != variable}
    rc, stats = run(tmp_path, job(task), env)
    message = assert_error(rc, stats, retryable=True)
    assert "Missing or malformed setting in network-fulfillment-ig" in message
    assert variable in message


@pytest.mark.parametrize("task", ["create_virtual_network", "create_subnet", "delete_subnet", "delete_virtual_network"])
def test_malformed_input_is_final(tmp_path: Path, task: str) -> None:
    bad = job(task, resource={"kind": "Subnet", "id": "not-a-uuid"})
    rc, stats = run(tmp_path, bad, GOOD_ENV)
    assert "Netris invalid task input" in assert_error(rc, stats, retryable=False)


def test_an_unknown_network_type_is_final(tmp_path: Path) -> None:
    rc, stats = run(tmp_path, job("create_virtual_network", network_type="vlan"), GOOD_ENV)
    assert "evpn only" in assert_error(rc, stats, retryable=False)


def test_a_malformed_class_map_does_not_break_tasks_that_do_not_need_it(tmp_path: Path) -> None:
    rc, stats = run(tmp_path, job("create_subnet", resource={"kind": "Subnet", "id": "x"}),
                    {**GOOD_ENV, "NETRIS_RESOURCE_CLASS_MAP": "{not json"})
    assert "Netris invalid task input" in assert_error(rc, stats, retryable=False)
