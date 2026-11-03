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
    POOL_FULFILLMENT_ID,
    TENANT_ONE,
    UID_ONE,
    UID_THREE,
    UID_TWO,
    register_external_ip_pool,
    reserve_external_address,
    store_for,
)

ROOT = Path(__file__).resolve().parents[2]
SECRET = "sensitive-provider-password-sentinel"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"
GENERIC = (
    "AgentlessNet network task failed. Inspect the AAP job log and correct "
    "the provider state before retrying."
)
MISSING_INVENTORY = (
    "AgentlessNet VirtualNetwork operations require AGENTLESS_NET_VN_INVENTORY "
    "in the network-fulfillment-ig ConfigMap."
)


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
        ("RuntimeError", "ExternalIPPool has no available addresses", False, False),
    ],
)
def test_module_only_exports_explicit_public_errors(monkeypatch, kind, code, retryable, public):
    module = importlib.import_module(
        "ansible_collections.osac.templates.plugins.modules.agentless_net_state"
    )
    state = importlib.import_module(
        "ansible_collections.osac.templates.plugins.module_utils.agentless_net_state"
    )
    error_class = RuntimeError if kind == "RuntimeError" else (
        getattr(module, kind, None) or getattr(state, kind)
    )
    module_object = SimpleNamespace(
        params={
            "action": "ensure_virtual_network", "state_file": "unused",
            "uid": UID_ONE, "tenant_id": "test", "virtual_network_cidr": "10.0.0.0/24",
        },
        check_mode=False,
        fail_json=Mock(),
    )
    error = error_class(code)
    if not public:
        error.public_message = SECRET
    store = SimpleNamespace(ensure_and_reconcile_virtual_network=Mock(side_effect=error))
    monkeypatch.setattr(module, "AnsibleModule", lambda **kwargs: module_object)
    monkeypatch.setattr(module, "StateStore", lambda path: store)
    if kind == "RuntimeError":
        with pytest.raises(RuntimeError, match=code):
            module.main()
        module_object.fail_json.assert_not_called()
        return
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
        ("osac.templates.agentless_net_state", {"msg": "ExternalIPPool has no available addresses"}, False, "AgentlessNet network task failed."),
    ],
)
def test_real_ansible_reporter_sanitizes_untrusted_results(tmp_path, action, result, retryable, expected):
    tasks = [
        {"name": "Supply a failed result", "ansible.builtin.set_fact": {
            "ansible_failed_task": {"action": action, "name": SECRET},
            "ansible_failed_result": result,
            "_agentless_net_failure_retryable": retryable,
        }, "no_log": True},
        capture_reporter(),
    ]
    run_play(tmp_path, tasks, {
        "message": GENERIC if expected == "AgentlessNet network task failed." else expected,
        "retryable": result.get("retryable", retryable),
    })


def capture_reporter():
    return {
        "name": "Capture reported failure",
        "block": [{
            "name": "Report failure",
            "ansible.builtin.include_role": {
                "name": "osac.templates.agentless_net", "tasks_from": "_report_task_error",
            },
        }],
        "rescue": [{
            "name": "Record the reporter failure",
            "ansible.builtin.set_fact": {
                "_reporter_failure_captured": True,
                "_reporter_failure_message": "{{ ansible_failed_result.msg }}",
            },
            "no_log": True,
        }],
    }


def run_play(tmp_path, tasks, expected):
    tasks.append({
        "name": "Require the reporter to have failed with the expected artifact",
        "ansible.builtin.assert": {"that": [
            "_reporter_failure_captured | default(false)",
            "_reporter_failure_message == expected_error.message",
            "_agentless_net_error == expected_error",
        ]},
        "vars": {"expected_error": expected},
    })
    play = tmp_path / "errors.json"
    play.write_text(json.dumps([{"hosts": "localhost", "gather_facts": False, "tasks": tasks}]))
    completed = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(play)],
        cwd=ROOT, env={
            **os.environ, "AGENTLESS_NET_VN_INVENTORY": "", "ANSIBLE_NOCOLOR": "1",
            "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
            "ANSIBLE_SHOW_CUSTOM_STATS": "true",
            "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
        },
        text=True, capture_output=True, timeout=60,
    )
    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert SECRET not in completed.stdout + completed.stderr
    output = json.loads(completed.stdout[completed.stdout.index("{"):])
    assert output["global_custom_stats"] == {"osac_error": expected}


def test_real_missing_inventory_reports_setting_despite_no_log(tmp_path):
    run_play(tmp_path, [
        {"name": "Set provider phase", "ansible.builtin.set_fact": {"_agentless_net_failure_retryable": True}},
        {"name": "Validate real inventory", "block": [
            {"name": "Load inventory", "ansible.builtin.include_role": {
                "name": "osac.templates.agentless_net", "tasks_from": "_load_network_node_inventory",
            }},
        ], "rescue": [
            {"name": "Record inventory validation failure", "ansible.builtin.set_fact": {
                "_inventory_failure_captured": True,
            }},
            capture_reporter(),
        ]},
        {"name": "Require inventory validation failure", "ansible.builtin.assert": {
            "that": ["_inventory_failure_captured | default(false)"],
        }},
    ], {"message": MISSING_INVENTORY, "retryable": False})


@pytest.mark.parametrize("check_mode", [False, True])
def test_real_state_module_to_role_exhaustion_artifact(tmp_path, check_mode):
    store = store_for(tmp_path)
    register_external_ip_pool(store, cidr="198.51.100.0/30")
    reserve_external_address(store, UID_ONE, POOL_UID, pool_cidr="198.51.100.0/30")
    reserve_external_address(store, UID_TWO, UID_THREE, pool_cidr="198.51.100.0/30")
    run_play(tmp_path, [{
        "name": "Exhaust the real pool through the module",
        "block": [{
            "name": "Reserve an unavailable address",
            "osac.templates.agentless_net_state": {
                "action": "reserve_external_ip", "state_file": str(store.path),
                "uid": UID_THREE, "fulfillment_id": UID_ONE, "tenant_id": TENANT_ONE,
                "pool_uid": POOL_UID, "pool_fulfillment_id": POOL_FULFILLMENT_ID,
                "pool_tenant_id": "shared", "pool_cidr": "198.51.100.0/30",
            },
            "check_mode": check_mode,
            "no_log": True,
        }],
        "rescue": [capture_reporter()],
    }], {"message": "ExternalIPPool has no available addresses", "retryable": False})
