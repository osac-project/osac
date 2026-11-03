import importlib
import json
import os
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_agentless_network_state import (
    POOL_UID,
    UID_ONE,
    UID_THREE,
    UID_TWO,
    register_external_ip_pool,
    reserve_external_address,
    store_for,
)

ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/agentless_net"
SECRET = "sensitive-provider-password-sentinel"


@pytest.mark.parametrize("check_mode", [False, True])
def test_real_pool_exhaustion_is_public_and_final(tmp_path, check_mode):
    state = importlib.import_module("agentless_net_state")
    store = store_for(tmp_path)
    register_external_ip_pool(store, cidr="198.51.100.0/30")
    reserve_external_address(store, UID_ONE, POOL_UID, pool_cidr="198.51.100.0/30")
    reserve_external_address(store, UID_TWO, UID_THREE, pool_cidr="198.51.100.0/30")
    with pytest.raises(state.PublicStateError) as caught:
        reserve_external_address(
            store, UID_THREE, UID_ONE, pool_cidr="198.51.100.0/30", check_mode=check_mode
        )
    assert caught.value.public_message == "ExternalIPPool has no available addresses"
    assert caught.value.retryable is False


@pytest.mark.parametrize(
    "kind,code,retryable,public",
    [
        ("PublicStateError", "pool_exhausted", False, True),
        ("PublicStateError", "pool_not_registered", False, True),
        ("PublicRetryableStateError", "attachment_conflict", True, True),
        ("StateError", "ExternalIPPool has no available addresses", False, False),
        ("StateError", SECRET, False, False),
        ("RetryableStateError", SECRET, True, False),
        ("SubnetProviderError", SECRET, True, False),
    ],
)
def test_module_only_exports_explicit_public_errors(monkeypatch, kind, code, retryable, public):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    state = importlib.import_module(
        "ansible_collections.osac.templates.plugins.module_utils.agentless_net_state"
    )
    error_class = getattr(module, kind, None) or getattr(state, kind)
    module_object = SimpleNamespace(
        params={
            "action": "ensure_virtual_network", "state_file": "unused",
            "uid": UID_ONE, "tenant_id": "test", "virtual_network_cidr": "10.0.0.0/24",
        },
        check_mode=False,
        fail_json=Mock(),
    )
    store = SimpleNamespace(ensure_and_reconcile_virtual_network=Mock(side_effect=error_class(code)))
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: module_object)
    monkeypatch.setattr(module, "StateStore", lambda path: store)
    module.main()
    result = module_object.fail_json.call_args.kwargs
    assert result["retryable"] is retryable
    assert ("public_message" in result) is public
    assert SECRET not in json.dumps(result)


@pytest.mark.parametrize(
    "action,result,retryable,expected",
    [
        ("osac.templates.agentless_net_state", {"public_message": "ExternalIPPool has no available addresses", "retryable": False}, True, "ExternalIPPool has no available addresses"),
        ("osac.templates.agentless_net_state", {"public_message": "physical address or switch port was reserved concurrently", "retryable": True}, False, "physical address or switch port was reserved concurrently"),
        ("osac.templates.agentless_net_state", {"msg": SECRET, "retryable": False}, True, "AgentlessNet network task failed."),
        ("ansible.builtin.command", {"msg": SECRET, "public_message": SECRET}, True, "AgentlessNet network task failed."),
        ("ansible.builtin.assert", {"msg": SECRET}, False, "AgentlessNet network task failed."),
    ],
)
def test_real_ansible_reporter_sanitizes_untrusted_results(tmp_path, action, result, retryable, expected):
    tasks = [
        {"name": "Supply a failed result", "ansible.builtin.set_fact": {
            "ansible_failed_task": {"action": action, "name": SECRET},
            "ansible_failed_result": result,
            "_agentless_net_failure_retryable": retryable,
        }, "no_log": True},
        {"name": "Capture reported failure", "block": [
            {"name": "Report failure", "ansible.builtin.include_tasks": str(ROLE / "tasks/_report_task_error.yaml")},
        ], "rescue": [
            {"name": "Verify safe artifact", "ansible.builtin.assert": {"that": [
                f"_agentless_net_error.message.startswith({expected!r})",
                f"_agentless_net_error.retryable == {str(result.get('retryable', retryable)).lower()}",
            ]}},
        ]},
    ]
    run_play(tmp_path, tasks)


def run_play(tmp_path, tasks):
    play = tmp_path / "errors.json"
    play.write_text(json.dumps([{"hosts": "localhost", "gather_facts": False, "tasks": tasks}]))
    completed = subprocess.run(
        [str(ROOT / ".venv/bin/ansible-playbook"), "-i", "localhost,", "-c", "local", str(play)],
        cwd=ROOT, env={**os.environ, "AGENTLESS_NET_VN_INVENTORY": "", "ANSIBLE_NOCOLOR": "1"},
        text=True, capture_output=True, timeout=60,
    )
    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert SECRET not in completed.stdout + completed.stderr


def test_real_missing_inventory_reports_setting_despite_no_log(tmp_path):
    run_play(tmp_path, [
        {"name": "Set provider phase", "ansible.builtin.set_fact": {"_agentless_net_failure_retryable": True}},
        {"name": "Validate real inventory", "block": [
            {"name": "Load inventory", "ansible.builtin.include_tasks": str(ROLE / "tasks/_load_network_node_inventory.yaml")},
        ], "rescue": [
            {"name": "Capture reported failure", "block": [
                {"name": "Report failure", "ansible.builtin.include_tasks": str(ROLE / "tasks/_report_task_error.yaml")},
            ], "rescue": [
                {"name": "Verify actionable final error", "ansible.builtin.assert": {"that": [
                    "'AGENTLESS_NET_VN_INVENTORY' in _agentless_net_error.message",
                    "not _agentless_net_error.retryable",
                ]}},
            ]},
        ]},
    ])
