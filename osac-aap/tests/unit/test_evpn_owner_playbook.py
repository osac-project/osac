import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/cudn_evpn"
MODULE = '''
import fcntl
import json
import os
import time
from ansible.module_utils.basic import AnsibleModule

module = AnsibleModule(argument_spec={
    key: {"type": "raw"} for key in (
        "api_version", "kind", "name", "namespace", "state", "definition",
        "kubeconfig", "apply", "server_side_apply", "wait", "wait_timeout", "label_selectors")
})
with open(os.environ["EVPN_STATE"] + ".lock", "a") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    with open(os.environ["EVPN_STATE"]) as stream:
        state = json.load(stream)
    definition = module.params.get("definition")
    kind = definition["kind"] if definition else module.params["kind"]
    name = definition["metadata"]["name"] if definition else module.params.get("name")
    identity = kind + "/" + (name or "")
    if not definition and not module.params.get("state"):
        if kind == "Subnet" and not name:
            resources = list(state["subnets"].values())
        else:
            resource = state["subnets"].get(name) if kind == "Subnet" else state["resources"].get(identity)
            resources = [resource] if resource else []
        module.exit_json(changed=False, resources=resources)
    if module.params.get("state") == "absent":
        if state.get("delete_failure_kind") == kind:
            module.fail_json(msg="Injected cleanup failure")
        state["resources"].pop(identity, None)
    elif kind == "Lease":
        stored = state["resources"].get(identity)
        if not stored:
            stored = definition
            stored["metadata"]["resourceVersion"] = "1"
        elif definition["metadata"].get("resourceVersion"):
            if definition["metadata"]["resourceVersion"] != stored["metadata"]["resourceVersion"]:
                module.fail_json(msg="Conflict: resourceVersion changed")
            stored["spec"].update(definition.get("spec", {}))
        for key, value in definition["metadata"].get("annotations", {}).items():
            if value is None:
                stored["metadata"].setdefault("annotations", {}).pop(key, None)
            else:
                stored["metadata"].setdefault("annotations", {})[key] = value
        stored["metadata"]["resourceVersion"] = str(int(stored["metadata"]["resourceVersion"]) + 1)
        state["resources"][identity] = stored
    elif kind == "Subnet":
        state["subnets"][name]["metadata"].setdefault("annotations", {}).update(definition["metadata"]["annotations"])
    else:
        if kind == "ClusterUserDefinedNetwork":
            stored = state["resources"].get(identity)
            owner = definition["metadata"]["annotations"]["osac.openshift.io/cudn-owner-subnet"]
            if stored and stored["metadata"]["annotations"]["osac.openshift.io/cudn-owner-subnet"] != owner:
                module.fail_json(msg="Competing CUDN owner write")
            state["network_writes"].append(owner)
            definition["metadata"]["generation"] = 1
            definition["status"] = {"conditions": [{"type":"Ready", "status":"True", "observedGeneration":1}]}
        state["resources"][identity] = definition
    with open(os.environ["EVPN_STATE"], "w") as stream:
        json.dump(state, stream)
module.exit_json(changed=True)
'''


def _environment(tmp_path):
    modules = tmp_path / "ansible_collections/kubernetes/core/plugins/modules"
    modules.mkdir(parents=True)
    for name in ("k8s", "k8s_info"):
        (modules / f"{name}.py").write_text(MODULE)
    return {**os.environ, "ANSIBLE_COLLECTIONS_PATH": f"{tmp_path}:{ROOT / 'collections'}:{ROOT / 'vendor'}",
            "EVPN_STATE": str(tmp_path / "state.json"), "ANSIBLE_NOCOLOR": "1"}


def _subnet(name):
    return {"apiVersion": "osac.openshift.io/v1alpha1", "kind": "Subnet",
            "metadata": {"name": name, "namespace": "management", "annotations": {
                "osac.openshift.io/tenant": "tenant"}},
            "spec": {"virtualNetwork": "vn-id", "ipv4Cidr": "192.0.2.0/24"}}


