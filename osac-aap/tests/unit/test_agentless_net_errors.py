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
TEST_REDACTION_SENTINEL = "sensitive-provider-password-sentinel"
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
    """Exhaustion stays public and final without depending on write mode."""
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
        ("StateError", TEST_REDACTION_SENTINEL, False, False),
        ("RetryableStateError", TEST_REDACTION_SENTINEL, True, False),
        ("SubnetProviderError", TEST_REDACTION_SENTINEL, True, False),
        ("RuntimeError", "ExternalIPPool has no available addresses", False, False),
    ],
)
def test_module_only_exports_explicit_public_errors(monkeypatch, kind, code, retryable, public):
    """Only authored exception provenance can export a public diagnostic."""
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
        error.public_message = TEST_REDACTION_SENTINEL
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
    assert TEST_REDACTION_SENTINEL not in json.dumps(result)


@pytest.mark.parametrize(
    "action,result,retryable,expected",
    [
        ("osac.templates.agentless_net_state", {"public_message": "ExternalIPPool has no available addresses", "retryable": False}, True, "ExternalIPPool has no available addresses"),
        ("osac.templates.agentless_net_state", {"public_message": "physical address or switch port was reserved concurrently", "retryable": True}, False, "physical address or switch port was reserved concurrently"),
        ("osac.templates.agentless_net_state", {"msg": TEST_REDACTION_SENTINEL, "retryable": False}, True, "AgentlessNet network task failed."),
        ("ansible.builtin.command", {"msg": TEST_REDACTION_SENTINEL, "public_message": TEST_REDACTION_SENTINEL}, True, "AgentlessNet network task failed."),
        ("ansible.builtin.assert", {"msg": TEST_REDACTION_SENTINEL}, False, "AgentlessNet network task failed."),
        ("osac.templates.agentless_net_state", {"msg": "ExternalIPPool has no available addresses"}, False, "AgentlessNet network task failed."),
    ],
)
def test_real_ansible_reporter_sanitizes_untrusted_results(tmp_path, action, result, retryable, expected):
    """Require real reporter failure and safe emitted stats for each provenance."""
    tasks = [
        {"name": "Supply a failed result", "ansible.builtin.set_fact": {
            "ansible_failed_task": {"action": action, "name": TEST_REDACTION_SENTINEL},
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
    """Capture the reporter's terminal failure without exposing its inputs."""
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


def run_play(tmp_path, tasks, expected, inventory=None, check_mode=False):
    """Run local Ansible and verify failure capture, redaction and actual stats."""
    if expected is not None:
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
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(play)]
        + (["--check"] if check_mode else []),
        cwd=ROOT, env={
            **os.environ, "AGENTLESS_NET_VN_INVENTORY": json.dumps(inventory) if inventory else "",
            "AGENTLESS_NET_SSH_PRIVATE_KEY": "", "ANSIBLE_NOCOLOR": "1",
            "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
            "ANSIBLE_SHOW_CUSTOM_STATS": "true",
            "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
        },
        text=True, capture_output=True, timeout=60,
    )
    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert TEST_REDACTION_SENTINEL not in completed.stdout + completed.stderr
    output = json.loads(completed.stdout[completed.stdout.index("{"):])
    assert output["global_custom_stats"] == ({"osac_error": expected} if expected else {})


@pytest.mark.parametrize("check_mode", [False, True])
@pytest.mark.parametrize("provider_retryable", [False, True])
def test_real_missing_inventory_reports_setting_despite_no_log(tmp_path, check_mode, provider_retryable):
    """Missing settings remain repairable regardless of provider phase or mode."""
    run_play(tmp_path, [
        {"name": "Set provider phase", "ansible.builtin.set_fact": {
            "_agentless_net_failure_retryable": provider_retryable,
        }},
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
    ], {"message": MISSING_INVENTORY, "retryable": True}, check_mode=check_mode)


@pytest.mark.parametrize("check_mode", [False, True])
def test_real_state_module_to_role_exhaustion_artifact(tmp_path, check_mode):
    """Trace real SQLite exhaustion through a censored module into public stats."""
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


@pytest.mark.parametrize("check_mode", [False, True])
@pytest.mark.parametrize("pool_case", ["missing", "mismatched"])
def test_existing_database_pool_errors_preserve_identity(tmp_path, pool_case, check_mode):
    """Distinguish absent pools from identity conflicts without changing state."""
    state = importlib.import_module("agentless_net_state")
    store = store_for(tmp_path)
    store.ensure_external_ip_pool(
        UID_THREE if pool_case == "missing" else POOL_UID,
        UID_THREE if pool_case == "missing" else POOL_FULFILLMENT_ID,
        "shared", "IPv4", ["198.51.100.0/30"],
    )
    before = store.path.read_bytes()
    requested_cidr = "198.51.100.0/30" if pool_case == "missing" else "198.51.100.4/30"
    with pytest.raises(state.StateError) as caught:
        reserve_external_address(
            store, UID_ONE, UID_ONE, pool_cidr=requested_cidr, check_mode=check_mode,
        )
    if pool_case == "missing":
        assert isinstance(caught.value, state.PublicStateError)
        assert caught.value.public_message == "ExternalIPPool is not registered"
    else:
        assert not isinstance(caught.value, state.PublicStateError)
        assert str(caught.value) == "ExternalIPPool identity does not match saved state"
    assert caught.value.retryable is False
    assert store.path.read_bytes() == before
    run_play(tmp_path, [{
        "name": "Reserve against missing or mismatched pool",
        "block": [{
            "name": "Allocate through the real state module",
            "osac.templates.agentless_net_state": {
                "action": "reserve_external_ip", "state_file": str(store.path),
                "uid": UID_ONE, "fulfillment_id": UID_ONE, "tenant_id": TENANT_ONE,
                "pool_uid": POOL_UID, "pool_fulfillment_id": POOL_FULFILLMENT_ID,
                "pool_tenant_id": "shared", "pool_cidr": requested_cidr,
            },
            "check_mode": check_mode, "no_log": True,
        }],
        "rescue": [capture_reporter()],
    }], {"message": "ExternalIPPool is not registered" if pool_case == "missing" else GENERIC,
         "retryable": False})
    assert store.path.read_bytes() == before


@pytest.mark.parametrize("invalid_field", ["ansible_host", "ansible_port", None])
@pytest.mark.parametrize("check_mode", [False, True])
@pytest.mark.parametrize("provider_retryable", [False, True])
def test_real_inventory_connection_validation_is_retryable(tmp_path, invalid_field, check_mode, provider_retryable):
    """Repairable connections retry under no_log; valid inventory still loads."""
    connection = {"ansible_host": "192.0.2.10", "ansible_user": TEST_REDACTION_SENTINEL}
    if invalid_field == "ansible_host":
        del connection["ansible_host"]
    elif invalid_field == "ansible_port":
        connection["ansible_port"] = TEST_REDACTION_SENTINEL
    inventory = {"all": {"children": {"net_nodes": {"hosts": {"test-node": connection}}}}}
    tasks = [
        {"name": "Set retryable provider phase", "ansible.builtin.set_fact": {
            "_agentless_net_failure_retryable": provider_retryable,
        }},
        {"name": "Load real inventory", "block": [{
            "name": "Validate connection", "ansible.builtin.include_role": {
                "name": "osac.templates.agentless_net", "tasks_from": "_load_network_node_inventory",
            },
        }], "rescue": [capture_reporter()]},
    ]
    if invalid_field is None:
        tasks.append({"name": "Require valid inventory success", "ansible.builtin.assert": {
            "that": ["not (_reporter_failure_captured | default(false))",
                     "'test-node' in groups['agentless_net_vn_network_node']"],
        }})
        expected = None
    else:
        expected = {
            "message": "AgentlessNet network-node inventory requires ansible_host, ansible_user, "
                       "a valid optional ansible_port, and text SSH arguments.",
            "retryable": True,
        }
    run_play(tmp_path, tasks, expected, inventory=inventory, check_mode=check_mode)


@pytest.mark.parametrize("check_mode", [False, True])
@pytest.mark.parametrize("provider_retryable", [False, True])
@pytest.mark.parametrize("inventory", [
    [TEST_REDACTION_SENTINEL],
    {"all": {"children": {"net_nodes": {"hosts": {"first": {}, "second": {}}}}}},
])
def test_real_inventory_shape_is_repairable(tmp_path, check_mode, provider_retryable, inventory):
    """Malformed shape and multiple nodes emit a retryable authored artifact."""
    run_play(tmp_path, [
        {"name": "Set provider phase", "ansible.builtin.set_fact": {
            "_agentless_net_failure_retryable": provider_retryable,
        }},
        {"name": "Validate real inventory", "block": [{
            "name": "Load inventory", "ansible.builtin.include_role": {
                "name": "osac.templates.agentless_net", "tasks_from": "_load_network_node_inventory",
            },
        }], "rescue": [capture_reporter()]},
    ], {
        "message": "AgentlessNet VirtualNetwork operations require exactly one host under "
                   "all.children.net_nodes.hosts in the provider inventory.",
        "retryable": True,
    }, inventory=inventory, check_mode=check_mode)


@pytest.mark.parametrize("check_mode", [False, True])
@pytest.mark.parametrize("provider_retryable", [False, True])
def test_real_invalid_manager_input_stays_final(tmp_path, check_mode, provider_retryable):
    """Invalid frozen task input stays final even with a retryable provider default."""
    run_play(tmp_path, [{
        "name": "Reject real invalid manager input", "block": [{
            "name": "Validate frozen input", "ansible.builtin.include_role": {
                "name": "osac.templates.agentless_net", "tasks_from": "_validate_task_input",
            },
            "vars": {"osac_job_vars": {"manager": {"name": TEST_REDACTION_SENTINEL}}},
        }], "rescue": [
            {"name": "Set provider default before reporting", "ansible.builtin.set_fact": {
                "_agentless_net_failure_retryable": provider_retryable,
            }},
            capture_reporter(),
        ],
    }], {
        "message": "AgentlessNet received an invalid or unsupported network-manager task input.",
        "retryable": False,
    }, check_mode=check_mode)


def test_inventory_repair_is_read_by_next_job(tmp_path):
    """A new local job rereads repaired settings instead of retaining the failure."""
    test_real_missing_inventory_reports_setting_despite_no_log(tmp_path, False, False)
    test_real_inventory_connection_validation_is_retryable(tmp_path, None, False, False)
