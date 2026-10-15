import importlib.util
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
from ansible.errors import AnsibleError


PLUGIN = (
    Path(__file__).parents[2]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "plugins"
    / "strategy"
    / "protected_linear.py"
)
COLLECTIONS = Path(__file__).parents[2] / "collections"
sys.path.insert(0, str(COLLECTIONS))
_SPEC = importlib.util.spec_from_file_location("protected_linear", PLUGIN)
protected_linear = importlib.util.module_from_spec(_SPEC)
assert _SPEC.loader is not None
_SPEC.loader.exec_module(protected_linear)


def _write_extra_vars(monkeypatch, tmp_path, value):
    path = tmp_path / "extravars"
    path.write_text(json.dumps(value))
    monkeypatch.setenv("OSAC_LAUNCH_EXTRA_VARS_PATH", str(path))


def test_allows_ordinary_original_launch_extra_vars(monkeypatch, tmp_path):
    _write_extra_vars(
        monkeypatch,
        tmp_path,
        {"osac_job_vars": {"resource": {"metadata": {"name": "network-1"}}}},
    )

    protected_linear._validate_launch_extra_vars()


@pytest.mark.parametrize(
    "extra_vars",
    [
        {"NETRIS_CONTROLLER_URL": "https://attacker.example"},
        {"nested": [{"NetrisControllerURL": None}]},
        {"osac_job_vars": {"resource": {"netris_validate_certs": False}}},
    ],
)
def test_rejects_protected_original_launch_extra_vars(
    monkeypatch, tmp_path, extra_vars
):
    _write_extra_vars(monkeypatch, tmp_path, extra_vars)

    with pytest.raises(AnsibleError, match="protected Netris setting"):
        protected_linear._validate_launch_extra_vars()


def test_fails_closed_when_original_launch_extra_vars_are_missing(
    monkeypatch, tmp_path
):
    monkeypatch.setenv(
        "OSAC_LAUNCH_EXTRA_VARS_PATH", str(tmp_path / "missing-extravars")
    )

    with pytest.raises(AnsibleError, match="extra_vars are unavailable"):
        protected_linear._validate_launch_extra_vars()


def test_fails_closed_when_original_launch_extra_vars_are_not_a_mapping(
    monkeypatch, tmp_path
):
    _write_extra_vars(monkeypatch, tmp_path, ["not", "a", "mapping"])

    with pytest.raises(AnsibleError, match="extra_vars must be a mapping"):
        protected_linear._validate_launch_extra_vars()


def test_validates_before_running_linear_strategy(monkeypatch):
    events = []
    monkeypatch.setattr(
        protected_linear,
        "_validate_launch_extra_vars",
        lambda: events.append("validated"),
    )
    monkeypatch.setattr(
        protected_linear.LinearStrategyModule,
        "run",
        lambda self, iterator, play_context: events.append("linear") or "result",
    )
    strategy = object.__new__(protected_linear.StrategyModule)

    assert strategy.run(None, None) == "result"
    assert events == ["validated", "linear"]


def _run_ansible_with_protected_strategy(tmp_path, extra_vars):
    playbook = tmp_path / "probe.yml"
    playbook.write_text(
        """---
- name: Protected strategy probe
  hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - name: Mark task execution
      ansible.builtin.debug:
        msg: PROTECTED_STRATEGY_TASK_RAN
"""
    )
    extra_vars_path = tmp_path / "extravars"
    extra_vars_path.write_text(json.dumps(extra_vars))
    local_tmp = tmp_path / "ansible-tmp"
    local_tmp.mkdir()
    environment = {
        **os.environ,
        "ANSIBLE_COLLECTIONS_PATH": str(COLLECTIONS),
        "ANSIBLE_LOCAL_TEMP": str(local_tmp),
        "ANSIBLE_NOCOLOR": "1",
        "ANSIBLE_STRATEGY": "netris.controller.protected_linear",
        "OSAC_LAUNCH_EXTRA_VARS_PATH": str(extra_vars_path),
    }
    environment.pop("ANSIBLE_CONFIG", None)

    return subprocess.run(
        [
            str(Path(sys.executable).with_name("ansible-playbook")),
            "-i",
            "localhost,",
            "-e",
            f"@{extra_vars_path}",
            str(playbook),
        ],
        check=False,
        capture_output=True,
        cwd=tmp_path,
        env=environment,
        text=True,
    )


def test_real_ansible_strategy_allows_ordinary_launch_before_tasks(tmp_path):
    result = _run_ansible_with_protected_strategy(
        tmp_path, {"osac_job_vars": {"resource": {"metadata": {"name": "network-1"}}}}
    )

    assert result.returncode == 0, result.stderr
    assert "PROTECTED_STRATEGY_TASK_RAN" in result.stdout


def test_real_ansible_strategy_rejects_protected_launch_before_tasks(tmp_path):
    result = _run_ansible_with_protected_strategy(
        tmp_path, {"NETRIS_CONTROLLER_URL": "https://attacker.example"}
    )
    output = result.stdout + result.stderr

    assert result.returncode != 0
    assert "protected Netris setting 'NETRIS_CONTROLLER_URL'" in output
    assert "PROTECTED_STRATEGY_TASK_RAN" not in output