def _playbook(tmp_path, name, tasks):
    path = tmp_path / f"{name}.yaml"
    path.write_text(yaml.safe_dump([{"hosts": "localhost", "gather_facts": False,
        "vars": {"subnet": _subnet(name), "subnet_name": name, "subnet_cidr": "192.0.2.0/24",
                 "tenant_id": "tenant", "vnet_uuid": "vn-id", "vnet_name": "vnet",
                 "vnet_lock_name": "shared", "vnet_lock_namespace": "management",
                 "vnet_lock_holder": name, "_vnet_lock_retry_delay": 1,
                 "l2_vni": 100, "l3_vni": 200, "fabric_reserved_range": "192.0.2.0/25",
                 "_evpn_fabric_ipam": {"gateway_ip": "192.0.2.1", "gateway_infrastructure_subnet": "192.0.2.1/32",
                     "management_infrastructure_subnet": "192.0.2.2/32", "reserved_subnets": ["192.0.2.0/25"]}},
        "tasks": tasks}], sort_keys=False))
    return path


def test_concurrent_real_ansible_claims_share_one_owner_and_retry_idempotently(tmp_path):
    environment = _environment(tmp_path)
    (tmp_path / "templates").mkdir()
    shutil.copy(ROLE / "templates/cudn.yaml.j2", tmp_path / "templates/cudn.yaml.j2")
    state_path = Path(environment["EVPN_STATE"])
    state_path.write_text(json.dumps({"subnets": {name: _subnet(name) for name in ("first", "second", "third")},
                                     "resources": {}, "network_writes": []}))
    production = yaml.safe_load((ROLE / "tasks/create_subnet.yaml").read_text())
    critical_section = next(task for task in production if "block" in task)
    tasks = [{"name": "Initialize shared Lease", "ansible.builtin.include_tasks": str(ROLE / "tasks/ensure_vnet_lock.yaml")},
             {"name": "Acquire shared CAS Lease", "ansible.builtin.include_role": {
                 "name": "osac.service.vnet_lock", "tasks_from": "acquire"}}, critical_section]
    workers = [subprocess.Popen(["ansible-playbook", "-i", "localhost,", "-c", "local",
                str(_playbook(tmp_path, name, tasks))], cwd=ROOT, env=environment,
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True) for name in ("first", "second", "third")]
    try:
        results = [(worker, worker.communicate(timeout=120)[0]) for worker in workers]
    finally:
        for worker in workers:
            if worker.poll() is None:
                worker.kill()
                worker.communicate()
    for worker, output in results:
        assert worker.returncode == 0, output
    state = json.loads(state_path.read_text())
    decisions = {name: resource["metadata"]["annotations"]["osac.openshift.io/compute-instance-supported"]
                 for name, resource in state["subnets"].items()}
    assert list(decisions.values()).count("true") == 1
    owner = next(name for name, decision in decisions.items() if decision == "true")
    assert set(state["network_writes"]) == {owner}
    repeated = subprocess.run(["ansible-playbook", "-i", "localhost,", "-c", "local",
        str(_playbook(tmp_path, owner, tasks))], cwd=ROOT, env=environment, capture_output=True, text=True, timeout=60)
    assert repeated.returncode == 0, repeated.stdout + repeated.stderr
    assert set(json.loads(state_path.read_text())["network_writes"]) == {owner}


@pytest.mark.parametrize("supported,phase,expected", [(False, "Ready", False), (None, "Ready", False),
                                                       (True, "Progressing", False), (True, "Ready", True)])
def test_real_vm_guard_blocks_before_resource_step(tmp_path, supported, phase, expected):
    environment = _environment(tmp_path)
    requested = _subnet("requested")
    requested["status"] = {"computeInstanceSupported": supported, "phase": phase}
    Path(environment["EVPN_STATE"]).write_text(json.dumps({"subnets": {"requested": requested}, "resources": {}, "network_writes": []}))
    marker = tmp_path / "resource-step"
    tasks = [{"name": "Read actual VM guard tasks", "ansible.builtin.include_tasks":
              str(ROLE.parent / "ocp_virt_vm/tasks/validate_subnet_eligibility.yaml")},
             {"name": "VM or Secret resource step", "ansible.builtin.copy": {"content": "created", "dest": str(marker), "mode": "0600"}}]
    play = [{"hosts": "localhost", "gather_facts": False, "vars": {"compute_instance": {
        "metadata": {"namespace": "management", "annotations": {"osac.openshift.io/subnet-target-namespace": "stale"}},
        "spec": {"networkAttachments": [{"subnetRef": "requested"}]}}}, "tasks": tasks}]
    path = tmp_path / "vm.yaml"
    path.write_text(yaml.safe_dump(play))
    result = subprocess.run(["ansible-playbook", "-i", "localhost,", "-c", "local", str(path)],
        cwd=ROOT, env=environment, text=True, capture_output=True, timeout=60)
    assert (result.returncode == 0) is expected, result.stdout + result.stderr
    assert marker.exists() is expected


