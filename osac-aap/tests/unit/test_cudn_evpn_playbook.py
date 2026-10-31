"""Run cudn_evpn tasks through the generic networking playbook, without a cluster.

Each case launches playbook_osac_network_task.yml in a child ansible-playbook
process with `-e @extravars`, as AAP runs a networking job, and reads the
set_stats artifacts. The hub API is an unreachable address, so these cases
cover dispatch, the osac_error shape and the final/retryable split of every
error raised before or at the first API call. Hub behavior needs a cluster
and is out of scope here.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_cudn_evpn_role import SG, job, snapshot  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"


def run(tmp_path: Path, job_vars: dict, env: dict | None = None) -> tuple[int, dict]:
    extra = tmp_path / "extravars.json"
    extra.write_text(json.dumps({"osac_job_vars": job_vars}))
    kubeconfig = tmp_path / "kubeconfig"
    kubeconfig.write_text(json.dumps({
        "apiVersion": "v1", "kind": "Config", "current-context": "x",
        "clusters": [{"name": "x", "cluster": {"server": "https://127.0.0.1:9"}}],
        "contexts": [{"name": "x", "context": {"cluster": "x", "user": "x"}}],
        "users": [{"name": "x", "user": {"token": "not-a-secret"}}],
    }))
    environ = {
        **os.environ,
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
        "K8S_AUTH_KUBECONFIG": str(kubeconfig),
        "K8S_AUTH_VERIFY_SSL": "false",
    }
    environ.update(env or {})
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
    assert isinstance(err["message"], str) and err["message"]
    assert err["retryable"] is retryable
    assert "not-a-secret" not in err["message"]
    return err["message"]


def test_invalid_input_fails_as_final(tmp_path: Path) -> None:
    rc, stats = run(tmp_path, job("create_subnet", network_segment={"type": "evpn", "l2_vni": 1, "l3_vni": 1}))
    message = assert_error(rc, stats, retryable=False)
    assert "invalid task input" in message


def test_icmp_rule_fails_as_final_before_any_api_call(tmp_path: Path) -> None:
    sg = snapshot("SecurityGroup", SG, {"ingressRules": [{"protocol": "icmp", "sourceCidr": "0.0.0.0/0"}], "egressRules": []})
    rc, stats = run(tmp_path, job("create_security_group", resource=sg))
    message = assert_error(rc, stats, retryable=False)
    assert "NetworkPolicy cannot match ICMP" in message


# create_subnet needs no setting: it goes straight to the hub API, and the removed
# resolvers setting, set or not, changes nothing.
REMOVED_SETTING = "CUDN_EVPN_DNS_RESOLVERS"  # removed with the per-resource DNS


@pytest.mark.parametrize("env", [{}, {REMOVED_SETTING: "192.0.2.53"}, {REMOVED_SETTING: "not-an-address"}])
def test_create_subnet_needs_no_setting_and_ignores_the_removed_dns_setting(tmp_path: Path, env: dict) -> None:
    rc, stats = run(tmp_path, job("create_subnet"), env)
    message = assert_error(rc, stats, retryable=True)
    assert message.startswith("cudn_evpn create_subnet: step ")
    assert REMOVED_SETTING not in message


@pytest.mark.parametrize(
    "task",
    [
        "create_virtual_network", "delete_virtual_network", "create_subnet", "delete_subnet",
        "create_security_group", "delete_security_group", "apply_network_attachment", "delete_network_attachment",
    ],
)
def test_an_unreachable_hub_api_is_retryable_for_every_task(tmp_path: Path, task: str) -> None:
    rc, stats = run(tmp_path, job(task))
    message = assert_error(rc, stats, retryable=True)
    assert message.startswith(f"cudn_evpn {task}: step ")
    assert "OSAC retries the task" in message
