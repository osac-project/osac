import base64
import copy
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
from ansible.errors import AnsibleFilterError

from cluster_pull_secret import cluster_pull_secret

OSAC_AAP = Path(__file__).resolve().parents[2]
MARKER = "OSAC7034_SYNTHETIC_RUNTIME"
PAYLOAD = json.dumps({"auths": {"registry.invalid": {"auth": MARKER}}})


def fixture():
    order = {
        "metadata": {
            "name": "fixture-order",
            "namespace": "fixture-hub",
            "uid": "fixture-uid",
            "annotations": {"osac.openshift.io/tenant": "tenant-a"},
        },
        "spec": {
            "templateID": "fixture",
            "templateParameters": '{"unrelated":"keep"}',
            "nodeRequests": [],
            "sshPublicKey": "synthetic-public-key",
            "pullSecretRef": {"name": "owned-secret"},
        },
    }
    secret = {
        "metadata": {
            "name": "owned-secret",
            "namespace": "fixture-hub",
            "annotations": {"osac.openshift.io/tenant": "tenant-a"},
            "ownerReferences": [
                {
                    "apiVersion": "osac.openshift.io/v1alpha1",
                    "kind": "ClusterOrder",
                    "name": "fixture-order",
                    "uid": "fixture-uid",
                    "controller": True,
                }
            ],
        },
        "type": "kubernetes.io/dockerconfigjson",
        "data": {".dockerconfigjson": base64.b64encode(PAYLOAD.encode()).decode()},
    }
    return order, secret


@pytest.mark.parametrize(
    "failure",
    [None, "missing", "owner", "tenant", "namespace", "type", "key", "malformed", "terminating"],
)
def test_runtime_reference_validation(failure):
    order, secret = fixture()
    resources = [secret]
    if failure == "missing":
        resources = []
    elif failure == "owner":
        secret["metadata"]["ownerReferences"][0]["uid"] = "other-uid"
    elif failure == "tenant":
        secret["metadata"]["annotations"]["osac.openshift.io/tenant"] = "tenant-b"
    elif failure == "namespace":
        secret["metadata"]["namespace"] = "other-namespace"
    elif failure == "type":
        secret["type"] = "Opaque"
    elif failure == "key":
        secret["data"] = {}
    elif failure == "malformed":
        secret["data"][".dockerconfigjson"] = MARKER
    elif failure == "terminating":
        secret["metadata"]["deletionTimestamp"] = "2026-01-01T00:00:00Z"
    before = copy.deepcopy(order)
    if failure:
        with pytest.raises(AnsibleFilterError) as caught:
            cluster_pull_secret(resources, order)
        assert MARKER not in str(caught.value)
    else:
        assert cluster_pull_secret(resources, order) == PAYLOAD
    assert order == before


@pytest.mark.parametrize("failure", [None, "missing", "forbidden", "malformed"])
def test_settings_and_extraction_runtime_contract(tmp_path, failure):
    order, secret = fixture()
    resources = [] if failure == "missing" else [secret]
    if failure == "malformed":
        secret["data"][".dockerconfigjson"] = MARKER
    module_dir = tmp_path / "ansible_collections/kubernetes/core/plugins/modules"
    module_dir.mkdir(parents=True)
    module = f"""from ansible.module_utils.basic import AnsibleModule
module = AnsibleModule(argument_spec={{key: dict(type='str') for key in ['api_version', 'kind', 'namespace', 'name']}})
if module.params['namespace'] != 'fixture-hub' or module.params['name'] != 'owned-secret':
    module.fail_json(msg='Unexpected Secret lookup')
if {failure == 'forbidden'!r}:
    module.fail_json(msg='Synthetic forbidden lookup')
module.exit_json(changed=False, resources={resources!r})
"""
    (module_dir / "k8s_info.py").write_text(module)
    tasks = [
        {"name": "Resolve runtime settings", "ansible.builtin.include_role": {"name": "osac.service.cluster_settings"}},
        {"name": "Extract template inputs", "ansible.builtin.include_role": {"name": "osac.service.extract_template_info"}},
        {
            "name": "Verify protected runtime values",
            "ansible.builtin.assert": {
                "that": [
                    "template_parameters.pull_secret == synthetic_expected",
                    "template_parameters.unrelated == 'keep'",
                    "cluster_order == original_order",
                ]
            },
            "no_log": True,
        },
        {"name": "Inspect safe order fact", "ansible.builtin.debug": {"var": "cluster_order"}},
    ]
    play = [{
        "name": "Protected cluster runtime",
        "hosts": "localhost",
        "gather_facts": False,
        "vars": {
            "cluster_order": order,
            "original_order": order,
            "synthetic_expected": PAYLOAD,
            "ansible_python_interpreter": sys.executable,
            "ansible_connection": "local",
        },
        "tasks": tasks,
    }]
    playbook = tmp_path / "runtime.json"
    playbook.write_text(json.dumps(play))
    environment = os.environ.copy()
    environment.update({
        "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path}:{OSAC_AAP / 'collections'}:{OSAC_AAP / 'vendor'}",
        "ANSIBLE_FORCE_COLOR": "false",
        "ANSIBLE_STDOUT_CALLBACK": "default",
    })
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", str(playbook)],
        cwd=OSAC_AAP, env=environment, capture_output=True, text=True, timeout=45, check=False,
    )
    output = result.stdout + result.stderr
    assert (result.returncode == 0) == (failure is None), output
    assert MARKER not in output
    assert base64.b64encode(PAYLOAD.encode()).decode() not in output
    if failure:
        assert "Referenced cluster pull secret is unavailable or invalid" in output