def test_real_teardown_failure_preserves_fence_and_new_claim_waits(tmp_path):
    environment = _environment(tmp_path)
    (tmp_path / "templates").mkdir()
    shutil.copy(ROLE / "templates/cudn.yaml.j2", tmp_path / "templates/cudn.yaml.j2")
    owner, candidate, sibling = (_subnet(name) for name in ("owner", "new", "fabric"))
    owner["metadata"]["deletionTimestamp"] = "2026-01-01T00:00:00Z"
    sibling["metadata"]["annotations"]["osac.openshift.io/compute-instance-supported"] = "false"
    state_path = Path(environment["EVPN_STATE"])
    state = {"subnets": {"owner": owner, "new": candidate, "fabric": sibling},
             "resources": {"Lease/shared": {"apiVersion": "coordination.k8s.io/v1", "kind": "Lease",
                 "metadata": {"name": "shared", "namespace": "management", "resourceVersion": "1",
                     "annotations": {"osac.openshift.io/cudn-owner-subnet": "owner"}}, "spec": {}}},
             "network_writes": [], "delete_failure_kind": "Namespace"}
    state_path.write_text(json.dumps(state))
    deletion = next(task for task in yaml.safe_load((ROLE / "tasks/delete_subnet.yaml").read_text()) if "block" in task)
    deletion_tasks = [{"name": "Set deleting Subnet identity", "ansible.builtin.set_fact": {
        "_deleting_subnet_name": "owner"}}, deletion]
    playbook = _playbook(tmp_path, "owner", deletion_tasks)

    def execute(path):
        return subprocess.run(["ansible-playbook", "-i", "localhost,", "-c", "local", str(path)],
            cwd=ROOT, env=environment, capture_output=True, text=True, timeout=120)

    failed = execute(playbook)
    assert failed.returncode != 0
    assert "Injected cleanup failure" in failed.stdout
    state = json.loads(state_path.read_text())
    lease = state["resources"]["Lease/shared"]
    assert lease["metadata"]["annotations"] == {"osac.openshift.io/cudn-owner-subnet": "owner"}
    state.pop("delete_failure_kind")
    state_path.write_text(json.dumps(state))
    cleaned = execute(playbook)
    assert cleaned.returncode == 0, cleaned.stdout + cleaned.stderr
    state = json.loads(state_path.read_text())
    assert state["resources"]["Lease/shared"]["metadata"]["annotations"]["osac.openshift.io/cudn-owner-cleaned"] == "true"
    creation = next(task for task in yaml.safe_load((ROLE / "tasks/create_subnet.yaml").read_text()) if "block" in task)
    new_playbook = _playbook(tmp_path, "new", [creation])
    waiting = execute(new_playbook)
    assert waiting.returncode != 0
    state = json.loads(state_path.read_text())
    assert "osac.openshift.io/compute-instance-supported" not in state["subnets"]["new"]["metadata"]["annotations"]
    assert state["network_writes"] == []
    del state["subnets"]["owner"]
    state_path.write_text(json.dumps(state))
    claimed = execute(new_playbook)
    assert claimed.returncode == 0, claimed.stdout + claimed.stderr
    state = json.loads(state_path.read_text())
    assert state["resources"]["Lease/shared"]["metadata"]["annotations"]["osac.openshift.io/cudn-owner-subnet"] == "new"
    assert state["subnets"]["fabric"]["metadata"]["annotations"]["osac.openshift.io/compute-instance-supported"] == "false"
    assert set(state["network_writes"]) == {"new"}
